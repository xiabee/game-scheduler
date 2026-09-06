package vision

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePNG renders a solid w×h image and saves it; used by the fake run below.
func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 64, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestCommandFrameSourceCapture(t *testing.T) {
	dir := t.TempDir()
	s := NewCommandFrameSource(`snip --out "{{.Path}}"`)
	var gotLine string
	s.run = func(line string) error {
		gotLine = line
		start := strings.Index(line, `"`) + 1
		end := strings.LastIndex(line, `"`)
		writePNG(t, line[start:end], 320, 240)
		return nil
	}
	frame, err := s.Capture()
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if frame.Width != 320 || frame.Height != 240 {
		t.Fatalf("frame=%dx%d", frame.Width, frame.Height)
	}
	if frame.Image == nil || frame.Captured == 0 {
		t.Fatalf("frame incomplete: %+v", frame)
	}
	if !strings.Contains(gotLine, dir) && !strings.Contains(gotLine, filepath.ToSlash(dir)) && !strings.Contains(gotLine, "vision_") {
		// The template must have received the path inside our dir.
		t.Fatalf("command line did not reference the frame path: %q", gotLine)
	}
}

func TestCommandFrameSourceCommandFailure(t *testing.T) {
	s := NewCommandFrameSource(`snip "{{.Path}}"`)
	s.run = func(line string) error { return errors.New("boom: screen locked") }
	if _, err := s.Capture(); err == nil || !strings.Contains(err.Error(), "screen locked") {
		t.Fatalf("want command error surfaced, got %v", err)
	}
}

func TestCommandFrameSourceNoFile(t *testing.T) {
	s := NewCommandFrameSource(`snip "{{.Path}}"`)
	s.run = func(line string) error { return nil } // writes nothing
	if _, err := s.Capture(); err == nil {
		t.Fatal("want error when no screenshot file is produced")
	}
}

func TestCommandFrameSourceBadTemplate(t *testing.T) {
	s := NewCommandFrameSource(`snip "{{.Path`)
	if _, err := s.Capture(); err == nil {
		t.Fatal("want template error")
	}
}

// Compile-time reference implementations showing how a future backend (or a
// test double) plugs into the interfaces.

type fakeDetector struct{}

func (fakeDetector) Detect(f Frame, confidence float64) ([]Detection, error) {
	return []Detection{{Label: "button_start", Confidence: 0.91, Region: Region{10, 10, 80, 30}}}, nil
}

type fakeMatcher struct{}

func (fakeMatcher) Find(f Frame, template image.Image, threshold float64) (Region, float64, error) {
	return Region{0, 0, 10, 10}, 0.95, nil
}

type fakeOCR struct{}

func (fakeOCR) Recognize(f Frame, region *Region) ([]TextResult, error) {
	return []TextResult{{Text: "168", Confidence: 0.99, Region: Region{0, 0, 40, 16}}}, nil
}

func TestInterfacesAcceptReferenceFakes(t *testing.T) {
	var _ Detector = fakeDetector{}
	var _ Matcher = fakeMatcher{}
	var _ OCR = fakeOCR{}
	var _ FrameSource = &CommandFrameSource{}
}
