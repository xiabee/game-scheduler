//! native-controller CLI (NC0). The only mode that exists today is
//! `--self-probe` (GameWindow smoke) and the dry-run skeleton. The dry-run
//! mode NEVER sends input: the input module does not exist yet by design.

fn main() {
    controller::window::ensure_dpi_awareness();
    let args: Vec<String> = std::env::args().collect();
    if args.iter().any(|a| a == "--self-probe") {
        std::process::exit(run_self_probe());
    }
    if args.iter().any(|a| a == "--list-windows") {
        std::process::exit(run_list_windows());
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
        let opts = match DryRunOptions::parse(&args) {
            Ok(o) => o,
            Err(e) => {
                eprintln!("dry-run: {e}");
                std::process::exit(2);
            }
        };
        std::process::exit(run_dry_run(&opts));
    }
    println!(
        "native-controller (NC0): use --list-windows | --self-probe | --capture-gdi | --dry-run"
    );
}

/// Enumerate visible top-level windows: the discovery aid for choosing a
/// `--window <title>` filter. Tab-separated: title / pid / process /
/// client size (physical pixels).
fn run_list_windows() -> i32 {
    use controller::window::GameWindow;
    match GameWindow::find_all(None, None) {
        Ok(windows) => {
            if windows.is_empty() {
                println!("(no visible top-level windows found)");
                return 0;
            }
            for w in &windows {
                match w.layout() {
                    Ok(l) => println!(
                        "{:?}\tpid={}\tprocess={:?}\tclient={}x{}",
                        w.title(),
                        w.pid(),
                        w.process_name(),
                        l.client_size.0,
                        l.client_size.1
                    ),
                    Err(e) => println!(
                        "{:?}\tpid={}\t(layout unavailable: {e})",
                        w.title(),
                        w.pid()
                    ),
                }
            }
            0
        }
        Err(e) => {
            eprintln!("list-windows: enumeration failed: {e}");
            1
        }
    }
}

/// Options for the dry-run pipeline loop. The dry-run NEVER sends input:
/// it observes, detects, plans and logs — that is its whole purpose.
#[derive(Debug, Clone)]
struct DryRunOptions {
    /// Window title substring; `@probe` creates an owned probe window so
    /// the demo runs standalone (default).
    window: String,
    backend: String, // auto | wgc | gdi | synthetic
    fps: f32,
    duration_secs: f32,
    model: u32,
    min_confidence: f32,
    debug_dir: Option<String>,
    session_log: Option<String>,
    /// Probe-window-only: resize the probe window to WxH after S seconds
    /// (exercises governor Pause + live recalibration deterministically).
    resize_after: Option<(f32, u32, u32)>,
    require_foreground: bool,
    /// Latch the governor's emergency stop after N seconds (exercises the
    /// emergency path deterministically).
    emergency_after_secs: Option<f32>,
}

