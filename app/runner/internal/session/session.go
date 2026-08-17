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
	// MouseClick performs an atomic mouse down+up with the given hold.
	MouseClick(hold time.Duration) error
}

// OrderedInputSession can execute a clicker cycle as one serialized action.
// Implementations must not allow another input action between the key tap and
// mouse click.
type OrderedInputSession interface {
	InputSession
	TapKeyThenMouseClick(vk int32, keyHold, mouseHold time.Duration) error
}
