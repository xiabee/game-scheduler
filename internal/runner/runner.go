// Package runner executes external automation tools as ordinary child
// processes and captures their output. It contains no game-specific logic and
// performs no injection, memory access, packet manipulation or anti-detection
// behaviour — it only spawns an executable, waits for it, and records the
// result. This boundary is intentional: every supported tool is treated as an
// opaque local process.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Spec fully describes how to launch one external tool invocation.
type Spec struct {
	Path    string        // absolute or PATH-resolvable executable
	Args    []string      // command-line arguments
	Dir     string        // working directory (optional)
	Env     []string      // extra environment, appended to os.Environ()
	Timeout time.Duration // 0 means no timeout
}

// CommandLine renders the spec for logging/storage. It is informational only
// and is not re-parsed.
func (s Spec) CommandLine() string {
	parts := append([]string{s.Path}, s.Args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\"") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

// Result is the outcome of running a Spec.
type Result struct {
	Command   string
	Stdout    string
	Stderr    string
	ExitCode  int // -1 if the process never started or was killed by signal
	Err       error
	StartTime time.Time
	EndTime   time.Time
	TimedOut  bool
}

// maxCapture bounds how much stdout/stderr is retained per stream so a chatty
// tool cannot exhaust memory. The tail is kept because errors usually surface
// at the end of output.
const maxCapture = 1 << 20 // 1 MiB

// Run launches the process described by spec, waits for it to exit (or for the
// timeout / ctx cancellation), and returns a populated Result. A non-zero exit
// code is reported via Result.ExitCode and Result.Err; the error is never
// silently dropped.
func Run(ctx context.Context, spec Spec) Result {
	res := Result{Command: spec.CommandLine(), ExitCode: -1, StartTime: time.Now()}

	if strings.TrimSpace(spec.Path) == "" {
		res.EndTime = time.Now()
		res.Err = errors.New("runner: empty executable path")
		return res
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if spec.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(cmd.Environ(), spec.Env...)
	}

	var stdout, stderr cappedBuffer
	stdout.limit = maxCapture
	stderr.limit = maxCapture
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	res.EndTime = time.Now()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()

	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.Err = fmt.Errorf("runner: timed out after %s", spec.Timeout)
		return res
	}

	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			res.Err = fmt.Errorf("runner: exit code %d", res.ExitCode)
		} else {
			res.Err = fmt.Errorf("runner: failed to start: %w", err)
		}
		return res
	}

	res.ExitCode = 0
	return res
}

// cappedBuffer keeps only the last `limit` bytes written to it.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if c.limit > 0 && len(p) > c.limit {
		p = p[len(p)-c.limit:]
		c.buf.Reset()
	}
	c.buf.Write(p)
	if c.limit > 0 && c.buf.Len() > c.limit {
		over := c.buf.Len() - c.limit
		c.buf.Next(over)
	}
	return n, nil
}

func (c *cappedBuffer) String() string { return c.buf.String() }
