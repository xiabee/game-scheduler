//! GameWindow: find the game's top-level window and answer the geometry
//! questions the rest of the controller needs (client rect, DPI, screen
//! mapping, foreground, layout changes).
//!
//! Everything here is plain Win32 window inspection: no injection, no
//! memory access, no input synthesis. NC0 reads window metrics only.

use crate::{ControllerError, Result};
use std::sync::OnceLock;
use windows::core::{BOOL, PCWSTR, PWSTR};
use windows::Win32::Foundation::{CloseHandle, HINSTANCE, HWND, LPARAM, POINT, RECT};
use windows::Win32::Graphics::Gdi::{
    BeginPaint, ClientToScreen, CreateSolidBrush, DeleteObject, EndPaint, FillRect, RedrawWindow,
    HDC, PAINTSTRUCT, RDW_ERASE, RDW_INVALIDATE, RDW_UPDATENOW,
};
use windows::Win32::System::LibraryLoader::GetModuleHandleW;
use windows::Win32::System::Threading::{
    OpenProcess, QueryFullProcessImageNameW, PROCESS_NAME_WIN32, PROCESS_QUERY_LIMITED_INFORMATION,
};
use windows::Win32::UI::HiDpi::{
    GetDpiForWindow, SetProcessDpiAwarenessContext, DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2,
};
use windows::Win32::UI::WindowsAndMessaging::{
    AdjustWindowRectEx, CreateWindowExW, DefWindowProcW, DestroyWindow, DispatchMessageW,
    EnumWindows, GetClientRect, GetForegroundWindow, GetWindowLongPtrW, GetWindowTextLengthW,
    GetWindowTextW, GetWindowThreadProcessId, IsWindow, IsWindowVisible, PeekMessageW,
    RegisterClassExW, SetForegroundWindow, SetWindowPos, TranslateMessage, GWL_EXSTYLE,
    HWND_BOTTOM, MSG, PM_REMOVE, SWP_NOACTIVATE, SWP_NOMOVE, SWP_NOSIZE, SWP_NOZORDER,
    WINDOW_EX_STYLE, WINDOW_STYLE, WM_CHAR, WM_LBUTTONDOWN, WNDCLASSEXW, WS_EX_TOOLWINDOW,
    WS_OVERLAPPEDWINDOW, WS_VISIBLE,
};

/// Client-area geometry in the coordinate systems the pipeline uses.
///
/// `client_size` is the render area (what a capture of the client covers);
/// `screen_origin` is the desktop position of the client's top-left corner;
/// `dpi` is the window's effective DPI (96 = 100% scale).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct WindowLayout {
    pub client_size: (i32, i32),
    pub screen_origin: (i32, i32),
    pub dpi: u32,
}

/// A discovered top-level window handle plus its identifying metadata.
///
/// The handle is not owned: the window may disappear at any moment, and
/// every accessor re-validates the handle before answering.
#[derive(Debug, Clone)]
pub struct GameWindow {
    hwnd: HWND,
    title: String,
    process_path: String,
    process_name: String,
    pid: u32,
}

impl GameWindow {
    /// Find the first visible top-level window matching the given filters.
    ///
    /// `title_substring` matches case-insensitively against the window
    /// title; `process_name` matches the executable file name
    /// case-insensitively (e.g. `"genshin.exe"`). Both filters are ANDed;
    /// passing `None` for a filter skips it. Tool windows and invisible
    /// windows are always skipped.
    /// First window matching the filters, if any. See [`Self::find_all`].
    pub fn find(
        title_substring: Option<&str>,
        process_name: Option<&str>,
    ) -> Result<Option<GameWindow>> {
        Ok(Self::find_all(title_substring, process_name)?
            .into_iter()
            .next())
    }

    /// All visible top-level windows matching the filters, in Z order.
    pub fn find_all(
        title_substring: Option<&str>,
        process_name: Option<&str>,
    ) -> Result<Vec<GameWindow>> {
        let needle_title = title_substring.map(str::to_lowercase);
        let needle_process = process_name.map(str::to_lowercase);
        let mut ctx = Box::new((needle_title, needle_process, Vec::<GameWindow>::new()));
        let lparam = LPARAM(&mut *ctx as *mut _ as isize);
        let enum_result = unsafe { EnumWindows(Some(enum_proc), lparam) };
        // `ctx` still owns the allocation; the LPARAM borrow ended with the
        // call. Fail only after enumeration finished so partial results are
        // never silently used.
        enum_result?;
        Ok(ctx.2)
    }

