package runner

import (
	"context"
	"fmt"
	"time"

	"ezrokit/runner/internal/timing"

	"github.com/Alia5/VIIPER/device/keyboard"
)

var keyNames = map[int32]string{
	0x08: "Backspace",
	0x09: "Tab",
	0x0D: "Enter",
	0x1B: "Escape",
	0x20: "Space",
	0x21: "PageUp", 0x22: "PageDown", 0x23: "End", 0x24: "Home",
	0x25: "Left",
	0x26: "Up",
	0x27: "Right",
	0x28: "Down",
	0x2D: "Insert",
	0x2E: "Delete",
	0x30: "0", 0x31: "1", 0x32: "2", 0x33: "3", 0x34: "4",
	0x35: "5", 0x36: "6", 0x37: "7", 0x38: "8", 0x39: "9",
	0x41: "A", 0x42: "B", 0x43: "C", 0x44: "D", 0x45: "E",
	0x46: "F", 0x47: "G", 0x48: "H", 0x49: "I", 0x4A: "J",
	0x4B: "K", 0x4C: "L", 0x4D: "M", 0x4E: "N", 0x4F: "O",
	0x50: "P", 0x51: "Q", 0x52: "R", 0x53: "S", 0x54: "T",
	0x55: "U", 0x56: "V", 0x57: "W", 0x58: "X", 0x59: "Y",
	0x5A: "Z",
	0x60: "Numpad0", 0x61: "Numpad1", 0x62: "Numpad2", 0x63: "Numpad3",
	0x64: "Numpad4", 0x65: "Numpad5", 0x66: "Numpad6", 0x67: "Numpad7",
	0x68: "Numpad8", 0x69: "Numpad9",
	0x70: "F1", 0x71: "F2", 0x72: "F3", 0x73: "F4", 0x74: "F5",
	0x75: "F6", 0x76: "F7", 0x77: "F8", 0x78: "F9", 0x79: "F10",
	0x7A: "F11", 0x7B: "F12",
	0xBA: "Semicolon", 0xBB: "Equals", 0xBC: "Comma", 0xBD: "Minus",
	0xBE: "Period", 0xBF: "Slash", 0xC0: "Grave",
	0xDB: "LeftBracket", 0xDC: "Backslash", 0xDD: "RightBracket",
	0xDE: "Quote",
}

var vkToHID = map[int32]uint8{
	0x41: keyboard.KeyA, 0x42: keyboard.KeyB, 0x43: keyboard.KeyC, 0x44: keyboard.KeyD,
	0x45: keyboard.KeyE, 0x46: keyboard.KeyF, 0x47: keyboard.KeyG, 0x48: keyboard.KeyH,
	0x49: keyboard.KeyI, 0x4A: keyboard.KeyJ, 0x4B: keyboard.KeyK, 0x4C: keyboard.KeyL,
	0x4D: keyboard.KeyM, 0x4E: keyboard.KeyN, 0x4F: keyboard.KeyO, 0x50: keyboard.KeyP,
	0x51: keyboard.KeyQ, 0x52: keyboard.KeyR, 0x53: keyboard.KeyS, 0x54: keyboard.KeyT,
	0x55: keyboard.KeyU, 0x56: keyboard.KeyV, 0x57: keyboard.KeyW, 0x58: keyboard.KeyX,
	0x59: keyboard.KeyY, 0x5A: keyboard.KeyZ,
	0x30: keyboard.Key0, 0x31: keyboard.Key1, 0x32: keyboard.Key2, 0x33: keyboard.Key3,
	0x34: keyboard.Key4, 0x35: keyboard.Key5, 0x36: keyboard.Key6, 0x37: keyboard.Key7,
	0x38: keyboard.Key8, 0x39: keyboard.Key9,
	0x20: keyboard.KeySpace,
	0x0D: keyboard.KeyEnter,
	0x08: keyboard.KeyBackspace,
	0x09: keyboard.KeyTab,
	0x1B: keyboard.KeyEscape,
	0x25: keyboard.KeyLeft, 0x26: keyboard.KeyUp,
	0x27: keyboard.KeyRight, 0x28: keyboard.KeyDown,
	0x70: keyboard.KeyF1, 0x71: keyboard.KeyF2, 0x72: keyboard.KeyF3, 0x73: keyboard.KeyF4,
	0x74: keyboard.KeyF5, 0x75: keyboard.KeyF6, 0x76: keyboard.KeyF7, 0x77: keyboard.KeyF8,
	0x78: keyboard.KeyF9, 0x79: keyboard.KeyF10, 0x7A: keyboard.KeyF11, 0x7B: keyboard.KeyF12,
}

func KeyName(vk int32) string {
	if name, ok := keyNames[vk]; ok {
		return name
	}
	return fmt.Sprintf("VK_0x%02X", vk)
}

func VKToHID(vk int32) (uint8, bool) {
	hid, ok := vkToHID[vk]
	return hid, ok
}

// WaitForKeyPress waits for the user to press AND release a key (a complete
// tap), and returns the virtual-key code of that key. Returns ok=false on
// timeout.
//
// The two-phase approach (press → wait for release → return) prevents a
// running clicker from immediately triggering when the bound key is added
// to the runner while the user is still holding it. The binding only takes
// effect after the key is released.
//
// Phase 1 — detect a rising edge (key transitions from not-pressed to
// pressed), ignoring any keys already held down when the function started.
// Phase 2 — wait for that specific key to be released.
func WaitForKeyPress(timeout time.Duration) (int32, bool) {
	return WaitForKeyPressContext(context.Background(), timeout)
}

// WaitForKeyPressContext is the cancellable form of WaitForKeyPress. It
// preserves the press-and-release binding semantics while allowing GUI
// shutdown to terminate the polling goroutine immediately.
func WaitForKeyPressContext(ctx context.Context, timeout time.Duration) (int32, bool) {
	deadline := time.Now().Add(timeout)

	// Track the previous state of each key so we can detect rising edges.
	prev := make(map[int32]bool, len(keyNames))
	for vk := range keyNames {
		if PhysicalKeyDown(vk) {
			prev[vk] = true
		}
	}

	// Phase 1: wait for any rising edge (new key press).
	for {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return 0, false
		}
		for vk := range keyNames {
			nowDown := PhysicalKeyDown(vk)
			if nowDown && !prev[vk] {
				// Found a new key press. Phase 2: wait for its release
				// before returning, so the binder doesn't sync the runner
				// while the key is still held down.
				for PhysicalKeyDown(vk) {
					if ctx.Err() != nil || time.Now().After(deadline) {
						return 0, false
					}
					timing.Sleep(ctx, timing.PollInterval)
				}
				if ctx.Err() != nil {
					return 0, false
				}
				return vk, true
			}
			prev[vk] = nowDown
		}
		timing.Sleep(ctx, timing.PollInterval)
	}
}

// StartToggleKeyWatcher starts a background goroutine that polls the start/stop
// toggle keys (End and F12) and calls onToggle(vk) each time one transitions
// from released to pressed. The callback runs in the polling goroutine — the
// caller must marshal to the GUI thread if needed. Stop by cancelling ctx.
func StartToggleKeyWatcher(ctx context.Context, onToggle func(vk int32)) {
	go func() {
		down := make([]bool, len(timing.ToggleVKs))
		for ctx.Err() == nil {
			for i, vk := range timing.ToggleVKs {
				if PollKeyToggle(&down[i], vk) {
					onToggle(vk)
				}
			}
			timing.Sleep(ctx, timing.PollInterval)
		}
	}()
}
