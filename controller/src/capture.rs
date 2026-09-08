//! Capture abstraction (NC0): a [`CaptureBackend`] produces BGRA
//! [`Frame`]s of the target window, fully in memory. Two backends exist:
//!
//! - [`SyntheticCapture`] — deterministic moving-rectangle patterns, used
//!   by unit tests and the dry-run mode when no real window is wanted.
//! - [`WgcCapture`] — Windows Graphics Capture per HWND (Windows 10
//!   1903+). Frames flow through D3D11 staging textures into CPU memory;
//!   nothing is written to disk in the capture path.
//!
//! DXGI Desktop Duplication remains a possible fallback backend later
//! (ROADMAP NC0); the trait boundary is where it would plug in.

use crate::frame::Frame;
use crate::window::WindowLayout;
use crate::{ControllerError, Result};
use std::thread;
use std::time::{Duration, Instant};

/// One window capture source. `capture` grabs the latest complete frame;
/// implementations may block briefly (frame is already on the GPU by the
/// time it is produced — CPU copy happens here).
pub trait CaptureBackend {
    fn capture(&mut self) -> Result<Frame>;
    /// The geometry this backend is calibrated against; the dry-run loop
    /// compares it against the live window layout each cycle.
    fn calibrated_layout(&self) -> WindowLayout;
}

// ---------------------------------------------------------------------------
// SyntheticCapture — deterministic pattern, no desktop needed
// ---------------------------------------------------------------------------

/// A synthetic backend drawing a moving bright rectangle over a dark grid.
/// The rectangle position is a pure function of `step`, so tests can assert
/// exact pixels; each `capture` advances the step.
pub struct SyntheticCapture {
    width: u32,
    height: u32,
    layout: WindowLayout,
    rect_size: u32,
    step: u64,
}

impl SyntheticCapture {
    pub fn new(width: u32, height: u32) -> Result<SyntheticCapture> {
        if width == 0 || height == 0 {
            return Err(ControllerError::InvalidInput(format!(
                "synthetic size must be positive, got {width}x{height}"
            )));
        }
        Ok(SyntheticCapture {
            width,
            height,
            layout: WindowLayout {
                client_size: (width as i32, height as i32),
                screen_origin: (0, 0),
                dpi: 96,
            },
            rect_size: 48,
            step: 0,
        })
    }

    pub fn step(&self) -> u64 {
        self.step
    }

    fn draw(&self) -> Frame {
        let mut f = Frame::new(self.width, self.height);
        // dark checker grid every 32px so the overlay/detector has texture
        for y in 0..self.height {
            for x in 0..self.width {
                let g = if ((x / 32 + y / 32) % 2) == 0 { 24 } else { 36 };
                f.set_pixel(x, y, [g, g, g, 255]);
            }
        }
        // moving rectangle: bounces diagonally
        let span = (self.width - self.rect_size).max(1);
        let span_y = (self.height - self.rect_size).max(1);
        let span64 = span as u64;
        let span_y64 = span_y as u64;
        let rx = (self.step % (span64 * 2)) as u32;
        let rx = if rx > span { span * 2 - rx } else { rx };
        let ry = (self.step % (span_y64 * 2)) as u32;
        let ry = if ry > span_y { span_y * 2 - ry } else { ry };
        for y in ry..ry + self.rect_size {
            for x in rx..rx + self.rect_size {
                let edge = x == rx
                    || y == ry
                    || x == rx + self.rect_size - 1
                    || y == ry + self.rect_size - 1;
                let bgra = if edge {
                    [16, 240, 16, 255]
                } else {
                    [16, 40, 200, 255]
                };
                f.set_pixel(x, y, bgra);
            }
        }
        f
    }
}

impl CaptureBackend for SyntheticCapture {
    fn capture(&mut self) -> Result<Frame> {
        self.step += 1;
        Ok(self.draw())
    }

    fn calibrated_layout(&self) -> WindowLayout {
        self.layout.clone()
    }
}

