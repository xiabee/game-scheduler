//! NC1 runtime acceptance: the fixture ONNX model through the REAL WinML
//! path (load → session → bind → evaluate → tensor parse → decode), plus
//! the resolution-independence contract (ROADMAP NC1 acceptance: the same
//! model detects consistently across window sizes once letterboxed).
//! These tests run live on Windows; WinML ships with the OS and the
//! session is pinned to the CPU device.

mod common;

use common::ensure_fixture;
use controller::vision::Detector;
use std::path::Path;

fn open_detector() -> (controller::onnx::OnnxDetector, std::path::PathBuf) {
    let (manifest_path, weights_path) = ensure_fixture();
    let manifest = controller::manifest::load(&manifest_path).expect("fixture manifest");
    let det = controller::onnx::OnnxDetector::open(&manifest, Path::new(&weights_path))
        .expect("winml session");
    (det, weights_path)
}

#[test]
fn winml_loads_the_fixture_and_decodes_the_constant_rows() {
    let (mut det, _w) = open_detector();
    // content is irrelevant: the fixture's output rows are constants
    let frame = controller::frame::Frame::new(64, 64);
    let dets = det.detect(&frame);
    assert!(det.take_error().is_none(), "no inference error expected");
    assert_eq!(
        dets.len(),
        1,
        "only the 0.9-confidence row survives the 0.6 gate: {dets:?}"
    );
    let d = &dets[0];
    assert_eq!(d.label, "rect_a");
    assert!(
        (d.rect.x - 22.0).abs() < 1e-5,
        "cx - w/2 = 32 - 10, got {}",
        d.rect.x
    );
    assert!(
        (d.rect.y - 11.0).abs() < 1e-5,
        "cy - h/2 = 16 - 5, got {}",
        d.rect.y
    );
    assert_eq!(d.rect.w, 20.0);
    assert_eq!(d.rect.h, 10.0);
    assert!((d.confidence - 0.9).abs() < 1e-6);
}

#[test]
fn confidence_gate_follows_the_manifest_value() {
    let (manifest_path, weights_path) = ensure_fixture();
    // lower the gate to 0.3: both constant rows become detections
    let text =
        common::MANIFEST_JSON.replace("\"default_confidence\": 0.6", "\"default_confidence\": 0.3");
    let alt_path = common::fixtures_dir().join("constant_yolo_low_gate.manifest.json");
    std::fs::write(&alt_path, &text).expect("write alt manifest");
    let manifest = controller::manifest::load(&alt_path).expect("alt manifest");
    let mut det = controller::onnx::OnnxDetector::open(&manifest, Path::new(&weights_path))
        .expect("winml session");
    let dets = det.detect(&controller::frame::Frame::new(64, 64));
    assert!(det.take_error().is_none());
    assert_eq!(dets.len(), 2, "both rows visible at 0.3: {dets:?}");
    assert_eq!(dets[0].label, "rect_a");
    assert_eq!(dets[1].label, "rect_b");
    let _ = std::fs::remove_file(&alt_path);
    let _ = manifest_path;
}

#[test]
fn the_same_model_maps_consistently_across_window_sizes() {
    // ROADMAP NC1 acceptance, deterministic form: one model + several
    // SAME-ASPECT client sizes → the constant detection maps back to the
    // same normalized client position, with client geometry scaling
    // linearly (letterbox pad cancels in the inverse chain).
    use controller::transform::Transform;
    let (manifest_path, _weights_path) = ensure_fixture();
    let manifest = controller::manifest::load(&manifest_path).expect("manifest");
    let (w, h) = manifest.imgsz();

    // the fixture's surviving row: rect (22,11,20,10), center (32,16)
    let rect = controller::transform::Rect::new(22.0, 11.0, 20.0, 10.0);
    for client in [(320u32, 240u32), (640, 480), (1280, 960)] {
        let layout = controller::window::WindowLayout {
            client_size: (client.0 as i32, client.1 as i32),
            screen_origin: (0, 0),
            dpi: 96,
        };
        let transform = Transform::new(&layout, w, h).expect("transform");
        let client_rect = transform.rect_model_to_client(rect).expect("map");
        let (cx, cy) = client_rect.center();
        let nx = cx / client.0 as f32;
        let ny = cy / client.1 as f32;
        assert!(
            (nx - 0.5).abs() < 0.01 && (ny - 1.0 / 6.0).abs() < 0.01,
            "{client:?}: normalized ({nx}, {ny}) must stay (0.5, 1/6)"
        );
        // the box occupies the same fraction of the client at every size
        let wf = client_rect.w / client.0 as f32;
        let hf = client_rect.h / client.1 as f32;
        assert!(
            (wf - 0.3125).abs() < 0.01 && (hf - 0.2083).abs() < 0.01,
            "{client:?}: box fraction ({wf}, {hf}) must stay (0.3125, 0.2083)"
        );
    }
}

#[test]
fn resolve_wires_the_onnx_detector_when_weights_exist() {
    let (manifest_path, _w) = ensure_fixture();
    let choice = controller::inference::resolve(&controller::inference::DetectorRequest {
        model_path: Some(manifest_path.to_str().expect("utf8")),
        imgsz: (256, 256),
        imgsz_explicit: false,
        min_confidence: 0.6,
        confidence_explicit: false,
    });
    match &choice.source {
        controller::inference::DetectorSource::Onnx { path, name } => {
            assert!(path.ends_with("constant_yolo.onnx"), "{path}");
            assert_eq!(name, "constant-yolo-fixture");
        }
        other => panic!("expected Onnx source, got {other:?}"),
    }
    // manifest geometry won (CLI did not override)
    assert_eq!(choice.imgsz, (64, 64));
}

#[test]
fn onnx_detector_flows_through_a_full_dry_run_cycle() {
    use controller::capture::{CaptureBackend, SyntheticCapture};
    use controller::safety::{SafetyConfig, SafetyGovernor};
    use std::time::Instant;

    let (_manifest_path, weights_path) = ensure_fixture();
    let manifest = controller::manifest::load(&common::manifest_path()).expect("manifest");
    let mut detector =
        controller::onnx::OnnxDetector::open(&manifest, Path::new(&weights_path)).expect("winml");
    let mut backend = SyntheticCapture::new(320, 240).expect("capture");
    let mut governor =
        SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("governor");
    let calibrated = backend.calibrated_layout();
    let report = controller::pipeline::run_cycle(
        0,
        &mut backend,
        &mut detector,
        &mut governor,
        &calibrated,
        &calibrated,
        true,
        true,
        64,
        64,
        Instant::now(),
    )
    .expect("cycle");
    assert!(
        report.allowed(),
        "happy path with real inference: {report:?}"
    );
    assert_eq!(report.client_detections.len(), 1);
    assert_eq!(report.client_detections[0].label, "rect_a");
    assert_eq!(detector.take_error(), None);
}
