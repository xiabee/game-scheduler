// Package notify fires an operator-configured shell command to deliver alerts
// (resource overload, task failure) so an operator is notified even when not
// watching the dashboard. The command is a template; it could pop a Windows
// toast, curl a webhook, hit Bark/ServerChan, etc. It is best-effort and never
// blocks or fails the caller.
package notify

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/xiabee/game-scheduler/internal/shellcmd"
)

// Notifier renders and runs the configured notify command.
type Notifier struct {
	tmpl string
	log  *slog.Logger
	run  func(line string) error // overridable for tests
}

// New builds a Notifier. An empty cmd makes Send a no-op.
func New(cmd string, log *slog.Logger) *Notifier {
	if log == nil {
		log = slog.Default()
	}
	n := &Notifier{tmpl: cmd, log: log}
	n.run = n.shellRun
	return n
}

// Send renders the template with the event/title/message and runs it. The three
// values are sanitized of shell metacharacters first so dynamic text (error
// messages, etc.) cannot break out of or inject into the command line.
func (n *Notifier) Send(event, title, message string) {
	if n == nil || strings.TrimSpace(n.tmpl) == "" {
		return
	}
	line, err := render(n.tmpl, map[string]string{
		"Event":   sanitize(event),
		"Title":   sanitize(title),
		"Message": sanitize(message),
	})
	if err != nil {
		n.log.Warn("notify template", "err", err)
		return
	}
	if err := n.run(line); err != nil {
		n.log.Warn("notify command failed", "event", event, "err", err)
	}
}

func (n *Notifier) shellRun(line string) error {
	cmd := shellcmd.Command(line)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return fmt.Errorf("%w: %s", err, s)
		}
		return err
	}
	return nil
}

// sanitize removes characters that could let a value break out of the shell
// command line. Alerts are short human-readable strings, so dropping shell
// metacharacters is harmless.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"', '`', '$', '&', '|', '<', '>', '^', '%', '\\', ';', '\r', '\n', '(', ')', '{', '}', '!':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func render(tmpl string, data any) (string, error) {
	t, err := template.New("notify").Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
