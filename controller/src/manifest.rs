//! Model manifest (NC1): the metadata sidecar that travels with every
//! exported ONNX model (ROADMAP §3 NC1, §4). Weights never enter Git — the
//! manifest is what the runtime trusts for labels, input geometry and the
//! default confidence threshold, so it is validated strictly before any
//! detector is built from it.
//!
//! Schema v1 (JSON):
//! ```json
//! {
//!   "schema_version": 1,
//!   "name": "probe-ui-nano",
//!   "version": "0.1.0",
//!   "game_profile": "generic-ui",
//!   "input_size": { "width": 256, "height": 256 },
//!   "labels": ["moving_rect"],
//!   "default_confidence": 0.6,
//!   "weights": "probe-ui-nano.onnx",
//!   "notes": "optional free text"
//! }
//! ```
//! `weights` is an optional external reference (external storage, never the
//! repo); its presence is not checked here — inference (next NC1 milestone)
//! decides what it does about a missing file.

use serde::Deserialize;
use std::path::Path;

/// Maximum labels per model: a UI-object detector needs tens, not
/// thousands; the cap keeps a typo'd file from claiming absurd sizes.
pub const MAX_LABELS: usize = 1024;

/// Model input geometry in pixels. Bounds mirror the dry-run `--model`
/// flag (1..=4096) so a manifest can never smuggle in an insane imgsz.
#[derive(Debug, Clone, Copy, Deserialize, PartialEq, Eq)]
pub struct InputSize {
    pub width: u32,
    pub height: u32,
}

/// The parsed and validated manifest. Construct only through
/// [`parse_json`] / [`load`] — validation lives there, not in `Default`.
#[derive(Debug, Clone, Deserialize, PartialEq)]
pub struct ModelManifest {
    /// On-disk schema version; only 1 exists today. Future versions must
    /// be opt-in: an unknown version is rejected, not guessed at.
    pub schema_version: u32,
    /// Human/model name (e.g. `probe-ui-nano`).
    pub name: String,
    /// Model version string (not the schema version).
    pub version: String,
    /// Which game/profile this model targets (e.g. `generic-ui`).
    pub game_profile: String,
    /// Model input size (imgsz) the inference must letterbox to.
    pub input_size: InputSize,
    /// Class names, index-aligned with model output rows.
    pub labels: Vec<String>,
    /// Default detection confidence threshold in (0, 1]; the operator can
    /// still override it per run.
    pub default_confidence: f32,
    /// Optional reference to the external weights file (path or URI).
    #[serde(default)]
    pub weights: Option<String>,
    /// Optional free-form notes.
    #[serde(default)]
    pub notes: Option<String>,
}

impl ModelManifest {
    /// Field-level validation over an already-parsed manifest. Kept
    /// separate from `Deserialize` so deserialization errors (shape) and
    /// semantic errors (values) produce distinct, precise messages.
    pub fn validate(&self) -> Result<(), String> {
        if self.schema_version != 1 {
            return Err(format!(
                "unsupported schema_version {} (only 1 exists)",
                self.schema_version
            ));
        }
        for (field, value) in [
            ("name", &self.name),
            ("version", &self.version),
            ("game_profile", &self.game_profile),
        ] {
            if value.trim().is_empty() {
                return Err(format!("{field} must be a non-blank string"));
            }
        }
        let s = self.input_size;
        if s.width == 0 || s.height == 0 {
            return Err(format!(
                "input_size must be positive, got {}x{}",
                s.width, s.height
            ));
        }
        if s.width > 4096 || s.height > 4096 {
            return Err(format!(
                "input_size must be <= 4096 per side, got {}x{}",
                s.width, s.height
            ));
        }
        if self.labels.is_empty() {
            return Err("labels must contain at least one entry".into());
        }
        if self.labels.len() > MAX_LABELS {
            return Err(format!(
                "labels must contain at most {MAX_LABELS} entries, got {}",
                self.labels.len()
            ));
        }
        for (i, label) in self.labels.iter().enumerate() {
            if label.trim().is_empty() {
                return Err(format!("labels[{i}] is blank"));
            }
        }
        let mut seen = std::collections::HashSet::new();
        for label in &self.labels {
            if !seen.insert(label.as_str()) {
                return Err(format!("duplicate label {label:?}"));
            }
        }
        if !(self.default_confidence > 0.0 && self.default_confidence <= 1.0) {
            return Err(format!(
                "default_confidence must be in (0, 1], got {}",
                self.default_confidence
            ));
        }
        if let Some(w) = &self.weights {
            if w.trim().is_empty() {
                return Err("weights, when present, must be a non-blank reference".into());
            }
        }
        Ok(())
    }

    /// The imgsz as a `(width, height)` tuple (pipeline convention).
    pub fn imgsz(&self) -> (u32, u32) {
        (self.input_size.width, self.input_size.height)
    }
}

/// Parse and validate a manifest from its JSON text.
pub fn parse_json(text: &str) -> Result<ModelManifest, String> {
    let m: ModelManifest = serde_json::from_str(text).map_err(|e| format!("invalid JSON: {e}"))?;
    m.validate().map_err(|e| format!("invalid manifest: {e}"))?;
    Ok(m)
}

