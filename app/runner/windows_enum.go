//go:build windows

package runner

import windows "ezrokit/runner/platform/windows"

// VisibleWindow is a top-level window with a non-empty title.
type VisibleWindow = windows.VisibleWindow

// ListVisibleWindows returns visible top-level windows with non-empty titles,
// sorted by title.
func ListVisibleWindows() []VisibleWindow {
	return windows.ListVisibleWindows()
}
