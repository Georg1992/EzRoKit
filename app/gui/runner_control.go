//go:build windows

package main

import (
	"context"
	"fmt"
	"sync"

	"ezrokit/runner"
)

// lifecycleRunner is the common GUI-facing runner lifecycle.
type lifecycleRunner interface {
	Start() error
	Stop()
	Wait()
	Running() bool
}

// replaceRunner stops the runner currently held by a GUI slot, starts a new
// one, and publishes it only after Start succeeds. Callers serialize this
// operation with guiApp.lifecycleMu; take and store serialize field access
// with guiApp.mu.
func replaceRunner(
	take func() lifecycleRunner,
	store func(lifecycleRunner),
	label string,
	log func(string),
	alert func(string),
	session func() runner.InputSession,
	wanted func() bool,
	construct func(runner.InputSession) lifecycleRunner,
) bool {
	if !wanted() {
		return false
	}
	sess := session()
	if sess == nil {
		return false
	}

	if old := take(); old != nil {
		old.Stop()
		old.Wait()
	}

	current := construct(sess)
	if err := current.Start(); err != nil {
		msg := fmt.Sprintf("%s start failed: %v", label, err)
		log(msg)
		alert(msg)
		return false
	}
	store(current)
	log(fmt.Sprintf("%s started", label))
	return true
}

// stopRunnerAsync stops and waits for a runner without blocking the GUI thread.
func stopRunnerAsync(r lifecycleRunner) {
	if r == nil {
		return
	}
	r.Stop()
	go r.Wait()
}

// bindKeyFlow owns the shared "press a key to bind" interaction used by all
// tabs. Binding state and UI changes are completed on the GUI thread.
func (a *guiApp) bindKeyFlow(
	gate func() bool,
	prompt string,
	cleanup func(),
	reenable func(),
	onPress func(vk int32),
) bool {
	if !gate() {
		return false
	}
	a.appendLog(prompt)
	ctx := a.lifetimeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	var cleanupOnce sync.Once
	runCleanup := func() { cleanupOnce.Do(cleanup) }

	go func() {
		defer func() {
			if ctx.Err() != nil {
				return
			}
			a.mainWindow.Synchronize(func() {
				runCleanup()
				reenable()
			})
		}()

		vk, ok := runner.WaitForKeyPressContext(ctx, runner.KeyBindTimeout)
		if ctx.Err() != nil {
			return
		}
		a.mainWindow.Synchronize(func() {
			if !ok {
				a.appendLog("Key bind timed out")
				return
			}
			if _, hidOK := runner.VKToHID(vk); !hidOK {
				a.appendLog(fmt.Sprintf("Key %s is not supported", runner.KeyName(vk)))
				return
			}
			onPress(vk)
		})
	}()
	return true
}