// ---------------------------------------------------------------------------
// FpsLimiter — tick pacing with an injectable clock for tests
// ---------------------------------------------------------------------------

/// Paces a loop to at most `fps` ticks per second. The clock is a closure
/// returning the current [`Instant`], so tests simulate time without
/// sleeping.
pub struct FpsLimiter<F: Fn() -> Instant> {
    min_interval: Duration,
    last_tick: Option<Instant>,
    now: F,
}

impl<F: Fn() -> Instant> FpsLimiter<F> {
    pub fn new(fps: f32, now: F) -> Result<FpsLimiter<F>> {
        if !(fps.is_finite() && fps > 0.0) {
            return Err(ControllerError::InvalidInput(format!(
                "fps must be > 0, got {fps}"
            )));
        }
        let min_interval = Duration::from_secs_f64(1.0 / fps as f64);
        Ok(FpsLimiter {
            min_interval,
            last_tick: None,
            now,
        })
    }

    /// Block until the next tick is due; returns false when no wait was
    /// needed. First call always passes immediately.
    pub fn wait_tick(&mut self) -> bool {
        let now = (self.now)();
        match self.last_tick {
            None => {
                self.last_tick = Some(now);
                false
            }
            Some(last) => {
                let elapsed = now.duration_since(last);
                if elapsed < self.min_interval {
                    thread::sleep(self.min_interval - elapsed);
                }
                self.last_tick = Some((self.now)());
                true
            }
        }
    }
}

// ---------------------------------------------------------------------------
// WgcCapture — Windows Graphics Capture per HWND
// ---------------------------------------------------------------------------

#[cfg(windows)]
mod wgc {
    //! Real capture path. Windows Graphics Capture captures the window's
    //! full client content into a GPU texture; we copy it to a staging
    //! texture and map it into a CPU [`Frame`]. No file I/O, no hooks, no
    //! injection — WGC is the OS-supported, user-consentable capture API.

    use super::CaptureBackend;
    use crate::frame::{Frame, BYTES_PER_PIXEL};
    use crate::window::WindowLayout;
    use crate::{ControllerError, Result};
    use std::thread;
    use std::time::Duration;
    use windows::core::Interface;
    use windows::Foundation::TypedEventHandler;
    use windows::Graphics::Capture::{Direct3D11CaptureFramePool, GraphicsCaptureItem};
    use windows::Graphics::DirectX::Direct3D11::IDirect3DDevice;
    use windows::Graphics::DirectX::DirectXPixelFormat;
    use windows::Win32::Foundation::HWND;
    use windows::Win32::Graphics::Direct3D::{
        D3D_DRIVER_TYPE_HARDWARE, D3D_DRIVER_TYPE_WARP, D3D_FEATURE_LEVEL_11_0,
    };
    use windows::Win32::Graphics::Direct3D11::{
        D3D11CreateDevice, ID3D11Device, ID3D11DeviceContext, ID3D11Texture2D,
        D3D11_CPU_ACCESS_READ, D3D11_CREATE_DEVICE_BGRA_SUPPORT, D3D11_MAPPED_SUBRESOURCE,
        D3D11_MAP_READ, D3D11_SDK_VERSION, D3D11_TEXTURE2D_DESC, D3D11_USAGE_STAGING,
    };
    use windows::Win32::Graphics::Dxgi::Common::DXGI_FORMAT_B8G8R8A8_UNORM;
    use windows::Win32::Graphics::Dxgi::IDXGIDevice;
    use windows::Win32::System::WinRT::Direct3D11::{
        CreateDirect3D11DeviceFromDXGIDevice, IDirect3DDxgiInterfaceAccess,
    };
    use windows::Win32::System::WinRT::Graphics::Capture::IGraphicsCaptureItemInterop;

