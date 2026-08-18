// Package session defines the InputSession interface — the minimum surface
// needed by any runner that emits keys or mouse clicks.
// *runner.ViiperSession implements these methods; tests can use stubs.
package session

import "time"

// InputSession is the single, canonical interface used by every runner
// (clicker, autopot, keychain, timerkey). Each runner pulls from cfg.Session
// without a concrete type binding. ViiperSession satisfies it.
type InputSession interface {
	// TapKey performs one complete key-down → hold → key-up action. The
	// implementation must serialize the whole action against other input.
	TapKey(vk int32, hold time.Duration) error
	// Reset sends key-up and mouse-up so a leftover HID down report cannot
	// keep the bind held after the runner stops.
	Reset()
}

// ClickerInputSession runs one clicker cycle as a single serialized action.
//
// A session only decides what the game receives. It never asks whether a
// physical key is still held: that belongs to the runner driving it, so the
// input side and the detection side stay independent.
type ClickerInputSession interface {
	InputSession
	// TapKeyWithClick presses the key, clicks the left button while it is down,
	// then releases the key. hold is how long each down state lasts, so a game
	// rendering at 60fps has a frame to see it.
	TapKeyWithClick(vk int32, hold time.Duration) error
}
