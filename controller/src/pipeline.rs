//! The NC0 dry-run pipeline: capture → letterbox → detect → inverse
//! transform → safety verdict → overlay. This is the "窗口 → 捕获 → resize
//! → mock detection → 坐标反算 → debug overlay" loop from ROADMAP §9, and
//! it NEVER sends input — the input module does not exist yet (NC4), which
//! is the strongest possible dry-run guarantee.

use crate::capture::CaptureBackend;
use crate::frame::Frame;
use crate::safety::{GovernorVerdict, SafetyGovernor};
use crate::transform::Transform;
use crate::vision::{Detection, Detector};
use crate::window::WindowLayout;
use crate::Result;
use std::time::{Duration, Instant};

/// Resize a client frame into `model_w x model_h` with nearest-neighbour
/// sampling and letterbox (uniform scale, centered padding, dark fill).
/// Real detectors do this on the GPU; NC0 does it in software so the
/// transform math is exercised end to end in tests.
pub fn letterbox_to_model(frame: &Frame, model_w: u32, model_h: u32) -> Result<Frame> {
    if model_w == 0 || model_h == 0 {
        return Err(crate::ControllerError::InvalidInput(format!(
            "model size must be positive, got {model_w}x{model_h}"
        )));
    }
    let mut out = Frame::new(model_w, model_h);
    let scale = (model_w as f32 / frame.width as f32).min(model_h as f32 / frame.height as f32);
    let shown_w = (frame.width as f32 * scale).round() as u32;
    let shown_h = (frame.height as f32 * scale).round() as u32;
    let pad_x = (model_w - shown_w) / 2;
    let pad_y = (model_h - shown_h) / 2;
    for my in pad_y..pad_y + shown_h {
        let sy = ((my - pad_y) as f32 / scale) as u32;
        for mx in pad_x..pad_x + shown_w {
            let sx = ((mx - pad_x) as f32 / scale) as u32;
            if let Some(px) = frame.pixel(sx, sy) {
                out.set_pixel(mx, my, px);
            }
        }
    }
    Ok(out)
}

/// Draw a detection's bounding box (in the frame's own coordinate space)
/// in bright yellow with corner ticks; pixels near edges are clipped by
/// `Frame::set_pixel`.
pub fn draw_overlay(frame: &mut Frame, detections: &[Detection], color: [u8; 4]) {
    for d in detections {
        let x0 = d.rect.x.max(0.0) as u32;
        let y0 = d.rect.y.max(0.0) as u32;
        let x1 = (d.rect.x + d.rect.w).min(frame.width as f32) as u32;
        let y1 = (d.rect.y + d.rect.h).min(frame.height as f32) as u32;
        for x in x0..x1.min(frame.width) {
            frame.set_pixel(x, y0, color);
            if y1 > 0 {
                frame.set_pixel(x, y1 - 1, color);
            }
        }
        for y in y0..y1.min(frame.height) {
            frame.set_pixel(x0, y, color);
            if x1 > 0 {
                frame.set_pixel(x1 - 1, y, color);
            }
        }
    }
}

/// What one dry-run cycle produced (for logging and tests).
#[derive(Debug, Clone)]
pub struct CycleReport {
    pub cycle: u32,
    /// Verdict from the pre-check phase (may already be Stop/Pause).
    pub pre_verdict: GovernorVerdict,
    pub geometry_ok: bool,
    /// Detections mapped back to CLIENT coordinates.
    pub client_detections: Vec<Detection>,
    /// Same detections' centers in DESKTOP coordinates.
    pub desktop_points: Vec<(f32, f32)>,
    pub action_verdict: Option<GovernorVerdict>,
    /// The captured client frame for this cycle, when one was taken
    /// (None when a precondition or geometry check short-circuited).
    /// Reuse it for overlays — do NOT capture again.
    pub frame: Option<Frame>,
}

impl CycleReport {
    pub fn allowed(&self) -> bool {
        self.pre_verdict.is_allow()
            && self.geometry_ok
            && self
                .action_verdict
                .as_ref()
                .is_none_or(GovernorVerdict::is_allow)
    }
}

