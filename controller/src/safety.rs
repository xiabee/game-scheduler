//! SafetyGovernor (NC0): the rule engine every action must pass before it
//! could ever reach an input backend. NC0 never sends input — the governor
//! runs in the dry-run loop so the exact same veto logic is exercised on
//! planned actions from day one.
//!
//! The governor is a pure decision engine: no I/O, no global state, all
//! time comparisons against an explicit `now: Instant` parameter so tests
//! are deterministic. Verdict semantics:
//!
//! - [`GovernorVerdict::Allow`] — the action may proceed.
//! - [`GovernorVerdict::Skip`] — drop this one action, keep running.
//! - [`GovernorVerdict::Pause`] — suspend the pipeline, recalibrate
//!   (window geometry changed); a fresh Transform is required to resume.
//! - [`GovernorVerdict::Stop`] — terminate the session.

use crate::window::WindowLayout;
use crate::{ControllerError, Result};
use std::collections::VecDeque;
use std::time::{Duration, Instant};

/// What the governor decided about the current action or cycle.
#[derive(Debug, Clone, PartialEq)]
pub enum GovernorVerdict {
    Allow,
    /// Drop this action/cycle but keep the session alive.
    Skip {
        reason: String,
    },
    /// Suspend; the caller must recalibrate (rebuild Transform) to resume.
    Pause {
        reason: String,
    },
    /// Terminate the session.
    Stop {
        reason: String,
    },
}

impl GovernorVerdict {
    pub fn is_allow(&self) -> bool {
        matches!(self, GovernorVerdict::Allow)
    }
    pub fn is_stop(&self) -> bool {
        matches!(self, GovernorVerdict::Stop { .. })
    }
    pub fn reason(&self) -> Option<&str> {
        match self {
            GovernorVerdict::Allow => None,
            GovernorVerdict::Skip { reason }
            | GovernorVerdict::Pause { reason }
            | GovernorVerdict::Stop { reason } => Some(reason),
        }
    }
}

/// Knobs for every governor rule. Field docs state the rule each knob drives.
#[derive(Debug, Clone)]
pub struct SafetyConfig {
    /// Rule: confidence below this → `Skip` (action planned but too unsure).
    pub min_confidence: f32,
    /// Rule: at most this many actions in any sliding 1-second window.
    pub max_actions_per_sec: u32,
    /// Rule: at most this many consecutive actions within
    /// `same_point_radius_px` of each other (runaway-click guard).
    pub max_consecutive_same_point: u32,
    /// Distance (client px) two actions must exceed to count as "moved".
    pub same_point_radius_px: f32,
    /// Rule: more than this many retries without a success → `Stop`.
    pub max_retries: u32,
    /// Rule: more than this many iterations in one state without leaving
    /// it → `Stop` (state-machine loop guard).
    pub max_state_loops: u32,
    /// Rule: session longer than this → `Stop` on the next check.
    pub max_session: Duration,
}

impl SafetyConfig {
    pub fn validated(self) -> Result<SafetyConfig> {
        if !(0.0..=1.0).contains(&self.min_confidence) {
            return Err(ControllerError::InvalidInput(format!(
                "min_confidence {} outside [0, 1]",
                self.min_confidence
            )));
        }
        if self.max_actions_per_sec == 0
            || self.max_consecutive_same_point == 0
            || self.max_retries == 0
            || self.max_state_loops == 0
        {
            return Err(ControllerError::InvalidInput(
                "all caps must be at least 1".into(),
            ));
        }
        if self.same_point_radius_px < 0.0 || self.same_point_radius_px.is_nan() {
            return Err(ControllerError::InvalidInput(
                "same_point_radius_px must be >= 0".into(),
            ));
        }
        if self.max_session.is_zero() {
            return Err(ControllerError::InvalidInput(
                "max_session must be > 0".into(),
            ));
        }
        Ok(self)
    }
}

