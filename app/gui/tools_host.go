//go:build windows

package main

import "ezrokit/runner"

// toolsHost owns the four tool runners so start/stop/isStarted can loop
// instead of repeating the same four fields.
type toolsHost struct {
	clicker  *runner.Runner
	autopot  *runner.AutoPotRunner
	timer    *runner.TimerKeyRunner
	keychain *runner.KeyChainRunner
}

func (t *toolsHost) anyRunning() bool {
	return t.clicker != nil && t.clicker.Running() ||
		t.autopot != nil && t.autopot.Running() ||
		t.timer != nil && t.timer.Running() ||
		t.keychain != nil && t.keychain.Running()
}

func (t *toolsHost) takeAll() toolsHost {
	taken := *t
	*t = toolsHost{}
	return taken
}

func (t toolsHost) stopAndWait() {
	if t.clicker != nil {
		t.clicker.Stop()
		t.clicker.Wait()
	}
	if t.autopot != nil {
		t.autopot.Stop()
		t.autopot.Wait()
	}
	if t.timer != nil {
		t.timer.Stop()
		t.timer.Wait()
	}
	if t.keychain != nil {
		t.keychain.Stop()
		t.keychain.Wait()
	}
}
