//! Coordinate transforms between the four systems the pipeline uses:
//!
//! ```text
//! 原始窗口 client → resize/letterbox → model input → detection
//!                → inverse letterbox → client coordinate → desktop coordinate
//! ```
//!
//! One `Transform` instance is built per capture against the window layout
//! observed at capture time, so a resize invalidates it (the SafetyGovernor
//! enforces recalibration). No code anywhere may hard-code absolute pixels
//! like `Click(1733, 944)` — everything flows through these mappings
//! (ROADMAP §3 NC0, architecture requirement).

use crate::window::WindowLayout;
use crate::{ControllerError, Result};

/// Letterbox parameters of a client→model mapping.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct LetterboxInfo {
    /// Uniform scale from client pixels to pre-padding model pixels.
    pub scale: f32,
    /// Left/right padding in model pixels (`pad_x` split evenly).
    pub pad_x: f32,
    /// Top/bottom padding in model pixels (`pad_y` split evenly).
    pub pad_y: f32,
}

/// Client size + screen origin + model size + derived letterbox.
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Transform {
    pub client_w: f32,
    pub client_h: f32,
    pub model_w: f32,
    pub model_h: f32,
    /// Client-area top-left corner in desktop coordinates.
    pub screen_origin: (i32, i32),
    pub letterbox: LetterboxInfo,
}

/// A rectangle in any coordinate system (top-left + size).
#[derive(Debug, Clone, Copy, PartialEq)]
pub struct Rect {
    pub x: f32,
    pub y: f32,
    pub w: f32,
    pub h: f32,
}

impl Rect {
    pub fn new(x: f32, y: f32, w: f32, h: f32) -> Rect {
        Rect { x, y, w, h }
    }
    pub fn center(&self) -> (f32, f32) {
        (self.x + self.w / 2.0, self.y + self.h / 2.0)
    }
}

impl Transform {
    /// Build from the layout observed for a capture and the model input
    /// size (e.g. 640×640 for a YOLO nano variant).
    pub fn new(layout: &WindowLayout, model_w: u32, model_h: u32) -> Result<Transform> {
        if layout.client_size.0 <= 0 || layout.client_size.1 <= 0 {
            return Err(ControllerError::InvalidInput(format!(
                "client size must be positive, got {:?}",
                layout.client_size
            )));
        }
        if model_w == 0 || model_h == 0 {
            return Err(ControllerError::InvalidInput(format!(
                "model size must be positive, got {model_w}x{model_h}"
            )));
        }
        let (cw, ch) = (layout.client_size.0 as f32, layout.client_size.1 as f32);
        let (mw, mh) = (model_w as f32, model_h as f32);
        let scale = (mw / cw).min(mh / ch);
        let pad_x = (mw - cw * scale) / 2.0;
        let pad_y = (mh - ch * scale) / 2.0;
        Ok(Transform {
            client_w: cw,
            client_h: ch,
            model_w: mw,
            model_h: mh,
            screen_origin: layout.screen_origin,
            letterbox: LetterboxInfo {
                scale,
                pad_x,
                pad_y,
            },
        })
    }

    fn in_client(&self, x: f32, y: f32) -> Result<()> {
        if x.is_nan()
            || y.is_nan()
            || x < 0.0
            || y < 0.0
            || x >= self.client_w
            || y >= self.client_h
        {
            return Err(ControllerError::InvalidInput(format!(
                "point ({x}, {y}) outside client {}x{}",
                self.client_w, self.client_h
            )));
        }
        Ok(())
    }

    fn in_model(&self, x: f32, y: f32) -> Result<()> {
        if x.is_nan() || y.is_nan() || x < 0.0 || y < 0.0 || x >= self.model_w || y >= self.model_h
        {
            return Err(ControllerError::InvalidInput(format!(
                "point ({x}, {y}) outside model {}x{}",
                self.model_w, self.model_h
            )));
        }
        Ok(())
    }