    pub struct WgcCapture {
        item: GraphicsCaptureItem,
        pool: Direct3D11CaptureFramePool,
        device: ID3D11Device,
        context: ID3D11DeviceContext,
        staging: ID3D11Texture2D,
        width: i32,
        height: i32,
        layout: WindowLayout,
        /// Count of FrameArrived events — diagnostics for "no frames".
        arrived: std::sync::Arc<std::sync::atomic::AtomicU32>,
        /// Keeps the FrameArrived registration alive; dropping it revokes
        /// the handler (this exact bug produced "no frames" on first run).
        _arrived_token: i64,
    }

    fn make_d3d_device() -> Result<(ID3D11Device, ID3D11DeviceContext)> {
        // hardware first, WARP (software) as fallback for headless-ish VMs
        for driver in [D3D_DRIVER_TYPE_HARDWARE, D3D_DRIVER_TYPE_WARP] {
            let mut device = None;
            let mut context = None;
            let hr = unsafe {
                D3D11CreateDevice(
                    None,
                    driver,
                    windows::Win32::Foundation::HMODULE::default(),
                    D3D11_CREATE_DEVICE_BGRA_SUPPORT,
                    Some(&[D3D_FEATURE_LEVEL_11_0]),
                    D3D11_SDK_VERSION,
                    Some(&mut device),
                    None,
                    Some(&mut context),
                )
            };
            if let (Ok(()), Some(d), Some(c)) = (hr, device, context) {
                if std::env::var_os("NF_DEBUG_D3D").is_some() {
                    eprintln!("d3d: driver_type={driver:?}");
                }
                return Ok((d, c));
            }
        }
        Err(ControllerError::Win(
            windows::core::Error::from_hresult(windows::core::HRESULT(0x887A0004u32 as i32)), // DXGI_ERROR_UNSUPPORTED
        ))
    }

    fn winrt_device(dxgi: &IDXGIDevice) -> Result<IDirect3DDevice> {
        let inspectable = unsafe { CreateDirect3D11DeviceFromDXGIDevice(dxgi) }?;
        inspectable
            .cast::<IDirect3DDevice>()
            .map_err(ControllerError::Win)
    }

    impl WgcCapture {
        /// Start capturing `hwnd`. The window must be visible on the
        /// current desktop.
        pub fn new(hwnd: HWND, layout: WindowLayout) -> Result<WgcCapture> {
            let stage =
                |name: &'static str, e: ControllerError| ControllerError::Stage(name, Box::new(e));
            let (device, context) = make_d3d_device().map_err(|e| stage("wgc:d3d-device", e))?;
            let dxgi: IDXGIDevice = device
                .cast()
                .map_err(|e| stage("wgc:dxgi-cast", ControllerError::Win(e)))?;
            let winrt_dev = winrt_device(&dxgi).map_err(|e| stage("wgc:winrt-device", e))?;

            let interop: IGraphicsCaptureItemInterop =
                windows::core::factory::<GraphicsCaptureItem, IGraphicsCaptureItemInterop>()
                    .map_err(|e| stage("wgc:factory", ControllerError::Win(e)))?;
            let item: GraphicsCaptureItem = unsafe { interop.CreateForWindow(hwnd) }
                .map_err(|e| stage("wgc:create-for-window", ControllerError::Win(e)))?;

            let size = item
                .Size()
                .map_err(|e| stage("wgc:item-size", ControllerError::Win(e)))?;
            let pool = Direct3D11CaptureFramePool::CreateFreeThreaded(
                &winrt_dev,
                DirectXPixelFormat::B8G8R8A8UIntNormalized,
                2,
                size,
            )
            .map_err(|e| stage("wgc:frame-pool", ControllerError::Win(e)))?;
            let session = pool
                .CreateCaptureSession(&item)
                .map_err(|e| stage("wgc:session", ControllerError::Win(e)))?;
            // cursor off; border requirement off is best-effort (Win11 may
            // refuse without user consent — ignore that failure)
            let _ = session.SetIsCursorCaptureEnabled(false);
            let arrived = std::sync::Arc::new(std::sync::atomic::AtomicU32::new(0));
            let arrived_cb = arrived.clone();
            let _arrived_token = pool.FrameArrived(&TypedEventHandler::<
                Direct3D11CaptureFramePool,
                windows::core::IInspectable,
            >::new(move |_pool, _args| {
                use std::sync::atomic::Ordering;
                arrived_cb.fetch_add(1, Ordering::Relaxed);
                Ok(())
            }))?;
            session
                .StartCapture()
                .map_err(|e| stage("wgc:start", ControllerError::Win(e)))?;

            let staging = create_staging(&device, size.Width, size.Height)
                .map_err(|e| stage("wgc:staging", e))?;
            Ok(WgcCapture {
                item,
                pool,
                device,
                context,
                staging,
                width: size.Width,
                height: size.Height,
                layout,
                arrived,
                _arrived_token,
            })
        }

