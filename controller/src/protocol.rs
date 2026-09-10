//! Process protocol to the Go scheduler (NC6 wire format, schema frozen).
//!
//! stdin/stdout JSON lines, one envelope per line (see
//! docs/controller-protocol-draft.md — this module IS the normative
//! schema; the doc mirrors it and a test pins the doc's example).
//!
//! Design points:
//! - `v` version mismatch is fail-fast: a v1 peer must never guess-parse a
//!   future line format.
//! - `type` selects the payload shape on the WIRE (outside the payload
//!   object), so parsing is two-pass: read the envelope generically, then
//!   deserialize the payload against the struct the kind names. A kind/payload
//!   mismatch is an error, not a best-effort match.
//! - Unknown fields inside a v1 payload are ignored (forward-compatible
//!   evolution within v1); a NEW kind or a different `v` is not.
//!
//! No timestamps are generated here: `ts` is an RFC 3339 string supplied by
//! the caller, keeping this module std+serde only.

use serde::{Deserialize, Serialize};

/// The only protocol version this build speaks.
pub const PROTOCOL_VERSION: u32 = 1;

#[derive(Debug)]
pub enum ProtocolError {
    /// The line is not valid JSON or misses envelope fields.
    BadJson(String),
    /// A peer speaking a different protocol version — fail fast, never guess.
    UnsupportedVersion { got: u32, supported: u32 },
    /// `type` is not one of the seven kinds.
    UnknownKind(String),
    /// The payload object does not satisfy the schema its kind names.
    BadPayload(String),
}

impl std::fmt::Display for ProtocolError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ProtocolError::BadJson(e) => write!(f, "protocol: bad JSON line: {e}"),
            ProtocolError::UnsupportedVersion { got, supported } => {
                write!(
                    f,
                    "protocol: version {got} unsupported (this peer speaks v{supported})"
                )
            }
            ProtocolError::UnknownKind(k) => write!(f, "protocol: unknown message kind {k:?}"),
            ProtocolError::BadPayload(e) => write!(f, "protocol: payload rejected: {e}"),
        }
    }
}

/// The seven message kinds. Wire spelling is UPPERCASE (`"HELLO"`).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "UPPERCASE")]
pub enum MessageKind {
    Hello,
    Ready,
    Event,
    Log,
    Result,
    Ping,
    Pong,
}

/// `HELLO` — controller announces itself (first line it emits).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct HelloPayload {
    pub protocol_version: u32,
    pub controller_version: String,
}

/// Model manifest summary echoed in `READY` (subset of schema-v1 fields
/// the scheduler needs for display and cross-checking).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ManifestInfo {
    pub name: String,
    pub version: String,
    /// `[width, height]` — the manifest's imgsz.
    pub imgsz: [u32; 2],
    pub labels: Vec<String>,
}

/// `READY` — session metadata right after bring-up.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ReadyPayload {
    pub session_id: String,
    /// Skill name when the session runs one.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub skill: Option<String>,
    /// Manifest summary when a model is actually loaded (absent for mock).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub manifest: Option<ManifestInfo>,
    pub backend: String,
}

/// One detection in EVENT payloads. Client-relative pixels: `cx, cy` is the
/// box CENTER in CLIENT coordinates (the coordinate system every consumer
/// downstream of the transform chain shares).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct DetectionMsg {
    pub label: String,
    pub cx: f32,
    pub cy: f32,
    pub w: f32,
    pub h: f32,
    pub conf: f32,
}

/// `EVENT` — semantic state change only (decision D1: per-cycle diagnostics
/// stay in the `--session-log` TSV, they never flood this stream).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct EventPayload {
    pub cycle: u32,
    pub state: String,
    #[serde(default)]
    pub probes_fired: Vec<String>,
    #[serde(default)]
    pub detections: Vec<DetectionMsg>,
    #[serde(default)]
    pub planned_actions: Vec<String>,
}

/// `LOG` — structured side-channel for human-readable warnings (low freq).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum LogLevel {
    Info,
    Warn,
    Error,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct LogPayload {
    pub level: LogLevel,
    pub message: String,
}

