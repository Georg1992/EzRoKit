//go:build windows

package runner

import (
	"context"

	windows "ezrokit/runner/platform/windows"
)

func init() {
	PhysicalKeyDown = windows.PhysicalKeyDown
	SwallowPhysicalKeys = windows.SwallowPhysicalKeys
	SetTappingVK = windows.SetTappingVK
	SetKeyboardLog = func(log func(string)) {
		if log == nil {
			windows.KeyboardLog = func(string) {}
			return
		}
		windows.KeyboardLog = log
	}
	EmergencyKeyDown = windows.AsyncKeyDown
	PollKeyToggle = windows.PollKeyToggle
}

// StartPhysicalKeyboard starts physical-key tracking for hold-to-spam binds.
func StartPhysicalKeyboard(ctx context.Context) error {
	return windows.StartPhysicalKeyboard(ctx)
}