impl DryRunOptions {
    fn parse(args: &[String]) -> Result<DryRunOptions, String> {
        fn opt(args: &[String], name: &str) -> Option<String> {
            args.iter()
                .position(|a| a == name)
                .and_then(|i| args.get(i + 1))
                .cloned()
        }
        let window = opt(args, "--window").unwrap_or_else(|| "@probe".into());
        if window.trim().is_empty() {
            return Err("--window must be a non-empty title substring (or @probe)".into());
        }
        let backend = opt(args, "--backend").unwrap_or_else(|| "auto".into());
        if !matches!(backend.as_str(), "auto" | "wgc" | "gdi" | "synthetic") {
            return Err(format!(
                "--backend must be auto|wgc|gdi|synthetic, got {backend:?}"
            ));
        }
        let fps: f32 = opt(args, "--fps")
            .and_then(|v| v.parse().ok())
            .unwrap_or(15.0);
        if !(fps.is_finite() && fps > 0.0) {
            return Err(format!("--fps must be a positive number, got {fps}"));
        }
        let model = opt(args, "--model")
            .and_then(|v| v.parse().ok())
            .unwrap_or(256);
        if model == 0 || model > 4096 {
            return Err(format!("--model must be in 1..=4096, got {model}"));
        }
        let min_confidence = opt(args, "--min-confidence")
            .and_then(|v| v.parse().ok())
            .unwrap_or(0.6);
        if !(0.0..=1.0).contains(&min_confidence) {
            return Err(format!(
                "--min-confidence must be in [0, 1], got {min_confidence}"
            ));
        }
        Ok(DryRunOptions {
            window,
            backend,
            fps,
            duration_secs: opt(args, "--duration")
                .and_then(|v| v.parse().ok())
                .unwrap_or(3.0),
            model,
            min_confidence,
            debug_dir: opt(args, "--debug-dir"),
            session_log: opt(args, "--session-log"),
            resize_after: {
                if args.iter().any(|a| a == "--resize-after") {
                    let secs: Option<f32> = args
                        .iter()
                        .position(|a| a == "--resize-after")
                        .and_then(|i| args.get(i + 1))
                        .and_then(|v| v.parse().ok());
                    let dims = args
                        .iter()
                        .position(|a| a == "--resize-after")
                        .and_then(|i| args.get(i + 2).cloned())
                        .unwrap_or_default();
                    let (w, h) = match dims.split_once('x') {
                        Some(pair) => pair,
                        None => {
                            return Err(
                                "--resize-after requires <seconds> <WxH>, e.g. --resize-after 5 700x500"
                                    .into(),
                            )
                        }
                    };
                    match (secs, w.trim().parse::<u32>(), h.trim().parse::<u32>()) {
                        (Some(secs), Ok(w), Ok(h)) if secs.is_finite() && secs >= 0.0 => {
                            Some((secs, w, h))
                        }
                        _ => {
                            return Err(
                                "--resize-after requires a numeric <seconds> and <WxH>".into()
                            )
                        }
                    }
                } else {
                    None
                }
            },
            require_foreground: args.iter().any(|a| a == "--require-foreground"),
            emergency_after_secs: opt(args, "--emergency-after").and_then(|v| v.parse().ok()),
        })
    }
}

/// Build the selected capture backend for `hwnd` at `layout`. `auto`
/// resolves WGC -> GDI -> synthetic with honest warnings (a silent WGC
/// costs one probe capture). Re-invoked after recalibration so the backend
/// rebuilds against the fresh layout.
fn build_backend(
    kind: &str,
    hwnd: windows::Win32::Foundation::HWND,
    layout: &controller::window::WindowLayout,
) -> Result<Box<dyn controller::capture::CaptureBackend>, String> {
    use controller::capture::{
        CaptureBackend, GdiPrintWindowCapture, SyntheticCapture, WgcCapture,
    };
    match kind {
        "synthetic" => Ok(Box::new(
            SyntheticCapture::new(320, 240).expect("synthetic"),
        )),
        "gdi" => GdiPrintWindowCapture::new(hwnd, layout.clone())
            .map(|b| Box::new(b) as Box<dyn CaptureBackend>)
            .map_err(|e| format!("gdi backend failed: {e}")),
        "wgc" => WgcCapture::new(hwnd, layout.clone())
            .map(|b| Box::new(b) as Box<dyn CaptureBackend>)
            .map_err(|e| format!("wgc backend failed: {e}")),
        _ => match WgcCapture::new(hwnd, layout.clone()) {
            Ok(mut w) => match w.capture() {
                Ok(_) => {
                    println!("dry-run: backend wgc (probe frame OK)");
                    Ok(Box::new(w))
                }
                Err(e) => {
                    eprintln!(
                        "dry-run: WARNING WGC silent ({e}) - falling back to GDI PrintWindow"
                    );
                    match GdiPrintWindowCapture::new(hwnd, layout.clone()) {
                        Ok(b) => Ok(Box::new(b)),
                        Err(e2) => {
                            eprintln!("dry-run: WARNING gdi fallback failed ({e2}) - synthetic");
                            Ok(
                                Box::new(SyntheticCapture::new(320, 240).expect("synthetic"))
                                    as Box<dyn controller::capture::CaptureBackend>,
                            )
                        }
                    }
                }
            },
            Err(e) => {
                eprintln!(
                    "dry-run: WARNING WGC unavailable ({e}) - falling back to GDI PrintWindow"
                );
                match GdiPrintWindowCapture::new(hwnd, layout.clone()) {
                    Ok(b) => Ok(Box::new(b)),
                    Err(e2) => {
                        eprintln!("dry-run: WARNING gdi fallback failed ({e2}) - synthetic");
                        Ok(
                            Box::new(SyntheticCapture::new(320, 240).expect("synthetic"))
                                as Box<dyn controller::capture::CaptureBackend>,
                        )
                    }
                }
            }
        },
    }
}

