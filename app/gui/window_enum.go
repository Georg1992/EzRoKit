//go:build windows

package main

import (
	"runtime"
	"sort"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var (
	enumUser32                  = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows             = enumUser32.NewProc("EnumWindows")
	procGetWindowTextW          = enumUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW    = enumUser32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible         = enumUser32.NewProc("IsWindowVisible")
	procGetWindowThreadProcessId = enumUser32.NewProc("GetWindowThreadProcessId")
)

// windowInfo holds the handle, title, and owning PID of a top-level window.
type windowInfo struct {
	hwnd  win.HWND
	title string
	pid   uint32
}

// listVisibleWindows returns all visible top-level windows with
// non-empty titles, sorted alphabetically, along with each window's PID.
func listVisibleWindows() []windowInfo {
	var windows []windowInfo
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1 // continue enumeration
		}

		length, _, _ := procGetWindowTextLengthW.Call(hwnd)
		if length == 0 {
			return 1
		}

		buf := make([]uint16, length+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
		title := syscall.UTF16ToString(buf)
		if title == "" {
			return 1
		}

		// Get the PID of the process that owns this window.
		var pid uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		windows = append(windows, windowInfo{hwnd: win.HWND(hwnd), title: title, pid: pid})
		return 1 // continue enumeration
	})
	procEnumWindows.Call(callback, 0)
	runtime.KeepAlive(callback)

	sort.Slice(windows, func(i, j int) bool {
		return windows[i].title < windows[j].title
	})

	return windows
}

// populateWindowComboBox fills a walk ComboBox with visible window titles.
// Returns the full window info list so the caller can map selection to PID.
func populateWindowComboBox(cb *walk.ComboBox) ([]windowInfo, error) {
	items := listVisibleWindows()

	if len(items) == 0 {
		// No windows found — this is unusual on a running Windows desktop
		// and may indicate that the EnumWindows call failed or all windows
		// have empty titles (e.g. during a fast user switch / lock screen).
		// Leave the old model intact so the user doesn't lose their selection.
		// Since we don't have the logger reference here, the caller (Refresh button)
		// can detect this via the returned empty slice and log accordingly.
		return items, nil
	}

	// Save current selection to restore if the same title still exists.
	selIdx := cb.CurrentIndex()
	selTitle := ""
	if selIdx >= 0 {
		selTitle = cb.Text()
	}

	// Clear and repopulate.
	cb.SetModel(nil)
	titles := make([]string, 0, len(items))
	for _, w := range items {
		titles = append(titles, w.title)
	}
	if err := cb.SetModel(titles); err != nil {
		return nil, err
	}

	// Restore selection if the same window title still exists.
	if selTitle != "" {
		for i, t := range titles {
			if t == selTitle {
				cb.SetCurrentIndex(i)
				break
			}
		}
	}

	return items, nil
}