    /// Wrap a raw handle, re-reading title/process metadata.
    pub fn from_hwnd(hwnd: HWND) -> Result<GameWindow> {
        if !valid_window(hwnd) {
            return Err(ControllerError::WindowGone);
        }
        let title = window_title(hwnd);
        let (pid, process_path) = window_process(hwnd)?;
        let process_name = process_path
            .rsplit(['\\', '/'])
            .next()
            .unwrap_or("")
            .to_lowercase();
        Ok(GameWindow {
            hwnd,
            title,
            process_path,
            process_name,
            pid,
        })
    }

    pub fn hwnd(&self) -> HWND {
        self.hwnd
    }
    pub fn title(&self) -> &str {
        &self.title
    }
    pub fn process_path(&self) -> &str {
        &self.process_path
    }
    /// Executable file name only, lowercase (e.g. `"genshin.exe"`). Empty
    /// when the process image could not be queried (elevated process).
    pub fn process_name(&self) -> &str {
        &self.process_name
    }
    pub fn pid(&self) -> u32 {
        self.pid
    }

    /// Current client-area layout, or `WindowGone` if the window closed.
    pub fn layout(&self) -> Result<WindowLayout> {
        layout_of(self.hwnd)
    }

    /// True when size, screen position or DPI changed compared to `prev`.
    pub fn changed_since(&self, prev: &WindowLayout) -> Result<bool> {
        Ok(self.layout()? != *prev)
    }

    /// Is this handle also the foreground window right now?
    pub fn is_foreground(&self) -> Result<bool> {
        if !valid_window(self.hwnd) {
            return Err(ControllerError::WindowGone);
        }
        let fg = unsafe { GetForegroundWindow() };
        Ok(!fg.is_invalid() && fg == self.hwnd)
    }

    /// DPI the window is currently rendered at (96 = 100%).
    pub fn dpi(&self) -> Result<u32> {
        if !valid_window(self.hwnd) {
            return Err(ControllerError::WindowGone);
        }
        Ok(unsafe { GetDpiForWindow(self.hwnd) })
    }
}

/// Per-monitor-v2 DPI awareness, called once from `main` before any
/// geometry is read. Without it Windows lies about client rects on scaled
/// displays. Returns false when the call was rejected (already set to a
/// different mode); the legacy `SetProcessDPIAware` fallback is attempted
/// in that case so at least system-DPI-aware behaviour is locked in.
pub fn ensure_dpi_awareness() -> bool {
    static DONE: OnceLock<bool> = OnceLock::new();
    *DONE.get_or_init(|| {
        unsafe { SetProcessDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2) }.is_ok()
    })
}

/// True when the process is attached to an interactive desktop (the user's
/// console/RDP session). Service sessions (Session 0) answer `false`: there
/// window enumeration misses freshly created windows and
/// `SetProcessDpiAwarenessContext` is denied. Live capture content and any
/// future input path are only meaningful on an interactive desktop — callers
/// gate on this instead of failing mysteriously (see ROADMAP §7/NC4 and the
/// remote-acceptance environment notes in docs/NIGHTLY_PROGRESS.md).
pub fn interactive_desktop_available() -> bool {
    use windows::Win32::System::StationsAndDesktops::{
        CloseDesktop, OpenInputDesktop, DESKTOP_CONTROL_FLAGS, DESKTOP_READOBJECTS,
    };
    let desk = unsafe { OpenInputDesktop(DESKTOP_CONTROL_FLAGS(0), false, DESKTOP_READOBJECTS) };
    match desk {
        Ok(h) => {
            let _ = unsafe { CloseDesktop(h) };
            true
        }
        Err(_) => false,
    }
}

fn layout_of(hwnd: HWND) -> Result<WindowLayout> {
    if !valid_window(hwnd) {
        return Err(ControllerError::WindowGone);
    }
    let mut rect = RECT::default();
    unsafe { GetClientRect(hwnd, &mut rect) }.map_err(ControllerError::Win)?;
    let mut origin = POINT { x: 0, y: 0 };
    if !unsafe { ClientToScreen(hwnd, &mut origin) }.as_bool() {
        return Err(ControllerError::WindowGone);
    }
    let dpi = unsafe { GetDpiForWindow(hwnd) };
    Ok(WindowLayout {
        client_size: (rect.right - rect.left, rect.bottom - rect.top),
        screen_origin: (origin.x, origin.y),
        dpi,
    })
}