fn run_dry_run(opts: &DryRunOptions) -> i32 {
    use controller::capture::FpsLimiter;
    use controller::pipeline::{draw_overlay, run_cycle};
    use controller::safety::{SafetyConfig, SafetyGovernor};
    use controller::vision::{Detector, MockDetector};
    use controller::window::{ensure_dpi_awareness, GameWindow, OwnedTestWindow};

    ensure_dpi_awareness();

    // --- window: @probe creates our own window; otherwise find by title ---
    let (target_hwnd, _owned_probe, mut calibrated) = if opts.window == "@probe" {
        let title = format!("NFCTRL-DRYRUN-{}", std::process::id());
        match OwnedTestWindow::new(640, 480, &title) {
            Ok(w) => match GameWindow::from_hwnd(w.hwnd).map(|g| g.layout()) {
                Ok(Ok(l)) => (w.hwnd, Some(w), l),
                Ok(Err(e)) | Err(e) => {
                    eprintln!("dry-run: probe window layout failed: {e}");
                    return 1;
                }
            },
            Err(e) => {
                eprintln!("dry-run: probe window failed: {e}");
                return 1;
            }
        }
    } else {
        match GameWindow::find(Some(&opts.window), None) {
            Ok(Some(g)) => match g.layout() {
                Ok(l) => (g.hwnd(), None, l),
                Err(e) => {
                    eprintln!("dry-run: layout failed: {e}");
                    return 1;
                }
            },
            Ok(None) => {
                eprintln!("dry-run: no window matching {:?}", opts.window);
                return 1;
            }
            Err(e) => {
                eprintln!("dry-run: find failed: {e}");
                return 1;
            }
        }
    };
    println!(
        "dry-run: window {:?} layout={calibrated:?} backend={} model={}x{} fps={} duration={}s",
        opts.window, opts.backend, opts.model, opts.model, opts.fps, opts.duration_secs
    );

    let mut backend = match build_backend(&opts.backend, target_hwnd, &calibrated) {
        Ok(b) => b,
        Err(e) => {
            eprintln!("dry-run: {e}");
            return 1;
        }
    };

    let mut detector: Box<dyn Detector> = Box::new(MockDetector::synthetic_rect());
    let t0 = std::time::Instant::now();
    let mut governor = match SafetyGovernor::new(
        SafetyConfig {
            min_confidence: opts.min_confidence,
            ..SafetyConfig::default()
        },
        t0,
    ) {
        Ok(g) => g,
        Err(e) => {
            eprintln!("dry-run: governor config failed: {e}");
            return 1;
        }
    };
    let mut limiter = match FpsLimiter::new(opts.fps, std::time::Instant::now) {
        Ok(l) => l,
        Err(e) => {
            eprintln!("dry-run: fps config failed: {e}");
            return 1;
        }
    };
    if let Some(dir) = &opts.debug_dir {
        std::fs::create_dir_all(dir).ok();
    }
    let mut session_log = opts
        .session_log
        .as_deref()
        .map(controller::session::SessionLogger::new);

    let mut cycle: u32 = 0;
    let mut allowed_count: u32 = 0;
    let mut verdict_notes: Vec<String> = Vec::new();
    let mut outcome = "completed";
    let mut resized_once = false;
    let mut retry_tracker = controller::pipeline::RetryTracker::new(5);
    while t0.elapsed().as_secs_f32() < opts.duration_secs {
        if let Some(after) = opts.emergency_after_secs {
            if t0.elapsed().as_secs_f32() >= after {
                governor.trigger_emergency_stop();
            }
        }
        limiter.wait_tick();
        cycle += 1;
        let cycle_started = std::time::Instant::now();

        // Deterministic mid-run resize for the probe window: drives the
        // governor Pause + recalibration path live. Must run on THIS thread
        // (the window's owner) — SetWindowPos from a foreign thread cannot
        // deliver WM_WINDOWPOSCHANGING without a message pump.
        if let (Some(_probe), Some((secs, w, h))) = (&_owned_probe, opts.resize_after) {
            if !resized_once && t0.elapsed().as_secs_f32() >= secs {
                match controller::window::resize_window(target_hwnd, w as i32, h as i32) {
                    Ok(()) => println!("dry-run: probe window resized to {w}x{h}"),
                    Err(e) => eprintln!("dry-run: WARNING probe resize failed: {e}"),
                }
                resized_once = true;
            }
        }
        #[allow(unused_assignments)]
        let mut last_cycle_duration = std::time::Duration::ZERO;

        let current = match GameWindow::from_hwnd(target_hwnd) {
            Ok(g) => match g.layout() {
                Ok(l) => l,
                Err(e) => {
                    eprintln!("dry-run: cycle {cycle}: window layout failed ({e}) - stopping");
                    break;
                }
            },
            Err(_) => {
                eprintln!("dry-run: cycle {cycle}: target window gone - stopping");
                break;
            }
        };
        let foreground = if opts.require_foreground {
            GameWindow::from_hwnd(target_hwnd)
                .and_then(|g| g.is_foreground())
                .unwrap_or(false)
        } else {
            true // NC0 dry-run observes without foreground requirements
        };

        let now = std::time::Instant::now();
        let report = match run_cycle(
            cycle,
            backend.as_mut(),
            detector.as_mut(),
            &mut governor,
            &calibrated,
            &current,
            true, // we re-resolved the same target hwnd above
            foreground,
            opts.model,
            opts.model,
            now,
        ) {
            Ok(r) => {
                retry_tracker.on_success();
                last_cycle_duration = std::time::Instant::now().duration_since(cycle_started);
                r
            }
            Err(controller::ControllerError::WindowGone) => {
                eprintln!("dry-run: cycle {cycle}: window gone - stopping");
                break;
            }
            Err(e) => match retry_tracker.on_failure() {
                Some(delay) => {
                    eprintln!(
                        "dry-run: cycle {cycle}: transient pipeline error: {e} - retrying in {delay:?}"
                    );
                    std::thread::sleep(delay);
                    cycle -= 1; // the retry reuses the cycle number
                    continue;
                }
                None => {
                    eprintln!("dry-run: cycle {cycle}: pipeline error persists ({e}) - giving up");
                    break;
                }
            },
        };

        if let Some(log) = session_log.as_mut() {
            log.write_cycle(&controller::session::SessionLine {
                cycle,
                elapsed_ms: t0.elapsed().as_millis(),
                cycle_duration: last_cycle_duration,
                backend: &opts.backend,
                report: &report,
            });
        }

        // Geometry drift (Pause from check_geometry): recalibrate NOW —
        // re-anchor the calibration to the fresh layout and rebuild the
        // backend so its buffers match the new size. Without this the loop
        // would report pause forever against a stale snapshot.
        if report.pre_verdict.is_allow() && !report.geometry_ok {
            calibrated = current.clone();
            if opts.backend != "synthetic" {
                backend = match build_backend(&opts.backend, target_hwnd, &calibrated) {
                    Ok(b) => b,
                    Err(e) => {
                        eprintln!(
                            "dry-run: WARNING recalibration backend failed ({e}) - continuing with synthetic frames"
                        );
                        Box::new(
                            controller::capture::SyntheticCapture::new(320, 240)
                                .expect("synthetic"),
                        )
                    }
                };
            }
            retry_tracker.on_success();
            println!(
                "dry-run: recalibrated to client {:?} (backend {})",
                calibrated.client_size, opts.backend
            );
        }

        if report.allowed() {
            allowed_count += 1;
        } else if let Some(reason) = report
            .pre_verdict
            .reason()
            .or(report.action_verdict.as_ref().and_then(|v| v.reason()))
        {
            let note = format!("cycle {cycle}: {reason}");
            if verdict_notes.last() != Some(&note) {
                verdict_notes.push(note);
                // long sessions alternate verdicts; keep the summary bounded
                if verdict_notes.len() > 50 {
                    verdict_notes.remove(0);
                }
            }
        }

        let reportable = cycle.is_multiple_of(15) || !report.pre_verdict.is_allow();
        if reportable {
            if let Some(d) = report.client_detections.first() {
                let c = d.rect.center();
                let dp = report.desktop_points.first().copied().unwrap_or((0.0, 0.0));
                println!(
                    "dry-run: cycle {} conf={:.2} client=({:.0},{:.0}) desktop=({:.0},{:.0})",
                    cycle, d.confidence, c.0, c.1, dp.0, dp.1
                );
            }
        }

        if let (Some(dir), Some(mut frame)) = (&opts.debug_dir, report.frame.clone()) {
            if reportable {
                draw_overlay(&mut frame, &report.client_detections, [0, 230, 255, 255]);
                let path = format!("{dir}/cycle_{cycle:05}.png");
                match export_png(&path, &frame) {
                    Ok(()) => println!("dry-run: debug frame {path}"),
                    Err(e) => eprintln!("dry-run: png export failed: {e}"),
                }
            }
        }

        if report.pre_verdict.is_stop() {
            if let Some(reason) = report.pre_verdict.reason() {
                println!("dry-run: governor STOP after cycle {cycle}: {reason}");
            }
            outcome = "stopped";
            break;
        }
    }

    println!(
        "dry-run: finished - cycles={cycle} allowed={allowed_count} distinct_verdicts={}",
        verdict_notes.len()
    );
    if let Some(log) = session_log.as_mut() {
        log.write_summary(cycle, allowed_count, verdict_notes.len() as u32, outcome);
    }
    for note in &verdict_notes {
        println!("dry-run: verdict {note}");
    }
    if cycle == 0 {
        eprintln!("dry-run: no cycles ran");
        return 1;
    }
    println!("dry-run: OK (no input was sent - NC0 is observation-only)");
    0
}

