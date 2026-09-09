//! Native vision controller for game-scheduler (ROADMAP NC0).
//!
//! Scope of this phase: window discovery, capture abstraction, coordinate
//! transforms, mock perception, a safety governor and a `--dry-run` mode
//! that never sends any input. The input module is intentionally a stub:
//! no key/mouse synthesis exists in this codebase yet.
//!
//! Safety red lines (ROADMAP §7): no DLL/process injection, no game memory
//! read/write, no packet interception, no anti-detection. Plain window
//! capture and plain Win32 only.

pub mod capture;
pub mod frame;
pub mod inference;
pub mod input;
pub mod manifest;
pub mod nms;
pub mod onnx;
pub mod perception;
pub mod pipeline;
pub mod protocol;
pub mod replay;
pub mod safety;
pub mod session;
pub mod state;
pub mod template;
pub mod transform;
pub mod vision;
pub mod window;

/// Error type shared across the controller crate.
#[derive(Debug)]
pub enum ControllerError {
    /// A Win32 call failed; wraps the HRESULT error from the windows crate.
    Win(windows::core::Error),
    /// The requested window could not be found.
    WindowNotFound(String),
    /// The window handle is no longer valid (closed / destroyed).
    WindowGone,
    /// A value violated a documented invariant (e.g. non-positive size).
    InvalidInput(String),
    /// A named pipeline stage failed; wraps the underlying error so
    /// operators can see WHICH init step broke (e.g. WGC bring-up).
    Stage(&'static str, Box<ControllerError>),
}

impl std::fmt::Display for ControllerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ControllerError::Win(e) => write!(f, "win32 error: {e}"),
            ControllerError::WindowNotFound(what) => write!(f, "window not found: {what}"),
            ControllerError::WindowGone => write!(f, "window is gone"),
            ControllerError::InvalidInput(what) => write!(f, "invalid input: {what}"),
            ControllerError::Stage(stage, e) => write!(f, "{stage}: {e}"),
        }
    }
}

impl std::error::Error for ControllerError {}

impl From<windows::core::Error> for ControllerError {
    fn from(e: windows::core::Error) -> Self {
        ControllerError::Win(e)
    }
}

pub type Result<T> = std::result::Result<T, ControllerError>;
