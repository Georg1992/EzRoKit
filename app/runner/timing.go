// Re-export of the timing constants used by gui/*. Other runner/*
// internals import internal/timing directly (see clicker.go,
// timer_key.go, keychain.go, autopot/autopot.go for the canonical
// pattern).
//
// Kept here so existing gui/* callers don't need to be rewritten —
// they continue to use runner.KeyBindTimeout and runner.DefaultAPIAddr.
package runner

import (
	"ezrokit/runner/internal/timing"
)

var (
	KeyBindTimeout = timing.KeyBindTimeout
	DefaultAPIAddr = timing.DefaultAPIAddr
)

// ToggleKeyLabel returns a short UI string for the start/stop hotkeys
// (e.g. "End / F12").
func ToggleKeyLabel() string {
	return timing.ToggleKeyLabel()
}
