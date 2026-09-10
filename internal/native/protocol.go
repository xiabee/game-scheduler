// Package native runs the Rust native controller as an ordinary child
// process and speaks the NC6 wire protocol to it (stdin/stdout JSON lines,
// schema frozen by controller/src/protocol.rs — the Rust module is the
// normative schema, this file is its Go mirror and the tests pin both
// together via the same fixtures).
//
// Boundaries (ROADMAP §7/§8): the controller is treated as an opaque local
// process exactly like the external adapters — no injection, no memory
// access, no packet manipulation.
package native

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the only wire version this build speaks. A peer
// announcing a different version is a fail-fast error, never a guess.
const ProtocolVersion = 1

// MessageKind is the wire `type` spelling (UPPERCASE, matching the Rust
// MessageKind serde rename).
type MessageKind string

const (
	KindHello  MessageKind = "HELLO"
	KindReady  MessageKind = "READY"
	KindEvent  MessageKind = "EVENT"
	KindLog    MessageKind = "LOG"
	KindResult MessageKind = "RESULT"
	KindPing   MessageKind = "PING"
	KindPong   MessageKind = "PONG"
)

// ParseError distinguishes the failure modes the protocol calls out.
type ParseError struct {
	Kind    string // "json" | "version" | "kind" | "payload"
	Line    string // the offending line, truncated for logs
	Detail  string
	Version int // set for kind="version"
	Want    int // set for kind="version"
}

func (e *ParseError) Error() string {
	switch e.Kind {
	case "version":
		return fmt.Sprintf("native: protocol version %d unsupported (this peer speaks v%d)", e.Version, e.Want)
	default:
		return fmt.Sprintf("native: protocol %s error: %s", e.Kind, e.Detail)
	}
}