/// Terminal outcomes. `timeout`/`cancelled` are NOT controller outcomes —
/// the Go runner owns those semantics (draft §lifecycle).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Outcome {
    Done,
    Failed,
    Stopped,
}

/// `RESULT` — exactly-once terminal line before exit.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ResultPayload {
    pub outcome: Outcome,
    pub state: String,
    pub cycles: u32,
    pub inference_count: u32,
    pub cache_hits: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// Payload variants, one per kind. `Ping`/`Pong` carry `{}`.
#[derive(Debug, Clone, PartialEq)]
pub enum Payload {
    Hello(HelloPayload),
    Ready(ReadyPayload),
    Event(EventPayload),
    Log(LogPayload),
    Result(ResultPayload),
    Ping,
    Pong,
}

impl Payload {
    pub fn kind(&self) -> MessageKind {
        match self {
            Payload::Hello(_) => MessageKind::Hello,
            Payload::Ready(_) => MessageKind::Ready,
            Payload::Event(_) => MessageKind::Event,
            Payload::Log(_) => MessageKind::Log,
            Payload::Result(_) => MessageKind::Result,
            Payload::Ping => MessageKind::Ping,
            Payload::Pong => MessageKind::Pong,
        }
    }
}

/// One wire line, both directions.
#[derive(Debug, Clone, PartialEq)]
pub struct Envelope {
    pub v: u32,
    pub seq: u64,
    /// RFC 3339; supplied by the caller (this crate stays timestamp-free).
    pub ts: String,
    pub payload: Payload,
}

#[derive(Serialize, Deserialize)]
struct RawEnvelope {
    v: u32,
    seq: u64,
    ts: String,
    /// Kind as raw string so an unknown kind produces a precise
    /// [`ProtocolError::UnknownKind`] instead of a generic JSON error.
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    payload: serde_json::Value,
}

/// Wire spelling ↔ kind. Kept explicit so both directions stay obvious.
fn kind_from_wire(s: &str) -> Option<MessageKind> {
    match s {
        "HELLO" => Some(MessageKind::Hello),
        "READY" => Some(MessageKind::Ready),
        "EVENT" => Some(MessageKind::Event),
        "LOG" => Some(MessageKind::Log),
        "RESULT" => Some(MessageKind::Result),
        "PING" => Some(MessageKind::Ping),
        "PONG" => Some(MessageKind::Pong),
        _ => None,
    }
}

fn kind_to_wire(k: MessageKind) -> &'static str {
    match k {
        MessageKind::Hello => "HELLO",
        MessageKind::Ready => "READY",
        MessageKind::Event => "EVENT",
        MessageKind::Log => "LOG",
        MessageKind::Result => "RESULT",
        MessageKind::Ping => "PING",
        MessageKind::Pong => "PONG",
    }
}

impl Envelope {
    pub fn new(seq: u64, ts: impl Into<String>, payload: Payload) -> Envelope {
        Envelope {
            v: PROTOCOL_VERSION,
            seq,
            ts: ts.into(),
            payload,
        }
    }

    /// Serialize to one JSON line (no trailing newline).
    pub fn encode(&self) -> Result<String, ProtocolError> {
        let kind = self.payload.kind();
        let payload = match &self.payload {
            Payload::Hello(p) => serde_json::to_value(p),
            Payload::Ready(p) => serde_json::to_value(p),
            Payload::Event(p) => serde_json::to_value(p),
            Payload::Log(p) => serde_json::to_value(p),
            Payload::Result(p) => serde_json::to_value(p),
            Payload::Ping | Payload::Pong => Ok(serde_json::json!({})),
        }
        .map_err(|e| ProtocolError::BadPayload(e.to_string()))?;
        let raw = RawEnvelope {
            v: self.v,
            seq: self.seq,
            ts: self.ts.clone(),
            kind: kind_to_wire(kind).to_string(),
            payload,
        };
        serde_json::to_string(&raw).map_err(|e| ProtocolError::BadJson(e.to_string()))
    }

