//! ONNX inference runtime (NC1) on WinML — the ONNX runtime Windows ships
//! with (ROADMAP NC1: "Windows 优先 WinML 或 ONNX Runtime"). No external
//! runtime download, no Python, no GPU provider: evaluation is pinned to
//! the CPU device so a night run never competes for the GPU.
//!
//! Decode contract for tonight: models export rows `[x_center, y_center,
//! width, height, confidence, class_id]` shaped `[1, N, >=6]` (a YOLO-style
//! flattened head). YOLOv8 anchor-free decode + NMS is deferred to the
//! perception-stack milestone; the fixture model in tests/fixtures is a
//! constant-output stand-in that exercises the REAL load→session→bind→
//! evaluate→parse path end to end.
//!
//! Red lines (ROADMAP §7): OS-provided inference over captured frames
//! only — no process injection, no game memory access.

use crate::frame::Frame;
use crate::manifest::ModelManifest;
use crate::transform::Rect;
use crate::vision::{Detection, Detector};
use crate::Result;
use std::path::Path;
use windows::core::{Interface, HSTRING};

/// An ONNX model executed through WinML. Construct via [`OnnxDetector::open`];
/// it implements [`Detector`] so the NC0 pipeline needs no changes.
pub struct OnnxDetector {
    session: windows::AI::MachineLearning::LearningModelSession,
    input_names: Vec<String>,
    output_name: String,
    imgsz: (u32, u32),
    labels: Vec<String>,
    confidence: f32,
    /// First evaluation/inference error, for honest reporting: a silent
    /// empty detection list must be distinguishable from "nothing there".
    last_error: Option<String>,
}

impl OnnxDetector {
    /// Load `weights_path` as an ONNX model and build a CPU session.
    /// `manifest` supplies labels/imgsz/threshold — the model must match
    /// the manifest's imgsz or binding will fail loudly.
    pub fn open(manifest: &ModelManifest, weights_path: &Path) -> Result<OnnxDetector> {
        use windows::AI::MachineLearning::{
            LearningModel, LearningModelDevice, LearningModelDeviceKind, LearningModelSession,
        };

        let canonical = std::fs::canonicalize(weights_path).map_err(|e| {
            crate::ControllerError::InvalidInput(format!("weights {}: {e}", weights_path.display()))
        })?;
        let path_h = HSTRING::from(canonical.as_os_str().to_string_lossy().as_ref());
        let model = LearningModel::LoadFromFilePath(&path_h)?;

        // CPU device on purpose: deterministic, RDP-safe, GPU left alone.
        let device = LearningModelDevice::Create(LearningModelDeviceKind::Cpu)?;
        let session = LearningModelSession::CreateFromModelOnDevice(&model, &device)?;

        let input_names = model
            .InputFeatures()?
            .into_iter()
            .map(|f| f.Name().map(|n| n.to_string()))
            .collect::<windows::core::Result<Vec<String>>>()?;
        if input_names.is_empty() {
            return Err(crate::ControllerError::InvalidInput(format!(
                "model {} declares no inputs",
                weights_path.display()
            )));
        }
        let output_name = model
            .OutputFeatures()?
            .into_iter()
            .next()
            .map(|f| f.Name().map(|n| n.to_string()))
            .transpose()?
            .ok_or_else(|| {
                crate::ControllerError::InvalidInput(format!(
                    "model {} declares no outputs",
                    weights_path.display()
                ))
            })?;

        Ok(OnnxDetector {
            session,
            input_names,
            output_name,
            imgsz: manifest.imgsz(),
            labels: manifest.labels.clone(),
            confidence: manifest.default_confidence,
            last_error: None,
        })
    }

