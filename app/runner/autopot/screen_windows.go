//go:build windows

package autopot

import (
	"image"

	windows "ezrokit/runner/platform/windows"
)

func init() {
	ScreenSize = windows.ScreenSize
	CaptureScreenRegion = func(roi Rect) (*image.RGBA, error) {
		return windows.CaptureScreenRegion(windows.ScreenROI{
			X: roi.X, Y: roi.Y, W: roi.W, H: roi.H,
		})
	}
	CaptureFullScreen = windows.CaptureFullScreen
}