fn export_png(path: &str, frame: &controller::frame::Frame) -> std::result::Result<(), String> {
    let file = std::fs::File::create(path).map_err(|e| e.to_string())?;
    let mut encoder = png::Encoder::new(std::io::BufWriter::new(file), frame.width, frame.height);
    encoder.set_color(png::ColorType::Rgba);
    encoder.set_depth(png::BitDepth::Eight);
    let mut writer = encoder.write_header().map_err(|e| e.to_string())?;
    // BGRA -> RGBA, honoring stride
    let mut rgba = Vec::with_capacity((frame.width * frame.height * 4) as usize);
    for y in 0..frame.height {
        for x in 0..frame.width {
            let px = frame.pixel(x, y).unwrap_or([0, 0, 0, 255]);
            rgba.extend_from_slice(&[px[2], px[1], px[0], px[3]]);
        }
    }
    writer.write_image_data(&rgba).map_err(|e| e.to_string())?;
    Ok(())
}

/// Real-capture smoke: create a probe window, capture it through Windows
/// Graphics Capture, and verify the frame is a live image (non-uniform
/// content, plausible size). Exits 0 on success.
fn run_monitor_probe() -> i32 {
    use controller::capture::CaptureBackend;
    use controller::window::ensure_dpi_awareness;
    ensure_dpi_awareness();
    println!(
        "monitor-probe: capture access = {}",
        controller::capture::WgcCapture::access_status()
    );
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
    use controller::vision::Detector;
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
    // poll a few cycles: the very first captured surface can still be a
    // transitional one while DWM composes the freshly painted pattern
    let mut outcome: Option<Result<String, String>> = None;
    for attempt in 0..6 {
        win.nudge();
        match cap.capture() {
            Ok(f) => {
                let nonzero = f.data.iter().filter(|&&b| b != 0).count();
                if f.width == 0 || f.height == 0 || nonzero == 0 {
                    outcome = Some(Err("frame is empty or all black".into()));
                    continue;
                }
                let distinct = f
                    .data
                    .iter()
                    .collect::<std::collections::HashSet<_>>()
                    .len();
                println!(
                    "gdi-probe: frame {}x{} stride={} nonzero={nonzero} distinct_values={distinct}",
                    f.width, f.height, f.stride
                );
                println!(
                    "gdi-probe: pixel(10,10)={:?} pixel(280,210)={:?} pixel(399,299)={:?}",
                    f.pixel(10, 10),
                    f.pixel(280, 210),
                    f.pixel(399, 299)
                );
                let mut detector = controller::vision::MockDetector::synthetic_rect();
                match controller::pipeline::letterbox_to_model(&f, 256, 256) {
                    Ok(model) => match detector.detect(&model).first() {
                        Some(d) => {
                            println!(
                                "gdi-probe: detect {} rect=({:.0},{:.0} {:.0}x{:.0}) conf={:.2}",
                                d.label,
                                d.rect.x,
                                d.rect.y,
                                d.rect.w,
                                d.rect.h,
                                d.confidence
                            );
                            outcome = Some(Ok(format!("OK (attempt {attempt})")));
                        }
                        None => {
                            // low-content frame; retry
                        }
                    },
                    Err(e) => {
                        outcome = Some(Err(format!("letterbox failed: {e}")));
                        break;
                    }
                }
            }
            Err(e) => {
                outcome = Some(Err(format!("capture failed: {e}")));
                break;
            }
        }
        std::thread::sleep(std::time::Duration::from_millis(120));
    }
    match outcome {
        Some(Ok(msg)) => {
            println!("gdi-probe: {msg}");
            0
        }
        Some(Err(msg)) => {
            eprintln!("gdi-probe: {msg}");
            1
        }
        None => {
            eprintln!("gdi-probe: probe pattern never detected after retries");
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

#[cfg(test)]
mod tests {
    use super::*;

    fn args(list: &[&str]) -> Vec<String> {
        std::iter::once("controller.exe".to_string())
            .chain(list.iter().map(|s| s.to_string()))
            .collect()
    }

    #[test]
    fn parse_defaults_without_flags() {
        let o = DryRunOptions::parse(&args(&["--dry-run"])).expect("ok");
        assert_eq!(o.window, "@probe");
        assert_eq!(o.backend, "auto");
        assert!((o.fps - 15.0).abs() < f32::EPSILON);
        assert!((o.duration_secs - 3.0).abs() < f32::EPSILON);
        assert_eq!(o.model, 256);
        assert!((o.min_confidence - 0.6).abs() < f32::EPSILON);
        assert!(!o.require_foreground);
        assert!(o.session_log.is_none() && o.debug_dir.is_none());
    }

    #[test]
    fn empty_window_is_rejected() {
        let e = DryRunOptions::parse(&args(&["--dry-run", "--window", ""])).unwrap_err();
        assert!(e.contains("non-empty"), "{e}");
        let e = DryRunOptions::parse(&args(&["--dry-run", "--window", "   "])).unwrap_err();
        assert!(e.contains("non-empty"), "{e}");
    }

    #[test]
    fn backend_whitelist_is_enforced() {
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--backend", "bogus"])).is_err());
        for ok in ["auto", "wgc", "gdi", "synthetic"] {
            assert!(
                DryRunOptions::parse(&args(&["--dry-run", "--backend", ok])).is_ok(),
                "{ok} must be accepted"
            );
        }
    }

    #[test]
    fn fps_model_confidence_ranges_are_enforced() {
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--fps", "0"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--fps", "-3"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--fps", "nan"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--model", "0"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--model", "8192"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--min-confidence", "1.5"])).is_err());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--min-confidence", "-0.1"])).is_err());
        // boundaries accepted
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--model", "4096"])).is_ok());
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--min-confidence", "1"])).is_ok());
    }

    #[test]
    fn export_png_roundtrips_pixels() {
        let mut frame = controller::frame::Frame::new(4, 4);
        frame.set_pixel(1, 2, [10, 20, 30, 255]);
        frame.set_pixel(3, 0, [200, 150, 100, 255]);
        let dir = std::env::temp_dir().join(format!("nf_png_{}", std::process::id()));
        std::fs::create_dir_all(&dir).ok();
        let path = dir.join("rt.png");
        export_png(path.to_str().expect("utf8 path"), &frame).expect("export");

        // decode and verify the two distinctive pixels survive BGRA->RGBA
        let file = std::fs::File::open(&path).expect("open");
        let mut reader = png::Decoder::new(std::io::BufReader::new(file))
            .read_info()
            .expect("png header");
        let mut buf = vec![0u8; reader.output_buffer_size().expect("buffer size")];
        let info = reader.next_frame(&mut buf).expect("decode");
        assert_eq!((info.width, info.height), (4, 4));
        let at = |x: u32, y: u32| {
            let i = (y as usize * info.width as usize + x as usize) * 4;
            (buf[i], buf[i + 1], buf[i + 2], buf[i + 3])
        };
        // BGRA [10,20,30,255] == RGBA (30,20,10,255)
        assert_eq!(at(1, 2), (30, 20, 10, 255));
        assert_eq!(at(3, 0), (100, 150, 200, 255));
        let _ = std::fs::remove_file(&path);
    }

    #[test]
    fn resize_after_parses_two_slots() {
        let o = DryRunOptions::parse(&args(&["--dry-run", "--resize-after", "1.5", "700x500"]))
            .expect("ok");
        let (secs, w, h) = o.resize_after.expect("resize option");
        assert!((secs - 1.5).abs() < f32::EPSILON);
        assert_eq!((w, h), (700, 500));

        // missing the second slot must be rejected, not silently defaulted
        assert!(DryRunOptions::parse(&args(&["--dry-run", "--resize-after", "1.5"])).is_err());
    }
}