        /// Capture the monitor containing point (0,0) — the diagnostic
        /// sibling of `new`: if monitor capture works while window capture
        /// does not, the failure is window-specific, not session-level.
        pub fn for_primary_monitor() -> Result<WgcCapture> {
            let hmon = unsafe {
                windows::Win32::Graphics::Gdi::MonitorFromPoint(
                    windows::Win32::Foundation::POINT { x: 0, y: 0 },
                    windows::Win32::Graphics::Gdi::MONITOR_DEFAULTTOPRIMARY,
                )
            };
            let mut info = windows::Win32::Graphics::Gdi::MONITORINFO {
                cbSize: std::mem::size_of::<windows::Win32::Graphics::Gdi::MONITORINFO>() as u32,
                ..Default::default()
            };
            if !unsafe { windows::Win32::Graphics::Gdi::GetMonitorInfoW(hmon, &mut info) }.as_bool()
            {
                return Err(ControllerError::Win(windows::core::Error::from_hresult(
                    windows::core::HRESULT(0x8007139Fu32 as i32),
                )));
            }
            let (device, context) = make_d3d_device()
                .map_err(|e| ControllerError::Stage("wgc:d3d-device", Box::new(e)))?;
            let dxgi: IDXGIDevice = device.cast().map_err(|e| {
                ControllerError::Stage("wgc:dxgi-cast", Box::new(ControllerError::Win(e)))
            })?;
            let winrt_dev = winrt_device(&dxgi)
                .map_err(|e| ControllerError::Stage("wgc:winrt-device", Box::new(e)))?;
            let interop: IGraphicsCaptureItemInterop =
                windows::core::factory::<GraphicsCaptureItem, IGraphicsCaptureItemInterop>()
                    .map_err(|e| {
                        ControllerError::Stage("wgc:factory", Box::new(ControllerError::Win(e)))
                    })?;
            let item: GraphicsCaptureItem =
                unsafe { interop.CreateForMonitor(hmon) }.map_err(|e| {
                    ControllerError::Stage(
                        "wgc:create-for-monitor",
                        Box::new(ControllerError::Win(e)),
                    )
                })?;
            let size = item.Size().map_err(|e| {
                ControllerError::Stage("wgc:item-size", Box::new(ControllerError::Win(e)))
            })?;
            let pool = Direct3D11CaptureFramePool::CreateFreeThreaded(
                &winrt_dev,
                DirectXPixelFormat::B8G8R8A8UIntNormalized,
                2,
                size,
            )
            .map_err(|e| {
                ControllerError::Stage("wgc:frame-pool", Box::new(ControllerError::Win(e)))
            })?;
            let session = pool.CreateCaptureSession(&item).map_err(|e| {
                ControllerError::Stage("wgc:session", Box::new(ControllerError::Win(e)))
            })?;
            let _ = session.SetIsCursorCaptureEnabled(false);
            let arrived = std::sync::Arc::new(std::sync::atomic::AtomicU32::new(0));
            let arrived_cb = arrived.clone();
            let _arrived_token = pool
                .FrameArrived(&TypedEventHandler::<
                    Direct3D11CaptureFramePool,
                    windows::core::IInspectable,
                >::new(move |_pool, _args| {
                    use std::sync::atomic::Ordering;
                    arrived_cb.fetch_add(1, Ordering::Relaxed);
                    Ok(())
                }))
                .map_err(|e| {
                    ControllerError::Stage("wgc:frame-arrived", Box::new(ControllerError::Win(e)))
                })?;
            session.StartCapture().map_err(|e| {
                ControllerError::Stage("wgc:start", Box::new(ControllerError::Win(e)))
            })?;
            let staging = create_staging(&device, size.Width, size.Height)
                .map_err(|e| ControllerError::Stage("wgc:staging", Box::new(e)))?;
            let layout = WindowLayout {
                client_size: (
                    info.rcMonitor.right - info.rcMonitor.left,
                    info.rcMonitor.bottom - info.rcMonitor.top,
                ),
                screen_origin: (info.rcMonitor.left, info.rcMonitor.top),
                dpi: 96,
            };
            Ok(WgcCapture {
                item,
                pool,
                device,
                context,
                staging,
                width: size.Width,
                height: size.Height,
                layout,
                arrived,
                _arrived_token,
            })
        }

