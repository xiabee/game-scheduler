//! Input controller (NC4): plain SendInput-level synthesis behind a hard
//! safety gate. ROADMAP §7 red lines still hold — SendInput only, no
//! driver/DLL injection, no memory manipulation, no anti-detection.
//!
//! Layers:
//! - [`InputController`] — the raw capability: keyboard + absolute mouse in
//!   DESKTOP coordinates (virtual-screen space, the only space the OS
//!   cursor lives in). Implementations: [`SendInputController`] (real) and
//!   [`NoInput`] (always unsupported — the dry-run default).
//! - [`GovernedInput`] — the ONLY sanctioned entry for a session. Every
//!   action passes the SafetyGovernor preconditions (HWND identity +
//!   foreground) first; pointer actions additionally need an explicit
//!   authorization. Pipeline plans already authorized by `run_cycle` go
//!   through [`GovernedInput::execute_authorized`] so the governor's
//!   counters are consumed exactly once.
//!
//! Honesty rules: when no interactive desktop is available (service
//! session, Session 0) the capability reports `Unsupported` instead of
//! silently failing; every veto carries its governor reason.

use crate::safety::{GovernorVerdict, SafetyGovernor};
use crate::window::{interactive_desktop_available, GameWindow};
use windows::Win32::Foundation::HWND;
use windows::Win32::UI::Input::KeyboardAndMouse::{
    MapVirtualKeyW, SendInput, INPUT, INPUT_0, INPUT_KEYBOARD, INPUT_MOUSE, KEYBDINPUT,
    KEYEVENTF_KEYUP, KEYEVENTF_SCANCODE, MAPVK_VK_TO_VSC, MOUSEEVENTF_ABSOLUTE,
    MOUSEEVENTF_LEFTDOWN, MOUSEEVENTF_LEFTUP, MOUSEEVENTF_MOVE, MOUSEEVENTF_VIRTUALDESK,
    MOUSEEVENTF_WHEEL, MOUSEINPUT,
};
use windows::Win32::UI::WindowsAndMessaging::{
    GetSystemMetrics, IsWindow, SM_CXVIRTUALSCREEN, SM_CYVIRTUALSCREEN, SM_XVIRTUALSCREEN,
    SM_YVIRTUALSCREEN,
};

/// Why an input action did not happen. `Unsupported` means the capability
/// cannot work in this context at all (stop asking); `Vetoed` is a
/// skip-class refusal (drop this action, keep going); `Blocked` is a
/// stop-class refusal (terminate the session — the governor decided the
/// environment is no longer safe to act in).
#[derive(Debug)]
pub enum InputError {
    Unsupported(String),
    Vetoed(String),
    Blocked(String),
    /// SendInput refused to insert the event(s) — a system-level failure,
    /// not a veto. Carries the recovered Win32 error.
    Win(windows::core::Error),
}

impl std::fmt::Display for InputError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            InputError::Unsupported(r) => write!(f, "input unsupported: {r}"),
            InputError::Vetoed(r) => write!(f, "input vetoed: {r}"),
            InputError::Blocked(r) => write!(f, "input blocked (stop-class): {r}"),
            InputError::Win(e) => write!(f, "input SendInput failed: {e}"),
        }
    }
}

/// A Win32 virtual-key code. Its own type so call sites cannot confuse it
/// with scancodes or characters.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct VirtualKey(pub u32);

impl VirtualKey {
    pub const A: VirtualKey = VirtualKey(0x41);
    pub const ESC: VirtualKey = VirtualKey(0x1B);
    pub const SPACE: VirtualKey = VirtualKey(0x20);
    pub const RETURN: VirtualKey = VirtualKey(0x0D);
    pub const TAB: VirtualKey = VirtualKey(0x09);
}

/// One executable action in DESKTOP coordinates. Built by callers from
/// Transform outputs; nothing here knows about letterboxing or detection.
#[derive(Debug, Clone, Copy, PartialEq)]
pub enum PlannedInput {
    MouseMove {
        x: i32,
        y: i32,
    },
    Click {
        x: i32,
        y: i32,
    },
    Drag {
        from: (i32, i32),
        to: (i32, i32),
        steps: u32,
    },
    Scroll {
        x: i32,
        y: i32,
        wheel_delta: i32,
    },
    KeyDown(VirtualKey),
    KeyUp(VirtualKey),
    KeyPress(VirtualKey),
}

