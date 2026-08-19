//go:build windows

package main

import (
	"context"
	"fmt"
	"time"
)

// Two log channels:
//   - appendLog / guiLog: status the user needs in the window
//     (start/stop, failures, bind prompts).
//   - logToFile / fileLog: diagnostics (runner internals, key chatter,
//     VIIPER device lines). Always written to logs/app.log.

// maxLogItems is the maximum number of log entries kept in the UI list.
const maxLogItems = 500

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

func (a *guiApp) stampLog(line string) string {
	return fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), line)
}

func (a *guiApp) writeLogFile(stamped string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	if a.logFile != nil {
		_, _ = a.logFile.WriteString(stamped + "\n")
	}
}

// logToFile writes a diagnostic line to the log file only.
func (a *guiApp) logToFile(line string) {
	a.writeLogFile(a.stampLog(line))
}

// fileLog is the callback runners and background diagnostics should use.
func (a *guiApp) fileLog() func(string) {
	return func(s string) { a.logToFile(s) }
}

// appendLog shows a line in the UI log and writes it to the file.
// Call from the GUI thread, or via guiLog.
func (a *guiApp) appendLog(line string) {
	stamped := a.stampLog(line)
	a.writeLogFile(stamped)

	if a.logList == nil {
		return
	}
	a.logItems = append(a.logItems, stamped)
	_ = a.logList.SetModel(a.logItems)
	if len(a.logItems) > 0 {
		_ = a.logList.SetCurrentIndex(len(a.logItems) - 1)
	}
}

// guiLog marshals a UI log callback onto the GUI thread.
func (a *guiApp) guiLog(fn func(string)) func(string) {
	return func(s string) {
		a.mainWindow.Synchronize(func() { fn(s) })
	}
}
