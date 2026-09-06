package vision

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // register decoders for whatever the screenshot command writes
	_ "image/png"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/xiabee/game-scheduler/internal/shellcmd"
)

// CommandFrameSource captures frames by running an operator-configured shell
// command — the same mechanism the failure-screenshot feature uses, so a setup
// that already captures failure shots can share it. The command template gets
// {{.Path}}: the file where the command must write the screenshot (PNG/JPG).
//
// The command runs synchronously; keep it fast. This is plain local
// screenshotting — it never reads game memory and never injects anything.
type CommandFrameSource struct {
	Cmd string
	Dir string // where frames are written; empty means os.TempDir()

	run func(line string) error // overridable for tests
}

// NewCommandFrameSource builds a source from a command template.
func NewCommandFrameSource(cmd string) *CommandFrameSource {
	s := &CommandFrameSource{Cmd: cmd}
	s.run = func(line string) error {
		var stderr bytes.Buffer
		cmd := shellcmd.Command(line)
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
				return fmt.Errorf("%w: %s", err, msg)
			}
			return err
		}
		return nil
	}
	return s
}

// Capture runs the screenshot command and decodes the produced image.
func (s *CommandFrameSource) Capture() (Frame, error) {
	dir := s.Dir
	if dir == "" {
		dir = os.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("vision_%d.png", time.Now().UnixNano()))
	rendered, err := renderPath(s.Cmd, path)
	if err != nil {
		return Frame{}, fmt.Errorf("vision: command template: %w", err)
	}
	if err := s.run(rendered); err != nil {
		return Frame{}, fmt.Errorf("vision: screenshot command: %w", err)
	}
	f, err := os.ReadFile(path)
	_ = os.Remove(path) // best-effort cleanup; decode failure reports the read error below
	if err != nil {
		return Frame{}, fmt.Errorf("vision: screenshot command wrote no readable file %s: %w", path, err)
	}
	img, _, err := image.Decode(bytes.NewReader(f))
	if err != nil {
		return Frame{}, fmt.Errorf("vision: decode screenshot: %w", err)
	}
	b := img.Bounds()
	return Frame{
		Image:    img,
		Width:    b.Dx(),
		Height:   b.Dy(),
		Captured: time.Now().UnixMilli(),
		Source:   path,
	}, nil
}

func renderPath(tpl, path string) (string, error) {
	t, err := template.New("vision-frame").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, map[string]string{"Path": path}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
