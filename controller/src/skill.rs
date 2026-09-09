//! Skill definitions and the state-machine engine (NC3 groundwork).
//!
//! A Skill is a data-driven state machine, not `if sees button: click`:
//! each state declares what EVIDENCE it expects (L0 probe names from the
//! perception layer, or L1/L2 detections), what actions it PLANS, and how
//! it times out, retries or falls back. The engine in this module is a
//! pure transition evaluator: `step` consumes one cycle's evidence and
//! returns the new position plus planned actions. Nothing here ever sends
//! input (NC4 decides that); the dry-run session just logs the plans.
//!
//! Definition format (JSON, validated on load):
//! ```json
//! {
//!   "name": "daily_claim",
//!   "start": "home",
//!   "states": [
//!     {
//!       "name": "home",
//!       "expect": [{ "probe": "menu_button" }],
//!       "actions": ["click menu_button"],
//!       "next": "menu",
//!       "timeout_ms": 10000,
//!       "max_retries": 2,
//!       "fallback": "home_reset",
//!       "terminal": false
//!     }
//!   ]
//! }
//! ```

use serde::Deserialize;

/// One evidence predicate: "L0 probe `probe` fired" or "a detection of
/// `label` with confidence >= `min_confidence` exists". Exactly one of
/// the two forms may be set.
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct Expectation {
    #[serde(default)]
    pub probe: Option<String>,
    #[serde(default)]
    pub label: Option<String>,
    #[serde(default = "default_min_confidence")]
    pub min_confidence: f32,
}

fn default_min_confidence() -> f32 {
    0.6
}

impl Expectation {
    /// Evaluate against one cycle's perception summary.
    pub fn holds(&self, fired_probes: &dyn Fn(&str) -> bool, detections: &[(String, f32)]) -> bool {
        if let Some(p) = &self.probe {
            fired_probes(p)
        } else if let Some(label) = &self.label {
            detections
                .iter()
                .any(|(l, c)| l == label && *c >= self.min_confidence)
        } else {
            false
        }
    }
}

/// One declared state of a skill.
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct StateDef {
    pub name: String,
    #[serde(default)]
    pub expect: Vec<Expectation>,
    /// Human-readable action plans (logged in dry-run; NC4 executes).
    #[serde(default)]
    pub actions: Vec<String>,
    /// State to move to when all expectations hold.
    pub next: String,
    /// Give up on this state after this long; `max_retries` re-entries
    /// are allowed before `fallback` (or failure) takes over.
    pub timeout_ms: u64,
    #[serde(default = "default_max_retries")]
    pub max_retries: u32,
    /// Optional state to retreat to on timeout exhaustion.
    #[serde(default)]
    pub fallback: Option<String>,
    /// Terminal states end the skill successfully.
    #[serde(default)]
    pub terminal: bool,
}

fn default_max_retries() -> u32 {
    2
}

/// A validated skill definition.
#[derive(Debug, Clone, Deserialize, PartialEq)]
#[serde(deny_unknown_fields)]
pub struct SkillDefinition {
    pub name: String,
    /// The state the engine starts in.
    pub start: String,
    pub states: Vec<StateDef>,
}

impl SkillDefinition {
    pub fn from_json(text: &str) -> Result<SkillDefinition, String> {
        let def: SkillDefinition =
            serde_json::from_str(text).map_err(|e| format!("invalid skill JSON: {e}"))?;
        def.validate()?;
        Ok(def)
    }

    /// Structural validation: unique state names, resolvable transitions,
    /// exactly one start state, terminal states need no resolvable `next`.
    pub fn validate(&self) -> Result<(), String> {
        if self.name.trim().is_empty() {
            return Err("skill name must be non-blank".into());
        }
        if self.states.is_empty() {
            return Err("skill must define at least one state".into());
        }
        if self.states.len() > 128 {
            return Err(format!("at most 128 states, got {}", self.states.len()));
        }
        let names: Vec<&str> = self.states.iter().map(|s| s.name.as_str()).collect();
        if names.iter().any(|n| n.trim().is_empty()) {
            return Err("state names must be non-blank".into());
        }
        for (i, n) in names.iter().enumerate() {
            if names[..i].contains(n) {
                return Err(format!("duplicate state name {n:?} (state #{i})"));
            }
        }
        if !names.contains(&self.start.as_str()) {
            return Err(format!("start state {:?} is not defined", self.start));
        }
        for s in &self.states {
            if s.terminal {
                continue;
            }
            if !names.contains(&s.next.as_str()) {
                return Err(format!(
                    "state {:?} transitions to undefined state {:?}",
                    s.name, s.next
                ));
            }
            if let Some(f) = &s.fallback {
                if !names.contains(&f.as_str()) {
                    return Err(format!(
                        "state {:?} falls back to undefined state {:?}",
                        s.name, f
                    ));
                }
            }
        }
        Ok(())
    }

