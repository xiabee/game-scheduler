//! Detector construction (NC1): decides WHICH detector the dry-run
//! pipeline runs and what geometry/threshold it runs with, based on the
//! optional `--model-path` manifest. The ONNX runtime itself lands in the
//! next NC1 milestone; this module already owns the degradation contract
//! from ROADMAP NC1 ("模型缺失→Mock"): a missing file, unreadable JSON or
//! invalid manifest degrades to the NC0 mock with a loud warning — it is
//! never a hard failure, and it never silently changes behavior (the
//! chosen source is always reported).

use crate::manifest::{self, ModelManifest};
use crate::vision::{Detector, MockDetector};
use std::path::Path;

/// What the pipeline is actually looking at, resolved from
/// `--model-path`. Reported in dry-run output and asserted in tests.
#[derive(Debug, Clone, PartialEq)]
pub enum DetectorSource {
    /// NC0 default: the synthetic color-class mock, no manifest involved.
    Mock,
    /// The manifest loaded and validated, but no usable weights were
    /// found next to it: degraded to the mock while honoring the
    /// manifest's imgsz/confidence contract.
    ManifestPending {
        path: String,
        name: String,
        version: String,
        labels: usize,
    },
    /// A model path was requested but could not be used (missing file,
    /// bad JSON, invalid manifest): degraded to the mock with the reason.
    MockFallback { path: String, reason: String },
    /// The ONNX weights were found and the WinML runtime session came up:
    /// the pipeline runs REAL inference (CPU device).
    Onnx { path: String, name: String },
}

/// The resolved detector plus the geometry/threshold the session runs at.
pub struct DetectorChoice {
    pub detector: Box<dyn Detector>,
    pub source: DetectorSource,
    /// Model input size (letterbox target). Manifest value wins over the
    /// CLI default, but an explicit `--model` wins over the manifest.
    pub imgsz: (u32, u32),
    /// Confidence threshold for the governor. Same precedence as `imgsz`.
    pub min_confidence: f32,
}

/// Inputs for [`resolve`]: everything the CLI knows about the model.
pub struct DetectorRequest<'a> {
    /// `--model-path` value, if the operator passed one.
    pub model_path: Option<&'a str>,
    /// `--model` (imgsz) as parsed by the CLI (default 256).
    pub imgsz: (u32, u32),
    /// True when the operator explicitly passed `--model`.
    pub imgsz_explicit: bool,
    /// `--min-confidence` as parsed by the CLI (default 0.6).
    pub min_confidence: f32,
    /// True when the operator explicitly passed `--min-confidence`.
    pub confidence_explicit: bool,
}

impl<'a> DetectorRequest<'a> {
    /// The CLI defaults when no model path is configured.
    pub fn cli_only(imgsz: (u32, u32), min_confidence: f32) -> DetectorRequest<'a> {
        DetectorRequest {
            model_path: None,
            imgsz,
            imgsz_explicit: true,
            min_confidence,
            confidence_explicit: true,
        }
    }
}

/// Resolve the detector for this session. Never fails: every bad-model
/// path degrades to the NC0 mock (that is the ROADMAP NC1 contract), so
/// the dry-run keeps observing instead of dying on a typo'd manifest.
pub fn resolve(req: &DetectorRequest) -> DetectorChoice {
    let Some(path) = req.model_path.filter(|p| !p.trim().is_empty()) else {
        return DetectorChoice {
            detector: Box::new(MockDetector::synthetic_rect()),
            source: DetectorSource::Mock,
            imgsz: req.imgsz,
            min_confidence: req.min_confidence,
        };
    };

    let manifest = match manifest::load(Path::new(path)) {
        Ok(m) => m,
        Err(reason) => {
            return fallback(path, reason, req);
        }
    };
    // kept intact for the ONNX session below (the destructure moves fields)
    let manifest_for_open = manifest.clone();
    let ModelManifest {
        name,
        version,
        labels,
        input_size,
        default_confidence,
        weights,
        ..
    } = manifest;

    // Real inference: when the manifest references weights and the file
    // exists next to it, bring up the WinML ONNX session. Any failure on
    // that path degrades to the mock loudly (never a hard error).
    if let Some(weights_ref) = weights.as_deref() {
        let weights_path = std::path::Path::new(path)
            .parent()
            .unwrap_or(std::path::Path::new("."))
            .join(weights_ref);
        if weights_path.is_file() {
            match crate::onnx::OnnxDetector::open(&manifest_for_open, &weights_path) {
                Ok(detector) => {
                    let m = &manifest_for_open;
                    return DetectorChoice {
                        detector: Box::new(detector),
                        source: DetectorSource::Onnx {
                            path: weights_path.display().to_string(),
                            name: m.name.clone(),
                        },
                        imgsz: if req.imgsz_explicit {
                            req.imgsz
                        } else {
                            (input_size.width, input_size.height)
                        },
                        min_confidence: if req.confidence_explicit {
                            req.min_confidence
                        } else {
                            m.default_confidence
                        },
                    };
                }
                Err(e) => {
                    return fallback(path, format!("onnx runtime init failed: {e}"), req);
                }
            }
        }
    }

    DetectorChoice {
        // No usable weights: manifest contract still governs
        // geometry/threshold, perception stays on the NC0 mock.
        detector: Box::new(MockDetector::synthetic_rect()),
        source: DetectorSource::ManifestPending {
            path: path.to_string(),
            name,
            version,
            labels: labels.len(),
        },
        imgsz: if req.imgsz_explicit {
            req.imgsz
        } else {
            (input_size.width, input_size.height)
        },
        min_confidence: if req.confidence_explicit {
            req.min_confidence
        } else {
            default_confidence
        },
    }
}