        fn recreate_if_resized(&mut self) -> Result<()> {
            let size = self.item.Size().map_err(ControllerError::Win)?;
            if size.Width != self.width || size.Height != self.height {
                self.pool
                    .Recreate(
                        &self.winrt_device()?,
                        DirectXPixelFormat::B8G8R8A8UIntNormalized,
                        2,
                        size,
                    )
                    .map_err(ControllerError::Win)?;
                self.staging = create_staging(&self.device, size.Width, size.Height)?;
                self.width = size.Width;
                self.height = size.Height;
            }
            Ok(())
        }

        fn winrt_device(&self) -> Result<IDirect3DDevice> {
            let dxgi: IDXGIDevice = self.device.cast().map_err(ControllerError::Win)?;
            winrt_device(&dxgi)
        }
    }

    fn create_staging(device: &ID3D11Device, w: i32, h: i32) -> Result<ID3D11Texture2D> {
        let desc = D3D11_TEXTURE2D_DESC {
            Width: w as u32,
            Height: h as u32,
            MipLevels: 1,
            ArraySize: 1,
            Format: DXGI_FORMAT_B8G8R8A8_UNORM,
            SampleDesc: windows::Win32::Graphics::Dxgi::Common::DXGI_SAMPLE_DESC {
                Count: 1,
                Quality: 0,
            },
            Usage: D3D11_USAGE_STAGING,
            BindFlags: 0,
            CPUAccessFlags: D3D11_CPU_ACCESS_READ.0 as u32,
            MiscFlags: 0,
        };
        let mut staging = None;
        unsafe { device.CreateTexture2D(&desc, None, Some(&mut staging)) }
            .map_err(ControllerError::Win)?;
        staging.ok_or(ControllerError::WindowGone)
    }

