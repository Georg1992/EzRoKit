// Package session defines the InputSession interface — the minimum surface
// needed by any runner that emits keys or mouse clicks.
// *runner.ViiperSession implements these methods; tests can use stubs.
package session

import "time"

// InputSession is the single, canonical interface used by every runner
// (clicker, autopot, keychain, timerkey). Each runner pulls from cfg.Session
// without a concrete type binding. ViiperSession satisfies it.
type InputSession interface {
	TapKey(vk int32, hold time.Duration) error
	// MouseClick performs an atomic mouse down+up with the given hold
	// between them, holding the wire mutex for the whole duration so
	// key events from other goroutines cannot interleave.
	MouseClick(hold time.Duration) error
}

// ClickerCycleSession is implemented by sessions that can keep the clicker's
// key -> mouse sequence under one wire lock. The optional interface preserves
// lightweight test sessions and non-VIIPER implementations; the real session
// implements it so other runners cannot interleave between the two actions.
type ClickerCycleSession interface {
	ClickerCycle(vk int32, keyHold, mouseHold time.Duration) error
}