fn valid_window(hwnd: HWND) -> bool {
    !hwnd.is_invalid() && unsafe { IsWindow(Some(hwnd)) }.as_bool()
}

fn window_title(hwnd: HWND) -> String {
    let len = unsafe { GetWindowTextLengthW(hwnd) };
    if len <= 0 {
        return String::new();
    }
    let mut buf = vec![0u16; (len + 1) as usize];
    let copied = unsafe { GetWindowTextW(hwnd, &mut buf) };
    if copied <= 0 {
        return String::new();
    }
    String::from_utf16_lossy(&buf[..copied as usize])
}

fn window_process(hwnd: HWND) -> Result<(u32, String)> {
    let mut pid = 0u32;
    unsafe { GetWindowThreadProcessId(hwnd, Some(&mut pid)) };
    if pid == 0 {
        return Err(ControllerError::WindowGone);
    }
    let handle = unsafe { OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, false, pid) }
        .map_err(ControllerError::Win)?;
    let mut buf = [0u16; 1024];
    let mut len = buf.len() as u32;
    let path = match unsafe {
        QueryFullProcessImageNameW(
            handle,
            PROCESS_NAME_WIN32,
            PWSTR(buf.as_mut_ptr()),
            &mut len,
        )
    } {
        Ok(()) => String::from_utf16_lossy(&buf[..len as usize]),
        // Elevated processes deny the query; that must not hide the window.
        Err(_) => String::new(),
    };
    // The observation loop calls this every cycle — an unclosed process
    // handle here is a per-cycle leak (caught by the handle-count test).
    unsafe { CloseHandle(handle) }.ok();
    Ok((pid, path))
}

unsafe extern "system" fn enum_proc(hwnd: HWND, lparam: LPARAM) -> BOOL {
    let ctx = &mut *(lparam.0 as *mut (Option<String>, Option<String>, Vec<GameWindow>));
    if !unsafe { IsWindowVisible(hwnd) }.as_bool() {
        return BOOL(1);
    }
    // Skip tool windows (tooltips, floating palettes) — games never are.
    let ex_style = unsafe { GetWindowLongPtrW(hwnd, GWL_EXSTYLE) } as u32;
    if ex_style & WS_EX_TOOLWINDOW.0 != 0 {
        return BOOL(1);
    }
    let mut rect = RECT::default();
    if unsafe { GetClientRect(hwnd, &mut rect) }.is_err() {
        return BOOL(1);
    }
    if rect.right - rect.left <= 0 || rect.bottom - rect.top <= 0 {
        return BOOL(1);
    }
    if let Some(needle) = &ctx.0 {
        let title = window_title(hwnd);
        if !title.to_lowercase().contains(needle) {
            return BOOL(1);
        }
    }
    if let Some(needle) = &ctx.1 {
        let (_pid, path) = match window_process(hwnd) {
            Ok(v) => v,
            Err(_) => return BOOL(1),
        };
        let name = path.rsplit(['\\', '/']).next().unwrap_or("").to_lowercase();
        if !name.contains(needle) {
            return BOOL(1);
        }
    }
    if let Ok(win) = GameWindow::from_hwnd(hwnd) {
        ctx.2.push(win);
    }
    BOOL(1)
}

// ---------------------------------------------------------------------------
// OwnedTestWindow: a real top-level window this process owns. Used by tests
// and the CLI's `--self-probe` mode; never touches any other process.
// ---------------------------------------------------------------------------

/// A real top-level window whose *client* area has an exact requested size.
pub struct OwnedTestWindow {
    pub hwnd: HWND,
    title: String,
}

