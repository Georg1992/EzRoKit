//go:build windows

package runner

import "testing"

const (
	asusMI00  = `\\?\HID#VID_0B05&PID_194B&MI_00#8&4d1c94b&0&0000#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`
	asusMI03  = `\\?\HID#VID_0B05&PID_194B&MI_03#8&2c753fe&0&0000#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`
	mouseKeys = `\\?\HID#VID_09DA&PID_9090&MI_00&Col01#8&1246f108&0&0000#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`
	aggregate = `\\?\Microsoft Keyboard RID\0`
	viiperKbd = `\\?\HID#VID_2E8A&PID_0010#3&24da644d&1&0000#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`
)

// newTestKeyboard classifies each handle the way classifyKeyboards does at
// startup, so no test event has to name a device.
func newTestKeyboard(handles map[uintptr]string) *physicalKeyboard {
	p := newPhysicalKeyboard()
	for device, name := range handles {
		p.keyboard[device] = keyboardFromName(name)
	}
	return p
}

// oneKeyboard is this machine's real layout: one keyboard reporting through two
// HID collections, plus the aggregate pseudo-keyboard Windows always exposes.
func oneKeyboard() *physicalKeyboard {
	return newTestKeyboard(map[uintptr]string{
		0x1003F: asusMI00,
		0x10041: asusMI03,
		0xBF80C: aggregate,
	})
}

func keyHeld(p *physicalKeyboard, vk int32) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, held := p.held[vk]
	return held
}

func TestKeyboardFromName_CollectionsOfOneKeyboardShareAnID(t *testing.T) {
	first, second := keyboardFromName(asusMI00), keyboardFromName(asusMI03)
	if first != second {
		t.Fatalf("collections of one keyboard got ids %q and %q", first, second)
	}
	if first != "VID_0B05&PID_194B" {
		t.Fatalf("keyboard id = %q, want VID_0B05&PID_194B", first)
	}
	if other := keyboardFromName(mouseKeys); other == first {
		t.Fatalf("a different device shares the keyboard id %q", other)
	}
	if id := keyboardFromName(`\\?\ACPI#PNP0303#4&1a2b3c4d&0#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`); id != "ACPI#PNP0303" {
		t.Fatalf("PS/2 keyboard id = %q, want ACPI#PNP0303", id)
	}
}

// Nothing but real hardware may hold a bind: the aggregate identifies no device
// and VIIPER types the very keys the user binds.
func TestKeyboardFromName_RejectsWhatCannotHoldABind(t *testing.T) {
	for _, name := range []string{aggregate, viiperKbd, ``, `Microsoft Keyboard RID`, `\\?\HID#`} {
		if id := keyboardFromName(name); id != "" {
			t.Fatalf("%q identified hardware as %q", name, id)
		}
	}
}

func TestPhysicalKeyboard_PressAndRelease(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 'E', true)
	if !keyHeld(p, 'E') {
		t.Fatal("press did not hold E")
	}
	p.applyKey(0x1003F, 'E', false)
	if keyHeld(p, 'E') {
		t.Fatal("release left E held")
	}
}

// Windows can report the press on one HID collection and the release on another.
// Both belong to the same keyboard, so the release must count.
func TestPhysicalKeyboard_ReleaseOnAnotherCollectionOfSameKeyboard(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 'E', true)
	p.applyKey(0x10041, 'E', false)
	if keyHeld(p, 'E') {
		t.Fatal("release on the keyboard's other collection left E held")
	}
}

// A held key repeats about every 31ms, and each repeat is a fresh key-down. They
// must not disturb the hold, and the release still has to end it.
func TestPhysicalKeyboard_AutoRepeatThenRelease(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 'E', true)
	for i := 0; i < 200; i++ {
		p.applyKey(0x1003F, 'E', true)
	}
	if !keyHeld(p, 'E') {
		t.Fatal("repeats dropped the hold")
	}
	p.applyKey(0x1003F, 'E', false)
	if keyHeld(p, 'E') {
		t.Fatal("release after 200 repeats left E held")
	}
}

// The aggregate handle can report another device's keys, including VIIPER's, so
// it may neither start nor end a hold.
func TestPhysicalKeyboard_AggregateCannotHoldOrRelease(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0xBF80C, 'E', true)
	if keyHeld(p, 'E') {
		t.Fatal("aggregate keyboard held E")
	}
	p.applyKey(0x1003F, 'E', true)
	p.applyKey(0xBF80C, 'E', false)
	if !keyHeld(p, 'E') {
		t.Fatal("aggregate keyboard released a hold it does not own")
	}
}

