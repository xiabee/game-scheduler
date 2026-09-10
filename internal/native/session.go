package native

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/xiabee/game-scheduler/internal/runner"
)

// SessionConfig describes one native-controller invocation.
type SessionConfig struct {
	// ControllerPath is the controller executable (absolute or
	// PATH-resolvable). Args carry the skill/probe/mode flags exactly as a
	// human would pass them — the protocol has no hidden control channel.
	ControllerPath string
	Args           []string
	Dir            string
	// Timeout bounds the whole session; 0 means unbounded (explicit ctx
	// cancellation still works).
	Timeout time.Duration
}

// SessionOutcome classifies how a session ended. done|failed|stopped come
// from the controller's RESULT line; cancelled|timeout|error are decided
// by the Go side (draft §lifecycle: process death is the cancel path and
// non-zero exit without RESULT is a process fault).
type SessionOutcome string

const (
	SessionDone      SessionOutcome = "done"
	SessionFailed    SessionOutcome = "failed"
	SessionStopped   SessionOutcome = "stopped"
	SessionCancelled SessionOutcome = "cancelled"
	SessionTimeout   SessionOutcome = "timeout"
	SessionError     SessionOutcome = "error"
)

// SessionResult is the terminal record for one session.
type SessionResult struct {
	Outcome  SessionOutcome
	State    string // controller state at RESULT (or last EVENT), best-effort
	Cycles   uint32
	ExitCode int // -1 when the process never started
	Err      error
}

