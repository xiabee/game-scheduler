//! NC0 dry-run pipeline integration: synthetic frames + MockDetector +
//! SafetyGovernor + Transform wired together — the cargo-test-able core of
//! the "capture → resize → mock detection → inverse transform" loop.

use controller::capture::{CaptureBackend, SyntheticCapture};
use controller::pipeline::{draw_overlay, letterbox_to_model, run_cycle};
use controller::safety::{SafetyConfig, SafetyGovernor};
use controller::transform::Transform;
use controller::vision::{Detector, MockDetector};
use std::time::{Duration, Instant};

#[test]
fn five_cycles_track_the_moving_rect_and_stay_allowed() {
    let t0 = Instant::now();
    let mut backend = SyntheticCapture::new(320, 240).expect("cap");
    let mut detector = MockDetector::synthetic_rect();
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), t0).expect("gov");
    let calibrated = backend.calibrated_layout();

    let mut centers = Vec::new();
    for cycle in 0..5u32 {
        let now = t0 + Duration::from_millis(cycle as u64 * 100);
        let report = run_cycle(
            cycle,
            &mut backend,
            &mut detector,
            &mut governor,
            &calibrated,
            &calibrated,
            true,
            true,
            256,
            256,
            now,
        )
        .expect("cycle");
        assert!(report.allowed(), "cycle {cycle} not allowed: {report:?}");
        assert_eq!(
            report.client_detections.len(),
            1,
            "cycle {cycle}: one rect expected"
        );
        centers.push(report.client_detections[0].rect.center());
    }
    // the rect must actually move across cycles
    let moved = centers
        .windows(2)
        .any(|w| (w[0].0 - w[1].0).abs() > 1.0 || (w[0].1 - w[1].1).abs() > 1.0);
    assert!(
        moved,
        "detection center never moved across cycles: {centers:?}"
    );
}

/// Round-trip fidelity at pipeline level: whatever the detector finds in
/// model space maps back to the same client pixels the source frame had —
/// verified by re-scanning the client frame directly.
#[test]
fn inverse_transform_lands_on_the_real_client_pixels() {
    let t0 = Instant::now();
    let mut backend = SyntheticCapture::new(320, 240).expect("cap");
    let frame = backend.capture().expect("client frame");

    // direct client-space scan for the rect's red fill
    let mut min = (u32::MAX, u32::MAX);
    let mut max = (0u32, 0u32);
    for y in 0..frame.height {
        for x in 0..frame.width {
            if let Some([b, g, r, _]) = frame.pixel(x, y) {
                if r > 150 && g < 100 && b < 60 {
                    min = (min.0.min(x), min.1.min(y));
                    max = (max.0.max(x), max.1.max(y));
                }
            }
        }
    }

    // pipeline path: letterbox → detect → inverse
    let mut detector = MockDetector::synthetic_rect();
    let model = letterbox_to_model(&frame, 256, 256).expect("model frame");
    let det = &detector.detect(&model)[0];
    let transform = Transform::new(&backend.calibrated_layout(), 256, 256).expect("t");
    let client = transform.rect_model_to_client(det.rect).expect("inverse");

    let direct = (
        min.0 as f32,
        min.1 as f32,
        (max.0 - min.0 + 1) as f32,
        (max.1 - min.1 + 1) as f32,
    );
    assert!(
        (client.x - direct.0).abs() <= 2.0
            && (client.y - direct.1).abs() <= 2.0
            && (client.w - direct.2).abs() <= 3.0
            && (client.h - direct.3).abs() <= 3.0,
        "inverse rect {client:?} diverges from direct scan {direct:?}"
    );

    // desktop point = client center + origin (synthetic origin is 0,0)
    let c = client.center();
    let report_desktop = transform.client_to_desktop(c.0, c.1).expect("desktop");
    assert!((report_desktop.0 - c.0).abs() < 0.01 && (report_desktop.1 - c.1).abs() < 0.01);
}

/// The overlay paints the detection's bbox; drawing must visibly change
/// the frame along the box edges.
#[test]
fn overlay_draws_visible_borders() {
    use controller::transform::Rect;
    let mut frame = controller::frame::Frame::new(64, 64);
    let dets = vec![controller::vision::Detection {
        label: "x".into(),
        rect: Rect::new(10.0, 10.0, 20.0, 20.0),
        confidence: 1.0,
    }];
    draw_overlay(&mut frame, &dets, [0, 230, 255, 255]);
    assert_eq!(
        frame.pixel(10, 10),
        Some([0, 230, 255, 255]),
        "top-left corner"
    );
    assert_eq!(
        frame.pixel(29, 29),
        Some([0, 230, 255, 255]),
        "bottom-right corner"
    );
    assert_eq!(
        frame.pixel(15, 15),
        Some([0, 0, 0, 0]),
        "interior untouched"
    );
    // box partially outside the frame must not panic and clips cleanly
    let clipped = vec![controller::vision::Detection {
        label: "edge".into(),
        rect: Rect::new(50.0, 50.0, 40.0, 40.0),
        confidence: 1.0,
    }];
    draw_overlay(&mut frame, &clipped, [0, 230, 255, 255]);
    assert_eq!(
        frame.pixel(63, 63),
        Some([0, 230, 255, 255]),
        "clipped corner still drawn"
    );
}

#[test]
fn emergency_latch_terminates_the_loop_midway() {
    let t0 = Instant::now();
    let mut backend = SyntheticCapture::new(320, 240).expect("cap");
    let mut detector = MockDetector::synthetic_rect();
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), t0).expect("gov");
    let calibrated = backend.calibrated_layout();

    let mut ran = 0u32;
    for cycle in 0..4u32 {
        if cycle == 2 {
            governor.trigger_emergency_stop();
        }
        let now = t0 + Duration::from_millis(cycle as u64 * 50);
        let report = run_cycle(
            cycle,
            &mut backend,
            &mut detector,
            &mut governor,
            &calibrated,
            &calibrated,
            true,
            true,
            256,
            256,
            now,
        )
        .expect("cycle");
        if cycle >= 2 {
            assert!(
                report.pre_verdict.is_stop(),
                "cycle {cycle} must be stopped"
            );
            break;
        }
        assert!(report.allowed());
        ran += 1;
    }
    assert_eq!(ran, 2, "exactly two cycles ran before the emergency stop");
}
