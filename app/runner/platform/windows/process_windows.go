//go:build windows

package runner

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procKernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procCreateToolhelp32Snapshot = procKernel32.NewProc("CreateToolhelp32Snapshot")
	procOpenProcess              = procKernel32.NewProc("OpenProcess")
	procReadProcessMemory        = procKernel32.NewProc("ReadProcessMemory")
	procCloseHandle              = procKernel32.NewProc("CloseHandle")

	winUser32                     = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows               = winUser32.NewProc("EnumWindows")
	procGetWindowTextW            = winUser32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW      = winUser32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible           = winUser32.NewProc("IsWindowVisible")
	procGetWindowThreadProcessId2 = winUser32.NewProc("GetWindowThreadProcessId")

	procModule32First = procKernel32.NewProc("Module32FirstW")
)

const (
	th32csSnapModule = 0x00000008
	processVMRead    = 0x0010
)

type moduleEntry32 struct {
	Size         uint32
	ModuleID     uint32
	ProcessID    uint32
	GlobalUsage  uint32
	ProccntUsage uint32
	ModBaseAddr  *byte // base address of the module in the target process
	ModBaseSize  uint32
	HModule      uintptr
	SzModule     [256]uint16
	SzExePath    [260]uint16
}

// OpenProcessHandle opens a handle to the process with the given PID
// for memory reading (PROCESS_VM_READ only). The handle must be closed
// with CloseProcessHandle when no longer needed.
func OpenProcessHandle(pid uint32) (windows.Handle, error) {
	h, _, err := procOpenProcess.Call(processVMRead, 0, uintptr(pid))
	if h == 0 {
		return windows.InvalidHandle, fmt.Errorf("OpenProcess(%d) failed: %w", pid, err)
	}
	return windows.Handle(h), nil
}

// CloseProcessHandle closes a process handle obtained from OpenProcessHandle.
func CloseProcessHandle(h windows.Handle) {
	if h != windows.InvalidHandle && h != 0 {
		procCloseHandle.Call(uintptr(h))
	}
}

// GetProcessBaseAddr returns the base address of the first module (the
// executable itself) in the process with the given PID. The base address
// is needed to resolve module-relative memory offsets (like those from
// Cheat Engine or AHK scripts) into absolute virtual addresses.
func GetProcessBaseAddr(pid uint32) (uintptr, error) {
	snapshot, _, err := procCreateToolhelp32Snapshot.Call(th32csSnapModule, uintptr(pid))
	if snapshot == uintptr(windows.Handle(0xFFFFFFFF)) {
		return 0, fmt.Errorf("CreateToolhelp32Snapshot(modules) for PID %d failed: %w", pid, err)
	}
	defer procCloseHandle.Call(snapshot)

	var entry moduleEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procModule32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	if ret == 0 {
		return 0, fmt.Errorf("Module32First failed for PID %d", pid)
	}

	return uintptr(unsafe.Pointer(entry.ModBaseAddr)), nil
}

// VisibleWindow is a top-level window with a non-empty title.
type VisibleWindow struct {
	Title string
	PID   uint32
}

func enumVisibleWindows() []VisibleWindow {
	var out []VisibleWindow
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
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
		var pid uint32
		procGetWindowThreadProcessId2.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		out = append(out, VisibleWindow{Title: title, PID: pid})
		return 1
	})
	procEnumWindows.Call(callback, 0)
	runtime.KeepAlive(callback)
	return out
}

// ListVisibleWindows returns visible top-level windows with non-empty titles,
// sorted by title.
func ListVisibleWindows() []VisibleWindow {
	windows := enumVisibleWindows()
	sort.Slice(windows, func(i, j int) bool {
		return windows[i].Title < windows[j].Title
	})
	return windows
}

// FindVisibleWindowPID searches visible top-level windows with non-empty
// titles for one whose title contains the given substring. Returns the PID
// of the first match in enumeration order, or 0 if none found.
func FindVisibleWindowPID(title string) uint32 {
	for _, w := range enumVisibleWindows() {
		if strings.Contains(w.Title, title) {
			return w.PID
		}
	}
	return 0
}

// ReadProcessUint32ByHandle reads a 32-bit value from the target process
// using an already-open handle. Use this with OpenProcessHandle to avoid
// the open/close pattern that anti-cheat systems like Gepard detect.
func ReadProcessUint32ByHandle(h windows.Handle, addr uintptr) (uint32, error) {
	var val uint32
	var nBytes uintptr
	ret, _, err := procReadProcessMemory.Call(
		uintptr(h),
		addr,
		uintptr(unsafe.Pointer(&val)),
		4,
		uintptr(unsafe.Pointer(&nBytes)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("ReadProcessMemory(0x%X) failed: %w", addr, err)
	}
	if nBytes != 4 {
		return 0, fmt.Errorf("ReadProcessMemory(0x%X): read %d bytes, want 4", addr, nBytes)
	}
	return val, nil
}