/// Load, parse and validate the manifest at `path`.
pub fn load(path: &Path) -> Result<ModelManifest, String> {
    let text = std::fs::read_to_string(path)
        .map_err(|e| format!("cannot read {}: {e}", path.display()))?;
    parse_json(&text).map_err(|e| format!("{}: {e}", path.display()))
}

#[cfg(test)]
mod tests {
    use super::*;

    const GOOD: &str = r#"{
        "schema_version": 1,
        "name": "probe-ui-nano",
        "version": "0.1.0",
        "game_profile": "generic-ui",
        "input_size": { "width": 256, "height": 256 },
        "labels": ["moving_rect"],
        "default_confidence": 0.6,
        "weights": "probe-ui-nano.onnx"
    }"#;

    #[test]
    fn parses_and_validates_a_good_manifest() {
        let m = parse_json(GOOD).expect("good manifest");
        assert_eq!(m.name, "probe-ui-nano");
        assert_eq!(m.imgsz(), (256, 256));
        assert_eq!(m.labels, vec!["moving_rect"]);
        assert!((m.default_confidence - 0.6).abs() < 1e-6);
        assert_eq!(m.weights.as_deref(), Some("probe-ui-nano.onnx"));
    }

    #[test]
    fn unknown_fields_are_tolerated_for_forward_compatibility() {
        let text = GOOD.replace(
            "\"weights\": \"probe-ui-nano.onnx\"",
            "\"weights\": \"probe-ui-nano.onnx\",\n\"future_field\": 42",
        );
        parse_json(&text).expect("unknown fields must not break v1 parsing");
    }

    #[test]
    fn optional_fields_default_to_none() {
        let text = GOOD.replace(",\n        \"weights\": \"probe-ui-nano.onnx\"", "");
        let m = parse_json(&text).expect("manifest without weights");
        assert!(m.weights.is_none() && m.notes.is_none());
    }

    #[test]
    fn schema_version_is_strict() {
        for v in [0, 2, 999] {
            let text = GOOD.replace("\"schema_version\": 1", &format!("\"schema_version\": {v}"));
            let err = parse_json(&text).unwrap_err();
            assert!(err.contains("schema_version"), "v{v}: {err}");
        }
    }

    #[test]
    fn blank_core_strings_are_rejected() {
        for field in ["name", "version", "game_profile"] {
            let mut m = parse_json(GOOD).unwrap();
            match field {
                "name" => m.name = "  ".into(),
                "version" => m.version = "".into(),
                "game_profile" => m.game_profile = "\t".into(),
                _ => unreachable!(),
            }
            let err = m.validate().unwrap_err();
            assert!(err.contains(field), "{field}: {err}");
        }
    }

    #[test]
    fn input_size_bounds_are_enforced() {
        let mut m = parse_json(GOOD).unwrap();
        m.input_size = InputSize {
            width: 0,
            height: 256,
        };
        assert!(m.validate().unwrap_err().contains("positive"));
        m.input_size = InputSize {
            width: 5000,
            height: 256,
        };
        assert!(m.validate().unwrap_err().contains("4096"));
        m.input_size = InputSize {
            width: 4096,
            height: 4096,
        };
        m.validate().expect("boundary accepted");
    }

    #[test]
    fn label_rules_are_enforced() {
        let mut m = parse_json(GOOD).unwrap();
        m.labels = vec![];
        assert!(m.validate().unwrap_err().contains("at least one"));

        m.labels = vec!["a".into(), "a".into()];
        assert!(m.validate().unwrap_err().contains("duplicate"));

        m.labels = vec!["  ".into()];
        assert!(m.validate().unwrap_err().contains("blank"));

        m.labels = (0..MAX_LABELS + 1).map(|i| format!("l{i}")).collect();
        assert!(m.validate().unwrap_err().contains("at most"));
    }

    #[test]
    fn confidence_bounds_are_enforced() {
        let mut m = parse_json(GOOD).unwrap();
        for bad in [0.0, -0.1, 1.01, f32::NAN] {
            m.default_confidence = bad;
            assert!(m.validate().is_err(), "{bad} must be rejected");
        }
        m.default_confidence = 1.0;
        m.validate().expect("1.0 is the inclusive upper bound");
    }

    #[test]
    fn blank_weights_reference_is_rejected() {
        let mut m = parse_json(GOOD).unwrap();
        m.weights = Some(" ".into());
        assert!(m.validate().unwrap_err().contains("weights"));
    }

    #[test]
    fn load_reports_missing_files_clearly() {
        let err = load(Path::new("Z:/definitely/missing/manifest.json")).unwrap_err();
        assert!(err.contains("cannot read"), "{err}");
    }

    #[test]
    fn load_reads_from_disk() {
        let dir = std::env::temp_dir().join(format!("nf_manifest_{}", std::process::id()));
        std::fs::create_dir_all(&dir).expect("mkdir");
        let path = dir.join("m.json");
        std::fs::write(&path, GOOD).expect("write");
        let m = load(&path).expect("load");
        assert_eq!(m.name, "probe-ui-nano");
        let _ = std::fs::remove_file(&path);
    }
}
