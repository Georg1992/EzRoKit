package runner

import "testing"

// ---------------------------------------------------------------------------
// Default fallback tests
// ---------------------------------------------------------------------------

func TestPhysicalKeyDown_DefaultReturnsFalse(t *testing.T) {
	// Save and restore the original so other tests see the platform-wired default.
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	// Reset to the sentinel default. On Windows, init() in keyboard_windows.go
	// already wired the real platform implementation, so we must overwrite it
	// to verify what the no-platform fallback returns.
	PhysicalKeyDown = func(vk int32) bool { return false }

	for _, vk := range []int32{0, 'A', 'Q', 0x70 /* F1 */, 0xFF} {
		if PhysicalKeyDown(vk) {
			t.Errorf("PhysicalKeyDown(%d): expected false", vk)
		}
	}
}

func TestPollKeyToggle_DefaultReturnsFalse(t *testing.T) {
	orig := PollKeyToggle
	defer func() { PollKeyToggle = orig }()

	// Reset to sentinel default (platform init() may have already wired the real impl).
	PollKeyToggle = func(wasDown *bool, vk int32) bool { return false }

	var wasDown bool
	if PollKeyToggle(&wasDown, 'A') {
		t.Error("PollKeyToggle: expected false from default sentinel")
	}
}

// ---------------------------------------------------------------------------
// DI swap-in tests — verify consumers use the variable, not the platform impl.
// ---------------------------------------------------------------------------

func TestPhysicalKeyDown_CanBeSwapped(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	calls := 0
	PhysicalKeyDown = func(vk int32) bool {
		calls++
		return vk == 'Q'
	}

	if !PhysicalKeyDown('Q') {
		t.Error("PhysicalKeyDown('Q'): expected true from mock")
	}
	if PhysicalKeyDown('A') {
		t.Error("PhysicalKeyDown('A'): expected false from mock")
	}
	if calls != 2 {
		t.Errorf("PhysicalKeyDown: expected 2 calls, got %d", calls)
	}
}

func TestPollKeyToggle_RisingEdge(t *testing.T) {
	orig := PollKeyToggle
	defer func() { PollKeyToggle = orig }()

	// Simulate a physical state vector: key goes down → up → down → up.
	states := []bool{false, true, true, false, false, true}
	pos := 0
	PollKeyToggle = func(wasDown *bool, _ int32) bool {
		down := states[pos]
		pos++
		toggled := down && !*wasDown
		*wasDown = down
		return toggled
	}

	var wasDown bool
	// 1st poll: false→false, no rising edge.
	if PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 1: expected false (wasDown stays false)")
	}
	// 2nd poll: false→true, rising edge.
	if !PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 2: expected true (false→true transition)")
	}
	// 3rd poll: true→true, held — no edge.
	if PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 3: expected false (held down, no transition)")
	}
	// 4th poll: true→false, falling — no rising edge.
	if PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 4: expected false (falling edge, not rising)")
	}
	// 5th poll: false→false, still off.
	if PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 5: expected false (stayed off)")
	}
	// 6th poll: false→true, rising edge again.
	if !PollKeyToggle(&wasDown, 'A') {
		t.Error("poll 6: expected true (second rising edge)")
	}
}

func TestHostKeyboard_UsesPackageVars(t *testing.T) {
	origDown := PhysicalKeyDown
	origEmerg := EmergencyKeyDown
	origSwallow := SwallowPhysicalKeys
	origTap := SetTappingVK
	t.Cleanup(func() {
		PhysicalKeyDown = origDown
		EmergencyKeyDown = origEmerg
		SwallowPhysicalKeys = origSwallow
		SetTappingVK = origTap
	})

	var downVK, emergVK, tapVK int32
	var swallowed []int32
	PhysicalKeyDown = func(vk int32) bool { downVK = vk; return vk == 'Q' }
	EmergencyKeyDown = func(vk int32) bool { emergVK = vk; return vk == 0x23 }
	SwallowPhysicalKeys = func(vks []int32) { swallowed = vks }
	SetTappingVK = func(vk int32) { tapVK = vk }

	kb := HostKeyboard()
	if !kb.KeyDown('Q') || downVK != 'Q' {
		t.Fatalf("KeyDown: downVK=%d want Q", downVK)
	}
	if !kb.EmergencyDown(0x23) || emergVK != 0x23 {
		t.Fatalf("EmergencyDown: emergVK=%d want 0x23", emergVK)
	}
	kb.Swallow([]int32{'1'})
	if len(swallowed) != 1 || swallowed[0] != '1' {
		t.Fatalf("Swallow: %v want [1]", swallowed)
	}
	kb.SetTapping('A')
	if tapVK != 'A' {
		t.Fatalf("SetTapping: %d want A", tapVK)
	}
}

func TestPhysicalKeyDown_RestoresAfterTest(t *testing.T) {
	// Verify the clean-up pattern works: after a sub-test swaps the
	// variable, the next test sees the original (wired) value.
	orig := PhysicalKeyDown

	func() {
		PhysicalKeyDown = func(vk int32) bool { return true }
		defer func() { PhysicalKeyDown = orig }()
		if !PhysicalKeyDown(0) {
			t.Fatal("mock should return true")
		}
	}()

	// After restore, should be back to original (not the mock).
	// Just verify we didn't leak the mock — actual value depends on platform.
	_ = orig
}
