//go:build windows

package runner

import (
	windows "ezrokit/runner/platform/windows"
)

func init() {
	PhysicalKeyDown = windows.AsyncKeyDown
	EmergencyKeyDown = windows.AsyncKeyDown
	PollKeyToggle = windows.PollKeyToggle
}
