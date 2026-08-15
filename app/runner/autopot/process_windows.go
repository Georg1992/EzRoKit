//go:build windows

package autopot

import windows "ezrokit/runner/platform/windows"

func init() {
	GetProcessBaseAddr = windows.GetProcessBaseAddr
	OpenProcessHandle = windows.OpenProcessHandle
	CloseProcessHandle = windows.CloseProcessHandle
	ReadProcessUint32ByHandle = windows.ReadProcessUint32ByHandle
	FindVisibleWindowPID = windows.FindVisibleWindowPID
}