// CommandLine renders the invocation for logging/storage — informational
// only, never re-parsed. Same quoting rules as runner.Spec.CommandLine.
func (c SessionConfig) CommandLine() string {
	parts := append([]string{c.ControllerPath}, c.Args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\"") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

// EventSink receives EVENT messages as they arrive, on the session's
// dispatch goroutine, in wire order. Keep handlers quick or forward to a
// channel — a slow sink delays the session loop.
type EventSink func(msg *Message)

// maxStderrKeep bounds how much stderr is retained for failure reports.
const maxStderrKeep = 8192

// eventQueueSize bounds the EVENT backlog between the reader goroutine and
// the dispatcher; a full queue makes the reader wait (backpressure), which
// is the correct failure mode — a slow consumer must slow the session, not
// silently drop state changes.
const eventQueueSize = 64

// RunSession spawns the controller, consumes the protocol stream, and
// blocks until RESULT + exit, cancellation, or timeout. Verdict semantics
// per the frozen draft:
//   - RESULT done|failed|stopped → matching outcome (exit 0 expected;
//     a non-zero exit with RESULT=done is a process fault and wins).
//   - ctx cancelled / process killed → cancelled (RESULT may be missing).
//   - DeadlineExceeded → timeout.
//   - protocol violation, or exit without RESULT → error.
func RunSession(ctx context.Context, cfg SessionConfig, onEvent EventSink) SessionResult {
	res := SessionResult{ExitCode: -1}
	if strings.TrimSpace(cfg.ControllerPath) == "" {
		res.Outcome = SessionError
		res.Err = fmt.Errorf("native: empty controller path")
		return res
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if cfg.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
	} else {
		// always derivable so the fail-fast path can kill the child even
		// without a configured timeout
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, cfg.ControllerPath, cfg.Args...)
	cmd.Dir = cfg.Dir
	// Same cancel semantics as external adapters: the whole process tree
	// dies, and a lingering grandchild cannot hold the pipes open forever.
	cmd.Cancel = func() error { return runner.KillProcessTree(cmd.Process) }
	cmd.WaitDelay = 5 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		res.Outcome = SessionError
		res.Err = fmt.Errorf("native: stdout pipe: %w", err)
		return res
	}
	var stderr syncBuffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		res.Outcome = SessionError
		res.Err = fmt.Errorf("native: start: %w", err)
		return res
	}
	if release, jerr := runner.AssignJob(cmd.Process); jerr == nil {
		defer release()
	}

	// Reader: every stdout line is one protocol envelope. A protocol
	// violation fails fast — the session must never guess past a line it
	// cannot parse.
	events := make(chan *Message, eventQueueSize)
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1 MiB line cap
		var lastErr error
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 {
				continue
			}
			msg, perr := ParseLine(line)
			if perr != nil {
				lastErr = perr
				break
			}
			switch {
			case msg.Hello != nil && msg.Hello.ProtocolVersion != ProtocolVersion:
				lastErr = &ParseError{Kind: "version",
					Version: msg.Hello.ProtocolVersion, Want: ProtocolVersion}
			case msg.Result != nil:
				res.State = msg.Result.State
				res.Cycles = msg.Result.Cycles
				if res.Outcome == "" {
					res.Outcome = outcomeFromPayload(msg.Result)
				}
				// RESULT is the in-band terminal line (draft §lifecycle):
				// stop parsing. Anything after it is protocol noise and is
				// drained, not interpreted.
				lastErr = io.EOF // sentinel: reuse the clean-exit drain path
			case msg.Event != nil:
				res.State = msg.Event.State
				res.Cycles = msg.Event.Cycle
				if onEvent != nil {
					events <- msg
				}
			}
			if lastErr != nil {
				break
			}
		}
		// Keep draining so the child's writes never block on a full pipe
		// after we stop parsing (cmd.Wait's WaitDelay is the hard bound).
		go func() { _, _ = io.Copy(io.Discard, stdout) }()
		readDone <- lastErr
		close(events)
	}()

	// Dispatch queued events on this goroutine (wire order preserved).
	for ev := range events {
		onEvent(ev)
	}
	readErr := <-readDone
	if errors.Is(readErr, io.EOF) {
		readErr = nil // RESULT-latched sentinel: clean in-band end
	}
	if readErr != nil && cancel != nil {
		// The peer violated the protocol — stop trusting it and kill the
		// tree now instead of waiting out its natural lifetime.
		cancel()
	}
	waitErr := cmd.Wait()
	res.ExitCode = exitCodeOf(waitErr)

	switch {
	case readErr != nil:
		res.Outcome = SessionError
		res.Err = readErr
	case res.Outcome == "":
		// No RESULT latched — the session ended out of band.
		switch {
		case runCtx.Err() == context.DeadlineExceeded:
			res.Outcome = SessionTimeout
			res.Err = fmt.Errorf("native: timed out after %s", cfg.Timeout)
		case ctx.Err() != nil || runCtx.Err() != nil:
			res.Outcome = SessionCancelled
		case res.ExitCode != 0:
			res.Outcome = SessionError
			res.Err = fmt.Errorf("native: exit code %d without RESULT; stderr tail: %s",
				res.ExitCode, stderr.tail())
		default:
			res.Outcome = SessionError
			res.Err = fmt.Errorf("native: clean exit without RESULT; stderr tail: %s",
				stderr.tail())
		}
	case res.Outcome == SessionDone && res.ExitCode != 0:
		res.Outcome = SessionError
		res.Err = fmt.Errorf("native: RESULT done but exit code %d", res.ExitCode)
	}
	return res
}

func outcomeFromPayload(r *ResultPayload) SessionOutcome {
	switch r.Outcome {
	case OutcomeDone:
		return SessionDone
	case OutcomeFailed:
		return SessionFailed
	case OutcomeStopped:
		return SessionStopped
	default:
		return SessionError
	}
}

func exitCodeOf(waitErr error) int {
	if waitErr == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// syncBuffer keeps the tail of stderr for failure reports.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxStderrKeep {
		b.buf = b.buf[len(b.buf)-maxStderrKeep:]
	}
	return len(p), nil
}

func (b *syncBuffer) tail() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.TrimSpace(string(b.buf))
	if len(s) > 512 {
		s = s[len(s)-512:]
	}
	return s
}
