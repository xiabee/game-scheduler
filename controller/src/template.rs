//! Template matching (NC0 M6): normalized cross-correlation over
//! grayscale. A skeleton implementation — exhaustive scan with stride,
//! score in [-1, 1], invariant to linear brightness changes. Faster
//! variants (FFT, integral images, L0/L1 layering) belong to NC2; the
//! interface here is the one the perception stack will call.

use crate::frame::Frame;

/// A match produced by a [`TemplateMatcher`].
#[derive(Debug, Clone, PartialEq)]
pub struct TemplateMatch {
    /// Top-left corner of the best match, in the searched image's pixels.
    pub x: u32,
    pub y: u32,
    /// NCC score in [-1, 1]; 1.0 = identical (up to linear brightness).
    pub score: f32,
}

/// Template matching over BGRA8 frames. Coordinates are in the searched
/// image's own space (the pipeline maps them further as needed).
pub trait TemplateMatcher {
    fn find(&mut self, image: &Frame, template: &Frame, stride: u32) -> Option<TemplateMatch>;
}

/// Grayscale NCC matcher. `stride` skips pixels both in scan position and
/// sampling inside the window (cheap coarse pass; NC2 refines).
pub struct NccTemplateMatcher;

impl NccTemplateMatcher {
    /// BGRA8 -> luminance row-major vector (Rec.601 weights).
    fn gray(frame: &Frame) -> Vec<f32> {
        let mut g = Vec::with_capacity((frame.width * frame.height) as usize);
        for y in 0..frame.height {
            for x in 0..frame.width {
                let px = frame.pixel(x, y).unwrap_or([0, 0, 0, 255]);
                let lum = 0.114 * px[0] as f32 + 0.587 * px[1] as f32 + 0.299 * px[2] as f32;
                g.push(lum);
            }
        }
        g
    }

    fn mean(v: &[f32]) -> f32 {
        if v.is_empty() {
            return 0.0;
        }
        v.iter().sum::<f32>() / v.len() as f32
    }
}

