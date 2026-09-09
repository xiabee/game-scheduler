//! NC2 L1 evidence over REAL captured frames: record a live GDI probe
//! window's frames, cut a template out of the first frame, and require
//! the NCC layer to keep finding it in every recorded frame. Complements
//! the synthetic template tests with real DWM-composed content.

use controller::capture::{CaptureBackend, SyntheticCapture};
use controller::frame::Frame;
use controller::perception::{LayeredPerception, PixelProbe, TemplateTarget};
use controller::pipeline::letterbox_to_model;
use controller::replay::{FrameRecorder, ReplayCapture};
use controller::safety::{SafetyConfig, SafetyGovernor};
use controller::skill::{SkillDefinition, SkillRunner, StepOutcome};
use controller::template::{NccTemplateMatcher, TemplateMatcher};
use controller::window::{ensure_dpi_awareness, OwnedTestWindow};
use std::path::Path;
use std::time::{Duration, Instant};

#[test]
fn l1_template_tracks_a_real_recorded_scene() {
    ensure_dpi_awareness();
    if !controller::window::interactive_desktop_available() {
        println!("skipped: needs an interactive desktop for live GDI content (service session)");
        return;
    }
    let dir = std::env::temp_dir().join(format!("nf_l1_real_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);

    // 1. record real frames of a probe window with a distinctive patch
    let title = format!("NFCTRL-L1-REAL-{}", std::process::id());
    let win = OwnedTestWindow::new(400, 300, &title).expect("probe window");
    let layout = controller::window::GameWindow::from_hwnd(win.hwnd)
        .expect("wrap")
        .layout()
        .expect("layout");
    let mut cap =
        controller::capture::GdiPrintWindowCapture::new(win.hwnd, layout).expect("gdi backend");
    let mut recorder = FrameRecorder::create(&dir, 6).expect("recorder");
    let mut recorded = 0u32;
    // the first DWM-composed surfaces can be transitional (the gdi-probe
    // CLI waits for the same reason): warm up until the red rect shows
    for _ in 0..12 {
        win.nudge();
        let f = cap.capture().expect("capture");
        let has_red = (0..f.width).step_by(4).any(|x| {
            (0..f.height).step_by(4).any(|y| {
                f.pixel(x, y)
                    .map(|p| p[2] > 150 && p[0] < 80)
                    .unwrap_or(false)
            })
        });
        if has_red {
            let model = letterbox_to_model(&f, 200, 150).expect("letterbox");
            recorder.record(&model).expect("record");
            recorded += 1;
            if recorded >= 6 {
                break;
            }
        }
    }
    let recorded = recorder.written();
    assert!(recorded >= 4, "need a few real frames, got {recorded}");

    // 2. cut a 40x40 TEXTURED template out of the first recorded frame:
    // the probe scene anchors its red rect at normalized (0.6,0.6)-(.8,.8)
    // -> in the 200x150 model frame that corner sits at (120,90); a window
    // at (100,70) captures the bg/rect edge (NCC is degenerate on flat
    // regions, so a two-color edge is what makes the match well-defined)
    let dir_reader = std::fs::read_dir(&dir).expect("dir");
    let mut pngs: Vec<_> = dir_reader
        .filter_map(|e| e.ok().map(|e| e.path()))
        .filter(|p| p.extension().map(|e| e == "png").unwrap_or(false))
        .collect();
    pngs.sort();
    let first = controller::replay::decode_png_bytes(&std::fs::read(&pngs[0]).expect("read"))
        .expect("decode");
    let mut template = Frame::new(40, 40);
    for y in 0..40u32 {
        for x in 0..40u32 {
            template.set_pixel(x, y, first.pixel(100 + x, 70 + y).unwrap_or([0, 0, 0, 255]));
        }
    }

    // 3. the L1 layer must keep finding it across every recorded frame
    let mut lp = LayeredPerception::new();
    let target = TemplateTarget {
        name: "probe_patch".into(),
        template,
        min_score: 0.9,
        stride: 2,
    };
    lp.add_template(target.clone());
    let mut matcher = NccTemplateMatcher;
    let mut matched_frames = 0u32;
    for p in &pngs {
        let frame =
            controller::replay::decode_png_bytes(&std::fs::read(p).expect("read")).expect("decode");
        let ev = lp.evaluate(&frame);
        if !ev.matches.is_empty() {
            matched_frames += 1;
        }
        // and through the matcher directly, same verdict
        let direct = matcher.find(&frame, &target.template, target.stride);
        assert_eq!(
            direct.is_some() && direct.unwrap().score >= target.min_score,
            !ev.matches.is_empty(),
            "L1 evidence and direct match must agree on {p:?}"
        );
    }
    assert_eq!(
        matched_frames, recorded,
        "a static scene must match in every recorded frame"
    );

    let _ = Path::new(&dir);
    let _ = std::fs::remove_dir_all(&dir);
}

#[test]
fn replay_backend_feeds_layered_perception_end_to_end() {
    // compose the new pieces: record synthetic frames, replay them as a
    // CaptureBackend, and run one pipeline cycle with L0+L1 perception
    use controller::safety::{SafetyConfig, SafetyGovernor};
    use controller::vision::MockDetector;
    use std::time::Instant;

    let dir = std::env::temp_dir().join(format!("nf_l1_replay_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);
    let mut rec = FrameRecorder::create(&dir, 4).expect("recorder");
    let mut src = SyntheticCapture::new(320, 240).expect("capture");
    for _ in 0..3 {
        let f = src.capture().expect("capture");
        rec.record(&f).expect("record");
    }

    let mut replay = controller::replay::ReplayCapture::open(&dir, true).expect("replay");
    let mut detector = MockDetector::synthetic_rect();
    let mut perception = LayeredPerception::new();
    perception.add_probe(PixelProbe {
        name: "anything".into(),
        x: 0,
        y: 0,
        w: 10,
        h: 10,
        expected: [0, 0, 0],
        tolerance: 255,
        min_fraction: 0.0 + 0.01,
        step: 2,
    });
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("gov");
    let calibrated = replay.calibrated_layout();
    let report = controller::pipeline::run_cycle(
        0,
        &mut replay,
        &mut detector,
        &mut governor,
        &calibrated,
        &calibrated,
        true,
        true,
        256,
        256,
        Some(&mut perception),
        Instant::now(),
    )
    .expect("cycle");
    assert!(report.allowed(), "replayed cycle must be clean: {report:?}");
    assert!(!report.evidence.probes.is_empty(), "L0 evidence must exist");
    assert_eq!(report.client_detections.len(), 1);
    let _ = std::fs::remove_dir_all(&dir);
}

#[test]
fn recorded_real_frames_drive_a_probe_skill_to_done() {
    // NC2+NC3 acceptance in miniature, on REAL content: record the probe
    // window, define an L0 probe over its anchored red rect, and require
    // the skill (expect: probe fired) to reach Done through replayed
    // cycles — offline, deterministic, input-free.
    ensure_dpi_awareness();
    if !controller::window::interactive_desktop_available() {
        println!("skipped: needs an interactive desktop for live GDI content (service session)");
        return;
    }
    let dir = std::env::temp_dir().join(format!("nf_probe_skill_{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&dir);

    // 1. record real frames of the probe window (red rect anchored at
    //    normalized (0.6,0.6)-(0.8,0.8) of the 400x300 client)
    let title = format!("NFCTRL-PROBE-SKILL-{}", std::process::id());
    let win = OwnedTestWindow::new(400, 300, &title).expect("probe window");
    let layout = controller::window::GameWindow::from_hwnd(win.hwnd)
        .expect("wrap")
        .layout()
        .expect("layout");
    let mut cap =
        controller::capture::GdiPrintWindowCapture::new(win.hwnd, layout).expect("gdi backend");
    let mut recorder = FrameRecorder::create(&dir, 6).expect("recorder");
    let mut warmup = 0;
    while recorder.written() < 3 && warmup < 12 {
        warmup += 1;
        win.nudge();
        let f = cap.capture().expect("capture");
        let has_red = (0..f.width).step_by(4).any(|x| {
            (0..f.height).step_by(4).any(|y| {
                f.pixel(x, y)
                    .map(|p| p[2] > 150 && p[0] < 80)
                    .unwrap_or(false)
            })
        });
        if has_red {
            recorder.record(&f).expect("record");
        }
    }
    let recorded = recorder.written();
    assert!(recorded >= 3, "need real frames, got {recorded}");

    // 2. a probe over the anchored red rect + a skill gated on it
    let mut perception = LayeredPerception::new();
    perception.add_probe(PixelProbe {
        name: "red_anchor".into(),
        x: 250,
        y: 190,
        w: 40,
        h: 40,
        expected: [16, 40, 200], // BGRA body of the anchor rect
        tolerance: 12,
        min_fraction: 0.8,
        step: 2,
    });
    let def = SkillDefinition::from_json(
        r#"{
            "name": "wait_anchor",
            "start": "watch",
            "states": [
                {
                    "name": "watch",
                    "expect": [{ "probe": "red_anchor" }],
                    "actions": ["acknowledge anchor"],
                    "next": "seen",
                    "timeout_ms": 60000
                },
                {
                    "name": "seen",
                    "terminal": true,
                    "expect": [],
                    "next": "seen",
                    "timeout_ms": 0
                }
            ]
        }"#,
    )
    .expect("skill");
    let mut runner = SkillRunner::start(def, 0);
    let mut replay = ReplayCapture::open(&dir, true).expect("replay");
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("gov");
    let calibrated = replay.calibrated_layout();
    let mut detector = controller::vision::MockDetector::synthetic_rect();
    let t0 = Instant::now();

    let mut reached_done = false;
    for cycle in 0..6u32 {
        let report = controller::pipeline::run_cycle(
            cycle,
            &mut replay,
            &mut detector,
            &mut governor,
            &calibrated,
            &calibrated,
            true,
            true,
            256,
            256,
            Some(&mut perception),
            t0 + Duration::from_millis(cycle as u64 * 50),
        )
        .expect("cycle");
        assert!(
            report.evidence.probe_fired("red_anchor"),
            "the anchored rect must fire the probe on real frames"
        );
        let fired = |name: &str| report.evidence.probe_fired(name);
        let dets: Vec<(String, f32)> = report
            .client_detections
            .iter()
            .map(|d| (d.label.clone(), d.confidence))
            .collect();
        let now_ms = (Instant::now() - t0).as_millis() as u64 + cycle as u64;
        match runner.step(now_ms, &fired, &dets) {
            StepOutcome::Done => {
                reached_done = true;
                break;
            }
            StepOutcome::Transitioned { to, planned } => {
                assert_eq!(to, "seen");
                assert_eq!(planned, vec!["acknowledge anchor".to_string()]);
                reached_done = true;
                break;
            }
            StepOutcome::Waiting => continue,
            other => panic!("unexpected outcome {other:?}"),
        }
    }
    assert!(reached_done, "the skill must complete on real evidence");
    let _ = std::fs::remove_dir_all(&dir);
}