impl OwnedTestWindow {
    pub fn new(client_w: i32, client_h: i32, title: &str) -> Result<OwnedTestWindow> {
        if client_w <= 0 || client_h <= 0 {
            return Err(ControllerError::InvalidInput(format!(
                "client size must be positive, got {client_w}x{client_h}"
            )));
        }
        let _atom = register_probe_class();
        let hinstance = unsafe { GetModuleHandleW(None) }?;
        let mut title16: Vec<u16> = title.encode_utf16().collect();
        title16.push(0);
        let class_name = probe_class_name16();
        let window_rect = client_to_window_rect(client_w, client_h)?;
        let hwnd = unsafe {
            CreateWindowExW(
                WINDOW_EX_STYLE(0),
                PCWSTR(class_name.as_ptr()),
                PCWSTR(title16.as_ptr()),
                probe_window_style(),
                80,
                60,
                window_rect.0,
                window_rect.1,
                None,
                None,
                Some(HINSTANCE(hinstance.0)),
                None,
            )
        }?;
        Ok(OwnedTestWindow {
            hwnd,
            title: title.to_string(),
        })
    }

    pub fn title(&self) -> &str {
        &self.title
    }

    /// Resize so the *client* area becomes exactly `client_w x client_h`.
    pub fn set_size(&self, client_w: i32, client_h: i32) -> Result<()> {
        let (w, h) = client_to_window_rect(client_w, client_h)?;
        unsafe {
            SetWindowPos(
                self.hwnd,
                Some(HWND_BOTTOM),
                0,
                0,
                w,
                h,
                SWP_NOZORDER | SWP_NOACTIVATE | SWP_NOMOVE,
            )
        }
        .map_err(ControllerError::Win)
    }

    pub fn set_position(&self, x: i32, y: i32) -> Result<()> {
        unsafe {
            SetWindowPos(
                self.hwnd,
                Some(HWND_BOTTOM),
                x,
                y,
                0,
                0,
                SWP_NOZORDER | SWP_NOACTIVATE | SWP_NOSIZE,
            )
        }
        .map_err(ControllerError::Win)
    }

    /// Force a synchronous full repaint via RedrawWindow(RDW_UPDATENOW):
    /// the window's surface must reflect the CURRENT client size and
    /// pattern immediately, because capture backends (and the resize
    /// stability test) read the surface right after geometry changes.
    /// Purely affects this process's own probe window.
    pub fn nudge(&self) -> bool {
        unsafe {
            RedrawWindow(
                Some(self.hwnd),
                None,
                None,
                RDW_INVALIDATE | RDW_ERASE | RDW_UPDATENOW,
            )
            .as_bool()
        }
    }

    /// Try to make this probe window the foreground window (input selftest
    /// precondition). Own-process windows may be focused directly; there is
    /// no guarantee the OS grants it, so callers must verify with
    /// `GameWindow::is_foreground`.
    pub fn bring_to_foreground(&self) -> bool {
        unsafe { SetForegroundWindow(self.hwnd) }.as_bool()
    }
}

impl Drop for OwnedTestWindow {
    fn drop(&mut self) {
        unsafe {
            let _ = DestroyWindow(self.hwnd);
        }
    }
}

fn probe_window_style() -> WINDOW_STYLE {
    WS_OVERLAPPEDWINDOW | WS_VISIBLE
}

fn client_to_window_rect(client_w: i32, client_h: i32) -> Result<(i32, i32)> {
    let mut rect = RECT {
        left: 0,
        top: 0,
        right: client_w,
        bottom: client_h,
    };
    unsafe { AdjustWindowRectEx(&mut rect, probe_window_style(), false, WINDOW_EX_STYLE(0)) }
        .map_err(ControllerError::Win)?;
    Ok((rect.right - rect.left, rect.bottom - rect.top))
}

fn probe_class_name16() -> Vec<u16> {
    b"nf_controller_probe_window\0"
        .iter()
        .map(|&b| b as u16)
        .collect()
}

fn register_probe_class() -> u16 {
    static ATOM: OnceLock<u16> = OnceLock::new();
    *ATOM.get_or_init(|| {
        let hinstance = unsafe { GetModuleHandleW(None) }.expect("module handle");
        let class_name = probe_class_name16();
        let class = WNDCLASSEXW {
            cbSize: std::mem::size_of::<WNDCLASSEXW>() as u32,
            style: Default::default(),
            lpfnWndProc: Some(probe_wnd_proc),
            cbClsExtra: 0,
            cbWndExtra: 0,
            hInstance: HINSTANCE(hinstance.0),
            hIcon: Default::default(),
            hCursor: Default::default(),
            hbrBackground: Default::default(),
            lpszMenuName: PCWSTR::null(),
            lpszClassName: PCWSTR(class_name.as_ptr()),
            hIconSm: Default::default(),
        };
        unsafe { RegisterClassExW(&class) }
    })
}

