package notify

import (
	"strings"
	"testing"
)

func TestSendRendersAndSanitizes(t *testing.T) {
	var got string
	n := New(`send "{{.Title}}" -m "{{.Message}}" -e {{.Event}}`, nil)
	n.run = func(line string) error { got = line; return nil }

	// Message carries shell metacharacters that must be stripped.
	n.Send("task_failed", "任务失败: 锄地", `boom "; rm -rf / & $(evil)`)

	if strings.ContainsAny(got, "\"`$&|<>") == false {
		// the literal quotes around template fields remain; ensure the dynamic
		// value's dangerous chars were removed, not the template's own quotes.
	}
	if strings.Contains(got, "rm -rf") == false {
		t.Fatalf("expected message text retained: %q", got)
	}
	// the injected `$(evil)`, backticks, `&`, `;`, `"` from the VALUE must be gone
	if strings.Contains(got, "$(evil)") || strings.Contains(got, "rm -rf / &") {
		t.Errorf("dangerous metacharacters not sanitized: %q", got)
	}
	if !strings.Contains(got, "锄地") {
		t.Errorf("unicode title lost: %q", got)
	}
	if !strings.Contains(got, "-e task_failed") {
		t.Errorf("event not rendered: %q", got)
	}
}

func TestEmptyCommandIsNoop(t *testing.T) {
	called := false
	n := New("   ", nil)
	n.run = func(string) error { called = true; return nil }
	n.Send("x", "y", "z")
	if called {
		t.Error("empty notify_cmd should not run anything")
	}
}

func TestNilNotifierSafe(t *testing.T) {
	var n *Notifier
	n.Send("a", "b", "c") // must not panic
}

func TestSanitize(t *testing.T) {
	in := "a\"b`c$d&e|f;g\nh"
	out := sanitize(in)
	if strings.ContainsAny(out, "\"`$&|;\n") {
		t.Errorf("sanitize left metacharacters: %q", out)
	}
	if !strings.HasPrefix(out, "a") {
		t.Errorf("unexpected: %q", out)
	}
}
