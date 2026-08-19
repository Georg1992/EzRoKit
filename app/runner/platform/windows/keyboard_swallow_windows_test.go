//go:build windows

package runner

import (
	"testing"
	"unsafe"
)

func TestKeyboardInputSizeMatchesSendInput(t *testing.T) {
	if n := unsafe.Sizeof(keyboardInput{}); n != 40 {
		t.Fatalf("sizeof(keyboardInput) = %d, want 40", n)
	}
}

func TestSwallowPhysicalKeys_BlocksListedKeys(t *testing.T) {
	t.Cleanup(func() { SwallowPhysicalKeys(nil) })

	SwallowPhysicalKeys([]int32{'1', 'A', 0})
	if !swallowed('1') {
		t.Fatal("trigger 1 was not swallowed")
	}
	if !swallowed('A') {
		t.Fatal("trigger A was not swallowed")
	}
	if swallowed(0) {
		t.Fatal("VK 0 must not be swallowed")
	}
	if swallowed('2') {
		t.Fatal("unlisted key 2 was swallowed")
	}
	if !physicalKeyBlocked('1') {
		t.Fatal("trigger 1 was not blocked")
	}
	if physicalKeyBlocked('2') {
		t.Fatal("unlisted key 2 was blocked")
	}
	if physicalKeyBlocked(0x23) { // VK_END
		t.Fatal("End was blocked")
	}

	SwallowPhysicalKeys(nil)
	if swallowed('1') || swallowed('A') {
		t.Fatal("clear left keys swallowed")
	}
}

func TestSwallowPhysicalKeys_ReplacesTheSet(t *testing.T) {
	t.Cleanup(func() { SwallowPhysicalKeys(nil) })

	SwallowPhysicalKeys([]int32{'1'})
	SwallowPhysicalKeys([]int32{'2'})
	if swallowed('1') {
		t.Fatal("old trigger stayed swallowed")
	}
	if !swallowed('2') {
		t.Fatal("new trigger was not swallowed")
	}
}

func TestConsumePhysicalKey_EatsTriggerDownAndQueuesKeyUp(t *testing.T) {
	t.Cleanup(func() {
		SwallowPhysicalKeys(nil)
		queueForcedKeyUp = postForceKeyUp
		SetTappingVK(0)
		rawState.setHeldFromHook('1', false)
	})
	var got []int32
	queueForcedKeyUp = func(vk int32) { got = append(got, vk) }
	SwallowPhysicalKeys([]int32{'1'})

	if !consumePhysicalKey('1', true) {
		t.Fatal("trigger down was not eaten")
	}
	if len(got) != 1 || got[0] != '1' {
		t.Fatalf("queued key-up = %v, want [1]", got)
	}
	if !PhysicalKeyDown('1') {
		t.Fatal("eaten trigger down did not hold")
	}
	if consumePhysicalKey('1', false) {
		t.Fatal("trigger up was eaten")
	}
	if PhysicalKeyDown('1') {
		t.Fatal("trigger up left 1 held")
	}
	if consumePhysicalKey('2', true) {
		t.Fatal("non-trigger down was eaten")
	}
}

func TestShouldInjectKeyUp_SkipsWhileTappingSameKey(t *testing.T) {
	t.Cleanup(func() { SetTappingVK(0) })

	if !shouldInjectKeyUp('1') {
		t.Fatal("idle should inject key-up of 1")
	}
	SetTappingVK('1')
	if shouldInjectKeyUp('1') {
		t.Fatal("injected key-up while tapping 1")
	}
	if !shouldInjectKeyUp('3') {
		t.Fatal("blocked key-up of 3 while tapping 1")
	}
}

func TestConsumePhysicalKey_LetsChainTriggerTapThrough(t *testing.T) {
	t.Cleanup(func() {
		SwallowPhysicalKeys(nil)
		queueForcedKeyUp = postForceKeyUp
		SetTappingVK(0)
		rawState.setHeldFromHook('1', false)
	})
	var got []int32
	queueForcedKeyUp = func(vk int32) { got = append(got, vk) }
	SwallowPhysicalKeys([]int32{'1'})

	if !consumePhysicalKey('1', true) {
		t.Fatal("physical trigger down was not eaten")
	}
	if !PhysicalKeyDown('1') {
		t.Fatal("physical trigger down did not hold")
	}

	SetTappingVK('1')
	if consumePhysicalKey('1', true) {
		t.Fatal("chain tap down of 1 was eaten")
	}
	if consumePhysicalKey('1', false) {
		t.Fatal("chain tap up of 1 was eaten")
	}
	if !PhysicalKeyDown('1') {
		t.Fatal("chain tap up released the physical hold")
	}
	if len(got) != 1 || got[0] != '1' {
		t.Fatalf("queued key-up during chain tap = %v, want [1]", got)
	}
}
