//go:build windows

package runner

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wmInput             = 0x00FF
	wmInputDeviceChange = 0x00FE
	wmQuit              = 0x0012
	ridInput            = 0x10000003
	ridiDeviceName      = 0x20000007
	rimTypeKeyboard     = 1
	riKeyBreak          = 0x0001
	riKeyE0             = 0x0002
	ridevInputSink      = 0x00000100
	ridevDevNotify      = 0x00002000
	gidArrival          = 1
	gidRemoval          = 2
	hwndMessage         = ^uintptr(2)
	virtualKeyboardVID  = "VID_2E8A"
	virtualKeyboardName = "VIIPER"
	virtualKeyboardBus  = "USBIP"
	vkNone              = 0xFF
	// firstBindableVK is the lowest virtual-key code a bind can use. Below it are
	// the mouse buttons, which a keyboard never reports.
	firstBindableVK = 0x07
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

type rawInputDeviceList struct {
	device uintptr
	typeID uint32
}

// physicalKeyboard tracks which keys the user is holding down, from Raw Input.
//
// Only real hardware may hold a bind. VIIPER types the same keys the user binds,
// and Windows also exposes an aggregate pseudo-keyboard that identifies no
// hardware and can report another device's keys; neither may touch a hold, or the
// tool would react to its own output.
//
// Windows reports one keyboard through several Raw Input handles, one per HID
// collection, and a press can arrive on one handle with its release on another.
// Handles are therefore grouped by hardware id, and a hold belongs to that
// keyboard rather than to a handle.
type physicalKeyboard struct {
	mu sync.Mutex
	// keyboard maps a Raw Input handle to the keyboard it belongs to, or "" for a
	// handle that may not hold a bind. Naming a handle costs a syscall, so the
	// answer is cached here, including the negative one.
	keyboard map[uintptr]string
	// held maps a virtual-key code to the keyboard holding it down.
	held map[int32]string
}

var (
	rawUser32   = windows.NewLazySystemDLL("user32.dll")
	rawKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterRawInputDevices = rawUser32.NewProc("RegisterRawInputDevices")
	procGetRawInputDeviceList   = rawUser32.NewProc("GetRawInputDeviceList")
	procGetRawInputData         = rawUser32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfoW  = rawUser32.NewProc("GetRawInputDeviceInfoW")
	procRegisterClassExW        = rawUser32.NewProc("RegisterClassExW")
	procCreateWindowExW         = rawUser32.NewProc("CreateWindowExW")
	procDestroyWindow           = rawUser32.NewProc("DestroyWindow")
	procDefWindowProcW          = rawUser32.NewProc("DefWindowProcW")
	procGetMessageW             = rawUser32.NewProc("GetMessageW")
	procDispatchMessageW        = rawUser32.NewProc("DispatchMessageW")
	procPostThreadMessageW      = rawUser32.NewProc("PostThreadMessageW")
	procGetCurrentThreadID      = rawKernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandleW        = rawKernel32.NewProc("GetModuleHandleW")
	procMapVirtualKeyW          = rawUser32.NewProc("MapVirtualKeyW")

	rawState    = newPhysicalKeyboard()
	rawWndProc  uintptr
	rawStart    sync.Once
	rawStartErr error
	rawThreadID uintptr
	KeyboardLog = func(string) {}
)

func newPhysicalKeyboard() *physicalKeyboard {
	return &physicalKeyboard{
		keyboard: make(map[uintptr]string),
		held:     make(map[int32]string),
	}
}

func StartPhysicalKeyboard(ctx context.Context) error {
	rawStart.Do(func() {
		rawStartErr = startPhysicalKeyboard(ctx)
	})
	return rawStartErr
}

// PhysicalKeyDown reports whether the user is holding vk on a real keyboard.
func PhysicalKeyDown(vk int32) bool {
	rawState.mu.Lock()
	defer rawState.mu.Unlock()
	_, held := rawState.held[vk]
	return held
}

