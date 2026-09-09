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
    /// Reused NCHW staging buffer (see `input_tensor`).
    buffer: Vec<f32>,
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
            buffer: Vec::new(),
            last_error: None,
        })
    }

    /// BGRA8 model-space frame → RGB f32/255 NCHW [1,3,h,w]. The staging
    /// buffer lives on the detector: at real imgsz (640 → ~4.9M floats)
    /// reallocating every cycle would churn tens of MB.
    fn input_tensor(&mut self, frame: &Frame) -> Result<windows::AI::MachineLearning::TensorFloat> {
        use windows::AI::MachineLearning::TensorFloat;
        let (w, h) = (frame.width, frame.height);
        self.buffer.clear();
        self.buffer.reserve((3 * w * h) as usize);
        // planes: R, then G, then B (BGRA pixels: [2]=r, [1]=g, [0]=b)
        for plane in [2usize, 1, 0] {
            for y in 0..h {
                for x in 0..w {
                    let px = frame.pixel(x, y).unwrap_or([0, 0, 0, 255]);
                    self.buffer.push(px[plane] as f32 / 255.0);
                }
            }
        }
        let tensor = TensorFloat::CreateFromShapeArrayAndDataArray(
            &[1, 3, h as i64, w as i64],
            &self.buffer,
        )?;
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

    /// Decode model output into detections, auto-detecting the layout:
    ///
    /// - channels-first `[1, 4+nc, N]` (YOLOv8 ONNX export style): rows
    ///   0..3 are (cx, cy, w, h) in letterbox pixels, rows 4.. are class
    ///   scores; selected when `shape[1] == 4 + labels.len()` exactly.
    /// - rows-major `[1, N, >=6]`: each row is (cx, cy, w, h, conf,
    ///   class); the NC1 default contract.
    ///
    /// Threshold filtering and class-aware NMS apply to both layouts.
    fn decode(&self, data: &[f32], shape: &[i64]) -> Vec<Detection> {
        if shape.len() != 3 {
            return Vec::new();
        }
        let dets = if shape[1] as usize == 4 + self.labels.len() && shape[2] as usize > 0 {
            self.decode_channels_first(data, shape[1] as usize, shape[2] as usize)
        } else if shape[2] >= 6 {
            self.decode_rows_major(data, shape[1] as usize, shape[2] as usize)
        } else {
            Vec::new()
        };
        crate::nms::non_max_suppression(dets, 0.45)
    }

    /// `[1, C, N]` YOLOv8 export layout.
    fn decode_channels_first(&self, data: &[f32], c: usize, n: usize) -> Vec<Detection> {
        let mut out = Vec::new();
        let classes = c - 4;
        for i in 0..n {
            let (cx, cy, w, h) = (data[i], data[n + i], data[2 * n + i], data[3 * n + i]);
            if !(cx.is_finite() && cy.is_finite() && w.is_finite() && h.is_finite()) {
                continue;
            }
            let mut best = (usize::MAX, 0.0f32);
            for k in 0..classes {
                let score = data[(4 + k) * n + i];
                if score.is_finite() && score > best.1 {
                    best = (k, score);
                }
            }
            let (class_id, conf) = best;
            if class_id == usize::MAX || !(conf.is_finite() && conf >= self.confidence) {
                continue;
            }
            out.push(self.detection(class_id, conf, cx, cy, w, h));
        }
        out
    }

    /// `[1, N, >=6]` rows layout: (cx, cy, w, h, conf, class).
    fn decode_rows_major(&self, data: &[f32], rows: usize, stride: usize) -> Vec<Detection> {
        let mut out = Vec::new();
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
            out.push(self.detection(class_id, conf, cx, cy, w, h));
        }
        out
    }

    fn detection(&self, class_id: usize, conf: f32, cx: f32, cy: f32, w: f32, h: f32) -> Detection {
        let label = self
            .labels
            .get(class_id)
            .cloned()
            .unwrap_or_else(|| format!("class_{class_id}"));
        Detection {
            label,
            rect: Rect::new(cx - w / 2.0, cy - h / 2.0, w, h),
            confidence: conf,
        }
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

    fn take_error(&mut self) -> Option<String> {
        self.last_error.take()
    }
}
