//go:build windows

package runner

import "testing"

func TestPhysicalKeyboard_PerDeviceStateAggregatesAndReleases(t *testing.T) {
	p := &physicalKeyboard{
		devices:        make(map[uintptr]map[int32]bool),
		down:           make(map[int32]int),
		virtualDevices: make(map[uintptr]bool),
	}

	p.setKey(1, 'D', true)
	p.setKey(1, 'D', true) // autorepeat must not increase the count.
	if got := p.down['D']; got != 1 {
		t.Fatalf("after first device down count = %d, want 1", got)
	}

	p.setKey(2, 'D', true)
	if got := p.down['D']; got != 2 {
		t.Fatalf("after second device down count = %d, want 2", got)
	}

	p.setKey(1, 'D', false)
	if got := p.down['D']; got != 1 {
		t.Fatalf("after first device release count = %d, want 1", got)
	}

	p.removeDevice(2)
	if _, ok := p.down['D']; ok {
		t.Fatalf("device removal left D pressed: %v", p.down)
	}
}

func TestPhysicalKeyboard_PerDeviceDuplicateReleaseIsIgnored(t *testing.T) {
	p := &physicalKeyboard{
		devices:        make(map[uintptr]map[int32]bool),
		down:           make(map[int32]int),
		virtualDevices: make(map[uintptr]bool),
	}

	p.setKey(1, 'F', false)
	p.setKey(1, 'F', false)
	if len(p.down) != 0 {
		t.Fatalf("duplicate release created state: %v", p.down)
	}
}
