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

// AsyncKeyDown is GetAsyncKeyState. End/F12 use this. Binds use Raw Input
// so a virtual tap-up of the same key is not treated as a release.
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