    pub fn state(&self, name: &str) -> Option<&StateDef> {
        self.states.iter().find(|s| s.name == name)
    }
}

/// Where the engine stands after one `step`.
#[derive(Debug, Clone, PartialEq)]
pub enum StepOutcome {
    /// Staying in the current state: expectations not yet met, still
    /// within the timeout/retry budget.
    Waiting,
    /// Expectations met: the engine moved to `to` and plans the source
    /// state's actions.
    Transitioned { to: String, planned: Vec<String> },
    /// Timeout exhausted (after retries): retreated to the fallback.
    FellBack { to: String },
    /// A terminal state was reached successfully.
    Done,
    /// Timeout exhausted and no fallback exists: the skill failed.
    Failed,
}

/// The running engine for one skill instance.
#[derive(Debug)]
pub struct SkillRunner {
    pub definition: SkillDefinition,
    current: String,
    state_entered_at_ms: u64,
    reentries: u32,
    done: bool,
    failed: bool,
}

impl SkillRunner {
    pub fn start(definition: SkillDefinition, now_ms: u64) -> SkillRunner {
        let current = definition.start.clone();
        SkillRunner {
            definition,
            current,
            state_entered_at_ms: now_ms,
            reentries: 0,
            done: false,
            failed: false,
        }
    }

    pub fn current(&self) -> &str {
        &self.current
    }

    pub fn is_done(&self) -> bool {
        self.done
    }

    pub fn is_failed(&self) -> bool {
        self.failed
    }

    /// Consume one cycle: evaluate the current state's expectations and
    /// the timeout budget. `now_ms` is the caller's monotonic clock.
    pub fn step(
        &mut self,
        now_ms: u64,
        fired_probes: &dyn Fn(&str) -> bool,
        detections: &[(String, f32)],
    ) -> StepOutcome {
        if self.done {
            return StepOutcome::Done;
        }
        if self.failed {
            return StepOutcome::Failed;
        }
        let state = match self.definition.state(&self.current) {
            Some(s) => s.clone(),
            None => {
                self.failed = true;
                return StepOutcome::Failed;
            }
        };
        if state.terminal {
            self.done = true;
            return StepOutcome::Done;
        }

        let all_hold = state
            .expect
            .iter()
            .all(|e| e.holds(fired_probes, detections));
        if all_hold {
            let planned = state.actions.clone();
            let to = state.next.clone();
            self.current = to.clone();
            self.state_entered_at_ms = now_ms;
            self.reentries = 0;
            if let Some(next_def) = self.definition.state(&to) {
                if next_def.terminal {
                    self.done = true;
                    return StepOutcome::Done;
                }
            }
            return StepOutcome::Transitioned { to, planned };
        }

        // expectations unmet: check the timeout budget
        if now_ms.saturating_sub(self.state_entered_at_ms) >= state.timeout_ms {
            // retries allow re-observation: each retry restarts the clock
            if self.reentries < state.max_retries {
                self.reentries += 1;
                self.state_entered_at_ms = now_ms;
                return StepOutcome::Waiting;
            }
            if let Some(fb) = state.fallback.clone() {
                self.reentries = 0;
                self.current = fb.clone();
                self.state_entered_at_ms = now_ms;
                return StepOutcome::FellBack { to: fb };
            }
            self.failed = true;
            return StepOutcome::Failed;
        }
        StepOutcome::Waiting
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SKILL: &str = r#"{
        "name": "daily_claim",
        "start": "home",
        "states": [
            {
                "name": "home",
                "expect": [{ "probe": "menu_button" }],
                "actions": ["click menu_button"],
                "next": "menu",
                "timeout_ms": 1000,
                "max_retries": 1,
                "fallback": "home_reset"
            },
            {
                "name": "home_reset",
                "expect": [{ "label": "reset_ok", "min_confidence": 0.5 }],
                "actions": [],
                "next": "home",
                "timeout_ms": 500
            },
            {
                "name": "menu",
                "expect": [{ "label": "daily_icon" }],
                "actions": ["click daily_icon"],
                "next": "claimed",
                "timeout_ms": 2000
            },
            {
                "name": "claimed",
                "expect": [{ "probe": "claim_banner" }],
                "actions": [],
                "next": "done",
                "timeout_ms": 1000
            },
            {
                "name": "done",
                "terminal": true,
                "expect": [],
                "next": "done",
                "timeout_ms": 0
            }
        ]
    }"#;