/// Run ONE dry-run cycle against a live backend. `same_window` and
/// `foreground` come from the caller's window observations; the calibrated
/// layout is the geometry the transform was built with.
#[allow(clippy::too_many_arguments)]
pub fn run_cycle(
    cycle: u32,
    backend: &mut dyn CaptureBackend,
    detector: &mut dyn Detector,
    governor: &mut SafetyGovernor,
    calibrated: &WindowLayout,
    current: &WindowLayout,
    same_window: bool,
    foreground: bool,
    model_w: u32,
    model_h: u32,
    now: Instant,
) -> Result<CycleReport> {
    let pre_verdict = governor.check_preconditions(now, same_window, foreground);
    if !pre_verdict.is_allow() {
        return Ok(CycleReport {
            cycle,
            pre_verdict,
            geometry_ok: false,
            client_detections: Vec::new(),
            desktop_points: Vec::new(),
            action_verdict: None,
            frame: None,
        });
    }
    let geometry_ok = governor.check_geometry(calibrated, current).is_allow();
    if !geometry_ok {
        return Ok(CycleReport {
            cycle,
            pre_verdict,
            geometry_ok,
            client_detections: Vec::new(),
            desktop_points: Vec::new(),
            action_verdict: None,
            frame: None,
        });
    }

    // capture → letterbox → detect → inverse transform
    let frame = backend.capture()?;
    let transform = Transform::new(current, model_w, model_h)?;
    let model_frame = letterbox_to_model(&frame, model_w, model_h)?;
    let model_detections = detector.detect(&model_frame);

    let mut client_detections = Vec::with_capacity(model_detections.len());
    let mut desktop_points = Vec::with_capacity(model_detections.len());
    let mut action_verdict: Option<GovernorVerdict> = None;

    for d in model_detections {
        let client_rect = transform.rect_model_to_client(d.rect)?;
        let center = client_rect.center();
        let desktop = transform.client_to_desktop(center.0, center.1)?;
        // authorize the *planned* action (nothing is ever sent in NC0)
        let v = governor.authorize_action(now, d.confidence, center);
        let allowed = v.is_allow();
        desktop_points.push(desktop);
        action_verdict = Some(match action_verdict.take() {
            None => v,
            Some(prev) if prev.is_allow() && !allowed => v,
            Some(prev) => prev,
        });
        client_detections.push(Detection {
            label: d.label,
            rect: client_rect,
            confidence: d.confidence,
        });
    }

    Ok(CycleReport {
        cycle,
        pre_verdict,
        geometry_ok,
        client_detections,
        desktop_points,
        action_verdict,
        frame: Some(frame),
    })
}

/// Bounded exponential-backoff retry tracker for the observation loop.
///
/// Transient capture/pipeline failures (a PrintWindow hiccup, a layout
/// read racing a resize) must not kill an overnight session; deterministic
/// end states (window gone, governor Stop) are handled by the caller and
/// never reach this tracker. `on_failure` returns the delay to sleep
/// before the next attempt, or `None` when the budget is exhausted.
#[derive(Debug)]
pub struct RetryTracker {
    max_consecutive: u32,
    consecutive: u32,
}

impl RetryTracker {
    pub fn new(max_consecutive: u32) -> RetryTracker {
        RetryTracker {
            max_consecutive: max_consecutive.max(1),
            consecutive: 0,
        }
    }

    /// Record a failure: `Some(delay)` to keep retrying (100ms doubling,
    /// capped at 2s), `None` to abort the session.
    pub fn on_failure(&mut self) -> Option<Duration> {
        self.consecutive += 1;
        if self.consecutive > self.max_consecutive {
            return None;
        }
        let exp = self.consecutive - 1;
        let shift = exp.min(5); // 100ms..3.2s raw, capped below
        let delay = 100u64.saturating_mul(1 << shift).min(2000);
        Some(Duration::from_millis(delay))
    }

    /// Record a success: consecutive-failure counter resets.
    pub fn on_success(&mut self) {
        self.consecutive = 0;
    }