    /// Parse one JSON line. Fail-fast on version mismatch or unknown kind;
    /// a payload that does not satisfy its kind's schema is an error (no
    /// best-effort matching).
    pub fn parse(line: &str) -> Result<Envelope, ProtocolError> {
        let raw: RawEnvelope =
            serde_json::from_str(line).map_err(|e| ProtocolError::BadJson(e.to_string()))?;
        if raw.v != PROTOCOL_VERSION {
            return Err(ProtocolError::UnsupportedVersion {
                got: raw.v,
                supported: PROTOCOL_VERSION,
            });
        }
        let kind = kind_from_wire(&raw.kind)
            .ok_or_else(|| ProtocolError::UnknownKind(raw.kind.clone()))?;
        let reject = |e: serde_json::Error| ProtocolError::BadPayload(e.to_string());
        let payload = match kind {
            MessageKind::Hello => {
                Payload::Hello(serde_json::from_value::<HelloPayload>(raw.payload).map_err(reject)?)
            }
            MessageKind::Ready => {
                Payload::Ready(serde_json::from_value::<ReadyPayload>(raw.payload).map_err(reject)?)
            }
            MessageKind::Event => {
                Payload::Event(serde_json::from_value::<EventPayload>(raw.payload).map_err(reject)?)
            }
            MessageKind::Log => {
                Payload::Log(serde_json::from_value::<LogPayload>(raw.payload).map_err(reject)?)
            }
            MessageKind::Result => Payload::Result(
                serde_json::from_value::<ResultPayload>(raw.payload).map_err(reject)?,
            ),
            MessageKind::Ping | MessageKind::Pong => {
                let empty = match &raw.payload {
                    serde_json::Value::Null => true,
                    serde_json::Value::Object(o) => o.is_empty(),
                    _ => false,
                };
                if !empty {
                    return Err(ProtocolError::BadPayload(
                        "PING/PONG payload must be {}".into(),
                    ));
                }
                if kind == MessageKind::Ping {
                    Payload::Ping
                } else {
                    Payload::Pong
                }
            }
        };
        Ok(Envelope {
            v: raw.v,
            seq: raw.seq,
            ts: raw.ts,
            payload,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ts() -> &'static str {
        "2026-09-10T08:00:00.500+08:00"
    }

    /// The exact envelope example from docs/controller-protocol-draft.md —
    /// the doc and the code must never drift apart silently.
    #[test]
    fn doc_example_envelope_parses() {
        let line = concat!(
            r#"{"v": 1, "seq": 42, "ts": "2026-09-10T08:00:00.500+08:00", "type": "EVENT","#,
            r#" "payload": {"cycle": 7, "state": "home/menu_open", "probes_fired": ["daily_banner"],"#,
            r#" "detections": [{"label": "button", "cx": 866.0, "cy": 472.0, "w": 120.0, "h": 40.0, "conf": 0.91}],"#,
            r#" "planned_actions": ["click(button)"]}}"#
        );
        let env = Envelope::parse(line).expect("doc example must parse");
        assert_eq!(env.v, 1);
        assert_eq!(env.seq, 42);
        assert_eq!(env.ts, ts());
        assert_eq!(env.payload.kind(), MessageKind::Event);
        match env.payload {
            Payload::Event(e) => {
                assert_eq!(e.cycle, 7);
                assert_eq!(e.state, "home/menu_open");
                assert_eq!(e.probes_fired, vec!["daily_banner".to_string()]);
                assert_eq!(e.detections.len(), 1);
                assert_eq!(e.detections[0].label, "button");
                assert_eq!(e.planned_actions, vec!["click(button)".to_string()]);
            }
            other => panic!("expected event, got {other:?}"),
        }
    }

    #[test]
    fn roundtrip_all_kinds() {
        let envelopes = vec![
            Envelope::new(
                0,
                ts(),
                Payload::Hello(HelloPayload {
                    protocol_version: 1,
                    controller_version: "native-controller 0.1.0".into(),
                }),
            ),
            Envelope::new(
                1,
                ts(),
                Payload::Ready(ReadyPayload {
                    session_id: "s-1".into(),
                    skill: Some("daily_reward".into()),
                    manifest: Some(ManifestInfo {
                        name: "ui-objects".into(),
                        version: "2026.9.10".into(),
                        imgsz: [640, 640],
                        labels: vec!["button".into(), "dialog".into()],
                    }),
                    backend: "gdi".into(),
                }),
            ),
            Envelope::new(
                2,
                ts(),
                Payload::Event(EventPayload {
                    cycle: 7,
                    state: "home/menu_open".into(),
                    probes_fired: vec!["daily_banner".into()],
                    detections: vec![DetectionMsg {
                        label: "button".into(),
                        cx: 866.0,
                        cy: 472.0,
                        w: 120.0,
                        h: 40.0,
                        conf: 0.91,
                    }],
                    planned_actions: vec!["click(button)".into()],
                }),
            ),
            Envelope::new(
                3,
                ts(),
                Payload::Log(LogPayload {
                    level: LogLevel::Warn,
                    message: "WGC silent, fell back to GDI".into(),
                }),
            ),
            Envelope::new(
                4,
                ts(),
                Payload::Result(ResultPayload {
                    outcome: Outcome::Stopped,
                    state: "daily/claim".into(),
                    cycles: 120,
                    inference_count: 44,
                    cache_hits: 76,
                    error: Some("governor: session exceeded max_session".into()),
                }),
            ),
            Envelope::new(5, ts(), Payload::Ping),
            Envelope::new(6, ts(), Payload::Pong),
        ];
        for env in envelopes {
            let line = env.encode().expect("encode");
            let back = Envelope::parse(&line).expect("parse own output");
            assert_eq!(back, env);
        }
    }

    #[test]
    fn version_mismatch_fails_fast() {
        let mut env = Envelope::new(1, ts(), Payload::Ping);
        env.v = 2; // a hypothetical future peer
        let line = env.encode().expect("encode");
        let err = Envelope::parse(&line).expect_err("v2 must not be parsed by a v1 peer");
        assert!(
            matches!(
                err,
                ProtocolError::UnsupportedVersion {
                    got: 2,
                    supported: 1
                }
            ),
            "{err:?}"
        );
    }

    #[test]
    fn unknown_kind_rejected() {
        let line = r#"{"v":1,"seq":1,"ts":"t","type":"NUKE","payload":{}}"#;
        let err = Envelope::parse(line).expect_err("unknown kind must fail");
        assert!(matches!(err, ProtocolError::UnknownKind(_)), "{err:?}");
    }

    #[test]
    fn kind_payload_mismatch_rejected() {
        // EVENT named on the wire, HELLO-shaped payload inside: `cycle` and
        // `state` are required, so the strict schema rejects it instead of
        // guessing a best-effort match.
        let line = r#"{"v":1,"seq":1,"ts":"t","type":"EVENT","payload":{"protocol_version":1,"controller_version":"x"}}"#;
        let err = Envelope::parse(line).expect_err("mismatched payload must fail");
        assert!(matches!(err, ProtocolError::BadPayload(_)), "{err:?}");

        // Unknown fields INSIDE a v1 payload are tolerated (forward-
        // compatible evolution within v1; see module docs).
        let tolerant = r#"{"v":1,"seq":1,"ts":"t","type":"EVENT","payload":{"cycle":1,"state":"s","future_field":{"a":1}}}"#;
        assert!(
            Envelope::parse(tolerant).is_ok(),
            "unknown v1 fields are tolerated"
        );
    }

    #[test]
    fn ping_rejects_nonempty_payload() {
        let bad = r#"{"v":1,"seq":1,"ts":"t","type":"PING","payload":{"x":1}}"#;
        let err = Envelope::parse(bad).expect_err("PING payload must be {}");
        assert!(matches!(err, ProtocolError::BadPayload(_)), "{err:?}");
        let ok = r#"{"v":1,"seq":1,"ts":"t","type":"PONG","payload":{}}"#;
        assert!(Envelope::parse(ok).is_ok());
    }

    #[test]
    fn result_omits_absent_error_field() {
        let env = Envelope::new(
            9,
            ts(),
            Payload::Result(ResultPayload {
                outcome: Outcome::Done,
                state: "done".into(),
                cycles: 3,
                inference_count: 1,
                cache_hits: 2,
                error: None,
            }),
        );
        let line = env.encode().expect("encode");
        assert!(!line.contains("error"), "absent error must not serialize");
        assert_eq!(Envelope::parse(&line).expect("parse"), env);
    }
}
