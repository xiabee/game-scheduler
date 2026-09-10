//! Frame storage: BGRA8 pixel buffers produced by a capture backend.
//!
//! Frames are plain memory: width × height pixels, `stride` bytes per row
//! (stride may exceed `width * 4` when the source pads rows — D3D staging
//! textures usually do). Nothing here writes files; debug PNG export lives
//! in the capture module where it belongs.

/// Bytes per pixel in the BGRA8 layout used across the controller.
pub const BYTES_PER_PIXEL: usize = 4;

/// A top-down BGRA8 frame.
#[derive(Debug, Clone)]
pub struct Frame {
    pub width: u32,
    pub height: u32,
    /// Bytes per row; always `>= width * BYTES_PER_PIXEL`.
    pub stride: u32,
    /// Pixel buffer, `stride * height` bytes (we allocate the full
    /// `stride * height` so every row address is valid).
    pub data: Vec<u8>,
}

impl Frame {
    /// Allocate a zeroed frame with tightly packed rows.
    pub fn new(width: u32, height: u32) -> Frame {
        let stride = width as usize * BYTES_PER_PIXEL;
        Frame {
            width,
            height,
            stride: stride as u32,
            data: vec![0u8; stride * height as usize],
        }
    }

    /// Adopt an externally produced buffer. `data.len()` must cover
    /// `stride * (height - 1) + width * 4` at minimum (the last row only
    /// needs its visible pixels — common with D3D staging textures).
    pub fn from_bgra(data: Vec<u8>, width: u32, height: u32, stride: u32) -> crate::Result<Frame> {
        if width == 0 || height == 0 {
            return Err(crate::ControllerError::InvalidInput(format!(
                "frame size must be positive, got {width}x{height}"
            )));
        }
        if stride < width * BYTES_PER_PIXEL as u32 {
            return Err(crate::ControllerError::InvalidInput(format!(
                "stride {stride} smaller than row size {}",
                width * BYTES_PER_PIXEL as u32
            )));
        }
        let min = stride as usize * (height as usize - 1) + width as usize * BYTES_PER_PIXEL;
        if data.len() < min {
            return Err(crate::ControllerError::InvalidInput(format!(
                "buffer {} bytes < required {min} for {width}x{height} stride {stride}",
                data.len()
            )));
        }
        Ok(Frame {
            width,
            height,
            stride,
            data,
        })
    }

    /// Visible bytes of row `y` (`width * 4`).
    pub fn row(&self, y: u32) -> Option<&[u8]> {
        if y >= self.height {
            return None;
        }
        let start = y as usize * self.stride as usize;
        self.data
            .get(start..start + self.width as usize * BYTES_PER_PIXEL)
    }

    /// BGRA value at pixel `(x, y)`.
    pub fn pixel(&self, x: u32, y: u32) -> Option<[u8; 4]> {
        if x >= self.width || y >= self.height {
            return None;
        }
        let idx = y as usize * self.stride as usize + x as usize * BYTES_PER_PIXEL;
        let px = self.data.get(idx..idx + BYTES_PER_PIXEL)?;
        Some([px[0], px[1], px[2], px[3]])
    }

    /// Write a BGRA value; silently ignored when out of bounds so overlay
    /// drawing can paint near edges without range checks at call sites.
    /// True when a strided sample of the frame is uniformly black. The
    /// RDP/hidden-console capture family yields exactly this signature
    /// (see NIGHTLY_PROGRESS environment findings), and detecting it turns
    /// a silent "nothing detected" session into an explicit warning.
    pub fn is_black_sampled(&self, step: u32) -> bool {
        let step = step.max(1);
        let mut y = 0u32;
        while y < self.height {
            let mut x = 0u32;
            while x < self.width {
                if let Some([b, g, r, _a]) = self.pixel(x, y) {
                    if b != 0 || g != 0 || r != 0 {
                        return false;
                    }
                }
                x += step;
            }
            y += step;
        }
        true
    }

    pub fn set_pixel(&mut self, x: u32, y: u32, bgra: [u8; 4]) {
        if x >= self.width || y >= self.height {
            return;
        }
        let idx = y as usize * self.stride as usize + x as usize * BYTES_PER_PIXEL;
        if let Some(slot) = self.data.get_mut(idx..idx + BYTES_PER_PIXEL) {
            slot.copy_from_slice(&bgra);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_frame_is_zeroed_and_tightly_packed() {
        let f = Frame::new(64, 32);
        assert_eq!(f.stride, 64 * 4);
        assert_eq!(f.data.len(), 64 * 4 * 32);
        assert!(f.data.iter().all(|&b| b == 0));
        assert_eq!(f.pixel(0, 0), Some([0, 0, 0, 0]));
        assert_eq!(f.pixel(63, 31), Some([0, 0, 0, 0]));
    }

    #[test]
    fn from_bgra_accepts_padded_rows_and_rejects_short_buffers() {
        // 3x2 with stride 16 (12 needed + 4 pad)
        let f = Frame::from_bgra(vec![1u8; 16 * 2], 3, 2, 16).expect("valid");
        assert_eq!(f.pixel(2, 1), Some([1, 1, 1, 1]));
        assert_eq!(f.pixel(3, 1), None, "x beyond width");

        assert!(
            Frame::from_bgra(vec![0u8; 10], 3, 2, 16).is_err(),
            "short buffer"
        );
        assert!(
            Frame::from_bgra(vec![0u8; 64], 0, 2, 16).is_err(),
            "zero width"
        );
        assert!(
            Frame::from_bgra(vec![0u8; 64], 4, 2, 8).is_err(),
            "stride < row"
        );
    }

    #[test]
    fn set_pixel_writes_and_ignores_out_of_bounds() {
        let mut f = Frame::new(8, 8);
        f.set_pixel(3, 5, [9, 8, 7, 6]);
        assert_eq!(f.pixel(3, 5), Some([9, 8, 7, 6]));
        f.set_pixel(8, 5, [1, 1, 1, 1]);
        f.set_pixel(3, 8, [1, 1, 1, 1]);
        assert_eq!(
            f.pixel(3, 5),
            Some([9, 8, 7, 6]),
            "out-of-bounds writes are no-ops"
        );
    }

    #[test]
    fn row_returns_visible_bytes_only() {
        let f = Frame::new(4, 4);
        assert_eq!(f.row(0).unwrap().len(), 16);
        assert!(f.row(4).is_none());
    }
}

#[cfg(test)]
mod black_tests {
    use super::*;

    #[test]
    fn black_detection_matches_sampled_content() {
        let mut f = Frame::new(64, 48);
        assert!(f.is_black_sampled(4), "all-zero frame is black");

        // a single lit sampled pixel breaks blackness
        f.set_pixel(32, 24, [16, 40, 200, 255]);
        assert!(!f.is_black_sampled(4));

        // lit pixel BETWEEN samples is invisible to the sampler: documented
        // sampling trade-off, not a bug
        let mut g = Frame::new(64, 48);
        g.set_pixel(33, 25, [16, 40, 200, 255]);
        assert!(g.is_black_sampled(4));
    }
}