impl TemplateMatcher for NccTemplateMatcher {
    fn find(&mut self, image: &Frame, template: &Frame, stride: u32) -> Option<TemplateMatch> {
        if template.width == 0
            || template.height == 0
            || template.width > image.width
            || template.height > image.height
        {
            return None;
        }
        let stride = stride.max(1);
        let img = Self::gray(image);
        let tpl = Self::gray(template);
        let (tw, th) = (template.width as usize, template.height as usize);
        let t_mean = Self::mean(&tpl);
        let t_dev: Vec<f32> = tpl.iter().map(|v| v - t_mean).collect();
        let t_norm: f32 = t_dev.iter().map(|v| v * v).sum::<f32>().sqrt();
        if t_norm < 1e-6 {
            // uniform template carries no information
            return None;
        }

        let mut best: Option<TemplateMatch> = None;
        let iw = image.width as usize;
        let max_x = image.width - template.width;
        let max_y = image.height - template.height;
        let mut oy = 0usize;
        while oy <= max_y as usize {
            let mut ox = 0usize;
            while ox <= max_x as usize {
                // window mean via direct sum (NC2 moves to integral images)
                let mut sum = 0.0f32;
                for ty in 0..th {
                    let row = (oy + ty) * iw + ox;
                    for tx in 0..tw {
                        sum += img[row + tx];
                    }
                }
                let w_mean = sum / (tw * th) as f32;
                let mut num = 0.0f32;
                let mut w_sq = 0.0f32;
                for ty in 0..th {
                    let row = (oy + ty) * iw + ox;
                    for tx in 0..tw {
                        let d = img[row + tx] - w_mean;
                        num += d * t_dev[ty * tw + tx];
                        w_sq += d * d;
                    }
                }
                let w_norm = w_sq.sqrt();
                let score = if w_norm < 1e-6 {
                    0.0 // uniform window: no correlation direction
                } else {
                    num / (w_norm * t_norm)
                };
                if best.as_ref().is_none_or(|b| score > b.score) {
                    best = Some(TemplateMatch {
                        x: ox as u32,
                        y: oy as u32,
                        score,
                    });
                }
                ox += stride as usize;
            }
            oy += stride as usize;
        }
        best
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Deterministic pseudo-random grayscale texture (LCG): no periodicity,
    /// so the best template match position is unique.
    fn texture(w: u32, h: u32) -> Frame {
        let mut state: u32 = 0x1234_5678;
        let mut next = move || {
            state = state.wrapping_mul(1_664_525).wrapping_add(1_013_904_223);
            (state >> 16) as u8
        };
        let mut f = Frame::new(w, h);
        for y in 0..h {
            for x in 0..w {
                let v = next();
                f.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        f
    }

    fn checkerboard(w: u32, h: u32) -> Frame {
        let mut f = Frame::new(w, h);
        for y in 0..h {
            for x in 0..w {
                let v = if ((x / 8 + y / 8) % 2) == 0 { 40 } else { 200 };
                f.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        f
    }

    fn crop(frame: &Frame, x: u32, y: u32, w: u32, h: u32) -> Frame {
        let mut out = Frame::new(w, h);
        for yy in 0..h {
            for xx in 0..w {
                if let Some(px) = frame.pixel(x + xx, y + yy) {
                    out.set_pixel(xx, yy, px);
                }
            }
        }
        out
    }

    #[test]
    fn template_cut_from_image_matches_exactly_at_origin() {
        let image = texture(64, 64);
        let template = crop(&image, 16, 24, 16, 16);
        let mut m = NccTemplateMatcher;
        let hit = m.find(&image, &template, 1).expect("match");
        assert_eq!(hit.x, 16);
        assert_eq!(hit.y, 24);
        assert!(
            hit.score > 0.999,
            "exact crop must score ~1, got {}",
            hit.score
        );
    }

    #[test]
    fn brighter_copy_still_matches_same_spot() {
        // NCC is invariant to linear brightness offsets
        let image = texture(64, 64);
        let mut template = crop(&image, 8, 40, 16, 16);
        for b in template.data.iter_mut() {
            *b = b.saturating_add(40);
        }
        let mut m = NccTemplateMatcher;
        let hit = m.find(&image, &template, 1).expect("match");
        assert_eq!(hit.x, 8);
        assert_eq!(hit.y, 40);
        assert!(
            hit.score > 0.95,
            "brightness-shifted match score {}",
            hit.score
        );
    }

    #[test]
    fn unrelated_template_scores_low() {
        // vertical stripes vs checkerboard: structurally different
        let image = checkerboard(64, 64);
        let mut template = Frame::new(16, 16);
        for y in 0..16 {
            for x in 0..16 {
                let v = if x % 4 < 2 { 30 } else { 220 };
                template.set_pixel(x, y, [v, v, v, 255]);
            }
        }
        let mut m = NccTemplateMatcher;
        let hit = m
            .find(&image, &template, 2)
            .expect("some position returned");
        assert!(
            hit.score < 0.9,
            "unrelated patterns must score low, got {}",
            hit.score
        );
    }

    #[test]
    fn degenerate_inputs_return_none() {
        let mut m = NccTemplateMatcher;
        let image = checkerboard(32, 32);
        let bigger = checkerboard(64, 64);
        assert!(
            m.find(&image, &bigger, 1).is_none(),
            "template larger than image"
        );
        assert!(
            m.find(&image, &Frame::new(0, 5), 1).is_none(),
            "empty template"
        );
        // uniform template carries no signal
        let uniform = Frame::new(8, 8);
        assert!(m.find(&image, &uniform, 1).is_none());
    }

    #[test]
    fn stride_finds_same_neighbourhood() {
        let image = texture(64, 64);
        let template = crop(&image, 16, 24, 16, 16);
        let mut m = NccTemplateMatcher;
        let fine = m.find(&image, &template, 1).expect("fine");
        let coarse = m.find(&image, &template, 4).expect("coarse");
        // coarse grid must land within one stride cell of the true spot
        assert!((fine.x as i32 - coarse.x as i32).abs() <= 4);
        assert!((fine.y as i32 - coarse.y as i32).abs() <= 4);
        assert!(coarse.score > 0.9);
    }
}
