//go:build windows

package runner

import (
	"context"

	windows "ezrokit/runner/platform/windows"
)

func init() {
	PhysicalKeyDown = windows.PhysicalKeyDown
	EmergencyKeyDown = windows.AsyncKeyDown
	PollKeyToggle = windows.PollKeyToggle
}

// StartPhysicalKeyboard starts physical-key tracking for trigger polling.
func StartPhysicalKeyboard(ctx context.Context) error {
	return windows.StartPhysicalKeyboard(ctx)
}
