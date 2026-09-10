//! Offline frame recording and replay (NC1/NC2 test infrastructure).
//!
//! Recording captures the raw client frames of a dry-run session to a
//! directory of PNGs; replay feeds them back through the pipeline as a
//! [`CaptureBackend`], so perception changes can be regression-tested
//! against REAL captured frames without the live window (the NC2 test
//! contract: "录制帧 → 断言层输出"). Nothing here talks to a desktop:
//! replay works in service sessions and on any OS the png crate supports.

use crate::capture::CaptureBackend;
use crate::frame::Frame;
use crate::window::WindowLayout;
use crate::{ControllerError, Result};
use std::path::{Path, PathBuf};

/// Encode a frame as RGBA PNG bytes (BGRA→RGBA, stride honored).
pub fn encode_png_bytes(frame: &Frame) -> Result<Vec<u8>> {
    let mut out = Vec::new();
    {
        let mut encoder = png::Encoder::new(&mut out, frame.width, frame.height);
        encoder.set_color(png::ColorType::Rgba);
        encoder.set_depth(png::BitDepth::Eight);
        let mut writer = encoder.write_header().map_err(png_err)?;
        let mut rgba = Vec::with_capacity((frame.width * frame.height * 4) as usize);
        for y in 0..frame.height {
            for x in 0..frame.width {
                let px = frame.pixel(x, y).unwrap_or([0, 0, 0, 255]);
                rgba.extend_from_slice(&[px[2], px[1], px[0], px[3]]);
            }
        }
        writer.write_image_data(&rgba).map_err(png_err)?;
    }
    Ok(out)
}

/// Decode RGBA or RGB PNG bytes into a frame. RGB is the format ffmpeg's
/// default frame extraction produces, so replay must not assume the
/// recorder's RGBA; anything else (16-bit, palette, interlaced, gray)
/// is rejected with a clear message instead of panicking on indexing.
pub fn decode_png_bytes(bytes: &[u8]) -> Result<Frame> {
    let decoder = png::Decoder::new(std::io::Cursor::new(bytes));
    let mut reader = decoder.read_info().map_err(png_decode_err)?;
    let info = reader.info();
    if info.bit_depth != png::BitDepth::Eight {
        return Err(ControllerError::InvalidInput(format!(
            "png: unsupported bit depth {:?} (only 8)",
            info.bit_depth
        )));
    }
    let channels: usize = match info.color_type {
        png::ColorType::Rgba => 4,
        png::ColorType::Rgb => 3,
        other => {
            return Err(ControllerError::InvalidInput(format!(
                "png: unsupported color type {other:?} (only RGB/RGBA)"
            )))
        }
    };
    let (width, height) = (info.width, info.height);
    let mut buf = vec![
        0u8;
        reader.output_buffer_size().ok_or_else(|| {
            ControllerError::InvalidInput("png: no output buffer size available".into())
        })?
    ];
    let info = reader.next_frame(&mut buf).map_err(png_decode_err)?;
    let mut frame = Frame::new(width, height);
    for y in 0..height.min(info.height) {
        for x in 0..width.min(info.width) {
            let i = (y as usize * info.width as usize + x as usize) * channels;
            let (r, g, b) = (buf[i], buf[i + 1], buf[i + 2]);
            let a = if channels == 4 { buf[i + 3] } else { 255 };
            frame.set_pixel(x, y, [b, g, r, a]);
        }
    }
    Ok(frame)
}

fn png_err(e: png::EncodingError) -> ControllerError {
    ControllerError::InvalidInput(format!("png encode: {e}"))
}

fn png_decode_err(e: png::DecodingError) -> ControllerError {
    ControllerError::InvalidInput(format!("png decode: {e}"))
}

/// Writes numbered PNG frames into a directory, bounded by `max_frames`
/// (disk is a shared resource; the default cap in the CLI is generous for
/// tests but bounded). `record` is a no-op returning `Ok(false)` once the
/// cap is reached.
pub struct FrameRecorder {
    dir: PathBuf,
    max_frames: u32,
    written: u32,
}

impl FrameRecorder {
    pub fn create(dir: &Path, max_frames: u32) -> Result<FrameRecorder> {
        std::fs::create_dir_all(dir).map_err(|e| {
            ControllerError::InvalidInput(format!("record dir {}: {e}", dir.display()))
        })?;
        Ok(FrameRecorder {
            dir: dir.to_path_buf(),
            max_frames: max_frames.max(1),
            written: 0,
        })
    }

    /// Write one frame; `Ok(true)` when stored, `Ok(false)` when the cap
    /// was already reached. Recording failures degrade the SESSION, not
    /// the pipeline: they surface as errors to the caller.
    pub fn record(&mut self, frame: &Frame) -> Result<bool> {
        if self.written >= self.max_frames {
            return Ok(false);
        }
        let name = self.dir.join(format!("frame_{:05}.png", self.written));
        let bytes = encode_png_bytes(frame)?;
        std::fs::write(&name, bytes)
            .map_err(|e| ControllerError::InvalidInput(format!("write {}: {e}", name.display())))?;
        self.written += 1;
        Ok(true)
    }

    pub fn written(&self) -> u32 {
        self.written
    }
}

