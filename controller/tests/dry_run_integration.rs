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

/// ROADMAP NC0 acceptance, automated: "把窗口从 1080p 拖到另一尺寸,dry-run
/// 的 normalized 坐标保持稳定". The probe window paints a red rect anchored
/// at normalized (0.6,0.6)-(0.8,0.8); after a resize the pipeline must —
/// after governor-mandated recalibration — report the same normalized
/// position. Uses the real GDI capture backend against a real window.
#[test]
fn window_resize_keeps_normalized_position_stable() {
    use controller::capture::GdiPrintWindowCapture;
    use controller::safety::SafetyGovernor;
    use controller::window::{ensure_dpi_awareness, GameWindow, OwnedTestWindow};

    ensure_dpi_awareness();
    let title = format!("NFCTRL-RESIZE-STABILITY-{}", std::process::id());
    let win = OwnedTestWindow::new(400, 300, &title).expect("create window");
    let wrapped = GameWindow::from_hwnd(win.hwnd).expect("wrap");

    let measure = |layout: &controller::window::WindowLayout| -> (f32, f32, String) {
        let mut cap = GdiPrintWindowCapture::new(win.hwnd, layout.clone()).expect("cap");
        let mut detector = MockDetector::synthetic_rect();
        // freshly composed content can lag by a frame or two (and after a
        // resize the surface still holds the old size for a while): skip
        // frames whose dimensions don't match the calibrated layout — the
        // same contract SafetyGovernor::check_geometry enforces live.
        for attempt in 0..20 {
            let paints_before = controller::window::probe_paint_count();
            let prints_before = controller::window::probe_print_count();
            win.nudge();
            let paints_after = controller::window::probe_paint_count();
            let _ = (paints_before, prints_before, paints_after);
            let frame = cap.capture().expect("frame");

            if frame.width != layout.client_size.0 as u32
                || frame.height != layout.client_size.1 as u32
            {
                std::thread::sleep(Duration::from_millis(15 + attempt * 5));
                continue;
            }
            let model = letterbox_to_model(&frame, 256, 256).expect("letterbox");
            let dets = detector.detect(&model);
            if let Some(det) = dets.first() {
                let t = Transform::new(layout, 256, 256).expect("t");
                let client = t.rect_model_to_client(det.rect).expect("inverse");
                let c = client.center();
                let n = t.client_to_normalized(c.0, c.1).expect("normalized");
                let info = format!(
                    "frame={}x{} client_rect=({:.0},{:.0} {:.0}x{:.0}) conf={:.2} paints={paints_after} prints_now={}",
                    frame.width,
                    frame.height,
                    client.x,
                    client.y,
                    client.w,
                    client.h,
                    det.confidence,
                    controller::window::probe_print_count() - prints_before
                );
                return (n.0, n.1, info);
            }
            std::thread::sleep(Duration::from_millis(15 + attempt * 5));
        }
        // diagnostic dump so a regression is diagnosable from the log
        let frame = cap.capture().expect("frame");
        let samples: Vec<Option<[u8; 4]>> = [
            (10u32, 10u32),
            (200, 150),
            (250, 180),
            (300, 220),
            (399, 299),
        ]
        .iter()
        .map(|(x, y)| frame.pixel(*x, *y))
        .collect();
        let distinct = frame
            .data
            .iter()
            .collect::<std::collections::HashSet<_>>()
            .len();
        panic!("probe pattern never detected after retries; distinct_bytes={distinct} samples={samples:?}");
    };

    let layout_a = wrapped.layout().expect("layout a");
    let (ax, ay, a_info) = measure(&layout_a);
    assert!(
        (ax - 0.7).abs() < 0.03 && (ay - 0.7).abs() < 0.03,
        "first measurement must be at normalized (0.7, 0.7), got ({ax},{ay}) [{a_info}]"
    );

    win.set_size(500, 400).expect("resize");
    let layout_b = wrapped.layout().expect("layout b");

    // the governor MUST flag the resize (Pause) before anyone recalibrates
    let mut gov = SafetyGovernor::new(SafetyConfig::default(), t0()).expect("gov");
    let verdict = gov.check_geometry(&layout_a, &layout_b);
    assert!(
        matches!(verdict, controller::safety::GovernorVerdict::Pause { .. }),
        "resize must produce Pause, got {verdict:?}"
    );

    // recalibrated measurement on the new geometry
    let (bx, by, b_info) = measure(&layout_b);
    assert!(
        (bx - 0.7).abs() < 0.03 && (by - 0.7).abs() < 0.03,
        "normalized position must survive the resize, got ({bx},{by}) layout_b={layout_b:?} [{b_info}]"
    );
    assert!(
        (ax - bx).abs() < 0.02 && (ay - by).abs() < 0.02,
        "before/after normalized positions diverge: ({ax},{ay}) [{a_info}] vs ({bx},{by}) [{b_info}]"
    );
}

fn t0() -> Instant {
    Instant::now()
}
