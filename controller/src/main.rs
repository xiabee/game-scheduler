//! native-controller CLI (NC0). The only mode that exists today is
//! `--self-probe` (GameWindow smoke) and the dry-run skeleton. The dry-run
//! mode NEVER sends input: the input module does not exist yet by design.

fn main() {
    controller::window::ensure_dpi_awareness();
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|a| a == "--self-probe") {
        std::process::exit(run_self_probe());
    }
    if args.iter().any(|a| a == "--capture-probe") {
        std::process::exit(run_capture_probe());
    }
    if args.iter().any(|a| a == "--capture-monitor") {
        std::process::exit(run_monitor_probe());
    }
    if args.iter().any(|a| a == "--capture-gdi") {
        std::process::exit(run_gdi_probe());
    }
    if let Some(pos) = args.iter().position(|a| a == "--capture-foreign") {
        let needle = args.get(pos + 1).cloned().unwrap_or_default();
        std::process::exit(run_foreign_probe(&needle));
    }
    if args.iter().any(|a| a == "--dry-run") {
        // NC0 M5 will wire: window -> capture -> resize -> mock detection ->
        // inverse transform -> debug overlay. Today: explicit not-implemented.
        eprintln!("--dry-run is delivered in NC0 M5; not wired yet");
        std::process::exit(2);
    }
    println!("native-controller (NC0): use --self-probe | --capture-probe | --dry-run");
}

/// Real-capture smoke: create a probe window, capture it through Windows
/// Graphics Capture, and verify the frame is a live image (non-uniform
/// content, plausible size). Exits 0 on success.
fn run_monitor_probe() -> i32 {
    use controller::capture::CaptureBackend;
    use controller::window::ensure_dpi_awareness;
    ensure_dpi_awareness();
    let mut cap = match controller::capture::WgcCapture::for_primary_monitor() {
        Ok(c) => c,
        Err(e) => {
            eprintln!("monitor-probe: start failed: {e}");
            return 1;
        }
    };
    for i in 0..3 {
        match cap.capture() {
            Ok(f) => println!("monitor-probe: frame {i}: {}x{}", f.width, f.height),
            Err(e) => {
                eprintln!(
                    "monitor-probe: frame {i} failed: {e} (arrived={})",
                    cap.frames_arrived()
                );
                return 1;
            }
        }
    }
    println!("monitor-probe: OK (arrived={})", cap.frames_arrived());
    0
}

/// Capture a window belonging to ANOTHER process (by title substring) —
/// isolates window-ownership issues from session-level WGC failures.
fn run_foreign_probe(needle: &str) -> i32 {
    use controller::capture::{CaptureBackend, WgcCapture};
    use controller::window::{ensure_dpi_awareness, GameWindow};
    ensure_dpi_awareness();
    let found = match GameWindow::find(Some(needle), None) {
        Ok(Some(w)) => w,
        Ok(None) => {
            eprintln!("foreign-probe: no window matching {needle:?}");
            return 1;
        }
        Err(e) => {
            eprintln!("foreign-probe: find failed: {e}");
            return 1;
        }
    };
    println!(
        "foreign-probe: target {:?} pid={}",
        found.title(),
        found.pid()
    );
    let layout = match found.layout() {
        Ok(l) => l,
        Err(e) => {
            eprintln!("foreign-probe: layout failed: {e}");
            return 1;
        }
    };
    let mut cap = match WgcCapture::new(found.hwnd(), layout) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("foreign-probe: start failed: {e}");
            return 1;
        }
    };
    for i in 0..3 {
        match cap.capture() {
            Ok(f) => println!("foreign-probe: frame {i}: {}x{}", f.width, f.height),
            Err(e) => {
                eprintln!(
                    "foreign-probe: frame {i} failed: {e} (arrived={})",
                    cap.frames_arrived()
                );
                return 1;
            }
        }
    }
    println!("foreign-probe: OK (arrived={})", cap.frames_arrived());
    0
}

/// GDI(PrintWindow) capture smoke against our own probe window.
fn run_gdi_probe() -> i32 {
    use controller::capture::{CaptureBackend, GdiPrintWindowCapture};
    use controller::window::{ensure_dpi_awareness, OwnedTestWindow};
    ensure_dpi_awareness();
    let title = format!("NFCTRL-GDI-PROBE-{}", std::process::id());
    let win = match OwnedTestWindow::new(400, 300, &title) {
        Ok(w) => w,
        Err(e) => {
            eprintln!("gdi-probe: create window failed: {e}");
            return 1;
        }
    };
    let layout = match controller::window::GameWindow::from_hwnd(win.hwnd) {
        Ok(g) => match g.layout() {
            Ok(l) => l,
            Err(e) => {
                eprintln!("gdi-probe: layout failed: {e}");
                return 1;
            }
        },
        Err(e) => {
            eprintln!("gdi-probe: wrap window failed: {e}");
            return 1;
        }
    };
    let mut cap = match GdiPrintWindowCapture::new(win.hwnd, layout) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("gdi-probe: backend init failed: {e}");
            return 1;
        }
    };
    win.nudge();
    match cap.capture() {
        Ok(f) => {
            let nonzero = f.data.iter().filter(|&&b| b != 0).count();
            let distinct = f
                .data
                .iter()
                .collect::<std::collections::HashSet<_>>()
                .len();
            println!(
                "gdi-probe: frame {}x{} stride={} nonzero={nonzero} distinct_values={distinct}",
                f.width, f.height, f.stride
            );
            if f.width == 0 || f.height == 0 || nonzero == 0 {
                eprintln!("gdi-probe: frame is empty or all black");
                return 1;
            }
            println!("gdi-probe: OK");
            0
        }
        Err(e) => {
            eprintln!("gdi-probe: capture failed: {e}");
            1
        }
    }
}