/// A capture backend that replays recorded PNG frames in file order,
/// looping by default. The calibrated layout mirrors the FIRST frame
/// (origin 0,0, dpi 96) — replay is about perception content, not live
/// geometry; the governor's drift checks see a static layout.
pub struct ReplayCapture {
    frames: Vec<Frame>,
    index: usize,
    layout: WindowLayout,
    loop_replay: bool,
}

impl ReplayCapture {
    /// Load every `frame_*.png` in `dir`, sorted by name.
    pub fn open(dir: &Path, loop_replay: bool) -> Result<ReplayCapture> {
        let mut paths: Vec<PathBuf> = std::fs::read_dir(dir)
            .map_err(|e| {
                ControllerError::InvalidInput(format!("replay dir {}: {e}", dir.display()))
            })?
            .filter_map(|e| e.ok().map(|e| e.path()))
            .filter(|p| {
                p.file_name()
                    .and_then(|n| n.to_str())
                    .map(|n| n.starts_with("frame_") && n.ends_with(".png"))
                    .unwrap_or(false)
            })
            .collect();
        paths.sort();
        if paths.is_empty() {
            return Err(ControllerError::InvalidInput(format!(
                "replay dir {} has no frame_*.png files",
                dir.display()
            )));
        }
        let mut frames = Vec::with_capacity(paths.len());
        for p in &paths {
            let bytes = std::fs::read(p)
                .map_err(|e| ControllerError::InvalidInput(format!("read {}: {e}", p.display())))?;
            frames.push(decode_png_bytes(&bytes)?);
        }
        let first = &frames[0];
        Ok(ReplayCapture {
            layout: WindowLayout {
                client_size: (first.width as i32, first.height as i32),
                screen_origin: (0, 0),
                dpi: 96,
            },
            frames,
            index: 0,
            loop_replay,
        })
    }

    pub fn frame_count(&self) -> usize {
        self.frames.len()
    }
}

impl CaptureBackend for ReplayCapture {
    fn capture(&mut self) -> Result<Frame> {
        if self.index >= self.frames.len() {
            // exhausted without looping (or an empty recording): the
            // recording is over — same terminal semantics as a window
            // disappearing on a live backend
            return Err(ControllerError::WindowGone);
        }
        let frame = self.frames[self.index].clone();
        self.index += 1;
        if self.index >= self.frames.len() && self.loop_replay {
            self.index = 0;
        }
        Ok(frame)
    }

    fn calibrated_layout(&self) -> WindowLayout {
        self.layout.clone()
    }
}

impl ReplayCapture {
    /// Whether the next `capture` would walk past the recording (only
    /// possible when not looping).
    pub fn exhausted(&self) -> bool {
        !self.loop_replay && self.index >= self.frames.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn gradient_frame(w: u32, h: u32) -> Frame {
        let mut f = Frame::new(w, h);
        for y in 0..h {
            for x in 0..w {
                f.set_pixel(x, y, [(x % 256) as u8, (y % 256) as u8, 128, 255]);
            }
        }
        f
    }

    #[test]
    fn png_roundtrip_is_pixel_exact() {
        let f = gradient_frame(37, 23); // odd sizes exercise stride math
        let bytes = encode_png_bytes(&f).expect("encode");
        let back = decode_png_bytes(&bytes).expect("decode");
        assert_eq!((back.width, back.height), (37, 23));
        for y in 0..23 {
            for x in 0..37 {
                assert_eq!(f.pixel(x, y), back.pixel(x, y), "pixel ({x},{y})");
            }
        }
    }

    #[test]
    fn record_then_replay_returns_the_same_frames() {
        let dir = std::env::temp_dir().join(format!("nf_replay_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let mut rec = FrameRecorder::create(&dir, 3).expect("recorder");
        let frames: Vec<Frame> = (0..3).map(|i| gradient_frame(16 + i, 12)).collect();
        for f in &frames {
            assert!(rec.record(f).expect("record"));
        }
        assert_eq!(rec.written(), 3);
        // cap reached: further records are a documented no-op
        assert!(!rec.record(&gradient_frame(8, 8)).expect("record at cap"));
        assert_eq!(rec.written(), 3);

        let mut replay = ReplayCapture::open(&dir, false).expect("replay");
        assert_eq!(replay.frame_count(), 3);
        for f in &frames {
            let got = replay.capture().expect("capture");
            assert_eq!((got.width, got.height), (f.width, f.height));
            assert_eq!(got.pixel(0, 0), f.pixel(0, 0));
        }
        assert!(replay.exhausted(), "no loop: recording must be exhausted");
        assert!(matches!(replay.capture(), Err(ControllerError::WindowGone)));
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn replay_loops_when_configured() {
        let dir = std::env::temp_dir().join(format!("nf_replay_loop_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        let mut rec = FrameRecorder::create(&dir, 3).expect("recorder");
        rec.record(&gradient_frame(4, 4)).expect("record");
        let mut replay = ReplayCapture::open(&dir, true).expect("replay");
        for _ in 0..7 {
            replay.capture().expect("looping capture never ends");
        }
        assert!(!replay.exhausted());
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn open_rejects_directories_without_frames() {
        let dir = std::env::temp_dir().join(format!("nf_replay_empty_{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).expect("mkdir");
        let err = match ReplayCapture::open(&dir, true) {
            Err(e) => e,
            Ok(_) => panic!("expected an error for an empty replay dir"),
        };
        assert!(err.to_string().contains("no frame_*.png"), "{err}");
        let _ = std::fs::remove_dir_all(&dir);
    }
}