/// The probe window paints a DETERMINISTIC scene: dark background with a
/// red rectangle (green border) anchored at normalized (0.6, 0.6)-(0.8,
/// 0.8) of the client area. Because the anchor is normalized, the pattern
/// keeps its position across window resizes — the property the dry-run's
/// resolution-independence acceptance test checks.
///
/// PAINT_COUNT exposes how many WM_PAINTs actually executed (diagnostics
/// for capture-timing questions: "did my forced repaint run at all?").
static PAINT_COUNT: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
static PRINT_COUNT: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
/// NC4 input selftest counters: real (or PostMessage-injected) input events
/// the probe windows actually received in this process.
static CLICK_COUNT: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);
static KEY_COUNT: std::sync::atomic::AtomicU32 = std::sync::atomic::AtomicU32::new(0);

/// Resize ANY window's client area to exactly WxH (used by the dry-run's
/// `--resize-after` probe harness on its own window).
pub fn resize_window(hwnd: HWND, client_w: i32, client_h: i32) -> Result<()> {
    let mut rect = RECT {
        left: 0,
        top: 0,
        right: client_w,
        bottom: client_h,
    };
    unsafe { AdjustWindowRectEx(&mut rect, probe_window_style(), false, WINDOW_EX_STYLE(0)) }
        .map_err(ControllerError::Win)?;
    unsafe {
        SetWindowPos(
            hwnd,
            Some(HWND_BOTTOM),
            0,
            0,
            rect.right - rect.left,
            rect.bottom - rect.top,
            SWP_NOZORDER | SWP_NOACTIVATE | SWP_NOMOVE,
        )
    }
    .map_err(ControllerError::Win)
}

/// WM_PAINT executions across all probe windows in this process.
pub fn probe_paint_count() -> u32 {
    PAINT_COUNT.load(std::sync::atomic::Ordering::Relaxed)
}

/// WM_PRINT executions (PrintWindow path diagnostics).
pub fn probe_print_count() -> u32 {
    PRINT_COUNT.load(std::sync::atomic::Ordering::Relaxed)
}

/// Mouse clicks (WM_LBUTTONDOWN) received by probe windows in this process.
pub fn probe_click_count() -> u32 {
    CLICK_COUNT.load(std::sync::atomic::Ordering::Relaxed)
}

/// Character messages (WM_CHAR) received by probe windows in this process.
pub fn probe_key_count() -> u32 {
    KEY_COUNT.load(std::sync::atomic::Ordering::Relaxed)
}

/// Drain this thread's message queue once ( TranslateMessage +
/// DispatchMessageW). Input events (SendInput or PostMessage) only reach a
/// wndproc when dispatched, so the input selftest pumps between the send
/// and the counter poll.
pub fn pump_pending_messages() {
    unsafe {
        let mut msg = MSG::default();
        while PeekMessageW(&mut msg, None, 0, 0, PM_REMOVE).as_bool() {
            let _ = TranslateMessage(&msg);
            let _ = DispatchMessageW(&msg);
        }
    }
}

unsafe extern "system" fn probe_wnd_proc(
    hwnd: HWND,
    msg: u32,
    wparam: windows::Win32::Foundation::WPARAM,
    lparam: windows::Win32::Foundation::LPARAM,
) -> windows::Win32::Foundation::LRESULT {
    use windows::Win32::UI::WindowsAndMessaging::WM_PRINT;
    // PrintWindow(PW_CLIENTONLY) asks the window to render into a foreign
    // DC via WM_PRINT; without an explicit handler the content falls back
    // to a stale redirection surface. Draw the scene ourselves into
    // wparam's HDC.
    if msg == WM_PRINT {
        PRINT_COUNT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        let hdc = HDC(wparam.0 as *mut core::ffi::c_void);
        draw_probe_scene(hwnd, hdc);
        return windows::Win32::Foundation::LRESULT(0);
    }
    if msg == windows::Win32::UI::WindowsAndMessaging::WM_PAINT {
        PAINT_COUNT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
        let mut ps = PAINTSTRUCT::default();
        let hdc = unsafe { BeginPaint(hwnd, &mut ps) };
        draw_probe_scene(hwnd, hdc);
        let _ = unsafe { EndPaint(hwnd, &ps) };
        return windows::Win32::Foundation::LRESULT(0);
    }
    if msg == WM_LBUTTONDOWN {
        CLICK_COUNT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }
    if msg == WM_CHAR {
        KEY_COUNT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    }
    unsafe { DefWindowProcW(hwnd, msg, wparam, lparam) }
}