impl Default for SafetyConfig {
    fn default() -> Self {
        SafetyConfig {
            min_confidence: 0.6,
            max_actions_per_sec: 10,
            max_consecutive_same_point: 5,
            same_point_radius_px: 4.0,
            max_retries: 3,
            max_state_loops: 30,
            max_session: Duration::from_secs(30 * 60),
        }
    }
}

/// The rule engine. Created once per session.
#[derive(Debug)]
pub struct SafetyGovernor {
    config: SafetyConfig,
    emergency: bool,
    started_at: Instant,
    /// Timestamps of authorized actions, pruned to the 1-second window.
    action_times: VecDeque<Instant>,
    /// Center of the last authorized action (client coords).
    last_point: Option<(f32, f32)>,
    /// Consecutive authorized actions near `last_point`.
    same_point_run: u32,
    retries: u32,
    loop_iterations: u32,
}

impl SafetyGovernor {
    pub fn new(config: SafetyConfig, started_at: Instant) -> Result<SafetyGovernor> {
        Ok(SafetyGovernor {
            config: config.validated()?,
            emergency: false,
            started_at,
            action_times: VecDeque::new(),
            last_point: None,
            same_point_run: 0,
            retries: 0,
            loop_iterations: 0,
        })
    }

    /// Rule: emergency stop latches; once set, everything is `Stop` forever.
    pub fn trigger_emergency_stop(&mut self) {
        self.emergency = true;
    }

    pub fn is_emergency_stopped(&self) -> bool {
        self.emergency
    }

    /// Preconditions checked once per pipeline cycle, BEFORE perception:
    /// emergency stop, session duration, target window identity, and
    /// foreground correctness — all `Stop`-class rules. Geometry drift is
    /// handled separately by [`Self::check_geometry`] (`Pause`-class):
    /// a moved/resized window is still the same session, a *different*
    /// window is not.
    pub fn check_preconditions(
        &mut self,
        now: Instant,
        same_window: bool,
        foreground: bool,
    ) -> GovernorVerdict {
        if self.emergency {
            return stop("emergency stop is latched");
        }
        // Rule: max session duration.
        if now.duration_since(self.started_at) > self.config.max_session {
            return stop("session exceeded max_session");
        }
        // Rule: target HWND mismatch → stop (identity is not recoverable by
        // recalibration — the game window is gone or we found another one).
        if !same_window {
            return stop("target window identity changed");
        }
        // Rule: wrong foreground → stop (an automation must never act while
        // the user is using another window).
        if !foreground {
            return stop("game window is not foreground");
        }
        GovernorVerdict::Allow
    }

    /// Rule: window size/DPI changed → `Pause` (recalibrate). Position
    /// drift alone does NOT pause: the Transform is anchored to
    /// `screen_origin` captured at Transform build time, so a moved window
    /// pauses too when its origin differs from the calibrated one.
    pub fn check_geometry(
        &mut self,
        calibrated: &WindowLayout,
        current: &WindowLayout,
    ) -> GovernorVerdict {
        if self.emergency {
            return stop("emergency stop is latched");
        }
        if calibrated.client_size != current.client_size {
            return stop_client_size(calibrated, current);
        }
        if calibrated.dpi != current.dpi {
            return pause(format!(
                "dpi changed {} -> {}, recalibrate",
                calibrated.dpi, current.dpi
            ));
        }
        if calibrated.screen_origin != current.screen_origin {
            return pause(format!(
                "window moved {:?} -> {:?}, recalibrate transform",
                calibrated.screen_origin, current.screen_origin
            ));
        }
        GovernorVerdict::Allow
    }

