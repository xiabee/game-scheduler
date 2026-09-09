//! Detection post-processing (NC1/NC2 groundwork): IoU and class-aware
//! greedy NMS. Pure functions over [`Rect`]/[`Detection`] — no runtime
//! dependencies, fully unit-tested. The ONNX detector applies NMS after
//! its threshold filter; the perception stack (NC2) will reuse these for
//! template/YOLO fusion.

use crate::transform::Rect;
use crate::vision::Detection;

/// Intersection-over-union of two rects. Degenerate (non-positive area)
/// inputs yield 0.0 — never NaN.
pub fn iou(a: &Rect, b: &Rect) -> f32 {
    let x0 = a.x.max(b.x);
    let y0 = a.y.max(b.y);
    let x1 = (a.x + a.w).min(b.x + b.w);
    let y1 = (a.y + a.h).min(b.y + b.h);
    let (iw, ih) = (x1 - x0, y1 - y0);
    if iw <= 0.0 || ih <= 0.0 {
        return 0.0;
    }
    let inter = iw * ih;
    let area_a = (a.w * a.h).max(0.0);
    let area_b = (b.w * b.h).max(0.0);
    let union = area_a + area_b - inter;
    if union <= 0.0 {
        0.0
    } else {
        inter / union
    }
}

/// Class-aware greedy NMS: sort by confidence descending, keep the top
/// box, then drop any *same-label* box whose IoU with an already-kept box
/// exceeds `iou_threshold`. Different labels never suppress each other
/// (a dialog frame must not delete the button inside it).
pub fn non_max_suppression(dets: Vec<Detection>, iou_threshold: f32) -> Vec<Detection> {
    let mut ordered = dets;
    ordered.sort_by(|a, b| {
        b.confidence
            .partial_cmp(&a.confidence)
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    let mut kept: Vec<Detection> = Vec::with_capacity(ordered.len());
    for d in ordered {
        let suppressed = kept
            .iter()
            .filter(|k| k.label == d.label)
            .any(|k| iou(&k.rect, &d.rect) > iou_threshold);
        if !suppressed {
            kept.push(d);
        }
    }
    kept
}

#[cfg(test)]
mod tests {
    use super::*;

    fn det(label: &str, x: f32, y: f32, w: f32, h: f32, c: f32) -> Detection {
        Detection {
            label: label.to_string(),
            rect: Rect::new(x, y, w, h),
            confidence: c,
        }
    }

    #[test]
    fn iou_identities_and_edges() {
        let a = Rect::new(0.0, 0.0, 10.0, 10.0);
        assert!((iou(&a, &a) - 1.0).abs() < 1e-6, "identical boxes");
        let b = Rect::new(20.0, 20.0, 10.0, 10.0);
        assert_eq!(iou(&a, &b), 0.0, "disjoint boxes");
        // half-overlapping on x: intersection 5x10, union 10x10+10x10-5x10
        let c = Rect::new(5.0, 0.0, 10.0, 10.0);
        assert!((iou(&a, &c) - 50.0 / 150.0).abs() < 1e-5);
        // touching edges do not intersect
        let d = Rect::new(10.0, 0.0, 10.0, 10.0);
        assert_eq!(iou(&a, &d), 0.0);
        // degenerate boxes never produce NaN
        let e = Rect::new(0.0, 0.0, 0.0, 0.0);
        assert_eq!(iou(&e, &a), 0.0);
    }

    #[test]
    fn nms_keeps_peaks_and_drops_same_label_overlaps() {
        let dets = vec![
            det("a", 0.0, 0.0, 10.0, 10.0, 0.6),
            det("a", 1.0, 1.0, 10.0, 10.0, 0.9), // peak, overlaps the 0.6 box
            det("a", 50.0, 50.0, 10.0, 10.0, 0.7), // disjoint
        ];
        let kept = non_max_suppression(dets, 0.5);
        assert_eq!(kept.len(), 2, "{kept:?}");
        assert_eq!(kept[0].confidence, 0.9, "highest confidence first");
        assert_eq!(kept[1].confidence, 0.7);
    }

    #[test]
    fn nms_is_class_aware() {
        // same rect, different labels: neither suppresses the other
        let dets = vec![
            det("dialog", 0.0, 0.0, 20.0, 20.0, 0.8),
            det("button", 0.0, 0.0, 20.0, 20.0, 0.7),
        ];
        let kept = non_max_suppression(dets, 0.5);
        assert_eq!(kept.len(), 2, "{kept:?}");
    }

    #[test]
    fn nms_threshold_zero_only_keeps_one_per_label_location() {
        let dets = vec![
            det("a", 0.0, 0.0, 10.0, 10.0, 0.9),
            det("a", 0.0, 0.0, 10.0, 10.0, 0.8), // IoU 1.0 > 0
        ];
        let kept = non_max_suppression(dets, 0.0);
        assert_eq!(kept.len(), 1);
    }
}