/// The raw capability. All mouse coordinates are DESKTOP coordinates.
pub trait InputController {
    fn execute(&mut self, action: PlannedInput) -> Result<(), InputError>;
}

/// The dry-run executor: structurally incapable of input. This type is the
/// default so "no input" is a property of the constructed session, not a
/// flag the loop must remember to check.
#[derive(Debug, Default, Clone, Copy)]
pub struct NoInput;

impl InputController for NoInput {
    fn execute(&mut self, _action: PlannedInput) -> Result<(), InputError> {
        Err(InputError::Unsupported(
            "NoInput backend: this session never sends input".into(),
        ))
    }
}

/// Real SendInput synthesis. `available()` must be checked (or construction
/// attempted) before use: without an interactive desktop (service context,
/// Session 0) no synthesis can ever work and the honest answer is
/// `Unsupported`, not a silent failure.
#[derive(Debug, Default, Clone, Copy)]
pub struct SendInputController;

impl SendInputController {
    /// Capability probe: synthesis requires an interactive desktop.
    pub fn available() -> bool {
        interactive_desktop_available()
    }
}

impl InputController for SendInputController {
    fn execute(&mut self, action: PlannedInput) -> Result<(), InputError> {
        let inputs = match action {
            PlannedInput::MouseMove { x, y } => vec![mouse_move_input(x, y)],
            PlannedInput::Click { x, y } => {
                vec![
                    mouse_move_input(x, y),
                    mouse_flag_input(MOUSEEVENTF_LEFTDOWN, 0),
                    mouse_flag_input(MOUSEEVENTF_LEFTUP, 0),
                ]
            }
            PlannedInput::Drag { from, to, steps } => {
                let steps = steps.max(1);
                let mut v = Vec::with_capacity(steps as usize + 3);
                v.push(mouse_move_input(from.0, from.1));
                v.push(mouse_flag_input(MOUSEEVENTF_LEFTDOWN, 0));
                for i in 1..=steps {
                    let t = i as f32 / steps as f32;
                    let x = from.0 as f32 + (to.0 - from.0) as f32 * t;
                    let y = from.1 as f32 + (to.1 - from.1) as f32 * t;
                    v.push(mouse_move_input(x.round() as i32, y.round() as i32));
                }
                v.push(mouse_flag_input(MOUSEEVENTF_LEFTUP, 0));
                v
            }
            PlannedInput::Scroll { x, y, wheel_delta } => vec![
                mouse_move_input(x, y),
                mouse_flag_input(MOUSEEVENTF_WHEEL, wheel_delta),
            ],
            PlannedInput::KeyDown(key) => vec![key_input(key, false)?],
            PlannedInput::KeyUp(key) => vec![key_input(key, true)?],
            PlannedInput::KeyPress(key) => vec![key_input(key, false)?, key_input(key, true)?],
        };
        send_all(&inputs)
    }
}

/// Insert every event; SendInput reports how many made it in — a partial
/// insert is a failure (the tail events are missing).
fn send_all(inputs: &[INPUT]) -> Result<(), InputError> {
    let n = unsafe { SendInput(inputs, std::mem::size_of::<INPUT>() as i32) };
    if n == inputs.len() as u32 {
        Ok(())
    } else {
        let code = unsafe { windows::Win32::Foundation::GetLastError() };
        Err(InputError::Win(windows::core::Error::from_hresult(
            windows::core::HRESULT::from_win32(code.0),
        )))
    }
}

fn mouse_move_input(x: i32, y: i32) -> INPUT {
    let (ax, ay) = desktop_to_absolute(x, y);
    INPUT {
        r#type: INPUT_MOUSE,
        Anonymous: INPUT_0 {
            mi: MOUSEINPUT {
                dx: ax,
                dy: ay,
                mouseData: 0,
                dwFlags: MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK,
                time: 0,
                dwExtraInfo: 0,
            },
        },
    }
}

