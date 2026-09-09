//! Perception stack (NC0): the `Detector` trait plus mock implementations.
//!
//! NC0 ships no real inference — the [`MockDetector`] finds color classes
//! with plain pixel scans, which is enough to exercise the full pipeline
//! (letterbox → detect → inverse transform → overlay → safety verdicts).
//! Real inference arrives with NC1 (ONNX, no Python at runtime); template
//! matching and OCR follow in NC2. No model downloads, no GPU code here.

use crate::frame::Frame;
use crate::transform::Rect;

/// One detected object, in MODEL coordinates (the space the detector ran
/// in). The pipeline maps it back to client/desktop coordinates.
#[derive(Debug, Clone, PartialEq)]
pub struct Detection {
    /// Class name (e.g. `"moving_rect"`, `"button_green"`).
    pub label: String,
    /// Bounding box in model pixels.
    pub rect: Rect,
    /// Detector confidence in [0, 1]; the SafetyGovernor rejects actions
    /// whose confidence is below its threshold.
    pub confidence: f32,
}

/// An object detector over BGRA8 frames in model space.
pub trait Detector {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection>;

    /// The first error since the last call, if any. A detector that
    /// returns an empty detection list must be distinguishable from one
    /// that could not run at all (silent empties hide broken inference);
    /// backends that cannot fail (the NC0 mock) default to `None`.
    fn take_error(&mut self) -> Option<String> {
        None
    }
}

/// Boxed detectors stay detectors: lets callers wrap trait objects in
/// decorators (e.g. the frame-hash inference cache) without unwrapping.
impl<T: Detector + ?Sized> Detector for Box<T> {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection> {
        (**self).detect(frame)
    }

    fn take_error(&mut self) -> Option<String> {
        (**self).take_error()
    }
}

/// What color a [`ColorClassDetector`] looks for.
#[derive(Debug, Clone, Copy)]
pub struct ColorTarget {
    /// Match predicate inputs: the detector scans every `step`-th pixel.
    pub name: &'static str,
    /// Predicate over (b, g, r, a); returns true when the pixel belongs to
    /// the class.
    pub matches: fn([u8; 4]) -> bool,
    /// Pixel sampling stride (1 = every pixel; 4 = every 4th, faster).
    pub step: u32,
}

/// A deliberately simple detector: bounding box of all pixels matching a
/// color predicate; confidence = matched pixels / bbox area. Deterministic
/// and dependency-free — exactly what NC0 needs to prove the pipeline.
#[derive(Debug, Clone)]
pub struct MockDetector {
    target: ColorTarget,
}

impl MockDetector {
    pub fn new(target: ColorTarget) -> MockDetector {
        MockDetector { target }
    }

    /// The default NC0 target: the synthetic capture's solid rectangle
    /// fill (BGRA [16, 40, 200] = strong red). Solid fill keeps confidence
    /// near 1.0 so the happy path passes the governor's confidence gate;
    /// real UI colors plug in via `MockDetector::new`.
    pub fn synthetic_rect() -> MockDetector {
        MockDetector::new(ColorTarget {
            name: "moving_rect",
            matches: |[b, g, r, _a]| r > 150 && g < 100 && b < 60,
            step: 2,
        })
    }
}

impl Detector for MockDetector {
    fn detect(&mut self, frame: &Frame) -> Vec<Detection> {
        let step = self.target.step.max(1);
        let mut min = (u32::MAX, u32::MAX);
        let mut max = (0u32, 0u32);
        let mut matched = 0u32;
        let mut y = 0;
        while y < frame.height {
            let mut x = 0;
            while x < frame.width {
                if let Some(px) = frame.pixel(x, y) {
                    if (self.target.matches)(px) {
                        matched += 1;
                        min = (min.0.min(x), min.1.min(y));
                        max = (max.0.max(x), max.1.max(y));
                    }
                }
                x += step;
            }
            y += step;
        }
        if matched == 0 {
            return Vec::new();
        }
        let w = (max.0 - min.0) as f32 + step as f32;
        let h = (max.1 - min.1) as f32 + step as f32;
        let rect = Rect::new(min.0 as f32, min.1 as f32, w, h);
        // clamp bbox area to the frame so confidence stays in [0, 1]
        let area = (rect.w * rect.h).max(1.0);
        let confidence = (matched as f32 * step as f32 * step as f32 / area).min(1.0);
        vec![Detection {
            label: self.target.name.to_string(),
            rect,
            confidence,
        }]
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn frame_with_red_box(x0: u32, y0: u32, w: u32, h: u32) -> Frame {
        let mut f = Frame::new(64, 64);
        for y in y0..y0 + h {
            for x in x0..x0 + w {
                f.set_pixel(x, y, [10, 40, 200, 255]);
            }
        }
        f
    }

    #[test]
    fn finds_box_with_expected_confidence() {
        let f = frame_with_red_box(10, 20, 16, 8);
        let mut det = MockDetector::synthetic_rect();
        let dets = det.detect(&f);
        assert_eq!(dets.len(), 1);
        let d = &dets[0];
        assert_eq!(d.label, "moving_rect");
        assert_eq!(d.rect.x, 10.0);
        assert_eq!(d.rect.y, 20.0);
        assert_eq!(d.rect.w, 16.0);
        assert_eq!(d.rect.h, 8.0);
        assert!(
            (d.confidence - 1.0).abs() < 0.01,
            "solid box -> confidence 1, got {}",
            d.confidence
        );
    }

    #[test]
    fn no_match_returns_empty() {
        let f = Frame::new(32, 32);
        let mut det = MockDetector::synthetic_rect();
        assert!(det.detect(&f).is_empty());
    }

    #[test]
    fn partial_fill_gives_partial_confidence_below_one() {
        // sampled rows (every 2nd) hit a matching row only every 4th row,
        // so roughly half the bbox samples match: confidence ~0.5
        let mut f = Frame::new(32, 32);
        for y in (0..16).step_by(4) {
            for x in 0..16 {
                f.set_pixel(x, y, [10, 40, 200, 255]);
            }
        }
        let mut det = MockDetector::synthetic_rect();
        let dets = det.detect(&f);
        assert_eq!(dets.len(), 1);
        let c = dets[0].confidence;
        assert!(
            c > 0.3 && c < 0.8,
            "striped fill should land mid-confidence, got {c}"
        );
    }
}
