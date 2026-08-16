//go:build windows

package main

import (
	"context"
	"time"

	"ezrokit/runner"

	"github.com/Alia5/VIIPER/viiperclient"
)

// viiperMonitor periodically pings the VIIPER server and calls
// onStatusChange whenever the active/inactive state transitions.
type viiperMonitor struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startViiperMonitor launches a goroutine that pings the VIIPER API
// every 2 seconds. onStatusChange is called on the monitor's goroutine
// — the caller must marshal UI updates to the GUI thread.
func startViiperMonitor(ctx context.Context, onStatusChange func(active bool)) *viiperMonitor {
	monitorCtx, cancel := context.WithCancel(ctx)
	m := &viiperMonitor{
		cancel: cancel,
		done:   make(chan struct{}),
	}

	go func() {
		defer close(m.done)
		addr := runner.DefaultAPIAddr
		api := viiperclient.New(addr)
		wasActive := false
		pollTimer := time.NewTimer(2 * time.Second)
		defer pollTimer.Stop()

		for monitorCtx.Err() == nil {
			pingCtx, pingCancel := context.WithTimeout(monitorCtx, 2*time.Second)
			_, err := api.PingCtx(pingCtx)
			pingCancel()

			active := err == nil
			if active != wasActive {
				wasActive = active
				onStatusChange(active)
			}

			// Drain the timer channel before Reset to avoid a documented
			// race: if the timer fired between PingCtx return and Stop(),
			// Reset without drain causes the select to read a stale value
			// immediately instead of waiting 2 seconds.
			if !pollTimer.Stop() {
				select {
				case <-pollTimer.C:
				default:
				}
			}
			pollTimer.Reset(2 * time.Second)

			select {
			case <-monitorCtx.Done():
				return
			case <-pollTimer.C:
			}
		}
	}()

	return m
}

// stop stops the monitor and waits for the goroutine to finish.
func (m *viiperMonitor) stop() {
	if m == nil {
		return
	}
	m.cancel()
	<-m.done
}