fn mouse_flag_input(
    flags: windows::Win32::UI::Input::KeyboardAndMouse::MOUSE_EVENT_FLAGS,
    data: i32,
) -> INPUT {
    INPUT {
        r#type: INPUT_MOUSE,
        Anonymous: INPUT_0 {
            mi: MOUSEINPUT {
                dx: 0,
                dy: 0,
                mouseData: data as u32,
                dwFlags: flags,
                time: 0,
                dwExtraInfo: 0,
            },
        },
    }
}

fn key_input(key: VirtualKey, up: bool) -> Result<INPUT, InputError> {
    // Scancode route: games read hardware scancodes; MapVirtualKeyW is a
    // pure lookup that works in any session. VK-only fallback covers the
    // handful of virtual keys with no scancode.
    let scancode = unsafe { MapVirtualKeyW(key.0, MAPVK_VK_TO_VSC) };
    let mut flags = if scancode != 0 {
        KEYEVENTF_SCANCODE
    } else {
        Default::default()
    };
    if up {
        flags |= KEYEVENTF_KEYUP;
    }
    Ok(INPUT {
        r#type: INPUT_KEYBOARD,
        Anonymous: INPUT_0 {
            ki: KEYBDINPUT {
                wVk: if scancode != 0 {
                    Default::default()
                } else {
                    windows::Win32::UI::Input::KeyboardAndMouse::VIRTUAL_KEY(key.0 as u16)
                },
                wScan: scancode as u16,
                dwFlags: flags,
                time: 0,
                dwExtraInfo: 0,
            },
        },
    })
}

/// Map a desktop point to SendInput's absolute 0..=65535 space over the
/// VIRTUAL screen. Out-of-range points clamp to the edges (the OS cursor
/// cannot leave the virtual screen anyway).
fn desktop_to_absolute(x: i32, y: i32) -> (i32, i32) {
    let vx = unsafe { GetSystemMetrics(SM_XVIRTUALSCREEN) };
    let vy = unsafe { GetSystemMetrics(SM_YVIRTUALSCREEN) };
    let vw = unsafe { GetSystemMetrics(SM_CXVIRTUALSCREEN) }.max(1);
    let vh = unsafe { GetSystemMetrics(SM_CYVIRTUALSCREEN) }.max(1);
    desktop_to_absolute_with(vx, vy, vw, vh, x, y)
}

/// Pure math core of [`desktop_to_absolute`] so tests do not depend on the
/// host's monitor layout. Formula: full span maps to 0..=65535 inclusive.
fn desktop_to_absolute_with(vx: i32, vy: i32, vw: i32, vh: i32, x: i32, y: i32) -> (i32, i32) {
    let span = |off: i32, size: i32, p: i32| -> i32 {
        let rel = (p - off).clamp(0, size.max(1) - 1) as i64;
        ((rel * 65535) / (size.max(1) - 1).max(1) as i64) as i32
    };
    (span(vx, vw, x), span(vy, vh, y))
}

/// The safety wrapper every session must act through. Preconditions
/// (HWND identity + foreground + governor clock rules) run BEFORE the
/// backend sees anything; pointer actions can additionally require a
/// fresh authorization.
pub struct GovernedInput<'a> {
    backend: &'a mut dyn InputController,
    hwnd: HWND,
    governor: &'a mut SafetyGovernor,
    /// Actions that reached the backend.
    pub executed: u32,
    /// Actions refused by authorization (skip-class).
    pub vetoed: u32,
}

