//! Inference timeout (NC1 deferred item, landed NC4-era): the WinML
//! evaluate call is synchronous and cannot be cancelled, so a hung
//! inference would freeze the observation loop forever — no cycles, no
//! SUMMARY, a session that only looks dead.
//!
//! [`TimeoutDetector`] moves the inner detector onto ONE exclusive worker
//! thread and bounds each `detect()` wait. A timed-out cycle reports a
//! timeout through `take_error()` — feeding the existing
//! consecutive-failure circuit breaker — and returns no detections, so
//! the session keeps cycling until the breaker honestly ends it.
//!
//! Ownership model: after handoff the worker exclusively owns the inner
//! detector; the main thread never touches it. Errors the inner detector
//! latches are collected by the worker per request and forwarded through
//! the same response, preserving `take_error()` semantics across the
//! thread boundary. A worker stuck inside WinML stays stuck (the call is
//! unkillable by design) — but the session no longer is.

use crate::frame::Frame;
use crate::vision::{Detection, Detector};
use std::sync::mpsc::{channel, Receiver, Sender};
use std::time::Duration;

struct Request {
    frame: Frame,
    respond: Sender<(Vec<Detection>, Option<String>)>,
}

pub struct TimeoutDetector {
    requests: Option<Sender<Request>>,
    timeout: Duration,
    error: Option<String>,
}

impl TimeoutDetector {
    /// Hand `inner` to a fresh worker thread; every `detect` round-trips
    /// through it under the `timeout` budget.
    pub fn new(inner: Box<dyn Detector + Send>, timeout: Duration) -> TimeoutDetector {
        let (tx, rx): (_, Receiver<Request>) = channel();
        std::thread::Builder::new()
            .name("inference-worker".into())
            .spawn(move || {
                let mut inner = inner;
                for req in rx {
                    let dets = inner.detect(&req.frame);
                    let err = inner.take_error();
                    let _ = req.respond.send((dets, err));
                }
            })
            .expect("spawn inference worker");
        TimeoutDetector {
            requests: Some(tx),
            timeout,
            error: None,
        }
    }
}

impl Detector for TimeoutDetector {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection> {
        let requests = match &self.requests {
            Some(tx) => tx,
            None => return Vec::new(),
        };
        let (respond_tx, respond_rx) = channel();
        if requests
            .send(Request {
                frame: frame.clone(),
                respond: respond_tx,
            })
            .is_err()
        {
            self.error = Some("inference worker is gone".into());
            return Vec::new();
        }
        match respond_rx.recv_timeout(self.timeout) {
            Ok((dets, inner_error)) => {
                // preserve the inner detector's error semantics across the
                // thread boundary
                if inner_error.is_some() {
                    self.error = inner_error;
                }
                dets
            }
            Err(_) => {
                self.error = Some(format!(
                    "inference timed out after {:?} (worker call unkillable; \
                     consecutive timeouts trip the existing breaker)",
                    self.timeout
                ));
                Vec::new()
            }
        }
    }

    fn take_error(&mut self) -> Option<String> {
        self.error.take()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::transform::Rect;
    use std::sync::atomic::{AtomicU32, Ordering};

    fn frame() -> Frame {
        Frame::new(32, 32)
    }

    fn det(label: &str) -> Detection {
        Detection {
            label: label.to_string(),
            rect: Rect::new(1.0, 2.0, 3.0, 4.0),
            confidence: 0.9,
        }
    }

    /// Normal detector: results pass through, no errors.
    struct Fine;

    impl Detector for Fine {
        fn detect(&mut self, _frame: &Frame) -> Vec<Detection> {
            vec![det("fine")]
        }
    }

    /// Sleeps `sleeps`-th call duration; later calls are fast. Proves the
    /// session survives a slow inference and recovers.
    struct SlowOnce {
        calls: AtomicU32,
    }

    impl Detector for SlowOnce {
        fn detect(&mut self, _frame: &Frame) -> Vec<Detection> {
            if self.calls.fetch_add(1, Ordering::SeqCst) == 0 {
                std::thread::sleep(Duration::from_millis(500));
            }
            vec![det("slow")]
        }
    }

    /// Hangs on the FIRST call forever (well past any test timeout).
    struct HangFirst {
        calls: AtomicU32,
    }

    impl Detector for HangFirst {
        fn detect(&mut self, _frame: &Frame) -> Vec<Detection> {
            if self.calls.fetch_add(1, Ordering::SeqCst) == 0 {
                std::thread::sleep(Duration::from_secs(60));
            }
            Vec::new()
        }
    }

    fn timeout() -> Duration {
        Duration::from_millis(100)
    }

    #[test]
    fn fast_results_pass_through() {
        let mut d = TimeoutDetector::new(Box::new(Fine), timeout());
        let dets = d.detect(&frame());
        assert_eq!(dets.len(), 1);
        assert_eq!(dets[0].label, "fine");
        assert!(d.take_error().is_none());
    }

    #[test]
    fn slow_inference_times_out_then_recovers() {
        let mut d = TimeoutDetector::new(
            Box::new(SlowOnce {
                calls: AtomicU32::new(0),
            }),
            timeout(),
        );
        // first call exceeds the budget: empty result + timeout error
        let dets = d.detect(&frame());
        assert!(
            dets.is_empty(),
            "a timed-out cycle must produce no detections"
        );
        let err = d.take_error().expect("timeout must surface as error");
        assert!(err.contains("timed out"), "{err}");
        // While the worker is STILL inside the slow call, further requests
        // legitimately time out too (they queue behind it) — each feeds
        // the breaker. Recovery happens once the worker frees up.
        let mut recovered = false;
        for _ in 0..10 {
            let dets = d.detect(&frame());
            if !dets.is_empty() {
                assert_eq!(dets[0].label, "slow");
                assert!(d.take_error().is_none(), "error is take-once");
                recovered = true;
                break;
            }
            d.take_error(); // clear the intermediate timeouts
        }
        assert!(recovered, "worker must eventually free up and serve");
    }

    #[test]
    fn hung_inference_keeps_timing_out_without_freezing() {
        let mut d = TimeoutDetector::new(
            Box::new(HangFirst {
                calls: AtomicU32::new(0),
            }),
            timeout(),
        );
        for _ in 0..3 {
            let t0 = std::time::Instant::now();
            let dets = d.detect(&frame());
            assert!(dets.is_empty());
            assert!(
                t0.elapsed() < Duration::from_millis(1000),
                "detect must return on budget, not hang"
            );
            assert!(d.take_error().unwrap().contains("timed out"));
        }
        // the loop above proves the SESSION stays live; the existing
        // consecutive-failure breaker is what ends it in production.
    }
}
