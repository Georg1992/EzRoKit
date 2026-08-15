//go:build windows

package runner

import (
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procGetDC            = user32.NewProc("GetDC")
	procReleaseDC        = user32.NewProc("ReleaseDC")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

func PollKeyToggle(wasDown *bool, vk int32) bool {
	down := PhysicalKeyDown(vk)
	toggled := down && !*wasDown
	*wasDown = down
	return toggled
}

func PhysicalKeyDown(vk int32) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return ret&0x8000 != 0
}

// WinPhysicalKeyDown and WinPollKeyToggle are exported aliases used by
// the runner package's init() in keyboard_windows.go to wire the
// PhysicalKeyDown / PollKeyToggle function variables. They are exported
// because keyboard_windows.go imports this package under an alias.
var (
	WinPhysicalKeyDown      = PhysicalKeyDown
	WinPollKeyToggle        = PollKeyToggle
)