// VIIPER taps the bind key many times per second while the clicker runs. None of
// that may look like the user pressing or letting go.
func TestPhysicalKeyboard_VirtualKeyboardCannotHoldOrRelease(t *testing.T) {
	p := newTestKeyboard(map[uintptr]string{0x1003F: asusMI00, 0xD1F0111: viiperKbd})
	p.applyKey(0xD1F0111, 'E', true)
	if keyHeld(p, 'E') {
		t.Fatal("VIIPER held E")
	}
	p.applyKey(0x1003F, 'E', true)
	for i := 0; i < 20; i++ {
		p.applyKey(0xD1F0111, 'E', true)
		p.applyKey(0xD1F0111, 'E', false)
	}
	if !keyHeld(p, 'E') {
		t.Fatal("a VIIPER tap ended the physical hold")
	}
	p.applyKey(0x1003F, 'E', false)
	if keyHeld(p, 'E') {
		t.Fatal("physical release left E held")
	}
}

// An event with no device behind it identifies no keyboard.
func TestPhysicalKeyboard_EventWithNoDeviceIsIgnored(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0, 'E', true)
	if keyHeld(p, 'E') {
		t.Fatal("key-down with no device held E")
	}
	p.applyKey(0x1003F, 'E', true)
	p.applyKey(0, 'E', false)
	if !keyHeld(p, 'E') {
		t.Fatal("key-up with no device released a hold it does not own")
	}
}

func TestPhysicalKeyboard_MouseButtonsAreNotBinds(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 0x01, true) // VK_LBUTTON
	if keyHeld(p, 0x01) {
		t.Fatal("a mouse button was tracked as a bind")
	}
}

func TestPhysicalKeyboard_KeysAreIndependent(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 'E', true)
	p.applyKey(0x1003F, 'W', true)
	p.applyKey(0x1003F, 'W', false)
	if !keyHeld(p, 'E') {
		t.Fatal("another key released E")
	}
	if keyHeld(p, 'W') {
		t.Fatal("release left W held")
	}
}

// Unplugging the keyboard means no release can ever arrive, so the hold drops —
// but only once the keyboard's last collection is gone.
func TestPhysicalKeyboard_UnpluggedKeyboardDropsItsHold(t *testing.T) {
	p := oneKeyboard()
	p.applyKey(0x1003F, 'E', true)
	p.removeDevice(0x1003F)
	if !keyHeld(p, 'E') {
		t.Fatal("one collection going away dropped a hold the keyboard can still release")
	}
	p.removeDevice(0x10041)
	if keyHeld(p, 'E') {
		t.Fatal("unplugged keyboard left E held")
	}
}

// VIIPER's keyboard attaches long after startup, so the handle that shows up
// then must be classified as unable to hold a bind, while a keyboard the user
// plugs in mid-session can hold one.
func TestPhysicalKeyboard_DeviceArrivingLater(t *testing.T) {
	p := oneKeyboard()
	if board, line := p.classify(0xD1F0111, viiperKbd); board != "" {
		t.Fatalf("VIIPER arriving later became keyboard %q (%s)", board, line)
	}
	p.applyKey(0xD1F0111, 'E', true)
	if keyHeld(p, 'E') {
		t.Fatal("VIIPER arriving later held E")
	}

	second := `\\?\HID#VID_046D&PID_C31C&MI_00#7&1e2b3c4d&0&0000#{884b96c3-56ef-11d1-bc8c-00a0c91405dd}`
	if board, _ := p.classify(0x20055, second); board != "VID_046D&PID_C31C" {
		t.Fatalf("second keyboard arriving later got id %q", board)
	}
	p.applyKey(0x20055, 'W', true)
	if !keyHeld(p, 'W') {
		t.Fatal("keyboard plugged in mid-session cannot hold a bind")
	}
	p.applyKey(0x20055, 'W', false)
	if keyHeld(p, 'W') {
		t.Fatal("release left W held")
	}
}

func TestIsVirtualKeyboardName(t *testing.T) {
	if !isVirtualKeyboardName(viiperKbd) {
		t.Fatal("VIIPER VID should be virtual")
	}
	if !isVirtualKeyboardName(`VIIPER HID Keyboard`) {
		t.Fatal("VIIPER product name should be virtual")
	}
	if !isVirtualKeyboardName(`\\?\USB#VID_2E8A&PID_0010#USBIP`) {
		t.Fatal("usbip-win2 path should be virtual")
	}
	if isVirtualKeyboardName(asusMI00) {
		t.Fatal("physical keyboard classified as virtual")
	}
}
