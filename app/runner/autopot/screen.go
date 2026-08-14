package autopot

import (
	"fmt"
	"image"
)

// screenCapturer is the narrow platform boundary used by visual readers.
// Keeping capture behind this interface makes reader behavior deterministic in
// tests without coupling the readers to Windows APIs.
type screenCapturer interface {
	ScreenSize() (int, int)
	CaptureScreenRegion(roi Rect) (*image.RGBA, error)
	CaptureFullScreen() (*image.RGBA, error)
}

type functionScreenCapturer struct {
	size   func() (int, int)
	region func(Rect) (*image.RGBA, error)
	full   func() (*image.RGBA, error)
}

func (c functionScreenCapturer) ScreenSize() (int, int) { return c.size() }
func (c functionScreenCapturer) CaptureScreenRegion(roi Rect) (*image.RGBA, error) {
	return c.region(roi)
}
func (c functionScreenCapturer) CaptureFullScreen() (*image.RGBA, error) { return c.full() }

// defaultScreenCapturer snapshots the current platform hooks. Tests that
// replace the hooks continue to work, while production readers receive the
// platform implementation selected by screen_windows.go.
func defaultScreenCapturer() screenCapturer {
	return functionScreenCapturer{
		size:   ScreenSize,
		region: CaptureScreenRegion,
		full:   CaptureFullScreen,
	}
}

// ScreenSize returns the primary monitor dimensions (width, height).
// Defaults to 0,0 with an error; the real implementation is wired via
// init() in screen_windows.go.
var ScreenSize = func() (int, int) {
	return 0, 0
}

// CaptureScreenRegion captures the given screen rectangle into an RGBA image.
// Defaults to nil with an error; the real implementation is wired via
// init() in screen_windows.go.
var CaptureScreenRegion = func(roi Rect) (*image.RGBA, error) {
	return nil, fmt.Errorf("CaptureScreenRegion: not available on this platform")
}

// CaptureFullScreen captures the entire primary monitor into an RGBA image.
// Defaults to nil with an error; the real implementation is wired via
// init() in screen_windows.go.
var CaptureFullScreen = func() (*image.RGBA, error) {
	return nil, fmt.Errorf("CaptureFullScreen: not available on this platform")
}