impl<'a> GovernedInput<'a> {
    pub fn new(
        backend: &'a mut dyn InputController,
        hwnd: HWND,
        governor: &'a mut SafetyGovernor,
    ) -> GovernedInput<'a> {
        GovernedInput {
            backend,
            hwnd,
            governor,
            executed: 0,
            vetoed: 0,
        }
    }

    /// Preconditions only. For actions ALREADY authorized this cycle by
    /// `run_cycle` (which consumed the governor's confidence/rate/same-point
    /// rules) — calling `authorize_and_execute` here would double-count.
    pub fn execute_authorized(
        &mut self,
        now: std::time::Instant,
        action: PlannedInput,
    ) -> Result<(), InputError> {
        let (same_window, foreground) = self.observe_window();
        self.execute_authorized_checked(now, same_window, foreground, action)
    }

    /// Preconditions + fresh pointer authorization (confidence gate, rate
    /// limit, same-point guard). For out-of-band actions that did not come
    /// from the perception pipeline.
    pub fn authorize_and_execute(
        &mut self,
        now: std::time::Instant,
        confidence: f32,
        client_point: (f32, f32),
        action: PlannedInput,
    ) -> Result<(), InputError> {
        let (same_window, foreground) = self.observe_window();
        self.authorize_and_execute_checked(
            now,
            same_window,
            foreground,
            confidence,
            client_point,
            action,
        )
    }

    /// HWND alive + foreground, as the OS currently reports it.
    fn observe_window(&self) -> (bool, bool) {
        let same_window = unsafe { IsWindow(Some(self.hwnd)) }.as_bool();
        let foreground = GameWindow::from_hwnd(self.hwnd)
            .and_then(|w| w.is_foreground())
            .unwrap_or(false);
        (same_window, foreground)
    }

    fn execute_authorized_checked(
        &mut self,
        now: std::time::Instant,
        same_window: bool,
        foreground: bool,
        action: PlannedInput,
    ) -> Result<(), InputError> {
        self.precheck(now, same_window, foreground)?;
        self.backend.execute(action)?;
        self.executed += 1;
        Ok(())
    }

    fn authorize_and_execute_checked(
        &mut self,
        now: std::time::Instant,
        same_window: bool,
        foreground: bool,
        confidence: f32,
        client_point: (f32, f32),
        action: PlannedInput,
    ) -> Result<(), InputError> {
        self.precheck(now, same_window, foreground)?;
        match self
            .governor
            .authorize_action(now, confidence, client_point)
        {
            GovernorVerdict::Allow => {}
            GovernorVerdict::Skip { reason } => {
                self.vetoed += 1;
                return Err(InputError::Vetoed(reason));
            }
            GovernorVerdict::Pause { reason } | GovernorVerdict::Stop { reason } => {
                self.vetoed += 1;
                return Err(InputError::Blocked(reason));
            }
        }
        self.backend.execute(action)?;
        self.executed += 1;
        Ok(())
    }

    /// HWND identity + foreground + governor clock/emergency rules.
    /// Stop-class verdicts surface as [`InputError::Blocked`]; the session
    /// loop must treat that as a termination signal.
    fn precheck(
        &mut self,
        now: std::time::Instant,
        same_window: bool,
        foreground: bool,
    ) -> Result<(), InputError> {
        match self
            .governor
            .check_preconditions(now, same_window, foreground)
        {
            GovernorVerdict::Allow => Ok(()),
            GovernorVerdict::Skip { reason } => {
                self.vetoed += 1;
                Err(InputError::Vetoed(reason))
            }
            GovernorVerdict::Pause { reason } | GovernorVerdict::Stop { reason } => {
                self.vetoed += 1;
                Err(InputError::Blocked(reason))
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::safety::SafetyConfig;

    // ---- NoInput -------------------------------------------------------

    #[test]
    fn no_input_is_unsupported_for_every_action() {
        let mut b = NoInput;
        let actions = [
            PlannedInput::MouseMove { x: 1, y: 2 },
            PlannedInput::Click { x: 1, y: 2 },
            PlannedInput::Drag {
                from: (0, 0),
                to: (9, 9),
                steps: 3,
            },
            PlannedInput::Scroll {
                x: 1,
                y: 2,
                wheel_delta: 120,
            },
            PlannedInput::KeyDown(VirtualKey::A),
            PlannedInput::KeyUp(VirtualKey::A),
            PlannedInput::KeyPress(VirtualKey::ESC),
        ];
        for a in actions {
            let err = b.execute(a).expect_err("NoInput must refuse");
            assert!(matches!(err, InputError::Unsupported(_)), "{err:?}");
        }
    }

    // ---- absolute-coordinate math --------------------------------------

    #[test]
    fn desktop_to_absolute_maps_span_to_full_range() {
        // single 1920x1080 screen at origin
        let (ax, ay) = desktop_to_absolute_with(0, 0, 1920, 1080, 0, 0);
        assert_eq!((ax, ay), (0, 0));
        let (ax, ay) = desktop_to_absolute_with(0, 0, 1920, 1080, 1919, 1079);
        assert_eq!((ax, ay), (65535, 65535));
        // Midpoint lands near 32767. 65535 does not divide evenly by
        // 1919 pixels, so the per-pixel spacing (≈34.15) forces a
        // one-pixel tolerance band rather than an exact value.
        let (ax, ay) = desktop_to_absolute_with(0, 0, 1920, 1080, 960, 540);
        let tol = (65535.0f64 / 1919.0).ceil() as i32 + 1;
        assert!(
            (ax - 32767).abs() <= tol && (ay - 32767).abs() <= tol,
            "{ax},{ay} must be within {tol} of center"
        );
    }

    #[test]
    fn desktop_to_absolute_offsets_for_secondary_monitors() {
        // virtual screen starting at -1920 (monitor left of the primary)
        let (ax, _) = desktop_to_absolute_with(-1920, 0, 3840, 1080, -1920, 0);
        assert_eq!(ax, 0, "virtual-screen left edge maps to 0");
        let (ax, _) = desktop_to_absolute_with(-1920, 0, 3840, 1080, 1919, 0);
        assert_eq!(ax, 65535, "virtual-screen right edge maps to 65535");
    }

    #[test]
    fn desktop_to_absolute_clamps_out_of_range_points() {
        let (ax, ay) = desktop_to_absolute_with(0, 0, 1920, 1080, -50, 99999);
        assert_eq!(ax, 0, "negative x clamps to left edge");
        assert_eq!(ay, 65535, "y past the bottom clamps to bottom edge");
    }

    // ---- SendInput event construction ----------------------------------

    #[test]
    fn letter_keys_have_scancodes() {
        // MapVirtualKeyW is a pure lookup usable in any session.
        assert_ne!(
            unsafe { MapVirtualKeyW(VirtualKey::A.0, MAPVK_VK_TO_VSC) },
            0
        );
        assert_ne!(
            unsafe { MapVirtualKeyW(VirtualKey::ESC.0, MAPVK_VK_TO_VSC) },
            0
        );
    }

    // ---- GovernedInput --------------------------------------------------

    struct RecordingMock {
        calls: u32,
    }

    impl InputController for RecordingMock {
        fn execute(&mut self, _action: PlannedInput) -> Result<(), InputError> {
            self.calls += 1;
            Ok(())
        }
    }

    fn gov() -> (SafetyGovernor, std::time::Instant) {
        let t0 = std::time::Instant::now();
        (
            SafetyGovernor::new(SafetyConfig::default(), t0).expect("gov"),
            t0,
        )
    }

    #[test]
    fn happy_path_executes_and_counts() {
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let executed = {
            let mut gi = GovernedInput::new(&mut mock, HWND::default(), &mut g);
            gi.execute_authorized_checked(t0, true, true, PlannedInput::Click { x: 5, y: 5 })
                .expect("allowed action must reach the backend");
            gi.executed
        };
        assert_eq!(mock.calls, 1);
        assert_eq!(executed, 1);
    }

    #[test]
    fn dead_hwnd_and_lost_foreground_block_before_backend() {
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let vetoed = {
            let mut gi = GovernedInput::new(&mut mock, HWND::default(), &mut g);
            let err = gi
                .execute_authorized_checked(t0, false, true, PlannedInput::KeyPress(VirtualKey::A))
                .expect_err("dead hwnd must block");
            assert!(matches!(err, InputError::Blocked(_)), "{err:?}");
            let err = gi
                .execute_authorized_checked(t0, true, false, PlannedInput::KeyPress(VirtualKey::A))
                .expect_err("lost foreground must block");
            assert!(matches!(err, InputError::Blocked(_)), "{err:?}");
            gi.vetoed
        };
        assert_eq!(mock.calls, 0, "no event may reach the backend");
        assert_eq!(vetoed, 2);
    }

    #[test]
    fn low_confidence_pointer_is_vetoed_not_executed() {
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let vetoed = {
            let mut gi = GovernedInput::new(&mut mock, HWND::default(), &mut g);
            let err = gi
                .authorize_and_execute_checked(
                    t0,
                    true,
                    true,
                    0.1,
                    (10.0, 10.0),
                    PlannedInput::Click { x: 1, y: 1 },
                )
                .expect_err("confidence 0.1 < 0.6 must veto");
            assert!(matches!(err, InputError::Vetoed(_)), "{err:?}");
            gi.vetoed
        };
        assert_eq!(mock.calls, 0, "vetoed actions never reach the backend");
        assert_eq!(vetoed, 1);
    }

    #[test]
    fn pointer_authorization_consumes_rate_budget_once() {
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let executed = {
            let mut gi = GovernedInput::new(&mut mock, HWND::default(), &mut g);
            // default max_actions_per_sec = 10
            for i in 0..10u32 {
                gi.authorize_and_execute_checked(
                    t0 + std::time::Duration::from_millis(i as u64 * 50),
                    true,
                    true,
                    0.9,
                    (i as f32 * 100.0, 0.0),
                    PlannedInput::Click { x: i as i32, y: 0 },
                )
                .expect("burst within the rate cap must pass");
            }
            let err = gi
                .authorize_and_execute_checked(
                    t0 + std::time::Duration::from_millis(600),
                    true,
                    true,
                    0.9,
                    (9999.0, 0.0),
                    PlannedInput::Click { x: 0, y: 0 },
                )
                .expect_err("11th action inside the 1s window must veto");
            assert!(matches!(err, InputError::Vetoed(_)), "{err:?}");
            gi.executed
        };
        assert_eq!(
            mock.calls, 10,
            "exactly the authorized count reached the backend"
        );
        assert_eq!(executed, 10);
    }

    #[test]
    fn keys_bypass_pointer_authorization_but_not_preconditions() {
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let calls = {
            let mut gi = GovernedInput::new(&mut mock, HWND::default(), &mut g);
            // Keys have no pointer semantics: preconditions still apply, the
            // confidence/same-point rules do not (documented contract).
            gi.execute_authorized_checked(t0, true, true, PlannedInput::KeyPress(VirtualKey::A))
                .expect("key under valid preconditions must pass");
            let err = gi
                .execute_authorized_checked(t0, true, false, PlannedInput::KeyPress(VirtualKey::A))
                .expect_err("lost foreground must still block keys");
            assert!(matches!(err, InputError::Blocked(_)));
            gi.executed
        };
        assert_eq!(mock.calls, 1);
        assert_eq!(calls, 1);
    }

    /// The OS-reading path: identity must hold while the probe window is
    /// alive, and the foreground verdict must match what GameWindow itself
    /// reports. Uses the recording backend — no real input is sent.
    #[test]
    fn governed_precheck_matches_live_window_state() {
        if !crate::window::interactive_desktop_available() {
            eprintln!("skipped: no interactive desktop (service context)");
            return;
        }
        let _lock = crate::window::tests::window_test_lock();
        crate::window::ensure_dpi_awareness();
        let title = format!(
            "NFCTRL-GOVEDIN-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .map(|d| d.subsec_nanos())
                .unwrap_or(0)
        );
        let probe = crate::window::OwnedTestWindow::new(320, 240, &title).expect("probe window");
        let (mut g, t0) = gov();
        let mut mock = RecordingMock { calls: 0 };
        let foreground = crate::window::GameWindow::from_hwnd(probe.hwnd)
            .and_then(|w| w.is_foreground())
            .unwrap_or(false);
        let outcome: Result<(), InputError> = {
            let mut gi = GovernedInput::new(&mut mock, probe.hwnd, &mut g);
            gi.execute_authorized(t0, PlannedInput::Click { x: 1, y: 1 })
        };
        if foreground {
            outcome.expect("live foreground window must pass precheck");
            assert_eq!(mock.calls, 1);
        } else {
            assert!(
                matches!(outcome, Err(InputError::Blocked(_))),
                "non-foreground window must block: {outcome:?}"
            );
            assert_eq!(mock.calls, 0);
        }
    }

    #[test]
    fn display_is_operator_readable() {
        assert!(InputError::Unsupported("no desktop".into())
            .to_string()
            .contains("unsupported"));
        assert!(InputError::Vetoed("rate".into())
            .to_string()
            .contains("vetoed"));
        assert!(InputError::Blocked("not foreground".into())
            .to_string()
            .contains("blocked"));
    }
}