    pub fn consecutive_failures(&self) -> u32 {
        self.consecutive
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::capture::SyntheticCapture;
    use crate::safety::SafetyConfig;
    use crate::vision::MockDetector;

    fn governor(t0: Instant) -> SafetyGovernor {
        SafetyGovernor::new(SafetyConfig::default(), t0).expect("gov")
    }

    #[test]
    fn retry_tracker_backs_off_then_exhausts() {
        let mut t = RetryTracker::new(5);
        // 100, 200, 400, 800, 1600 -> capped 2000 on 5th? no: shifts 0..4
        let expected = [100, 200, 400, 800, 1600];
        for (i, e) in expected.iter().enumerate() {
            assert_eq!(
                t.on_failure(),
                Some(Duration::from_millis(*e)),
                "failure {i}"
            );
        }
        // 6th consecutive failure: budget exhausted
        assert_eq!(t.on_failure(), None);
        // success resets the counter
        t.on_success();
        assert_eq!(t.consecutive_failures(), 0);
        assert_eq!(t.on_failure(), Some(Duration::from_millis(100)));
    }

    #[test]
    fn retry_tracker_max_is_at_least_one() {
        let mut t = RetryTracker::new(0);
        assert_eq!(t.on_failure(), Some(Duration::from_millis(100)));
        assert_eq!(t.on_failure(), None, "budget of 1 allows exactly one retry");
    }

    #[test]
    fn letterbox_exact_pixels_2x2_into_4x2() {
        // 2x2 into 4x2: scale = min(4/2, 2/2) = 1, shown 2x2, pad_x = 1,
        // pad_y = 0; source (sx,sy) lands at (sx+1, sy); padding stays black.
        let mut src = Frame::new(2, 2);
        src.set_pixel(0, 0, [1, 1, 1, 255]);
        src.set_pixel(1, 1, [2, 2, 2, 255]);
        let out = letterbox_to_model(&src, 4, 2).expect("out");
        assert_eq!(out.pixel(1, 0), Some([1, 1, 1, 255]), "src (0,0) -> (1,0)");
        assert_eq!(out.pixel(2, 1), Some([2, 2, 2, 255]), "src (1,1) -> (2,1)");
        assert_eq!(
            out.pixel(0, 0),
            Some([0, 0, 0, 0]),
            "left padding stays black"
        );
        assert_eq!(
            out.pixel(3, 1),
            Some([0, 0, 0, 0]),
            "right padding stays black"
        );
        assert_eq!(out.pixel(0, 1), Some([0, 0, 0, 0]));
    }

    #[test]
    fn letterbox_rejects_zero_model() {
        let f = Frame::new(4, 4);
        assert!(letterbox_to_model(&f, 0, 8).is_err());
    }

    /// The pipeline's heart: detection found in model space maps back to a
    /// stable normalized position on BOTH resolutions, and the governor
    /// authorizes the planned action.
    #[test]
    fn full_cycle_detection_survives_transform_roundtrip() {
        let t0 = Instant::now();
        let mut backend = SyntheticCapture::new(320, 240).expect("cap");
        let mut det = MockDetector::synthetic_rect();
        let mut gov = governor(t0);
        let calibrated = backend.calibrated_layout();
        let report = run_cycle(
            0,
            &mut backend,
            &mut det,
            &mut gov,
            &calibrated,
            &calibrated,
            true,
            true,
            256,
            256,
            t0,
        )
        .expect("cycle");
        assert!(
            report.allowed(),
            "happy-path cycle must be allowed: {report:?}"
        );
        assert_eq!(report.client_detections.len(), 1);
        let d = &report.client_detections[0];
        assert_eq!(d.label, "moving_rect");
        // desktop point = client center + origin (synthetic origin is 0,0)
        let ctr = d.rect.center();
        let dp = report.desktop_points[0];
        assert!((ctr.0 - dp.0).abs() < 0.01 && (ctr.1 - dp.1).abs() < 0.01);
    }

    #[test]
    fn geometry_change_blocks_the_cycle_with_pause() {
        let t0 = Instant::now();
        let mut backend = SyntheticCapture::new(320, 240).expect("cap");
        let mut det = MockDetector::synthetic_rect();
        let mut gov = governor(t0);
        let calibrated = backend.calibrated_layout();
        let drifted = WindowLayout {
            client_size: (400, 300),
            ..calibrated.clone()
        };
        let report = run_cycle(
            0,
            &mut backend,
            &mut det,
            &mut gov,
            &calibrated,
            &drifted,
            true,
            true,
            256,
            256,
            t0,
        )
        .expect("cycle");
        assert!(!report.allowed());
        assert!(
            report.client_detections.is_empty(),
            "no detection may run after geometry drift"
        );
        assert!(report.pre_verdict.is_allow());
    }

    #[test]
    fn lost_foreground_stops_before_any_capture() {
        let t0 = Instant::now();
        let mut backend = SyntheticCapture::new(320, 240).expect("cap");
        let mut det = MockDetector::synthetic_rect();
        let mut gov = governor(t0);
        let calibrated = backend.calibrated_layout();
        let report = run_cycle(
            0,
            &mut backend,
            &mut det,
            &mut gov,
            &calibrated,
            &calibrated,
            true,
            false,
            256,
            256,
            t0,
        )
        .expect("cycle");
        assert!(
            report.pre_verdict.is_stop(),
            "lost foreground must stop: {report:?}"
        );
        assert!(report.action_verdict.is_none());
    }
}