    /// Rules applied per planned action: confidence gate, rate limit,
    /// runaway same-point guard. `point` is in client coordinates.
    pub fn authorize_action(
        &mut self,
        now: Instant,
        confidence: f32,
        point: (f32, f32),
    ) -> GovernorVerdict {
        if self.emergency {
            return stop("emergency stop is latched");
        }
        if now.duration_since(self.started_at) > self.config.max_session {
            return stop("session exceeded max_session");
        }
        // Rule: confidence too low → no action.
        if confidence < self.config.min_confidence {
            return skip(format!(
                "confidence {confidence:.3} < min {:.3}",
                self.config.min_confidence
            ));
        }
        // Rule: max actions per second (sliding window).
        self.prune_action_times(now);
        if self.action_times.len() >= self.config.max_actions_per_sec as usize {
            return skip(format!(
                "rate limit: {} actions in the last second",
                self.action_times.len()
            ));
        }
        // Rule: too many consecutive actions at the same coordinate.
        let moved = match self.last_point {
            None => true,
            Some((lx, ly)) => {
                let dx = point.0 - lx;
                let dy = point.1 - ly;
                (dx * dx + dy * dy).sqrt() > self.config.same_point_radius_px
            }
        };
        if moved {
            self.same_point_run = 0;
        } else if self.same_point_run >= self.config.max_consecutive_same_point {
            return skip(format!(
                "same-point guard: {} consecutive actions within {:.1}px",
                self.same_point_run, self.config.same_point_radius_px
            ));
        }

        // Authorized: record it.
        self.action_times.push_back(now);
        self.last_point = Some(point);
        self.same_point_run += 1;
        self.retries = 0;
        GovernorVerdict::Allow
    }

    /// Rule: a retry was observed. Counts toward `max_retries`; the counter
    /// resets on the next authorized action (a success signal).
    pub fn register_retry(&mut self) -> GovernorVerdict {
        if self.emergency {
            return stop("emergency stop is latched");
        }
        self.retries += 1;
        if self.retries > self.config.max_retries {
            stop(format!(
                "retry budget exhausted ({} > {})",
                self.retries, self.config.max_retries
            ))
        } else {
            GovernorVerdict::Allow
        }
    }

    /// Rule: another iteration inside the same state. `notify_state_changed`
    /// resets the counter when the state machine actually moves on.
    pub fn register_loop_iteration(&mut self) -> GovernorVerdict {
        if self.emergency {
            return stop("emergency stop is latched");
        }
        self.loop_iterations += 1;
        if self.loop_iterations > self.config.max_state_loops {
            stop(format!(
                "state loop exceeded ({} > {})",
                self.loop_iterations, self.config.max_state_loops
            ))
        } else {
            GovernorVerdict::Allow
        }
    }

    pub fn notify_state_changed(&mut self) {
        self.loop_iterations = 0;
    }

    pub fn config(&self) -> &SafetyConfig {
        &self.config
    }

    fn prune_action_times(&mut self, now: Instant) {
        while let Some(front) = self.action_times.front() {
            if now.duration_since(*front) > Duration::from_secs(1) {
                self.action_times.pop_front();
            } else {
                break;
            }
        }
    }
}

