//go:build windows

package runner

import (
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

func PollKeyToggle(wasDown *bool, vk int32) bool {
	down := PhysicalKeyDown(vk)
	toggled := down && !*wasDown
	*wasDown = down
	return toggled
}

// WinPollKeyToggle is exported for the runner package's platform wiring.
var WinPollKeyToggle = PollKeyToggle
