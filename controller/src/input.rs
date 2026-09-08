//! Input controller (NC4, NOT implemented in NC0 by design).
//!
//! NC0 must never send input: this module stays an empty stub until the
//! SafetyGovernor and state machine are proven in dry-run. When NC4 lands
//! it will use plain SendInput-level APIs only — no driver/DLL injection,
//! no memory manipulation (ROADMAP §7 red lines).