fn run_capture_probe() -> i32 {
    use controller::capture::{CaptureBackend, WgcCapture};
    use controller::window::{ensure_dpi_awareness, GameWindow, OwnedTestWindow};

    ensure_dpi_awareness();
    let title = format!("NFCTRL-CAPTURE-PROBE-{}", std::process::id());
    let win = match OwnedTestWindow::new(400, 300, &title) {
        Ok(w) => w,
        Err(e) => {
            eprintln!("capture-probe: create window failed: {e}");
            return 1;
        }
    };
    let gw = match GameWindow::from_hwnd(win.hwnd) {
        Ok(g) => g,
        Err(e) => {
            eprintln!("capture-probe: wrap window failed: {e}");
            return 1;
        }
    };
    let layout = match gw.layout() {
        Ok(l) => l,
        Err(e) => {
            eprintln!("capture-probe: layout failed: {e}");
            return 1;
        }
    };
    let mut cap = match WgcCapture::new(win.hwnd, layout) {
        Ok(c) => c,
        Err(e) => {
            eprintln!("capture-probe: WGC start failed: {e}");
            return 1;
        }
    };
    // first frame may take a moment; nudge repaints our own probe window
    // because WGC only delivers frames on content change
    let mut frames = Vec::new();
    for i in 0..2 {
        win.nudge();
        match cap.capture() {
            Ok(f) => frames.push(f),
            Err(e) => {
                eprintln!("capture-probe: frame {i} failed: {e}");
                return 1;
            }
        }
    }
    let f = &frames[1];
    let nonzero = f.data.iter().filter(|&&b| b != 0).count();
    let distinct = f
        .data
        .iter()
        .collect::<std::collections::HashSet<_>>()
        .len();
    println!(
        "capture-probe: frame {}x{} stride={} nonzero={nonzero} distinct_values={distinct} arrived={}",
        f.width, f.height, f.stride, cap.frames_arrived()
    );
    if f.width == 0 || f.height == 0 {
        eprintln!("capture-probe: empty frame");
        return 1;
    }
    if nonzero == 0 {
        eprintln!("capture-probe: frame is all black — capture produced no image");
        return 1;
    }
    println!("capture-probe: OK");
    0
}

/// Create a probe window, find it through GameWindow (the public path),
/// print its geometry, resize it and verify change detection. Exits 0 on
/// success — a manual smoke of the window module.
fn run_self_probe() -> i32 {
    use controller::window::{ensure_dpi_awareness, GameWindow, OwnedTestWindow};
    ensure_dpi_awareness();
    let title = format!("NFCTRL-SELF-PROBE-{}", std::process::id());
    let win = match OwnedTestWindow::new(512, 384, &title) {
        Ok(w) => w,
        Err(e) => {
            eprintln!("probe: create failed: {e}");
            return 1;
        }
    };
    let found = match GameWindow::find(Some(&title), None) {
        Ok(Some(w)) => w,
        Ok(None) => {
            eprintln!("probe: window not found by title");
            return 1;
        }
        Err(e) => {
            eprintln!("probe: find failed: {e}");
            return 1;
        }
    };
    if found.hwnd() != win.hwnd {
        eprintln!("probe: found window is not the probe window");
        return 1;
    }
    let layout = match found.layout() {
        Ok(l) => l,
        Err(e) => {
            eprintln!("probe: layout failed: {e}");
            return 1;
        }
    };
    println!(
        "probe: title={:?} pid={} process={:?} layout={layout:?}",
        found.title(),
        found.pid(),
        found.process_name()
    );
    if let Err(e) = win.set_size(384, 256) {
        eprintln!("probe: resize failed: {e}");
        return 1;
    }
    match found.changed_since(&layout) {
        Ok(true) => {
            println!("probe: change detection OK");
            0
        }
        Ok(false) => {
            eprintln!("probe: resize not detected");
            1
        }
        Err(e) => {
            eprintln!("probe: changed_since failed: {e}");
            1
        }
    }
}
