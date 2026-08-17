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

// AsyncKeyDown reads the Windows desktop keyboard state. It is used only for
// the emergency toggle keys; clicker triggers remain Raw Input-only so
// VIIPER's virtual keyboard cannot affect them.
func AsyncKeyDown(vk int32) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return ret&0x8000 != 0
}

func PollKeyToggle(wasDown *bool, vk int32) bool {
	down := AsyncKeyDown(vk)
	toggled := down && !*wasDown
	*wasDown = down
	return toggled
}