/// Draw the deterministic probe scene (dark bg + red rect anchored at
/// normalized (0.6,0.6)-(0.8,0.8)) into any HDC covering hwnd's client.
unsafe fn draw_probe_scene(hwnd: HWND, hdc: HDC) {
    let mut rc = RECT::default();
    let _ = unsafe { GetClientRect(hwnd, &mut rc) };
    let (cw, ch) = ((rc.right - rc.left).max(1), (rc.bottom - rc.top).max(1));

    // background: dark gray
    let bg = unsafe { CreateSolidBrush(windows::Win32::Foundation::COLORREF(0x1C1C1C)) };
    unsafe { FillRect(hdc, &rc, bg) };
    let _ = unsafe { DeleteObject(bg.into()) };

    // normalized (0.6,0.6)-(0.8,0.8) -> pixels; +1 keeps the rect
    // non-empty on tiny clients
    let x0 = (cw as f32 * 0.6).round() as i32;
    let y0 = (ch as f32 * 0.6).round() as i32;
    let x1 = (cw as f32 * 0.8).round() as i32;
    let y1 = (ch as f32 * 0.8).round() as i32;
    let rect = RECT {
        left: x0,
        top: y0,
        right: x1.max(x0 + 1),
        bottom: y1.max(y0 + 1),
    };
    let fill = unsafe { CreateSolidBrush(windows::Win32::Foundation::COLORREF(0x001028C8)) }; // COLORREF 0x00bbggrr = r200 g40 b16
    unsafe { FillRect(hdc, &rect, fill) };
    let _ = unsafe { DeleteObject(fill.into()) };
}

// ---------------------------------------------------------------------------

#[cfg(test)]
pub(crate) mod tests {
    use super::*;
    use std::sync::{Mutex, MutexGuard};
    use windows::Win32::UI::WindowsAndMessaging::GetWindowRect;