/// Degrade to the mock with the given reason, keeping CLI geometry.
fn fallback(path: &str, reason: String, req: &DetectorRequest) -> DetectorChoice {
    DetectorChoice {
        detector: Box::new(MockDetector::synthetic_rect()),
        source: DetectorSource::MockFallback {
            path: path.to_string(),
            reason,
        },
        imgsz: req.imgsz,
        min_confidence: req.min_confidence,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn good_manifest_text() -> String {
        r#"{
            "schema_version": 1,
            "name": "probe-ui-nano",
            "version": "1.2.3",
            "game_profile": "generic-ui",
            "input_size": { "width": 320, "height": 320 },
            "labels": ["button_green", "dialog"],
            "default_confidence": 0.75
        }"#
        .to_string()
    }

    fn write_manifest(dir: &Path, text: &str) -> String {
        std::fs::create_dir_all(dir).expect("mkdir");
        let path = dir.join("model.manifest.json");
        std::fs::write(&path, text).expect("write");
        path.to_string_lossy().into_owned()
    }

    fn req<'a>(
        model_path: Option<&'a str>,
        imgsz_explicit: bool,
        confidence_explicit: bool,
    ) -> DetectorRequest<'a> {
        DetectorRequest {
            model_path,
            imgsz: (256, 256),
            imgsz_explicit,
            min_confidence: 0.6,
            confidence_explicit,
        }
    }

    #[test]
    fn without_model_path_the_mock_stands_alone() {
        let mut c = resolve(&req(None, true, true));
        assert_eq!(c.source, DetectorSource::Mock);
        assert_eq!(c.imgsz, (256, 256));
        assert!((c.min_confidence - 0.6).abs() < 1e-6);
        // the detector still works: it finds the synthetic red box
        let mut f = crate::frame::Frame::new(64, 64);
        for y in 4..20 {
            for x in 8..24 {
                f.set_pixel(x, y, [10, 40, 200, 255]);
            }
        }
        let dets = c.detector.detect(&f);
        assert_eq!(dets.len(), 1);
        assert_eq!(dets[0].label, "moving_rect");
    }

    #[test]
    fn blank_model_path_behaves_like_none() {
        let c = resolve(&req(Some("   "), true, true));
        assert_eq!(c.source, DetectorSource::Mock);
    }

    #[test]
    fn missing_file_degrades_to_mock_with_reason() {
        let c = resolve(&req(Some("Z:/nope/missing.json"), true, true));
        match c.source {
            DetectorSource::MockFallback { path, reason } => {
                assert!(path.contains("missing.json"));
                assert!(reason.contains("cannot read"), "{reason}");
            }
            other => panic!("expected MockFallback, got {other:?}"),
        }
        assert_eq!(c.imgsz, (256, 256), "CLI geometry kept on fallback");
    }

    #[test]
    fn invalid_json_degrades_with_reason() {
        let dir = std::env::temp_dir().join(format!("nf_infer_{}", std::process::id()));
        let path = write_manifest(&dir, "{ not json ");
        let c = resolve(&req(Some(&path), true, true));
        match c.source {
            DetectorSource::MockFallback { reason, .. } => {
                assert!(reason.contains("invalid JSON"), "{reason}");
            }
            other => panic!("expected MockFallback, got {other:?}"),
        }
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn invalid_manifest_values_degrade_with_reason() {
        let dir = std::env::temp_dir().join(format!("nf_infer2_{}", std::process::id()));
        let bad = good_manifest_text().replace("\"schema_version\": 1", "\"schema_version\": 2");
        let path = write_manifest(&dir, &bad);
        let c = resolve(&req(Some(&path), true, true));
        match c.source {
            DetectorSource::MockFallback { reason, .. } => {
                assert!(reason.contains("schema_version"), "{reason}");
            }
            other => panic!("expected MockFallback, got {other:?}"),
        }
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn valid_manifest_is_pending_runtime_and_honors_contract() {
        let dir = std::env::temp_dir().join(format!("nf_infer3_{}", std::process::id()));
        let path = write_manifest(&dir, &good_manifest_text());
        let mut r = req(Some(&path), false, false);
        r.imgsz_explicit = false;
        r.confidence_explicit = false;
        let c = resolve(&r);
        match &c.source {
            DetectorSource::ManifestPending {
                path: p,
                name,
                version,
                labels,
            } => {
                assert!(p.ends_with("model.manifest.json"));
                assert_eq!(name, "probe-ui-nano");
                assert_eq!(version, "1.2.3");
                assert_eq!(*labels, 2);
            }
            other => panic!("expected ManifestPending, got {other:?}"),
        }
        // manifest geometry/threshold win when the CLI did not override
        assert_eq!(c.imgsz, (320, 320));
        assert!((c.min_confidence - 0.75).abs() < 1e-6);
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn explicit_cli_flags_win_over_the_manifest() {
        let dir = std::env::temp_dir().join(format!("nf_infer4_{}", std::process::id()));
        let path = write_manifest(&dir, &good_manifest_text());
        let c = resolve(&req(Some(&path), true, true)); // both explicit
        assert_eq!(c.imgsz, (256, 256), "explicit --model beats manifest");
        assert!(
            (c.min_confidence - 0.6).abs() < 1e-6,
            "explicit --min-confidence beats manifest"
        );
        let _ = std::fs::remove_file(&path);
    }
}