    fn probes<'a>(fired: &'a [&'a str]) -> impl Fn(&str) -> bool + 'a {
        move |n: &str| fired.contains(&n)
    }

    fn detections(items: &[(String, f32)]) -> Vec<(String, f32)> {
        items.to_vec()
    }

    #[test]
    fn definition_validates_and_rejects_broken_graphs() {
        SkillDefinition::from_json(SKILL).expect("valid skill");
        // start state missing
        let bad = SKILL.replace("\"start\": \"home\"", "\"start\": \"nowhere\"");
        assert!(SkillDefinition::from_json(&bad)
            .unwrap_err()
            .contains("start state"));
        // dangling transition
        let bad = SKILL.replace("\"next\": \"menu\"", "\"next\": \"menow\"");
        assert!(SkillDefinition::from_json(&bad)
            .unwrap_err()
            .contains("undefined"));
        // duplicate state names
        let bad = SKILL
            .replace("\"name\": \"menu\"", "\"name\": \"home\"")
            .replace("\"name\": \"home_reset\"", "\"name\": \"home_reset_x\"");
        assert!(SkillDefinition::from_json(&bad)
            .unwrap_err()
            .contains("duplicate"));
    }

    #[test]
    fn happy_path_walks_probe_then_label_states() {
        let def = SkillDefinition::from_json(SKILL).expect("skill");
        let mut r = SkillRunner::start(def, 0);
        assert_eq!(r.current(), "home");
        // expectations unmet -> Waiting
        assert_eq!(
            r.step(100, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        // probe fires -> transition to menu (planning home's action)
        assert_eq!(
            r.step(200, &probes(&["menu_button"]), &detections(&[])),
            StepOutcome::Transitioned {
                to: "menu".into(),
                planned: vec!["click menu_button".into()]
            }
        );
        // menu needs a detection
        assert_eq!(
            r.step(300, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        assert_eq!(
            r.step(
                400,
                &probes(&[]),
                &detections(&[("daily_icon".into(), 0.8)])
            ),
            StepOutcome::Transitioned {
                to: "claimed".into(),
                planned: vec!["click daily_icon".into()]
            }
        );
        assert_eq!(
            r.step(500, &probes(&["claim_banner"]), &detections(&[])),
            StepOutcome::Done
        );
        assert!(r.is_done());
        assert_eq!(
            r.step(600, &probes(&[]), &detections(&[])),
            StepOutcome::Done
        );
    }

    #[test]
    fn label_expectation_respects_min_confidence() {
        let def = SkillDefinition::from_json(SKILL).expect("skill");
        let mut r = SkillRunner::start(def, 0);
        r.step(0, &probes(&["menu_button"]), &detections(&[]));
        // below the 0.6 default gate
        assert_eq!(
            r.step(10, &probes(&[]), &detections(&[("daily_icon".into(), 0.3)])),
            StepOutcome::Waiting
        );
    }

    #[test]
    fn timeout_budget_retries_then_falls_back() {
        let def = SkillDefinition::from_json(SKILL).expect("skill");
        let mut r = SkillRunner::start(def, 0);
        assert_eq!(
            r.step(0, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        // timeout_ms=1000 with 1 retry: at 1000 the retry restarts the clock
        assert_eq!(
            r.step(1000, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        // retry also expired: fallback to home_reset
        assert_eq!(
            r.step(2000, &probes(&[]), &detections(&[])),
            StepOutcome::FellBack {
                to: "home_reset".into()
            }
        );
        // home_reset wants a detection; its max_retries reset on entry
        assert_eq!(
            r.step(2100, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        // ...label met (confidence >= 0.5) -> back to home
        assert_eq!(
            r.step(2200, &probes(&[]), &detections(&[("reset_ok".into(), 0.5)])),
            StepOutcome::Transitioned {
                to: "home".into(),
                planned: vec![]
            }
        );
    }

    #[test]
    fn timeout_without_fallback_fails_the_skill() {
        // "menu" has no fallback: entering it with nothing detected until
        // its 2000ms budget ends must fail the skill
        let def = SkillDefinition::from_json(SKILL).expect("skill");
        let mut r = SkillRunner::start(def, 0);
        r.step(0, &probes(&["menu_button"]), &detections(&[]));
        assert_eq!(r.current(), "menu");
        // menu timeout 2000, default max_retries 2 -> two Waiting restarts
        assert_eq!(
            r.step(2000, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        assert_eq!(
            r.step(4000, &probes(&[]), &detections(&[])),
            StepOutcome::Waiting
        );
        assert_eq!(
            r.step(6000, &probes(&[]), &detections(&[])),
            StepOutcome::Failed
        );
        assert!(r.is_failed());
    }

    #[test]
    fn the_committed_example_skill_always_parses() {
        // keeps examples/skills/*.example.json honest by construction
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("examples/skills/daily_claim.example.json");
        let text = std::fs::read_to_string(&path).expect("example file");
        let def = SkillDefinition::from_json(&text).expect("example must validate");
        assert_eq!(def.name, "daily_claim_example");
        assert!(def.state("done").expect("done state").terminal);
    }

    #[test]
    fn json_expectation_forms_are_strict() {
        // unknown fields are rejected outright
        let bad = r#"[{ "probe": "x", "bogus": 1 }]"#;
        let e = serde_json::from_str::<Vec<Expectation>>(bad).unwrap_err();
        assert!(e.to_string().contains("unknown field"), "{e}");
    }
}