    fn in_unit(&self, x: f32, y: f32) -> Result<()> {
        if x.is_nan() || y.is_nan() || !(0.0..=1.0).contains(&x) || !(0.0..=1.0).contains(&y) {
            return Err(ControllerError::InvalidInput(format!(
                "normalized point ({x}, {y}) outside [0, 1]"
            )));
        }
        Ok(())
    }

    /// client pixels → model pixels (letterbox incl. padding).
    pub fn client_to_model(&self, x: f32, y: f32) -> Result<(f32, f32)> {
        self.in_client(x, y)?;
        Ok((
            x * self.letterbox.scale + self.letterbox.pad_x,
            y * self.letterbox.scale + self.letterbox.pad_y,
        ))
    }

    /// model pixels → client pixels (inverse letterbox).
    pub fn model_to_client(&self, x: f32, y: f32) -> Result<(f32, f32)> {
        self.in_model(x, y)?;
        Ok((
            (x - self.letterbox.pad_x) / self.letterbox.scale,
            (y - self.letterbox.pad_y) / self.letterbox.scale,
        ))
    }

    /// client pixels → [0, 1]² normalized.
    pub fn client_to_normalized(&self, x: f32, y: f32) -> Result<(f32, f32)> {
        self.in_client(x, y)?;
        Ok((x / self.client_w, y / self.client_h))
    }

    /// [0, 1]² normalized → client pixels.
    pub fn normalized_to_client(&self, x: f32, y: f32) -> Result<(f32, f32)> {
        self.in_unit(x, y)?;
        Ok((x * self.client_w, y * self.client_h))
    }

    /// client pixels → desktop pixels (pure translation).
    pub fn client_to_desktop(&self, x: f32, y: f32) -> Result<(f32, f32)> {
        self.in_client(x, y)?;
        Ok((
            x + self.screen_origin.0 as f32,
            y + self.screen_origin.1 as f32,
        ))
    }

    /// desktop pixels → client pixels (clamped to the client when the
    /// desktop point lies outside it, since desktop → client is the input
    /// direction for "user pointed here" style queries).
    pub fn desktop_to_client(&self, x: f32, y: f32) -> (f32, f32) {
        (
            (x - self.screen_origin.0 as f32).clamp(0.0, self.client_w - 1.0),
            (y - self.screen_origin.1 as f32).clamp(0.0, self.client_h - 1.0),
        )
    }

    /// Detection box in model space → box in client space.
    pub fn rect_model_to_client(&self, r: Rect) -> Result<Rect> {
        let (x0, y0) = self.model_to_client(r.x, r.y)?;
        let (x1, y1) = self.model_to_client(r.x + r.w, r.y + r.h)?;
        Ok(Rect::new(x0, y0, x1 - x0, y1 - y0))
    }

