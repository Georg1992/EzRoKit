//go:build windows

package runner

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"ezrokit/runner/internal/timing"
)

const (
	whKeyboardLL   = 13
	llkhfInjected  = 0x10
	wmKeyDown      = 0x0100
	wmSysKeyDown   = 0x0104
	wmForceKeyUp   = 0x8001 // WM_APP + 1
	inputKeyboard  = 1
	keyeventfKeyup = 0x0002
	mapvkVkToVsc   = 0
)

type kbdllhookstruct struct {
	vkCode    uint32
	scanCode  uint32
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// keyboardInput is INPUT + KEYBDINPUT padded to the MOUSEINPUT union size
// on 64-bit Windows (40 bytes).
type keyboardInput struct {
	inputType uint32
	_         uint32
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	_         uint32
	extraInfo uintptr
	_         [8]byte
}

var (
	procSetWindowsHookExW        = rawUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx      = rawUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx           = rawUser32.NewProc("CallNextHookEx")
	procGetForegroundWindow      = rawUser32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = rawUser32.NewProc("GetWindowThreadProcessId")
	procGetCurrentProcessId      = rawKernel32.NewProc("GetCurrentProcessId")
	procSendInput                = rawUser32.NewProc("SendInput")

	llKeyboardProc uintptr
	llKeyboardHook uintptr
	swallowOurPID  uint32

	swallowMu  sync.RWMutex
	swallowVKs map[int32]struct{}

	tappingVK atomic.Int32
)

// SwallowPhysicalKeys blocks these virtual-key codes from reaching the
// foreground app (the game). Hold detection still sees them via Raw Input
// and via the hook itself. Pass nil or an empty list to block nothing.
func SwallowPhysicalKeys(vks []int32) {
	next := make(map[int32]struct{}, len(vks))
	for _, vk := range vks {
		if vk != 0 {
			next[vk] = struct{}{}
		}
	}
	swallowMu.Lock()
	swallowVKs = next
	swallowMu.Unlock()
}

// SetTappingVK marks the virtual-key currently being sent by VIIPER. That
// key must reach the game (VIIPER HID is not flagged injected), and a forced
// key-up of the same code must not cut the tap short.
func SetTappingVK(vk int32) {
	tappingVK.Store(vk)
}

func installKeyboardSwallowHook() error {
	if llKeyboardProc == 0 {
		llKeyboardProc = syscall.NewCallback(lowLevelKeyboardProc)
	}
	pid, _, _ := procGetCurrentProcessId.Call()
	swallowOurPID = uint32(pid)

	instance, _, _ := procGetModuleHandleW.Call(0)
	hook, _, err := procSetWindowsHookExW.Call(
		whKeyboardLL,
		llKeyboardProc,
		instance,
		0,
	)
	if hook == 0 {
		return fmt.Errorf("SetWindowsHookExW: %w", err)
	}
	llKeyboardHook = hook
	return nil
}

func uninstallKeyboardSwallowHook() {
	if llKeyboardHook == 0 {
		return
	}
	procUnhookWindowsHookEx.Call(llKeyboardHook)
	llKeyboardHook = 0
	SwallowPhysicalKeys(nil)
	SetTappingVK(0)
}

func lowLevelKeyboardProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) >= 0 && lParam != 0 {
		kbd := (*kbdllhookstruct)(unsafe.Pointer(lParam))
		if kbd.flags&llkhfInjected == 0 && !foregroundIsOurProcess() {
			down := wParam == wmKeyDown || wParam == wmSysKeyDown
			if consumePhysicalKey(int32(kbd.vkCode), down) {
				return 1
			}
		}
	}
	next, _, _ := procCallNextHookEx.Call(0, nCode, wParam, lParam)
	return next
}

// consumePhysicalKey tracks hold state for a swallowed trigger. Key-down
// (including auto-repeat) is eaten so the game cannot type it, and a key-up
// is queued so GetAsyncKeyState does not stay down. Key-up is left in the
// stream so a real release still reaches the game.
//
// While VIIPER is tapping this same key, the event is left alone: the virtual
// keyboard is a real HID device, so the chain's own trigger press would
// otherwise be eaten as if the user was still holding it.
func consumePhysicalKey(vk int32, down bool) bool {
	if !physicalKeyBlocked(vk) {
		return false
	}
	if tappingVK.Load() == vk {
		return false
	}
	rawState.setHeldFromHook(vk, down)
	if !down {
		return false
	}
	queueForcedKeyUp(vk)
	return true
}

func physicalKeyBlocked(vk int32) bool {
	if vk == 0 {
		return false
	}
	for _, emergency := range timing.ToggleVKs {
		if vk == emergency {
			return false
		}
	}
	swallowMu.RLock()
	defer swallowMu.RUnlock()
	_, ok := swallowVKs[vk]
	return ok
}

func swallowed(vk int32) bool {
	swallowMu.RLock()
	defer swallowMu.RUnlock()
	_, ok := swallowVKs[vk]
	return ok
}

func shouldInjectKeyUp(vk int32) bool {
	return vk != 0 && tappingVK.Load() != vk
}

var queueForcedKeyUp = postForceKeyUp

func postForceKeyUp(vk int32) {
	if !shouldInjectKeyUp(vk) || rawThreadID == 0 {
		return
	}
	procPostThreadMessageW.Call(rawThreadID, wmForceKeyUp, uintptr(uint32(vk)), 0)
}

func sendInjectedKeyUp(vk int32) {
	if !shouldInjectKeyUp(vk) {
		return
	}
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(uint32(vk)), mapvkVkToVsc)
	in := keyboardInput{
		inputType: inputKeyboard,
		vk:        uint16(vk),
		scan:      uint16(scan),
		flags:     keyeventfKeyup,
	}
	procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

func foregroundIsOurProcess() bool {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return false
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid == swallowOurPID
}