    impl CaptureBackend for WgcCapture {
        fn capture(&mut self) -> Result<Frame> {
            self.recreate_if_resized()?;
            // Poll for the newest frame. WGC delivers frames only when the
            // window content changes; callers that need a frame from a
            // static window should nudge it (repaint) between attempts.
            // TryGetNextFrame errors while the pool is empty — retry for up
            // to ~1s before reporting failure.
            let mut frame = None;
            let mut last_err = None;
            for _ in 0..250 {
                match self.pool.TryGetNextFrame() {
                    Ok(f) => {
                        frame = Some(f);
                        break;
                    }
                    Err(e) => {
                        if last_err.is_none() {
                            last_err = Some(e.to_string());
                        }
                        thread::sleep(Duration::from_millis(4));
                    }
                }
            }
            let frame = frame.ok_or_else(|| {
                let arrived_n = self.arrived.load(std::sync::atomic::Ordering::Relaxed);
                ControllerError::Stage(
                    "wgc:try-next(1s)",
                    Box::new(ControllerError::InvalidInput(format!(
                        "no frame; frame_arrived_events={arrived_n}; last_poll={:?}",
                        last_err.unwrap_or_else(|| "none".into())
                    ))),
                )
            })?;
            let surface = frame.Surface().map_err(ControllerError::Win)?;
            let access: IDirect3DDxgiInterfaceAccess =
                surface.cast().map_err(ControllerError::Win)?;
            let tex: ID3D11Texture2D =
                unsafe { access.GetInterface() }.map_err(ControllerError::Win)?;

            let mut desc = D3D11_TEXTURE2D_DESC::default();
            unsafe { tex.GetDesc(&mut desc) };
            if desc.Width != self.staging_width() || desc.Height != self.staging_height() {
                // resize raced us; recreate staging from the actual frame
                self.staging = create_staging(&self.device, desc.Width as i32, desc.Height as i32)?;
                self.width = desc.Width as i32;
                self.height = desc.Height as i32;
            }

            unsafe {
                self.context.CopyResource(&self.staging, &tex);
            }
            let mut mapped = D3D11_MAPPED_SUBRESOURCE::default();
            unsafe {
                self.context
                    .Map(&self.staging, 0, D3D11_MAP_READ, 0, Some(&mut mapped))
            }
            .map_err(ControllerError::Win)?;
            let bytes = unsafe {
                std::slice::from_raw_parts(
                    mapped.pData as *const u8,
                    mapped.RowPitch as usize * self.height as usize,
                )
            };
            let data = copy_with_row_pitch(
                bytes,
                mapped.RowPitch as usize,
                self.width as usize,
                self.height as usize,
            );
            unsafe { self.context.Unmap(&self.staging, 0) };

            Frame::from_bgra(
                data,
                self.width as u32,
                self.height as u32,
                self.width as u32 * BYTES_PER_PIXEL as u32,
            )
        }

        fn calibrated_layout(&self) -> WindowLayout {
            self.layout.clone()
        }
    }

    impl WgcCapture {
        fn staging_width(&self) -> u32 {
            self.width as u32
        }
        fn staging_height(&self) -> u32 {
            self.height as u32
        }
        /// FrameArrived event count since construction (diagnostics).
        pub fn frames_arrived(&self) -> u32 {
            self.arrived.load(std::sync::atomic::Ordering::Relaxed)
        }
    }

    fn copy_with_row_pitch(src: &[u8], pitch: usize, w: usize, h: usize) -> Vec<u8> {
        let row = w * BYTES_PER_PIXEL;
        let mut out = Vec::with_capacity(row * h);
        for y in 0..h {
            let start = y * pitch;
            out.extend_from_slice(&src[start..start + row]);
        }
        out
    }

    #[cfg(test)]
    mod tests {
        use super::*;

        #[test]
        fn row_pitch_copy_tightens_padded_rows() {
            // 2x2 rows of 8 bytes with pitch 16
            let mut src = vec![0u8; 16 * 2];
            src[0..8].copy_from_slice(&[1, 2, 3, 4, 5, 6, 7, 8]);
            src[16..24].copy_from_slice(&[9, 10, 11, 12, 13, 14, 15, 16]);
            let out = copy_with_row_pitch(&src, 16, 2, 2);
            assert_eq!(out.len(), 16);
            assert_eq!(&out[0..8], &[1, 2, 3, 4, 5, 6, 7, 8]);
            assert_eq!(&out[8..16], &[9, 10, 11, 12, 13, 14, 15, 16]);
        }
    }
}

// ---------------------------------------------------------------------------
// GdiPrintWindowCapture — plain GDI fallback via PrintWindow
// ---------------------------------------------------------------------------