    /// BGRA8 model-space frame → RGB f32/255 NCHW [1,3,h,w].
    fn input_tensor(&self, frame: &Frame) -> Result<windows::AI::MachineLearning::TensorFloat> {
        use windows::AI::MachineLearning::TensorFloat;
        let (w, h) = (frame.width, frame.height);
        let mut data = Vec::with_capacity((3 * w * h) as usize);
        // planes: R, then G, then B (BGRA pixels: [2]=r, [1]=g, [0]=b)
        for plane in [2usize, 1, 0] {
            for y in 0..h {
                for x in 0..w {
                    let px = frame.pixel(x, y).unwrap_or([0, 0, 0, 255]);
                    data.push(px[plane] as f32 / 255.0);
                }
            }
        }
        let tensor =
            TensorFloat::CreateFromShapeArrayAndDataArray(&[1, 3, h as i64, w as i64], &data)?;
        Ok(tensor)
    }

    /// Evaluate once and return the raw output floats plus their shape.
    fn evaluate(&mut self, frame: &Frame) -> Result<(Vec<f32>, Vec<i64>)> {
        use windows::AI::MachineLearning::{LearningModelBinding, TensorFloat};
        let tensor = self.input_tensor(frame)?;
        let binding = LearningModelBinding::CreateFromSession(&self.session)?;
        for name in &self.input_names {
            binding.Bind(&HSTRING::from(name.as_str()), &tensor)?;
        }
        // windows-rs exposes the synchronous Evaluate overload; it blocks
        // until the CPU session finishes.
        let result = self.session.Evaluate(&binding, &HSTRING::new())?;
        let outputs = result.Outputs()?;
        let value = outputs.Lookup(&HSTRING::from(self.output_name.as_str()))?;
        let out: TensorFloat = value.cast()?;
        let view = out.GetAsVectorView()?;
        let data: Vec<f32> = view.into_iter().collect();
        let shape: Vec<i64> = out.Shape()?.into_iter().collect();
        Ok((data, shape))
    }

    /// The first recorded inference error, if any (and clear it).
    pub fn take_error(&mut self) -> Option<String> {
        self.last_error.take()
    }

    /// Decode `[1, N, >=6]` rows of (cx, cy, w, h, conf, class) into
    /// detections, dropping rows below the manifest threshold.
    fn decode(&self, data: &[f32], shape: &[i64]) -> Vec<Detection> {
        let mut out = Vec::new();
        if shape.len() != 3 || shape[2] < 6 {
            return out;
        }
        let (rows, stride) = (shape[1] as usize, shape[2] as usize);
        for r in 0..rows {
            let row = &data[r * stride..(r + 1) * stride];
            let conf = row[4];
            if !(conf.is_finite() && conf >= self.confidence) {
                continue;
            }
            let (cx, cy, w, h) = (row[0], row[1], row[2], row[3]);
            if !(cx.is_finite() && cy.is_finite() && w.is_finite() && h.is_finite()) {
                continue;
            }
            let class_id = row[5] as usize;
            let label = self
                .labels
                .get(class_id)
                .cloned()
                .unwrap_or_else(|| format!("class_{class_id}"));
            out.push(Detection {
                label,
                rect: Rect::new(cx - w / 2.0, cy - h / 2.0, w, h),
                confidence: conf,
            });
        }
        out
    }
}

impl Detector for OnnxDetector {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection> {
        // The pipeline letterboxes to the manifest imgsz before calling;
        // honor the contract by re-letterboxing when shapes disagree (a
        // mismatched fixture still runs instead of panicking).
        if (frame.width, frame.height) != self.imgsz {
            match crate::pipeline::letterbox_to_model(frame, self.imgsz.0, self.imgsz.1) {
                Ok(f) => return self.detect(&f),
                Err(e) => {
                    self.last_error = Some(e.to_string());
                    return Vec::new();
                }
            }
        }
        match self.evaluate(frame) {
            Ok((data, shape)) => self.decode(&data, &shape),
            Err(e) => {
                if self.last_error.is_none() {
                    self.last_error = Some(e.to_string());
                }
                Vec::new()
            }
        }
    }
}
