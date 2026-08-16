//go:build windows

package main

import (
	"context"
	"fmt"
	"time"
)

// maxLogItems is the maximum number of log entries kept in memory to
// prevent unbounded memory growth during long sessions.
const maxLogItems = 500

// setupLogLimit attaches a timer that trims the log items slice on the
// GUI thread every 30 seconds, ensuring old entries are dropped when the
// log exceeds maxLogItems. This prevents the in-memory log from growing
// unboundedly over hours of use.
func (a *guiApp) setupLogLimit() error {
	ctx := a.lifetimeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	t := time.NewTicker(30 * time.Second)
	go func() {
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			a.mainWindow.Synchronize(func() {
				if a.shuttingDown.Load() || a.logList == nil {
					return
				}
				if len(a.logItems) > maxLogItems {
					excess := len(a.logItems) - maxLogItems
					a.logItems = a.logItems[excess:]
					_ = a.logList.SetModel(a.logItems)
					_ = a.logList.SetCurrentIndex(len(a.logItems) - 1)
				}
			})
		}
	}()
	return nil
}

func (a *guiApp) appendLog(line string) {
	stamped := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line)

	// Write to persistent log file (best-effort — file may be missing).
	if a.logFile != nil {
		_, _ = a.logFile.WriteString(stamped + "\n")
	}

	if a.logList == nil {
		return
	}
	a.logItems = append(a.logItems, stamped)
	// UI update errors are not critical; log display may fail but log entry is recorded.
	_ = a.logList.SetModel(a.logItems)
	if len(a.logItems) > 0 {
		_ = a.logList.SetCurrentIndex(len(a.logItems) - 1)
	}
}
