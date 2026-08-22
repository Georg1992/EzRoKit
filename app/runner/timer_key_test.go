package runner

import "testing"

func TestTimerConfig_AnyActive(t *testing.T) {
	if (TimerKeyConfig{}).AnyActive() {
		t.Fatal("no keys should not be active")
	}
	if !(TimerKeyConfig{Slots: [TimerKeySlotCount]TimerSlot{{KeyVK: 'Q'}}}).AnyActive() {
		t.Fatal("mapped key should be active")
	}
}
