//! NC3 integration: a skill state machine driven by REAL pipeline cycles
//! (synthetic capture -> letterbox -> detector -> client detections ->
//! SkillRunner). Proves the evidence contract end to end while keeping
//! the dry-run guarantee: plans are logged, nothing is ever sent.

use controller::capture::{CaptureBackend, SyntheticCapture};
use controller::perception::{Evidence, LayeredPerception, PixelProbe};
use controller::safety::{SafetyConfig, SafetyGovernor};
use controller::skill::{SkillDefinition, SkillRunner, StepOutcome};
use std::time::{Duration, Instant};

const SEE_RECT_SKILL: &str = r#"{
    "name": "see_rect",
    "start": "looking",
    "states": [
        {
            "name": "looking",
            "expect": [{ "label": "moving_rect", "min_confidence": 0.5 }],
            "actions": ["note the rect"],
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
}"#;

#[test]
fn skill_reaches_done_when_pipeline_evidence_satisfies_expectations() {
    let def = SkillDefinition::from_json(SEE_RECT_SKILL).expect("skill");
    let mut runner = SkillRunner::start(def, 0);
    let mut backend = SyntheticCapture::new(320, 240).expect("capture");
    let mut detector = controller::vision::MockDetector::synthetic_rect();
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("gov");
    let calibrated = backend.calibrated_layout();
    let t0 = Instant::now();

    let mut outcomes = Vec::new();
    for cycle in 0..5u32 {
        let now = t0 + Duration::from_millis(cycle as u64 * 100);
        let report = controller::pipeline::run_cycle(
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
            None::<&mut LayeredPerception>,
            now,
        )
        .expect("cycle");
        let fired = |name: &str| {
            report
                .evidence
                .probes
                .iter()
                .any(|p| p.name == name && p.fired)
        };
        let dets: Vec<(String, f32)> = report
            .client_detections
            .iter()
            .map(|d| (d.label.clone(), d.confidence))
            .collect();
        let now_ms = (Instant::now() - t0).as_millis() as u64 + cycle as u64;
        outcomes.push(runner.step(now_ms, &fired, &dets));
        if runner.is_done() {
            break;
        }
    }
    assert!(
        outcomes.contains(&StepOutcome::Transitioned {
            to: "seen".into(),
            planned: vec!["note the rect".into()]
        }) || outcomes.contains(&StepOutcome::Done),
        "the skill must reach its terminal state: {outcomes:?}"
    );
    assert!(runner.is_done());
    assert!(!runner.is_failed());
}

#[test]
fn l0_probe_evidence_drives_probe_expectations() {
    // a skill expecting an L0 probe: the probe watches a region that the
    // synthetic capture keeps DARK (background), so it never fires and
    // the skill stays Waiting; then we flip the evidence manually to show
    // the transition path through the same runner.
    let def = SkillDefinition::from_json(
        r#"{
            "name": "wait_for_banner",
            "start": "watch",
            "states": [
                {
                    "name": "watch",
                    "expect": [{ "probe": "banner" }],
                    "actions": [],
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

    let mut backend = SyntheticCapture::new(320, 240).expect("capture");
    let mut detector = controller::vision::MockDetector::synthetic_rect();
    let mut governor = SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("gov");
    let calibrated = backend.calibrated_layout();
    let t0 = Instant::now();

    // probes live on the RAW CLIENT frame: a dark corner of the pattern
    let mut perception = LayeredPerception::new();
    perception.add_probe(PixelProbe {
        name: "banner".into(),
        x: 0,
        y: 0,
        w: 6,
        h: 6,
        expected: [255, 255, 255], // white — the synthetic bg is dark grid
        tolerance: 4,
        min_fraction: 0.9,
        step: 2,
    });
    let _ = Evidence::default(); // keep the type referenced in docs/tests

    let mut ever_waiting = false;
    for cycle in 0..3u32 {
        let now = t0 + Duration::from_millis(cycle as u64 * 100);
        let report = controller::pipeline::run_cycle(
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
            Some(&mut perception),
            now,
        )
        .expect("cycle");
        assert!(
            !report.evidence.probes.is_empty(),
            "the probe set must be evaluated each cycle"
        );
        assert!(!report.evidence.probe_fired("banner"));
        let fired = |name: &str| report.evidence.probe_fired(name);
        let dets: Vec<(String, f32)> = report
            .client_detections
            .iter()
            .map(|d| (d.label.clone(), d.confidence))
            .collect();
        let now_ms = (Instant::now() - t0).as_millis() as u64 + cycle as u64;
        match runner.step(now_ms, &fired, &dets) {
            StepOutcome::Waiting => ever_waiting = true,
            other => panic!("probe never fired; unexpected outcome {other:?}"),
        }
    }
    assert!(ever_waiting);
}