// Envelope is one wire line, both directions.
type Envelope struct {
	V       int             `json:"v"`
	Seq     uint64          `json:"seq"`
	TS      string          `json:"ts"`
	Type    MessageKind     `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// Typed payloads — field names and JSON tags mirror protocol.rs exactly
// (snake_case wire spelling via struct tags).
type HelloPayload struct {
	ProtocolVersion   int    `json:"protocol_version"`
	ControllerVersion string `json:"controller_version"`
}

type ManifestInfo struct {
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Imgsz   [2]uint32 `json:"imgsz"`
	Labels  []string  `json:"labels"`
}

type ReadyPayload struct {
	SessionID string        `json:"session_id"`
	Skill     *string       `json:"skill"`
	Manifest  *ManifestInfo `json:"manifest"`
	Backend   string        `json:"backend"`
}

type DetectionMsg struct {
	Label string  `json:"label"`
	CX    float32 `json:"cx"`
	CY    float32 `json:"cy"`
	W     float32 `json:"w"`
	H     float32 `json:"h"`
	Conf  float32 `json:"conf"`
}

type EventPayload struct {
	Cycle          uint32         `json:"cycle"`
	State          string         `json:"state"`
	ProbesFired    []string       `json:"probes_fired"`
	Detections     []DetectionMsg `json:"detections"`
	PlannedActions []string       `json:"planned_actions"`
}

type LogLevel string

const (
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

type LogPayload struct {
	Level   LogLevel `json:"level"`
	Message string   `json:"message"`
}

type Outcome string

const (
	OutcomeDone    Outcome = "done"
	OutcomeFailed  Outcome = "failed"
	OutcomeStopped Outcome = "stopped"
)

type ResultPayload struct {
	Outcome        Outcome `json:"outcome"`
	State          string  `json:"state"`
	Cycles         uint32  `json:"cycles"`
	InferenceCount uint32  `json:"inference_count"`
	CacheHits      uint32  `json:"cache_hits"`
	Error          *string `json:"error"`
}

// Message is a parsed envelope with the payload decoded against the
// schema the kind names. Exactly one payload field is non-nil (PING/PONG
// carry no payload object).
type Message struct {
	Env    Envelope
	Hello  *HelloPayload
	Ready  *ReadyPayload
	Event  *EventPayload
	Log    *LogPayload
	Result *ResultPayload
	Ping   bool
	Pong   bool
}

func parseError(kind, line, detail string) *ParseError {
	const maxLine = 200
	if len(line) > maxLine {
		line = line[:maxLine] + "..."
	}
	return &ParseError{Kind: kind, Line: line, Detail: detail}
}

// ParseEnvelope validates one wire line: JSON shape, protocol version
// (fail-fast), known kind, and payload presence for its kind. The payload
// is NOT decoded here — use (*Message).decode or Decode for the typed form.
func ParseEnvelope(line []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return Envelope{}, parseError("json", string(line), err.Error())
	}
	if env.V != ProtocolVersion {
		return Envelope{}, &ParseError{Kind: "version", Version: env.V, Want: ProtocolVersion,
			Line: string(line)}
	}
	switch env.Type {
	case KindHello, KindReady, KindEvent, KindLog, KindResult:
		if len(env.Payload) == 0 {
			return Envelope{}, parseError("payload", string(line),
				fmt.Sprintf("%s requires a payload object", env.Type))
		}
	case KindPing, KindPong:
		// payload may be {} or absent
	default:
		return Envelope{}, parseError("kind", string(line),
			fmt.Sprintf("unknown message kind %q", string(env.Type)))
	}
	return env, nil
}

// Decode turns a validated envelope into a Message with the payload
// decoded against its kind's schema. Unknown fields inside a v1 payload
// are tolerated (forward-compatible evolution, matching the Rust side);
// REQUIRED fields and enum values are enforced so a buggy/future peer
// cannot silently hand us a half-valid payload (parity with the strict
// serde decode in protocol.rs).
func Decode(env Envelope) (*Message, error) {
	msg := &Message{Env: env}
	decode := func(dst any) error {
		if err := json.Unmarshal(env.Payload, dst); err != nil {
			return parseError("payload", string(env.Payload), err.Error())
		}
		return nil
	}
	require := func(fields ...string) error {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(env.Payload, &m); err != nil {
			return parseError("payload", string(env.Payload), err.Error())
		}
		for _, f := range fields {
			if _, ok := m[f]; !ok {
				return parseError("payload", string(env.Payload), f+" is required")
			}
		}
		return nil
	}
	switch env.Type {
	case KindHello:
		if err := require("protocol_version", "controller_version"); err != nil {
			return nil, err
		}
		msg.Hello = &HelloPayload{}
		if err := decode(msg.Hello); err != nil {
			return nil, err
		}
	case KindReady:
		if err := require("session_id", "backend"); err != nil {
			return nil, err
		}
		msg.Ready = &ReadyPayload{}
		if err := decode(msg.Ready); err != nil {
			return nil, err
		}
	case KindEvent:
		if err := require("cycle", "state"); err != nil {
			return nil, err
		}
		msg.Event = &EventPayload{}
		if err := decode(msg.Event); err != nil {
			return nil, err
		}
	case KindLog:
		if err := require("level", "message"); err != nil {
			return nil, err
		}
		msg.Log = &LogPayload{}
		if err := decode(msg.Log); err != nil {
			return nil, err
		}
		switch msg.Log.Level {
		case LogInfo, LogWarn, LogError:
		default:
			return nil, parseError("payload", string(env.Payload),
				fmt.Sprintf("unknown log level %q", string(msg.Log.Level)))
		}
	case KindResult:
		if err := require("outcome"); err != nil {
			return nil, err
		}
		msg.Result = &ResultPayload{}
		if err := decode(msg.Result); err != nil {
			return nil, err
		}
		switch msg.Result.Outcome {
		case OutcomeDone, OutcomeFailed, OutcomeStopped:
		default:
			return nil, parseError("payload", string(env.Payload),
				fmt.Sprintf("unknown outcome %q", string(msg.Result.Outcome)))
		}
	case KindPing:
		msg.Ping = true
	case KindPong:
		msg.Pong = true
	default:
		return nil, parseError("kind", string(env.Type),
			fmt.Sprintf("unknown message kind %q", string(env.Type)))
	}
	return msg, nil
}

// ParseLine = ParseEnvelope + Decode for one wire line.
func ParseLine(line []byte) (*Message, error) {
	env, err := ParseEnvelope(line)
	if err != nil {
		return nil, err
	}
	return Decode(env)
}
