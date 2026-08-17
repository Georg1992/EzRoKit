//go:build windows

package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmInput               = 0x00FF
	wmInputDeviceChange   = 0x00FE
	wmQuit                = 0x0012
	ridInput              = 0x10000003
	ridiDeviceName        = 0x20000007
	rimTypeKeyboard       = 1
	riKeyBreak            = 0x0001
	ridevInputSink        = 0x00000100
	ridevDevNotify        = 0x00002000
	gidRemoval            = 2
	hwndMessage           = ^uintptr(2)
	virtualKeyboardVIDPID = "VID_2E8A&PID_0010"
)

type rawInputDevice struct {
	usagePage uint16
	usage     uint16
	flags     uint32
	target    uintptr
}

type rawInputHeader struct {
	typeID uint32
	size   uint32
	device uintptr
	wParam uintptr
}

type rawKeyboard struct {
	makeCode         uint16
	flags            uint16
	reserved         uint16
	virtualKey       uint16
	message          uint32
	extraInformation uint32
}

type rawInput struct {
	header   rawInputHeader
	keyboard rawKeyboard
}

type rawMessage struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	private uint32
}

type rawWindowClass struct {
	size        uint32
	style       uint32
	wndProc     uintptr
	classExtra  int32
	windowExtra int32
	instance    uintptr
	icon        uintptr
	cursor      uintptr
	background  uintptr
	menuName    *uint16
	className   *uint16
	iconSmall   uintptr
}

type physicalKeyboard struct {
	mu sync.RWMutex

	// devices stores key state separately for every physical keyboard. The
	// aggregate map below remains down until every physical device holding a
	// key has reported its release.
	devices map[uintptr]map[int32]bool
	down    map[int32]int

	// virtualDevices caches the device classification. Keeping this separate
	// from key state prevents a virtual key-up from clearing a physical key
	// that has the same virtual-key code.
	virtualDevices map[uintptr]bool
}

var (
	rawUser32   = windows.NewLazySystemDLL("user32.dll")
	rawKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterRawInputDevices = rawUser32.NewProc("RegisterRawInputDevices")
	procGetRawInputData         = rawUser32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfoW  = rawUser32.NewProc("GetRawInputDeviceInfoW")
	procRegisterClassExW        = rawUser32.NewProc("RegisterClassExW")
	procCreateWindowExW         = rawUser32.NewProc("CreateWindowExW")
	procDestroyWindow           = rawUser32.NewProc("DestroyWindow")
	procDefWindowProcW          = rawUser32.NewProc("DefWindowProcW")
	procGetMessageW             = rawUser32.NewProc("GetMessageW")
	procTranslateMessage        = rawUser32.NewProc("TranslateMessage")
	procDispatchMessageW        = rawUser32.NewProc("DispatchMessageW")
	procPostThreadMessageW      = rawUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID      = rawKernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW        = rawKernel32.NewProc("GetModuleHandleW")

	rawState = &physicalKeyboard{
		devices:        make(map[uintptr]map[int32]bool),
		down:           make(map[int32]int),
		virtualDevices: make(map[uintptr]bool),
	}
	rawWndProc  uintptr
	rawStart    sync.Once
	rawStartErr error
)

// StartPhysicalKeyboard starts the Raw Input message loop used by
// PhysicalKeyDown. It receives physical keyboard events even when the game
// window is focused and ignores EzRoKit's virtual keyboard device.
func StartPhysicalKeyboard(ctx context.Context) error {
	rawStart.Do(func() {
		rawStartErr = startPhysicalKeyboard(ctx)
	})
	return rawStartErr
}

// PhysicalKeyDown reports the current state of a physical keyboard key.
func PhysicalKeyDown(vk int32) bool {
	rawState.mu.RLock()
	defer rawState.mu.RUnlock()
	return rawState.down[vk] > 0
}

func (p *physicalKeyboard) setKey(device uintptr, vk int32, down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	keys := p.devices[device]
	if keys == nil {
		keys = make(map[int32]bool)
		p.devices[device] = keys
	}
	wasDown := keys[vk]
	if wasDown == down {
		return // Ignore autorepeat make packets and duplicate releases.
	}
	keys[vk] = down
	if down {
		p.down[vk]++
		return
	}
	if p.down[vk] <= 1 {
		delete(p.down, vk)
	} else {
		p.down[vk]--
	}
}

func (p *physicalKeyboard) removeDevice(device uintptr) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for vk, down := range p.devices[device] {
		if down {
			if p.down[vk] <= 1 {
				delete(p.down, vk)
			} else {
				p.down[vk]--
			}
		}
	}
	delete(p.devices, device)
	delete(p.virtualDevices, device)
}