#[cfg(windows)]
mod gdi {
    //! `PrintWindow(PW_RENDERFULLCONTENT)` capture: plain GDI, works even
    //! when Windows Graphics Capture is unavailable (RDP sessions, managed
    //! policies, VMs without DWM access). Reads only the window's own
    //! redirection surface — no injection, no hooks, no memory access.

    use super::CaptureBackend;
    use crate::frame::{Frame, BYTES_PER_PIXEL};
    use crate::window::WindowLayout;
    use crate::{ControllerError, Result};
    use windows::Win32::Foundation::HWND;
    use windows::Win32::Graphics::Gdi::{
        CreateCompatibleBitmap, CreateCompatibleDC, DeleteDC, DeleteObject, GetDC, GetDIBits,
        ReleaseDC, SelectObject, BITMAPINFO, BITMAPINFOHEADER, BI_RGB, DIB_RGB_COLORS, HGDIOBJ,
    };
    use windows::Win32::Storage::Xps::{PrintWindow, PRINT_WINDOW_FLAGS, PW_CLIENTONLY};

    pub struct GdiPrintWindowCapture {
        hwnd: HWND,
        width: i32,
        height: i32,
        layout: WindowLayout,
    }

    impl GdiPrintWindowCapture {
        /// Capture the window's client area. The layout is the calibration
        /// snapshot used by the transform pipeline.
        pub fn new(hwnd: HWND, layout: WindowLayout) -> Result<GdiPrintWindowCapture> {
            let (width, height) = layout.client_size;
            if width <= 0 || height <= 0 {
                return Err(ControllerError::InvalidInput(format!(
                    "client size must be positive, got {width}x{height}"
                )));
            }
            Ok(GdiPrintWindowCapture {
                hwnd,
                width,
                height,
                layout,
            })
        }
    }

    impl CaptureBackend for GdiPrintWindowCapture {
        fn capture(&mut self) -> Result<Frame> {
            let (w, h) = (self.width, self.height);
            // top-down 32bpp: negative height
            let mut bi = BITMAPINFO {
                bmiHeader: BITMAPINFOHEADER {
                    biSize: std::mem::size_of::<BITMAPINFOHEADER>() as u32,
                    biWidth: w,
                    biHeight: -h,
                    biPlanes: 1,
                    biBitCount: 32,
                    biCompression: BI_RGB.0,
                    ..Default::default()
                },
                bmiColors: [Default::default()],
            };
            let row = w as usize * BYTES_PER_PIXEL;
            let mut data = vec![0u8; row * h as usize];

            unsafe {
                let hdc_window = GetDC(Some(self.hwnd));
                if hdc_window.is_invalid() {
                    return Err(ControllerError::Win(windows::core::Error::from_hresult(
                        windows::core::HRESULT(0x80070006u32 as i32), // E_HANDLE
                    )));
                }
                let hdc_mem = CreateCompatibleDC(Some(hdc_window));
                let bitmap = CreateCompatibleBitmap(hdc_window, w, h);
                let old = SelectObject(hdc_mem, HGDIOBJ(bitmap.0));
                let printed = PrintWindow(
                    self.hwnd,
                    hdc_mem,
                    PRINT_WINDOW_FLAGS(PW_CLIENTONLY.0 | 2u32),
                ); // PW_CLIENTONLY | PW_RENDERFULLCONTENT
                let got = if printed.as_bool() {
                    GetDIBits(
                        hdc_mem,
                        bitmap,
                        0,
                        h as u32,
                        Some(data.as_mut_ptr().cast()),
                        &mut bi,
                        DIB_RGB_COLORS,
                    )
                } else {
                    0
                };
                SelectObject(hdc_mem, old);
                let _ = DeleteObject(HGDIOBJ(bitmap.0));
                let _ = DeleteDC(hdc_mem);
                ReleaseDC(Some(self.hwnd), hdc_window);
                if !printed.as_bool() {
                    // PrintWindow fails when the hwnd is no longer valid —
                    // the deterministic end state for the observation loop.
                    return Err(ControllerError::WindowGone);
                }
                if got == 0 {
                    // the window lives but the bitmap read hiccuped —
                    // retryable, NOT a terminal WindowGone
                    return Err(ControllerError::Win(windows::core::Error::from_hresult(
                        windows::core::HRESULT(0x8007139Fu32 as i32), // ERROR_OPERATION_ABORTED-ish transient marker
                    )));
                }
            }
            Frame::from_bgra(data, w as u32, h as u32, row as u32)
        }

