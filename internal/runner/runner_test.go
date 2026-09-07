package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is not a real test; it is re-executed as a child process by
// the tests below so we have a cross-platform program with controllable
// behaviour (exit code, output, sleep).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GS_WANT_HELPER") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	switch mode {
	case "echo":
		os.Stdout.WriteString("hello-stdout")
		os.Stderr.WriteString("hello-stderr")
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "sleep":
		d, _ := time.ParseDuration(args[1])
		time.Sleep(d)
	case "spawn_exit", "spawn_hold":
		// Spawn a long-lived grandchild, report its PID on stdout, then either
		// exit right away (leaving the grandchild orphaned: the dead-root case
		// for killProcessTree) or hold so the tree stays alive.
		var gc *exec.Cmd
		if runtime.GOOS == "windows" {
			gc = exec.Command("cmd", "/C", "ping", "-n", "15", "127.0.0.1")
		} else {
			gc = exec.Command("sh", "-c", "sleep 15")
		}
		if err := gc.Start(); err != nil {
			os.Exit(3)
		}
		fmt.Fprintf(os.Stdout, "GCPID=%d\n", gc.Process.Pid)
		if mode == "spawn_exit" {
			os.Exit(0)
		}
		time.Sleep(60 * time.Second)
	}
	os.Exit(0)
}

func helperSpec(mode string, extra ...string) Spec {
	args := append([]string{"-test.run=TestHelperProcess", "--", mode}, extra...)
	return Spec{Path: os.Args[0], Args: args, Env: []string{"GS_WANT_HELPER=1"}}
}

func TestRunSuccessCapturesOutput(t *testing.T) {
	res := Run(context.Background(), helperSpec("echo"))
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hello-stdout") {
		t.Errorf("stdout=%q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "hello-stderr") {
		t.Errorf("stderr=%q", res.Stderr)
	}
	if res.EndTime.Before(res.StartTime) {
		t.Error("end before start")
	}
}

func TestRunNonZeroExit(t *testing.T) {
	res := Run(context.Background(), helperSpec("exit", "7"))
	if res.Err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit=%d want 7", res.ExitCode)
	}
}

func TestRunTimeout(t *testing.T) {
	// Long sleep + short timeout: a working timeout returns almost immediately,
	// while a broken one would only return near the 30s natural completion. The
	// generous 10s margin keeps the test from flaking on slow/contended CI.
	spec := helperSpec("sleep", "30s")
	spec.Timeout = 200 * time.Millisecond
	start := time.Now()
	res := Run(context.Background(), spec)
	if !res.TimedOut {
		t.Fatalf("expected timeout, err=%v", res.Err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("timeout did not kill the process promptly: took %s", time.Since(start))
	}
}

func TestRunContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(150 * time.Millisecond); cancel() }()
	res := Run(ctx, helperSpec("sleep", "5s"))
	if res.Err == nil {
		t.Fatal("expected error after cancel")
	}
}

func TestRunEmptyPath(t *testing.T) {
	res := Run(context.Background(), Spec{})
	if res.Err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestCappedBuffer(t *testing.T) {
	var c cappedBuffer
	c.limit = 8
	c.Write([]byte("123456"))
	c.Write([]byte("ABCDEF"))
	got := c.String()
	if len(got) != 8 {
		t.Fatalf("len=%d want 8 (%q)", len(got), got)
	}
	if !strings.HasSuffix(got, "ABCDEF") {
		t.Errorf("want tail retained, got %q", got)
	}
}

func TestCommandLineQuoting(t *testing.T) {
	s := Spec{Path: "tool.exe", Args: []string{"--name", "has space"}}
	if got := s.CommandLine(); !strings.Contains(got, `"has space"`) {
		t.Errorf("CommandLine=%q", got)
	}
}