// applyKey folds one keyboard report into the hold state.
//
// While a key is held the keyboard repeats the press about every 31ms, so a press
// only starts a hold when the key is not already held, and only the keyboard
// holding a key can release it.
func (p *physicalKeyboard) setHeldFromHook(vk int32, down bool) {
	if vk < firstBindableVK || vk == vkNone {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if down {
		if _, held := p.held[vk]; !held {
			p.held[vk] = "hook"
		}
		return
	}
	delete(p.held, vk)
}

func (p *physicalKeyboard) applyKey(device uintptr, vk int32, down bool) {
	if vk < firstBindableVK || vk == vkNone {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	board := p.boardLocked(device)
	if board == "" {
		return
	}
	if down {
		if _, held := p.held[vk]; !held {
			p.held[vk] = board
		}
		return
	}
	if p.held[vk] == board {
		delete(p.held, vk)
	}
}

func (p *physicalKeyboard) boardLocked(device uintptr) string {
	if board, known := p.keyboard[device]; known {
		return board
	}
	board := keyboardFromName(deviceName(device))
	p.keyboard[device] = board
	return board
}

// keyboardFromName identifies the physical keyboard a Raw Input device name
// belongs to. Every HID collection of one keyboard maps to the same id:
//
//	\\?\HID#VID_0B05&PID_194B&MI_00#8&4d1c94b&0&0000#{884b...} -> VID_0B05&PID_194B
//	\\?\HID#VID_0B05&PID_194B&MI_03#8&2c753fe&0&0000#{884b...} -> VID_0B05&PID_194B
//	\\?\ACPI#PNP0303#4&1a2b3c4d&0#{884b...}                    -> ACPI#PNP0303
//
// It returns "" for a name that may not hold a bind: VIIPER's own keyboard, and
// names that identify no hardware such as the aggregate \\?\Microsoft Keyboard
// RID\0.
func keyboardFromName(name string) string {
	if isVirtualKeyboardName(name) || !strings.HasPrefix(name, `\\?\`) {
		return ""
	}
	fields := strings.Split(strings.ToUpper(strings.TrimPrefix(name, `\\?\`)), "#")
	if len(fields) < 3 {
		return ""
	}
	bus, hardware := fields[0], fields[1]
	if bus == "" || hardware == "" {
		return ""
	}
	if vid, pid, ok := vidPID(hardware); ok {
		return vid + "&" + pid
	}
	return bus + "#" + hardware
}

// vidPID pulls the USB vendor and product id out of a hardware id such as
// VID_0B05&PID_194B&MI_00. The collection suffix is dropped so every collection
// of one keyboard maps to the same id.
func vidPID(hardware string) (string, string, bool) {
	var vid, pid string
	for _, field := range strings.Split(hardware, "&") {
		switch {
		case strings.HasPrefix(field, "VID_"):
			vid = field
		case strings.HasPrefix(field, "PID_"):
			pid = field
		}
	}
	return vid, pid, vid != "" && pid != ""
}

func isVirtualKeyboardName(name string) bool {
	n := strings.ToUpper(name)
	return strings.Contains(n, virtualKeyboardVID) ||
		strings.Contains(n, virtualKeyboardName) ||
		strings.Contains(n, virtualKeyboardBus)
}

// classifyKeyboards records every keyboard Windows currently reports. The log
// then shows which ones can hold a bind, and the event path never has to name a
// handle while input is flowing.
func (p *physicalKeyboard) classifyKeyboards() error {
	devices, err := listRawKeyboards()
	if err != nil {
		return fmt.Errorf("list physical keyboards: %w", err)
	}
	p.mu.Lock()
	p.keyboard = make(map[uintptr]string, len(devices))
	p.held = make(map[int32]string)
	p.mu.Unlock()

	boards := make(map[string]bool)
	lines := make([]string, 0, len(devices))
	for _, device := range devices {
		board, line := p.classify(device, deviceName(device))
		if board != "" {
			boards[board] = true
		}
		lines = append(lines, line)
	}
	KeyboardLog(fmt.Sprintf("physical keyboards: %d, from %d Raw Input handle(s)", len(boards), len(devices)))
	for _, line := range lines {
		KeyboardLog(line)
	}
	if len(boards) == 0 {
		return fmt.Errorf("no physical keyboard found for bind hold")
	}
	return nil
}

// addDevice classifies a keyboard plugged in while the tool runs. VIIPER's
// keyboard attaches this way, well after startup, and is classified out here.
func (p *physicalKeyboard) addDevice(device uintptr) {
	if device == 0 {
		return
	}
	_, line := p.classify(device, deviceName(device))
	KeyboardLog(line)
}

// classify records what a handle is, and describes it for the log.
func (p *physicalKeyboard) classify(device uintptr, name string) (string, string) {
	board := keyboardFromName(name)
	p.mu.Lock()
	p.keyboard[device] = board
	p.mu.Unlock()
	return board, describeKeyboard(device, name, board)
}

// removeDevice drops a keyboard that was unplugged. Once its last handle is gone
// no release can arrive for it, so anything it held has to drop now.
func (p *physicalKeyboard) removeDevice(device uintptr) {
	p.mu.Lock()
	board, known := p.keyboard[device]
	delete(p.keyboard, device)
	if !known || board == "" || p.hasHandleLocked(board) {
		p.mu.Unlock()
		return
	}
	dropped := make([]string, 0, len(p.held))
	for vk, owner := range p.held {
		if owner == board {
			dropped = append(dropped, keyLabel(vk))
			delete(p.held, vk)
		}
	}
	p.mu.Unlock()
	KeyboardLog(fmt.Sprintf("keyboard %s unplugged, released %s", board, keyList(dropped)))
}

func (p *physicalKeyboard) hasHandleLocked(board string) bool {
	for _, other := range p.keyboard {
		if other == board {
			return true
		}
	}
	return false
}

func describeKeyboard(device uintptr, name, board string) string {
	if board == "" {
		return fmt.Sprintf("keyboard ignored, handle 0x%X %s", device, name)
	}
	return fmt.Sprintf("keyboard %s, handle 0x%X %s", board, device, name)
}

func keyLabel(vk int32) string {
	if vk >= '0' && vk <= 'Z' {
		return string(rune(vk))
	}
	return fmt.Sprintf("VK_0x%02X", vk)
}

func keyList(keys []string) string {
	if len(keys) == 0 {
		return "nothing"
	}
	return strings.Join(keys, ", ")
}

func startPhysicalKeyboard(ctx context.Context) error {
	ready := make(chan error, 1)
	go rawKeyboardThread(ctx, ready)
	return <-ready
}

// rawKeyboardThread owns the Raw Input window and its message loop.
//
// A window, its message queue and GetMessage all belong to one OS thread, so this
// goroutine must stay on the thread that created the window. Without the lock the
// Go runtime is free to resume it on another thread, where GetMessage drains a
// queue no keyboard event is ever posted to: input goes silent from that moment
// on, and a key held at the time stays held forever because its release sits
// unread in the original thread's queue.
func rawKeyboardThread(ctx context.Context, ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	threadID, _, _ := procGetCurrentThreadID.Call()
	rawThreadID = threadID
	defer func() { rawThreadID = 0 }()
	className, _ := syscall.UTF16PtrFromString("EzRoKitPhysicalKeyboard")

	rawWndProc = syscall.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
		switch uint32(message) {
		case wmInput:
			handleRawInput(lParam)
		case wmInputDeviceChange:
			switch wParam {
			case gidArrival:
				rawState.addDevice(lParam)
			case gidRemoval:
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
		0, 0,
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

	// RIDEV_INPUTSINK delivers keyboard reports even though the game, not this
	// window, has the focus.
	device := rawInputDevice{
		usagePage: 0x01,
		usage:     0x06,
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
	if err := rawState.classifyKeyboards(); err != nil {
		procDestroyWindow.Call(hwnd)
		ready <- err
		return
	}
	if err := installKeyboardSwallowHook(); err != nil {
		procDestroyWindow.Call(hwnd)
		ready <- err
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
		if message.message == wmForceKeyUp {
			sendInjectedKeyUp(int32(message.wParam))
			continue
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}

	uninstallKeyboardSwallowHook()
	procDestroyWindow.Call(hwnd)
	clearPhysicalKeyState()
}

func clearPhysicalKeyState() {
	rawState.mu.Lock()
	rawState.keyboard = make(map[uintptr]string)
	rawState.held = make(map[int32]string)
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
	if input.header.typeID != rimTypeKeyboard {
		return
	}
	down := input.keyboard.flags&riKeyBreak == 0
	rawState.applyKey(input.header.device, virtualKey(input.keyboard), down)
}

func listRawKeyboards() ([]uintptr, error) {
	var n uint32
	size := unsafe.Sizeof(rawInputDeviceList{})
	ret, _, err := procGetRawInputDeviceList.Call(0, uintptr(unsafe.Pointer(&n)), size)
	if ret == ^uintptr(0) {
		return nil, err
	}
	if n == 0 {
		return nil, nil
	}
	buf := make([]rawInputDeviceList, n)
	ret, _, err = procGetRawInputDeviceList.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), size)
	if ret == ^uintptr(0) {
		return nil, err
	}
	out := make([]uintptr, 0, n)
	for i := uint32(0); i < n && i < uint32(len(buf)); i++ {
		if buf[i].typeID == rimTypeKeyboard {
			out = append(out, buf[i].device)
		}
	}
	return out, nil
}

// virtualKey prefers the virtual-key code in the report and falls back to the
// scan code only when the report carries none, as happens for some multimedia
// keys.
func virtualKey(kbd rawKeyboard) int32 {
	vk := int32(kbd.virtualKey)
	if vk != 0 && vk != vkNone {
		return vk
	}
	code := uint32(kbd.makeCode)
	if kbd.flags&riKeyE0 != 0 {
		code |= 0xE000
	}
	mapped, _, _ := procMapVirtualKeyW.Call(uintptr(code), 3) // MAPVK_VSC_TO_VK_EX
	return int32(mapped)
}

func deviceName(device uintptr) string {
	if device == 0 {
		return ""
	}
	var size uint32
	result, _, _ := procGetRawInputDeviceInfoW.Call(
		device,
		ridiDeviceName,
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if result == ^uintptr(0) || size == 0 {
		return ""
	}
	name := make([]uint16, size+1)
	result, _, _ = procGetRawInputDeviceInfoW.Call(
		device,
		ridiDeviceName,
		uintptr(unsafe.Pointer(&name[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if result == ^uintptr(0) {
		return ""
	}
	return windows.UTF16ToString(name)
}
