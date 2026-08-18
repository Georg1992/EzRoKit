//go:build windows

// Command rawkeys records every Raw Input keyboard event Windows delivers, with
// the device handle behind it, and writes them to rawkeys.log next to the exe.
//
// It is a diagnostic, not part of app.exe, and the reference for what the hold
// layer in runner/platform/windows should see: run it while holding a bind in the
// game with the clicker running, and the log shows which Raw Input handle carries
// each press and release, VIIPER's taps included, plus a summary of any key-down
// that arrived after that key's last key-up. It only reads input; it never sends
// any.
//
// Build and run, from the app directory. The log lands next to the exe, in the
// build directory, so neither shows up beside the shipped app.exe:
//
//	go build -o .\build\rawkeys.exe .\tools\rawkeys
//	.\build\rawkeys.exe [seconds]
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
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
	hwndMessage         = ^uintptr(2)
	vkNone              = 0xFF
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

type windowClass struct {
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

type deviceListEntry struct {
	device uintptr
	typeID uint32
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterRawInputDevices = user32.NewProc("RegisterRawInputDevices")
	procGetRawInputDeviceList   = user32.NewProc("GetRawInputDeviceList")
	procGetRawInputData         = user32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfoW  = user32.NewProc("GetRawInputDeviceInfoW")
	procRegisterClassExW        = user32.NewProc("RegisterClassExW")
	procCreateWindowExW         = user32.NewProc("CreateWindowExW")
	procDefWindowProcW          = user32.NewProc("DefWindowProcW")
	procGetMessageW             = user32.NewProc("GetMessageW")
	procDispatchMessageW        = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW      = user32.NewProc("PostThreadMessageW")
	procMapVirtualKeyW          = user32.NewProc("MapVirtualKeyW")
	procGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadID      = kernel32.NewProc("GetCurrentThreadId")

	out   *bufio.Writer
	start = time.Now()
	names = map[uintptr]string{}
	// events holds every keyboard report in arrival order, so the summary can
	// answer whether a key-down arrived after the key's last key-up.
	events []event
)

type event struct {
	at     int64
	vk     int32
	up     bool
	device uintptr
}

func main() {
	// The message pump and its window must stay on one OS thread.
	runtime.LockOSThread()

	record := 25 * time.Second
	if len(os.Args) > 1 {
		if seconds, err := strconv.Atoi(os.Args[1]); err == nil && seconds > 0 {
			record = time.Duration(seconds) * time.Second
		}
	}

	path, err := logPath()
	if err != nil {
		fmt.Println("cannot place log:", err)
		return
	}
	file, err := os.Create(path)
	if err != nil {
		fmt.Println("cannot create log:", err)
		return
	}
	defer file.Close()
	out = bufio.NewWriter(file)
	defer out.Flush()

	logf("rawkeys started %s, recording for %s", time.Now().Format(time.RFC3339), record)
	listDevices()

	fmt.Printf("Recording keyboard events for %s into %s\n", record, path)
	fmt.Println("Hold your bind in the game, release it, and leave it alone until this closes itself.")
	logf("--- events (offset from start, key, state, device) ---")

	stopAfter(record)
	if err := pump(); err != nil {
		logf("stopped: %v", err)
		fmt.Println("stopped:", err)
	}
	summarize()
	fmt.Println("Done. Summary written to", path)
}

// stopAfter ends the recording without the user having to close the window, so
// the seconds after the key is released are always captured.
func stopAfter(d time.Duration) {
	threadID, _, _ := procGetCurrentThreadID.Call()
	go func() {
		time.Sleep(d)
		procPostThreadMessageW.Call(threadID, wmQuit, 0, 0)
	}()
}

// summarize reports, per key and per device, how the reports ended. The line
// that matters is whether a key-down arrived after that device's last key-up:
// such a report is a leftover repeat from a hold the user already ended.
func summarize() {
	type track struct {
		downs, ups          int
		firstDown, lastDown int64
		lastUp              int64
		downsAfterLastUp    int
		lastUpSeen          bool
	}
	tracks := map[string]*track{}
	order := []string{}
	for _, e := range events {
		key := fmt.Sprintf("%-9s dev=0x%-9X %s", keyName(e.vk), e.device, shortName(e.device))
		t, ok := tracks[key]
		if !ok {
			t = &track{firstDown: -1, lastUp: -1}
			tracks[key] = t
			order = append(order, key)
		}
		if e.up {
			t.ups++
			t.lastUp = e.at
			t.lastUpSeen = true
			t.downsAfterLastUp = 0
			continue
		}
		t.downs++
		if t.firstDown < 0 {
			t.firstDown = e.at
		}
		t.lastDown = e.at
		if t.lastUpSeen {
			t.downsAfterLastUp++
		}
	}
	sort.Strings(order)
	logf("--- summary after %dms ---", time.Since(start).Milliseconds())
	for _, key := range order {
		t := tracks[key]
		line := fmt.Sprintf("%s: %d down, %d up, first down %dms, last down %dms",
			key, t.downs, t.ups, t.firstDown, t.lastDown)
		if t.lastUpSeen {
			line += fmt.Sprintf(", last up %dms", t.lastUp)
		} else {
			line += ", NO UP EVER"
		}
		if t.downsAfterLastUp > 0 {
			line += fmt.Sprintf(", %d down AFTER the last up", t.downsAfterLastUp)
		}
		logf("%s", line)
	}
}

func logPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate exe: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "rawkeys.log"), nil
}

