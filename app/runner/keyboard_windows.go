//go:build windows

package runner

import windows "ezrokit/runner/platform/windows"

func init() {
	PhysicalKeyDown = windows.WinPhysicalKeyDown
	PollKeyToggle = windows.WinPollKeyToggle
}