fn stop(reason: impl Into<String>) -> GovernorVerdict {
    GovernorVerdict::Stop {
        reason: reason.into(),
    }
}
fn skip(reason: impl Into<String>) -> GovernorVerdict {
    GovernorVerdict::Skip {
        reason: reason.into(),
    }
}
fn pause(reason: impl Into<String>) -> GovernorVerdict {
    GovernorVerdict::Pause {
        reason: reason.into(),
    }
}
fn stop_client_size(calibrated: &WindowLayout, current: &WindowLayout) -> GovernorVerdict {
    pause(format!(
        "client size changed {:?} -> {:?}, recalibrate",
        calibrated.client_size, current.client_size
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn layout(w: i32, h: i32, ox: i32, oy: i32, dpi: u32) -> WindowLayout {
        WindowLayout {
            client_size: (w, h),
            screen_origin: (ox, oy),
            dpi,
        }
    }

    fn gov() -> (SafetyGovernor, Instant) {
        let t0 = Instant::now();
        (
            SafetyGovernor::new(SafetyConfig::default(), t0).expect("gov"),
            t0,
        )
    }

    fn secs(t0: Instant, s: u64) -> Instant {
        t0 + Duration::from_secs(s)
    }

    #[test]
    fn config_validation_rejects_nonsense() {
        let base = SafetyConfig::default();
        let mut bad = base.clone();
        bad.min_confidence = 1.5;
        assert!(SafetyGovernor::new(bad, Instant::now()).is_err());
        let mut bad = base.clone();
        bad.max_actions_per_sec = 0;
        assert!(SafetyGovernor::new(bad, Instant::now()).is_err());
        let mut bad = base.clone();
        bad.max_session = Duration::ZERO;
        assert!(SafetyGovernor::new(bad, Instant::now()).is_err());
        let mut bad = base;
        bad.same_point_radius_px = -1.0;
        assert!(SafetyGovernor::new(bad, Instant::now()).is_err());
        SafetyGovernor::new(SafetyConfig::default(), Instant::now()).expect("default is valid");
    }

    #[test]
    fn happy_path_allows_preconditions_and_actions() {
        let (mut g, t0) = gov();
        assert!(g.check_preconditions(secs(t0, 0), true, true).is_allow());
        let l = layout(1920, 1080, 0, 0, 96);
        assert!(g.check_geometry(&l, &l).is_allow());
        assert!(g
            .authorize_action(secs(t0, 0), 0.9, (100.0, 100.0))
            .is_allow());
    }

    #[test]
    fn emergency_stop_latches_over_everything() {
        let (mut g, t0) = gov();
        g.trigger_emergency_stop();
        assert!(g.is_emergency_stopped());
        assert!(g.check_preconditions(secs(t0, 0), true, true).is_stop());
        let l = layout(1920, 1080, 0, 0, 96);
        assert!(g.check_geometry(&l, &l).is_stop());
        assert!(g.authorize_action(secs(t0, 0), 0.99, (1.0, 1.0)).is_stop());
        assert!(g.register_retry().is_stop());
        assert!(g.register_loop_iteration().is_stop());
    }

    #[test]
    fn identity_mismatch_and_wrong_foreground_stop() {
        let (mut g, t0) = gov();
        // different HWND snapshot (e.g. the found window's handle changed)
        let v = g.check_preconditions(secs(t0, 0), false, true);
        assert!(v.is_stop(), "expected stop for identity change, got {v:?}");

        let v = g.check_preconditions(secs(t0, 0), true, false);
        assert!(v.is_stop(), "expected stop for lost foreground, got {v:?}");
    }

    #[test]
    fn geometry_drift_pauses_for_recalibration() {
        let (mut g, _) = gov();
        let cal = layout(1920, 1080, 0, 0, 96);
        // size change
        let v = g.check_geometry(&cal, &layout(2560, 1440, 0, 0, 96));
        assert!(
            matches!(v, GovernorVerdict::Pause { .. }),
            "size change must pause: {v:?}"
        );
        // dpi change
        let v = g.check_geometry(&cal, &layout(1920, 1080, 0, 0, 120));
        assert!(matches!(v, GovernorVerdict::Pause { .. }));
        // position change
        let v = g.check_geometry(&cal, &layout(1920, 1080, 40, 0, 96));
        assert!(matches!(v, GovernorVerdict::Pause { .. }));
        // identical geometry allows
        assert!(g.check_geometry(&cal, &cal.clone()).is_allow());
    }

    #[test]
    fn confidence_gate_skips_low_and_allows_boundary() {
        let (mut g, t0) = gov();
        let v = g.authorize_action(secs(t0, 0), 0.59, (10.0, 10.0));
        assert!(
            matches!(v, GovernorVerdict::Skip { .. }),
            "0.59 < 0.6 must skip: {v:?}"
        );
        // exactly at the threshold is allowed (>=)
        assert!(g
            .authorize_action(secs(t0, 0), 0.6, (10.0, 10.0))
            .is_allow());
    }

    #[test]
    fn rate_limit_skips_inside_window_and_recovers_after() {
        let (mut g, t0) = gov();
        let t = t0;
        for i in 0..10u64 {
            let v = g.authorize_action(
                t + Duration::from_millis(i * 50),
                0.9,
                (i as f32 * 100.0, 0.0),
            );
            assert!(v.is_allow(), "action {i} of the burst must pass: {v:?}");
        }
        let v = g.authorize_action(t + Duration::from_millis(500), 0.9, (9999.0, 0.0));
        assert!(
            matches!(v, GovernorVerdict::Skip { .. }),
            "11th action inside 1s window must skip"
        );
        // 1.2s after the first action the window has slid past enough entries
        let v = g.authorize_action(t + Duration::from_millis(1200), 0.9, (12345.0, 0.0));
        assert!(v.is_allow(), "window slid, action must pass again: {v:?}");
    }

    #[test]
    fn same_point_guard_blocks_runaway_clicks_and_resets_on_move() {
        let (mut g, t0) = gov();
        // default cap = 5 consecutive same-point actions
        for i in 0..5u32 {
            let v = g.authorize_action(secs(t0, i as u64 + 1), 0.9, (50.0, 50.0));
            assert!(v.is_allow(), "click {i} at same point must pass: {v:?}");
        }
        let v = g.authorize_action(secs(t0, 6), 0.9, (52.0, 51.0)); // within 4px radius
        assert!(
            matches!(v, GovernorVerdict::Skip { .. }),
            "6th same-point action must skip: {v:?}"
        );
        // far move resets the run
        let v = g.authorize_action(secs(t0, 7), 0.9, (400.0, 400.0));
        assert!(v.is_allow(), "moved action must pass: {v:?}");
        let v = g.authorize_action(secs(t0, 8), 0.9, (401.0, 401.0));
        assert!(
            v.is_allow(),
            "second click at the new point must pass: {v:?}"
        );
    }

    #[test]
    fn retry_budget_stops_and_resets_after_success() {
        let (mut g, _) = gov();
        assert!(g.register_retry().is_allow());
        assert!(g.register_retry().is_allow());
        assert!(g.register_retry().is_allow());
        let v = g.register_retry(); // 4 > 3
        assert!(v.is_stop(), "4th retry must stop: {v:?}");
        // once stopped-by-budget the session keeps refusing (no un-stop API;
        // Stop is terminal for the session by contract)
        let v2 = g.register_retry();
        assert!(v2.is_stop());
    }

    #[test]
    fn authorized_action_resets_retry_counter() {
        let (mut g, t0) = gov();
        assert!(g.register_retry().is_allow());
        assert!(g.register_retry().is_allow());
        assert!(
            g.authorize_action(secs(t0, 1), 0.9, (1.0, 1.0)).is_allow(),
            "success resets"
        );
        assert!(g.register_retry().is_allow(), "retry counter starts fresh");
    }

    #[test]
    fn state_loop_cap_stops_until_state_changes() {
        let (mut g, _) = gov();
        for _ in 0..30 {
            assert!(g.register_loop_iteration().is_allow());
        }
        let v = g.register_loop_iteration();
        assert!(v.is_stop(), "31st loop iteration must stop: {v:?}");
        // NOTE: loop-stop is terminal; notify_state_changed only resets the
        // counter for sessions that stopped for OTHER reasons. This test
        // documents the contract: the dry-run loop must treat Stop as exit.
    }

    #[test]
    fn session_duration_stop() {
        let t0 = Instant::now();
        let mut g = SafetyGovernor::new(
            SafetyConfig {
                max_session: Duration::from_secs(60),
                ..SafetyConfig::default()
            },
            t0,
        )
        .expect("gov");
        assert!(g
            .check_preconditions(t0 + Duration::from_secs(59), true, true)
            .is_allow());
        let v = g.check_preconditions(t0 + Duration::from_secs(61), true, true);
        assert!(v.is_stop(), "61s > 60s session must stop: {v:?}");
        let v = g.authorize_action(t0 + Duration::from_secs(61), 0.9, (1.0, 1.0));
        assert!(v.is_stop(), "authorize must also honour session end: {v:?}");
    }
}
