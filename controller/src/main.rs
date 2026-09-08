//! native-controller CLI (NC0). The only mode that exists today is
//! `--self-probe` (GameWindow smoke) and the dry-run skeleton. The dry-run
//! mode NEVER sends input: the input module does not exist yet by design.

fn main() {
    controller::window::ensure_dpi_awareness();
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|a| a == "--self-probe") {
        std::process::exit(run_self_probe());
    }
    if args.iter().any(|a| a == "--dry-run") {
        // NC0 M5 will wire: window -> capture -> resize -> mock detection ->
        // inverse transform -> debug overlay. Today: explicit not-implemented.
        eprintln!("--dry-run is delivered in NC0 M5; not wired yet");
        std::process::exit(2);
    }
    println!("native-controller (NC0): use --self-probe | --dry-run");
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
