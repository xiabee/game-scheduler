//! Frame-hash inference cache (NC1/NC2 performance budget, ROADMAP §5:
//! "YOLO 按需触发……不每帧跑"). Wraps any [`Detector`]; identical model
//! frames skip inference entirely and replay the cached detections.
//! The cache is exact (full-pixel FNV-1a of the model-space frame), so a
//! cache hit is semantically identical to re-running the detector, and a
//! forced refresh every `refresh_every` cycles bounds staleness against
//! any non-determinism in the backend (e.g. GPU-denosed inference).

use crate::frame::Frame;
use crate::vision::{Detection, Detector};
use std::collections::HashMap;

fn fnv1a(data: &[u8], seed: u64) -> u64 {
    let mut h = seed;
    for &b in data {
        h ^= b as u64;
        h = h.wrapping_mul(0x0000_0100_0000_01B3);
    }
    h
}

/// A detector wrapper that skips inference for repeated frames.
pub struct CachingDetector<D: Detector> {
    inner: D,
    cache: HashMap<u64, Vec<Detection>>,
    last_key: Option<u64>,
    last_detections: Vec<Detection>,
    last_error: Option<String>,
    cycles: u64,
    refresh_every: u64,
    pub hits: u64,
    pub misses: u64,
}

// 64-bit FNV-1a over full frame data: collision odds on real frames are
// negligible, and the forced refresh bounds even a theoretical collision.
const HASH_SEED: u64 = 0xcbf2_9ce4_8422_2325;

impl<D: Detector> CachingDetector<D> {
    /// `refresh_every = 0` means never force a refresh (pure content
    /// addressing); a value of 32 re-runs inference roughly twice a second
    /// at the default 15fps, bounding staleness cheaply.
    pub fn new(inner: D, refresh_every: u64) -> CachingDetector<D> {
        CachingDetector {
            inner,
            cache: HashMap::new(),
            last_key: None,
            last_detections: Vec::new(),
            last_error: None,
            cycles: 0,
            refresh_every,
            hits: 0,
            misses: 0,
        }
    }

    fn key(frame: &Frame) -> u64 {
        let mut h = fnv1a(&frame.data, HASH_SEED);
        h = fnv1a(&frame.width.to_le_bytes(), h);
        h = fnv1a(&frame.height.to_le_bytes(), h);
        h
    }

    pub fn inference_count(&self) -> u64 {
        self.misses
    }
}

impl<D: Detector> Detector for CachingDetector<D> {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection> {
        self.cycles += 1;
        let key = Self::key(frame);
        let forced = self.refresh_every > 0 && self.cycles.is_multiple_of(self.refresh_every);
        let same_as_last = self.last_key == Some(key);
        if !forced && same_as_last {
            self.hits += 1;
            return self.last_detections.clone();
        }
        if !same_as_last {
            if let Some(cached) = self.cache.get(&key) {
                self.hits += 1;
                self.last_key = Some(key);
                self.last_detections = cached.clone();
                return self.last_detections.clone();
            }
        }

        self.misses += 1;
        let dets = self.inner.detect(frame);
        if let Some(e) = self.inner.take_error() {
            self.last_error = Some(e);
            // do not cache failures: the next cycle retries inference
            return Vec::new();
        }
        if self.cache.len() >= 64 {
            self.cache.clear(); // bounded: old scenes fall out wholesale
        }
        self.cache.insert(key, dets.clone());
        self.last_key = Some(key);
        self.last_detections = dets;
        self.last_detections.clone()
    }

    fn take_error(&mut self) -> Option<String> {
        // surface both the cache's own wrapping and the inner detector's
        if self.last_error.is_some() {
            self.last_error.take()
        } else {
            self.inner.take_error()
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vision::MockDetector;
    use std::cell::Cell;

    /// Counts how many times inference actually ran.
    struct CountingDetector {
        calls: Cell<u32>,
    }

    impl Detector for CountingDetector {
        fn detect(&mut self, _frame: &Frame) -> Vec<Detection> {
            self.calls.set(self.calls.get() + 1);
            vec![Detection {
                label: "x".into(),
                rect: crate::transform::Rect::new(0.0, 0.0, 1.0, 1.0),
                confidence: 1.0,
            }]
        }

        fn take_error(&mut self) -> Option<String> {
            None
        }
    }

    fn frame(color: u8) -> Frame {
        let mut f = Frame::new(32, 32);
        f.data.fill(color);
        f
    }

    #[test]
    fn identical_frames_skip_inference() {
        let counting = CountingDetector {
            calls: Cell::new(0),
        };
        let mut det = CachingDetector::new(counting, 0);
        let f = frame(7);
        for _ in 0..10 {
            let dets = det.detect(&f);
            assert_eq!(dets.len(), 1);
        }
        assert_eq!(det.inference_count(), 1, "one real inference");
        assert_eq!(det.hits, 9, "nine cache hits");
    }

    #[test]
    fn changed_frames_re_run_inference() {
        let counting = CountingDetector {
            calls: Cell::new(0),
        };
        let mut det = CachingDetector::new(counting, 0);
        assert_eq!(det.detect(&frame(1)).len(), 1);
        assert_eq!(det.detect(&frame(2)).len(), 1);
        assert_eq!(det.detect(&frame(1)).len(), 1, "old scene back: cached");
        assert_eq!(det.inference_count(), 2, "scene B then cached scene A");
    }

    #[test]
    fn forced_refresh_bounds_staleness() {
        let counting = CountingDetector {
            calls: Cell::new(0),
        };
        let mut det = CachingDetector::new(counting, 3);
        let f = frame(9);
        // cycles 1..3: only the 3rd forces a refresh
        det.detect(&f);
        det.detect(&f);
        det.detect(&f);
        assert_eq!(det.inference_count(), 2, "refresh on cycle 3");
    }

    #[test]
    fn wrap_mock_and_still_detect() {
        let mut det = CachingDetector::new(MockDetector::synthetic_rect(), 0);
        let mut f = Frame::new(64, 64);
        for y in 4..20 {
            for x in 8..24 {
                f.set_pixel(x, y, [10, 40, 200, 255]);
            }
        }
        let dets = det.detect(&f);
        assert_eq!(dets.len(), 1);
        assert_eq!(dets[0].label, "moving_rect");
        assert!(det.take_error().is_none());
    }
}
