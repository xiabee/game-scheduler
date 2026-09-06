// Package vision defines the future screenshot-assist interfaces: object
// detection (YOLO-style), template matching and OCR over plain frames.
//
// This package is deliberately minimal and dependency-free. It carries data
// types and interfaces only — no model weights are downloaded, no GPU code
// exists, and nothing here runs inference. Concrete backends (an ONNX YOLO
// runtime, a Tesseract binding, a pure-Go template matcher) would implement
// these interfaces behind build tags or separate modules; the scheduler does
// not link any of them today.
//
// Everything is decoupled from the task runner by design: a Frame comes from an
// operator-configured screenshot command or a file, never from game memory,
// injection or packet capture.
package vision

import "image"

// Frame is one captured screen image plus metadata.
type Frame struct {
	Image    image.Image
	Width    int
	Height   int
	Captured int64 // unix millis
	Source   string
}

// Region is a rectangle in frame pixel coordinates (top-left origin).
type Region struct {
	X, Y, W, H int
}

// Detection is one object found by a Detector.
type Detection struct {
	Label      string
	Confidence float64 // 0..1
	Region     Region
}

// TextResult is one text fragment found by an OCR engine.
type TextResult struct {
	Text       string
	Confidence float64 // 0..1
	Region     Region
}

// FrameSource produces frames on demand.
type FrameSource interface {
	// Capture grabs one fresh frame. Implementations must be safe for
	// sequential use; concurrency requirements are implementation-specific and
	// should be documented.
	Capture() (Frame, error)
}

// Detector finds labelled objects (buttons, icons, resource nodes, UI states)
// in a frame — the interface a YOLO-style backend would implement.
type Detector interface {
	// Detect returns detections at or above confidence (0..1).
	Detect(f Frame, confidence float64) ([]Detection, error)
}

// Matcher locates a known static UI template inside a frame. Good for stable
// fixed UI chrome; use Detector for anything appearance-variable.
type Matcher interface {
	// Find returns the best match location whose similarity is at least
	// threshold (0..1), or a zero Region and nil error when nothing matches.
	Find(f Frame, template image.Image, threshold float64) (Region, float64, error)
}

// OCR reads text (numbers, material counts, character names, popup lines).
type OCR interface {
	// Recognize returns the text fragments found in the frame (or in the given
	// sub-region when it is non-nil).
	Recognize(f Frame, region *Region) ([]TextResult, error)
}
