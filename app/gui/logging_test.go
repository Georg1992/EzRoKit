//go:build windows

package main

import "testing"

func TestLogToFileDoesNotTouchUI(t *testing.T) {
	a := &guiApp{}
	a.logToFile("pixel bars not found")
	if len(a.logItems) != 0 {
		t.Fatalf("file-only log leaked into UI: %v", a.logItems)
	}
}

func TestAppendLogWritesUIWhenListMissing(t *testing.T) {
	a := &guiApp{}
	a.appendLog("Tools started")
	if len(a.logItems) != 0 {
		t.Fatalf("appendLog without a list still stored UI items: %v", a.logItems)
	}
}