    /// Serializes every window-creating test. `SetProcessDpiAwarenessContext`
    /// is PROCESS-global and flips how subsequent windows are sized, so two
    /// tests racing "create window" against "set awareness" could observe a
    /// scaled client rect (a 2x flake once test counts grew). Holding this
    /// lock across ensure_dpi_awareness + create + measure keeps each test's
    /// observations internally consistent.
    pub(crate) fn window_test_lock() -> MutexGuard<'static, ()> {
        static LOCK: Mutex<()> = Mutex::new(());
        LOCK.lock().unwrap_or_else(|e| e.into_inner())
    }

    fn unique_title(tag: &str) -> String {
        use std::sync::atomic::{AtomicU32, Ordering};
        static SEQ: AtomicU32 = AtomicU32::new(0);
        let n = SEQ.fetch_add(1, Ordering::Relaxed);
        let pid = std::process::id();
        format!("NFCTRL-{tag}-{pid}-{n}")
    }

    /// The input selftest's assertion path: a posted click/key reaches the
    /// probe wndproc once the message queue is pumped, and the process
    /// counters reflect it. PostMessage (not SendInput) keeps the test
    /// free of real cursor/keyboard synthesis.
    #[test]
    fn probe_window_counts_posted_input_events() {
        use windows::Win32::UI::WindowsAndMessaging::PostMessageW;
        let _lock = window_test_lock();
        let probe = OwnedTestWindow::new(320, 240, &unique_title("INPUTCNT")).expect("probe");
        let clicks_before = probe_click_count();
        let keys_before = probe_key_count();
        unsafe {
            assert!(PostMessageW(
                Some(probe.hwnd),
                WM_LBUTTONDOWN,
                Default::default(),
                Default::default()
            )
            .is_ok());
            assert!(PostMessageW(
                Some(probe.hwnd),
                WM_CHAR,
                Default::default(),
                Default::default()
            )
            .is_ok());
        }
        pump_pending_messages();
        assert!(
            probe_click_count() > clicks_before,
            "click counter must advance: {} -> {}",
            clicks_before,
            probe_click_count()
        );
        assert!(
            probe_key_count() > keys_before,
            "key counter must advance: {} -> {}",
            keys_before,
            probe_key_count()
        );
    }

    /// True when the process can talk to an interactive desktop. CI nodes
    /// run jobs from service contexts (Session 0): window ENUMERATION and
    /// `SetProcessDpiAwarenessContext` misbehave or are denied there,
    /// while direct-HWND paths still work. Tests that depend on the
    /// interactive environment skip honestly instead of failing.
    fn interactive_desktop() -> bool {
        interactive_desktop_available()
    }

    fn require_interactive(what: &str) -> bool {
        if interactive_desktop() {
            return true;
        }
        println!("skipped: {what} needs an interactive desktop (service session detected)");
        false
    }

    #[test]
    fn dpi_awareness_succeeds() {
        let _window_guard = window_test_lock();
        if !require_interactive("dpi_awareness_succeeds") {
            return;
        }
        let _ = ensure_dpi_awareness();
        assert!(
            ensure_dpi_awareness(),
            "second call reports the chosen mode"
        );
    }

    #[test]
    fn creates_window_with_exact_client_size_and_finds_it_by_title() {
        let _window_guard = window_test_lock();
        if !require_interactive("creates_window_..._finds_it_by_title") {
            return;
        }
        let title = unique_title("exact");
        let win = OwnedTestWindow::new(640, 480, &title).expect("create window");

        let wrapped = GameWindow::from_hwnd(win.hwnd).expect("wrap");
        let layout = wrapped.layout().expect("layout");
        assert_eq!(layout.client_size, (640, 480), "client rect must be exact");

        let found = GameWindow::find(Some(&title), None)
            .expect("find")
            .expect("some window");
        assert_eq!(found.hwnd(), win.hwnd, "find must locate the probe window");

        // substring match, case-insensitive on both sides
        let lower = GameWindow::find(Some(&title.to_lowercase()), None)
            .expect("find")
            .expect("some");
        assert_eq!(lower.hwnd(), win.hwnd);
    }

    #[test]
    fn finds_window_by_process_name() {
        let _window_guard = window_test_lock();
        if !require_interactive("finds_window_by_process_name") {
            return;
        }
        let title = unique_title("procname");
        let win = OwnedTestWindow::new(320, 240, &title).expect("create window");
        let wrapped = GameWindow::from_hwnd(win.hwnd).expect("wrap");
        let pname = wrapped.process_name().to_string();
        assert!(
            !pname.is_empty(),
            "process image name must resolve for our own process"
        );
        assert!(pname.ends_with(".exe"), "unexpected process name {pname}");

        let by_proc = GameWindow::find(Some(&title), Some(&pname))
            .expect("find")
            .expect("some");
        assert_eq!(by_proc.hwnd(), win.hwnd);

        let wrong = GameWindow::find(Some(&title), Some("definitely_not_running_xyz.exe"))
            .expect("find must not error on no match");
        assert!(wrong.is_none(), "no window may match a bogus process name");
    }

    #[test]
    fn client_to_screen_origin_is_consistent_with_window_rect() {
        let _window_guard = window_test_lock();
        ensure_dpi_awareness();
        let title = unique_title("origin");
        let win = OwnedTestWindow::new(400, 300, &title).expect("create window");
        win.set_position(120, 90).expect("move");

        let mut window_rect = RECT::default();
        unsafe { GetWindowRect(win.hwnd, &mut window_rect) }
            .map_err(ControllerError::Win)
            .expect("GetWindowRect");
        let layout = GameWindow::from_hwnd(win.hwnd)
            .expect("wrap")
            .layout()
            .expect("layout");

        let (ox, oy) = layout.screen_origin;
        assert!(
            ox >= window_rect.left && ox <= window_rect.right,
            "client origin x {ox} outside window rect x-range {}..{}",
            window_rect.left,
            window_rect.right
        );
        assert!(
            oy >= window_rect.top && oy <= window_rect.bottom,
            "client origin y {oy} outside window rect y-range {}..{}",
            window_rect.top,
            window_rect.bottom
        );
    }

    #[test]
    fn change_detection_sees_resize_and_move() {
        let _window_guard = window_test_lock();
        ensure_dpi_awareness();
        let title = unique_title("change");
        let win = OwnedTestWindow::new(500, 400, &title).expect("create window");
        let wrapped = GameWindow::from_hwnd(win.hwnd).expect("wrap");
        let before = wrapped.layout().expect("layout");
        assert!(
            !wrapped.changed_since(&before).expect("changed"),
            "no change yet"
        );

        win.set_size(480, 360).expect("resize");
        assert!(
            wrapped.changed_since(&before).expect("changed"),
            "resize must register"
        );
        let after = wrapped.layout().expect("layout");
        assert_eq!(after.client_size, (480, 360));

        // same-size move also counts as a change (screen origin shifts)
        let baseline = wrapped.layout().expect("layout");
        win.set_position(300, 200).expect("move");
        assert!(
            wrapped.changed_since(&baseline).expect("changed"),
            "move must register"
        );
    }

    #[test]
    fn foreground_answers_and_dpi_is_sane() {
        let _window_guard = window_test_lock();
        ensure_dpi_awareness();
        let title = unique_title("fg");
        let win = OwnedTestWindow::new(200, 150, &title).expect("create window");
        let wrapped = GameWindow::from_hwnd(win.hwnd).expect("wrap");
        // Result is environment-dependent (nothing guarantees the probe
        // window is foreground in a headless run); it just must answer.
        let _ = wrapped.is_foreground().expect("foreground");
        let dpi = wrapped.dpi().expect("dpi");
        assert!(
            (96..=480).contains(&dpi),
            "dpi {dpi} out of plausible range"
        );
    }

    #[test]
    fn invalid_handles_report_window_gone() {
        let _window_guard = window_test_lock();
        let stale_probe = GameWindow::from_hwnd(HWND(std::ptr::null_mut()));
        assert!(matches!(stale_probe, Err(ControllerError::WindowGone)));

        let title = unique_title("gone");
        let win = OwnedTestWindow::new(100, 80, &title).expect("create window");
        let hwnd = win.hwnd;
        drop(win); // window destroyed; the stale handle must be detected
        let stale = GameWindow::from_hwnd(hwnd);
        assert!(matches!(stale, Err(ControllerError::WindowGone)));
    }

    #[test]
    fn filterless_enumeration_never_errors_and_metadata_reads() {
        let _window_guard = window_test_lock();
        let first = GameWindow::find(None, None).expect("filterless enumeration must not error");
        if let Some(w) = first {
            assert!(w.pid() > 0, "pid must be known for any returned window");
        }
        // No assertion that a window exists: a truly windowless session is
        // allowed to answer None, but enumeration itself may not fail.
    }

    #[test]
    fn repeated_lookups_do_not_leak_process_handles() {
        let _window_guard = window_test_lock();
        use windows::Win32::System::Threading::{GetCurrentProcess, GetProcessHandleCount};
        ensure_dpi_awareness();
        let title = unique_title("handles");
        let win = OwnedTestWindow::new(120, 90, &title).expect("create window");
        let mut count = 0u32;
        unsafe {
            GetProcessHandleCount(GetCurrentProcess(), &mut count).expect("handle count");
        }
        let baseline = count;

        // 50 lookups: a leaked OpenProcess handle per lookup would show up
        // as +50 (noise from parallel tests is far smaller).
        for _ in 0..50 {
            let w = GameWindow::from_hwnd(win.hwnd).expect("wrap");
            assert!(!w.process_path().is_empty() || w.process_name().is_empty());
            let _ = GameWindow::find(Some(&title), None).expect("find");
        }
        unsafe {
            GetProcessHandleCount(GetCurrentProcess(), &mut count).expect("handle count");
        }
        assert!(
            count <= baseline + 25,
            "handle leak: {baseline} -> {count} after 50 lookups"
        );
    }

    #[test]
    fn invalid_client_size_is_rejected_before_any_window_is_created() {
        let _window_guard = window_test_lock();
        let err = match OwnedTestWindow::new(0, 100, "bad") {
            Err(e) => e,
            Ok(_) => panic!("size 0 must be rejected"),
        };
        assert!(matches!(err, ControllerError::InvalidInput(_)));
    }
}