func startPhysicalKeyboard(ctx context.Context) error {
	ready := make(chan error, 1)
	go rawKeyboardThread(ctx, ready)
	return <-ready
}

func rawKeyboardThread(ctx context.Context, ready chan<- error) {
	threadID, _, _ := procGetCurrentThreadID.Call()
	className, _ := syscall.UTF16PtrFromString("EzRoKitPhysicalKeyboard")

	rawWndProc = syscall.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
		switch uint32(message) {
		case wmInput:
			handleRawInput(lParam)
			// WM_INPUT is one of the messages for which Windows expects the
			// default window procedure to perform message cleanup.
		case wmInputDeviceChange:
			if wParam == gidRemoval {
				rawState.removeDevice(lParam)
			}
		}
		return callDefWindowProc(hwnd, message, wParam, lParam)
	})

	instance, _, _ := procGetModuleHandleW.Call(0)
	class := rawWindowClass{
		size:      uint32(unsafe.Sizeof(rawWindowClass{})),
		wndProc:   rawWndProc,
		instance:  instance,
		className: className,
	}
	if result, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); result == 0 {
		ready <- fmt.Errorf("RegisterClassExW: %w", err)
		return
	}

	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		0,
		0, 0, 0, 0,
		hwndMessage,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %w", err)
		return
	}

	device := rawInputDevice{
		usagePage: 0x01, // Generic Desktop
		usage:     0x06, // Keyboard
		flags:     ridevInputSink | ridevDevNotify,
		target:    hwnd,
	}
	if result, _, err := procRegisterRawInputDevices.Call(
		uintptr(unsafe.Pointer(&device)),
		1,
		unsafe.Sizeof(device),
	); result == 0 {
		procDestroyWindow.Call(hwnd)
		ready <- fmt.Errorf("RegisterRawInputDevices: %w", err)
		return
	}

	ready <- nil
	go func() {
		<-ctx.Done()
		procPostThreadMessageW.Call(threadID, wmQuit, 0, 0)
	}()

	for {
		var message rawMessage
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if result == 0 || result == ^uintptr(0) {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}

	procDestroyWindow.Call(hwnd)
	clearPhysicalKeyState()
}

func clearPhysicalKeyState() {
	rawState.mu.Lock()
	rawState.devices = make(map[uintptr]map[int32]bool)
	rawState.down = make(map[int32]int)
	rawState.mu.Unlock()
}

func callDefWindowProc(hwnd, message, wParam, lParam uintptr) uintptr {
	result, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return result
}

func handleRawInput(handle uintptr) {
	var size uint32
	_, _, _ = procGetRawInputData.Call(handle, ridInput, 0, uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(rawInputHeader{}))
	if size < uint32(unsafe.Sizeof(rawInput{})) {
		return
	}

	data := make([]byte, size)
	read, _, _ := procGetRawInputData.Call(
		handle,
		ridInput,
		uintptr(unsafe.Pointer(&data[0])),
		uintptr(unsafe.Pointer(&size)),
		unsafe.Sizeof(rawInputHeader{}),
	)
	if read == ^uintptr(0) || read < unsafe.Sizeof(rawInput{}) {
		return
	}

	input := (*rawInput)(unsafe.Pointer(&data[0]))
	if input.header.typeID != rimTypeKeyboard || isVirtualKeyboard(input.header.device) {
		return
	}

	vk := int32(input.keyboard.virtualKey)
	if vk == 0 {
		return
	}
	down := input.keyboard.flags&riKeyBreak == 0
	rawState.setKey(input.header.device, vk, down)
}

func isVirtualKeyboard(device uintptr) bool {
	rawState.mu.RLock()
	known, ok := rawState.virtualDevices[device]
	rawState.mu.RUnlock()
	if ok {
		return known
	}

	var size uint32
	result, _, _ := procGetRawInputDeviceInfoW.Call(
		device,
		ridiDeviceName,
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if result == ^uintptr(0) || size == 0 {
		return false
	}

	name := make([]uint16, size+1)
	result, _, _ = procGetRawInputDeviceInfoW.Call(
		device,
		ridiDeviceName,
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if result == ^uintptr(0) {
		return false
	}

	virtual := strings.Contains(strings.ToUpper(windows.UTF16ToString(name)), virtualKeyboardVIDPID)
	rawState.mu.Lock()
	rawState.virtualDevices[device] = virtual
	rawState.mu.Unlock()
	return virtual
}