        fn calibrated_layout(&self) -> WindowLayout {
            self.layout.clone()
        }
    }
}

#[cfg(windows)]
pub use gdi::GdiPrintWindowCapture;

#[cfg(windows)]
pub use wgc::WgcCapture;

#[cfg(test)]
mod tests {
    use super::*;
    use crate::transform::Rect;

    fn find_moving_rect(f: &Frame) -> Option<Rect> {
        // locate the brightest-red rectangle bounding box
        let mut min = (u32::MAX, u32::MAX);
        let mut max = (0u32, 0u32);
        let mut found = false;
        for y in 0..f.height {
            for x in 0..f.width {
                if let Some([b, g, r, _a]) = f.pixel(x, y) {
                    if r > 150 && g < 100 && b < 100 {
                        found = true;
                        min = (min.0.min(x), min.1.min(y));
                        max = (max.0.max(x), max.1.max(y));
                    }
                }
            }
        }
        if !found {
            return None;
        }
        Some(Rect::new(
            min.0 as f32,
            min.1 as f32,
            (max.0 - min.0 + 1) as f32,
            (max.1 - min.1 + 1) as f32,
        ))
    }

    #[test]
    fn synthetic_capture_rejects_zero_size() {
        assert!(SyntheticCapture::new(0, 100).is_err());
    }

    #[test]
    fn synthetic_frames_are_deterministic_and_advance() {
        let mut c = SyntheticCapture::new(200, 150).expect("cap");
        let f1 = c.capture().expect("f1");
        let f2 = c.capture().expect("f2");
        assert_eq!(c.step(), 2);
        let r1 = find_moving_rect(&f1).expect("rect in f1");
        let r2 = find_moving_rect(&f2).expect("rect in f2");
        // the finder matches the red fill only; the 1px green border is
        // excluded, so the measured box is 46x46 for a 48px rect
        assert_eq!(r1.w, 46.0);
        assert_eq!(r1.h, 46.0);
        assert_ne!(r1, r2, "rectangle must move between steps");
        // same step => same frame (determinism): a fresh generator at step 2
        // must reproduce f2's rectangle exactly
        let mut c2 = SyntheticCapture::new(200, 150).expect("cap2");
        let _ = c2.capture();
        let f2_again = c2.capture().expect("f2 again");
        let r2_again = find_moving_rect(&f2_again).expect("rect again");
        assert_eq!(r2, r2_again);
    }

    #[test]
    fn fps_limiter_first_tick_is_free_and_rate_is_capped() {
        use std::cell::Cell;
        let t = Cell::new(Instant::now());
        let mut lim = FpsLimiter::new(50.0, || t.get()).expect("lim"); // 20ms interval
        assert!(!lim.wait_tick(), "first tick passes immediately");
        // simulate 5ms elapsed: the limiter must sleep the remaining ~15ms
        t.set(t.get() + Duration::from_millis(5));
        let before = Instant::now();
        lim.wait_tick();
        let took = Instant::now().duration_since(before);
        assert!(
            took >= Duration::from_millis(10),
            "tick must pace to the fps interval, took {took:?}"
        );
    }

    #[test]
    fn fps_limiter_rejects_bad_fps() {
        assert!(FpsLimiter::new(0.0, Instant::now).is_err());
        assert!(FpsLimiter::new(f32::NAN, Instant::now).is_err());
        assert!(FpsLimiter::new(-5.0, Instant::now).is_err());
    }
}
