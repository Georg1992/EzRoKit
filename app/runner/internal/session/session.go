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

// OrderedInputSession can execute a clicker cycle as one serialized action:
// key down, mouse click, key up. The virtual key must come up every cycle
// so Windows can see a physical release.
type OrderedInputSession interface {
	InputSession
	KeyDownThenMouseClick(vk int32, afterKey, afterMouse time.Duration) error
}