func logf(format string, args ...any) {
	fmt.Fprintf(out, format+"\r\n", args...)
	out.Flush()
}

// listDevices records every keyboard Windows knows about, so an event's handle
// can be read as a device instead of a number.
func listDevices() {
	var n uint32
	size := unsafe.Sizeof(deviceListEntry{})
	if ret, _, err := procGetRawInputDeviceList.Call(0, uintptr(unsafe.Pointer(&n)), size); ret == ^uintptr(0) {
		logf("GetRawInputDeviceList: %v", err)
		return
	}
	if n == 0 {
		logf("no input devices")
		return
	}
	list := make([]deviceListEntry, n)
	if ret, _, err := procGetRawInputDeviceList.Call(
		uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&n)), size,
	); ret == ^uintptr(0) {
		logf("GetRawInputDeviceList: %v", err)
		return
	}
	logf("--- keyboards ---")
	for i := uint32(0); i < n && i < uint32(len(list)); i++ {
		if list[i].typeID != rimTypeKeyboard {
			continue
		}
		logf("handle 0x%X %s", list[i].device, deviceName(list[i].device))
	}
}

func pump() error {
	className, _ := syscall.UTF16PtrFromString("EzRoKitRawKeys")
	instance, _, _ := procGetModuleHandleW.Call(0)
	class := windowClass{
		size:      uint32(unsafe.Sizeof(windowClass{})),
		wndProc:   syscall.NewCallback(wndProc),
		instance:  instance,
		className: className,
	}
	if ret, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); ret == 0 {
		return fmt.Errorf("RegisterClassExW: %w", err)
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
		return fmt.Errorf("CreateWindowExW: %w", err)
	}
	device := rawInputDevice{
		usagePage: 0x01,
		usage:     0x06,
		flags:     ridevInputSink | ridevDevNotify,
		target:    hwnd,
	}
	if ret, _, err := procRegisterRawInputDevices.Call(
		uintptr(unsafe.Pointer(&device)), 1, unsafe.Sizeof(device),
	); ret == 0 {
		return fmt.Errorf("RegisterRawInputDevices: %w", err)
	}
	for {
		var message rawMessage
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if ret == 0 || ret == ^uintptr(0) {
			return nil
		}
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch uint32(message) {
	case wmInput:
		record(lParam)
	case wmInputDeviceChange:
		change := "arrived"
		if wParam == 2 {
			change = "removed"
		}
		logf("%7dms device %s 0x%X %s", elapsed(), change, lParam, deviceName(lParam))
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return ret
}

func record(handle uintptr) {
	var size uint32
	procGetRawInputData.Call(handle, ridInput, 0, uintptr(unsafe.Pointer(&size)), unsafe.Sizeof(rawInputHeader{}))
	if size < uint32(unsafe.Sizeof(rawInput{})) {
		return
	}
	data := make([]byte, size)
	read, _, _ := procGetRawInputData.Call(
		handle, ridInput,
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
	vk := virtualKey(input.keyboard)
	up := input.keyboard.flags&riKeyBreak != 0
	state := "down"
	if up {
		state = "UP  "
	}
	device := input.header.device
	at := elapsed()
	events = append(events, event{at: at, vk: vk, up: up, device: device})
	logf("%7dms %-4s %-9s make=0x%02X flags=0x%02X msg=0x%04X dev=0x%X %s",
		at, state, keyName(vk), input.keyboard.makeCode, input.keyboard.flags,
		input.keyboard.message, device, shortName(device))
}

func elapsed() int64 {
	return time.Since(start).Milliseconds()
}

func keyName(vk int32) string {
	if vk >= '0' && vk <= 'Z' {
		return string(rune(vk))
	}
	return fmt.Sprintf("VK_0x%02X", vk)
}

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

// shortName trims the GUID tail off a device instance path so one event fits on
// one line.
func shortName(device uintptr) string {
	name, ok := names[device]
	if !ok {
		name = deviceName(device)
		names[device] = name
	}
	if name == "" {
		return "(no device)"
	}
	if cut := strings.Index(name, "#{"); cut > 0 {
		name = name[:cut]
	}
	return strings.TrimPrefix(name, `\\?\`)
}

func deviceName(device uintptr) string {
	if device == 0 {
		return ""
	}
	var size uint32
	ret, _, _ := procGetRawInputDeviceInfoW.Call(device, ridiDeviceName, 0, uintptr(unsafe.Pointer(&size)))
	if ret == ^uintptr(0) || size == 0 {
		return ""
	}
	buf := make([]uint16, size+1)
	ret, _, _ = procGetRawInputDeviceInfoW.Call(
		device, ridiDeviceName,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ret == ^uintptr(0) {
		return ""
	}
	return windows.UTF16ToString(buf)
}
