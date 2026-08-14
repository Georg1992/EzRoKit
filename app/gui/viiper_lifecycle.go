//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"

	"belarus-champ-tools/runner"
)

// onStartViiper starts the VIIPER server and opens an input session. Called
// from the VIIPER Start button. Runs the blocking startup on a background
// goroutine so the GUI stays responsive.
func (a *guiApp) onStartViiper() {
	a.viiperStartBtn.SetEnabled(false)
	a.appendLog("Starting VIIPER server...")
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.viiperStartupCancel = cancel
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.viiperStartupCancel = nil
			a.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				_, _ = fmt.Fprintf(os.Stderr, "PANIC in onStartViiper: %v\n%s\n", r, debug.Stack())
			}
		}()
		logFn := func(s string) {
			if ctx.Err() != nil {
				return
			}
			a.mainWindow.Synchronize(func() { a.appendLog(s) })
		}

		_, err := ensureViiperServer(ctx, logFn)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			a.mainWindow.Synchronize(func() {
				a.appendLog(fmt.Sprintf("VIIPER start failed: %v", err))
				a.viiperStartBtn.SetEnabled(true) // retry
			})
			return
		}
		if ctx.Err() != nil {
			stopViiperServerIfStarted()
			return
		}

		logFn("Opening VIIPER session...")
		session, err := runner.OpenViiperSession(ctx, runner.DefaultAPIAddr, logFn)
		if err != nil {
			stopViiperServerIfStarted()
			if ctx.Err() != nil {
				return
			}
			a.mainWindow.Synchronize(func() {
				a.appendLog(fmt.Sprintf("VIIPER session failed: %v", err))
				a.viiperStartBtn.SetEnabled(true) // retry
			})
			return
		}
		if ctx.Err() != nil {
			session.Close()
			stopViiperServerIfStarted()
			return
		}

		a.mu.Lock()
		if ctx.Err() != nil {
			a.mu.Unlock()
			session.Close()
			stopViiperServerIfStarted()
			return
		}
		// Close any stale session before replacing it.
		if a.inputSession != nil {
			a.inputSession.Close()
		}
		a.inputSession = session
		a.mu.Unlock()

		a.mainWindow.Synchronize(func() {
			a.viiperBadge.SetStatus(viiperActive)
			a.appendLog("VIIPER server ready")
			// VIIPER is running — enable config and Tools Start button.
			a.setConfigEnabled(true)
			a.startBtn.SetEnabled(true)
			a.stopBtn.SetEnabled(false)
			a.viiperStartBtn.SetEnabled(false) // already running
		})
	}()
}