    /// Detection box in model space → box in desktop space. This is the
    /// full inverse chain of the pipeline: detection → client → desktop.
    pub fn rect_model_to_desktop(&self, r: Rect) -> Result<Rect> {
        let c = self.rect_model_to_client(r)?;
        let (dx, dy) = (
            c.x + self.screen_origin.0 as f32,
            c.y + self.screen_origin.1 as f32,
        );
        Ok(Rect::new(dx, dy, c.w, c.h))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn layout(w: i32, h: i32, ox: i32, oy: i32) -> WindowLayout {
        WindowLayout {
            client_size: (w, h),
            screen_origin: (ox, oy),
            dpi: 96,
        }
    }

    const EPS: f32 = 0.01;
    fn close(a: f32, b: f32) -> bool {
        (a - b).abs() < EPS
    }
    fn assert_pt(a: (f32, f32), b: (f32, f32)) {
        assert!(close(a.0, b.0) && close(a.1, b.1), "{a:?} != {b:?}");
    }

    #[test]
    fn letterbox_16_9_client_into_square_model_has_no_y_padding() {
        // 1920x1080 -> 640x640: scale 640/1920 = 1/3, displayed 640x360,
        // vertical padding (640-360)/2 = 140.
        let t = Transform::new(&layout(1920, 1080, 0, 0), 640, 640).expect("t");
        assert!(close(t.letterbox.scale, 640.0 / 1920.0));
        assert!(close(t.letterbox.pad_x, 0.0));
        assert!(close(t.letterbox.pad_y, 140.0));

        let m = t.client_to_model(960.0, 540.0).expect("in");
        assert_pt(m, (320.0, 320.0));
        let back = t.model_to_client(m.0, m.1).expect("back");
        assert_pt(back, (960.0, 540.0));
    }

    #[test]
    fn letterbox_21_9_client_has_exact_padding() {
        // 2560x1080 -> 640x640: scale = 640/2560 = 0.25, displayed
        // 640x270, pad_y = (640-270)/2 = 185, pad_x = 0.
        let t = Transform::new(&layout(2560, 1080, 0, 0), 640, 640).expect("t");
        assert!(close(t.letterbox.scale, 0.25));
        assert!(close(t.letterbox.pad_x, 0.0));
        assert!(close(t.letterbox.pad_y, 185.0));
    }

    #[test]
    fn letterbox_taller_than_model_pads_horizontally() {
        // 800x1200 portrait client -> 640x640: scale = 640/1200 ≈ 0.5333,
        // displayed 426.67x640, pad_x = (640-426.67)/2 ≈ 106.67.
        let t = Transform::new(&layout(800, 1200, 0, 0), 640, 640).expect("t");
        assert!(close(
            t.letterbox.pad_x,
            (640.0 - 800.0 * (640.0 / 1200.0)) / 2.0,
        ));
        assert!(close(t.letterbox.pad_y, 0.0));
    }

    /// THE NC0 resolution-independence acceptance: the same UI element,
    /// expressed as a normalized position, produces identical model-space
    /// coordinates on 1080p and 1440p clients; a detection found on one
    /// resolution maps back to the same normalized position on the other.
    #[test]
    fn resolution_independence_1080p_vs_1440p() {
        let t1080 = Transform::new(&layout(1920, 1080, 100, 100), 640, 640).expect("t1080");
        let t1440 = Transform::new(&layout(2560, 1440, 0, 220), 640, 640).expect("t1440");

        // a UI button at 73% / 86% of the client in both resolutions
        let norm = (0.73f32, 0.86f32);
        let c1080 = t1080.normalized_to_client(norm.0, norm.1).expect("c1080");
        let c1440 = t1440.normalized_to_client(norm.0, norm.1).expect("c1440");

        let m1080 = t1080.client_to_model(c1080.0, c1080.1).expect("m1080");
        let m1440 = t1440.client_to_model(c1440.0, c1440.1).expect("m1440");
        assert!(
            close(m1080.0, m1440.0) && close(m1080.1, m1440.1),
            "model coords diverge across resolutions: {m1080:?} vs {m1440:?}"
        );

        // a detector reports the box in model space once; inverse must land
        // on the same normalized point on BOTH resolutions
        let det = Rect::new(m1080.0 - 20.0, m1080.1 - 8.0, 40.0, 16.0);
        let back1080 = t1080.rect_model_to_client(det).expect("back1080");
        let back1440 = t1440.rect_model_to_client(det).expect("back1440");
        let ctr1080 = back1080.center();
        let ctr1440 = back1440.center();
        let n1080 = t1080
            .client_to_normalized(ctr1080.0, ctr1080.1)
            .expect("n1080");
        let n1440 = t1440
            .client_to_normalized(ctr1440.0, ctr1440.1)
            .expect("n1440");
        assert!(close(n1080.0, norm.0) && close(n1080.1, norm.1));
        assert!(
            close(n1440.0, norm.0) && close(n1440.1, norm.1),
            "1440p inverse landed on {n1440:?}, expected {norm:?}"
        );
    }

    #[test]
    fn desktop_mapping_round_trips_with_distinct_origins() {
        let t = Transform::new(&layout(1280, 720, -1920, 45), 512, 512).expect("t");
        let c = (640.0f32, 360.0f32);
        let d = t.client_to_desktop(c.0, c.1).expect("desktop");
        assert_pt(d, (640.0 - 1920.0, 360.0 + 45.0));
        let back = t.desktop_to_client(d.0, d.1);
        assert_pt(back, c);

        // full detection → desktop chain
        let model_box = Rect::new(100.0, 100.0, 50.0, 25.0);
        let desk = t.rect_model_to_desktop(model_box).expect("desk box");
        // inverse: desktop box center → client → model center must match
        let dctr = desk.center();
        let cctr = t.desktop_to_client(dctr.0, dctr.1);
        let mctr = t.client_to_model(cctr.0, cctr.1).expect("mctr");
        let expected = model_box.center();
        assert!(
            close(mctr.0, expected.0) && close(mctr.1, expected.1),
            "chain round-trip diverged: {mctr:?} vs {expected:?}"
        );
    }

    #[test]
    fn out_of_bounds_inputs_are_rejected() {
        let t = Transform::new(&layout(1920, 1080, 0, 0), 640, 640).expect("t");
        assert!(
            t.client_to_model(1920.0, 0.0).is_err(),
            "x == width rejected"
        );
        assert!(t.client_to_model(-0.5, 10.0).is_err(), "negative rejected");
        assert!(t.client_to_model(f32::NAN, 1.0).is_err(), "NaN rejected");
        assert!(t.client_to_normalized(100.0, 5000.0).is_err());
        assert!(
            t.model_to_client(641.0, 0.0).is_err(),
            "beyond model rejected"
        );
        assert!(t.model_to_client(0.0, -1.0).is_err());
        assert!(t.normalized_to_client(1.01, 0.5).is_err());
        assert!(t.normalized_to_client(f32::NAN, 0.5).is_err());
        assert!(
            Transform::new(&layout(0, 1080, 0, 0), 640, 640).is_err(),
            "zero client"
        );
        assert!(
            Transform::new(&layout(1920, 1080, 0, 0), 0, 640).is_err(),
            "zero model"
        );
    }

    #[test]
    fn desktop_to_client_clamps_outside_points() {
        let t = Transform::new(&layout(1000, 500, 40, 50), 320, 320).expect("t");
        let c = t.desktop_to_client(40.0 - 100.0, 50.0 + 900.0);
        assert_pt(c, (0.0, 499.0));
    }

    /// Property test: uniformly sampled client points round-trip through
    /// the model space within half a pixel, at several window geometries.
    #[test]
    fn client_model_round_trip_within_half_pixel() {
        for (cw, ch, mw, mh) in [
            (1920u32, 1080u32, 640u32, 640u32),
            (2560, 1440, 640, 640),
            (3440, 1440, 640, 640),
            (1280, 720, 416, 416),
            (1024, 768, 512, 512),
        ] {
            let t = Transform::new(&layout(cw as i32, ch as i32, 0, 0), mw, mh).expect("t");
            for i in 1..8 {
                for j in 1..8 {
                    let px = cw as f32 * i as f32 / 8.0 - 0.5;
                    let py = ch as f32 * j as f32 / 8.0 - 0.5;
                    let m = t.client_to_model(px, py).expect("fwd");
                    let back = t.model_to_client(m.0, m.1).expect("inv");
                    assert!(
                        (back.0 - px).abs() < 0.5 && (back.1 - py).abs() < 0.5,
                        "{cw}x{ch}->{mw}x{mh}: ({px},{py}) -> {m:?} -> {back:?}"
                    );
                }
            }
        }
    }
}
