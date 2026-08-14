package runner

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"belarus-champ-tools/runner/internal/timing"
)

type clickerEvent struct {
	kind string
	vk   int32
	at   time.Time
}

type clickerTestSession struct {
	mu       sync.Mutex
	events   []clickerEvent
	failKeys bool
}

func (s *clickerTestSession) TapKey(vk int32, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failKeys {
		return fmt.Errorf("key failed")
	}
	s.events = append(s.events, clickerEvent{kind: "key", vk: vk, at: time.Now()})
	return nil
}
func (s *clickerTestSession) MouseClick(_ time.Duration) error {
	s.mu.Lock()
	s.events = append(s.events, clickerEvent{kind: "mouse", at: time.Now()})
	s.mu.Unlock()
	return nil
}
func (s *clickerTestSession) ClickerCycle(vk int32, _, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failKeys {
		return fmt.Errorf("key failed")
	}
	now := time.Now()
	s.events = append(s.events,
		clickerEvent{kind: "key", vk: vk, at: now},
		clickerEvent{kind: "mouse", at: now},
	)
	return nil
}
func (s *clickerTestSession) snapshot() []clickerEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]clickerEvent(nil), s.events...)
}

func TestClicker_AlwaysEmitsKeyThenMouse(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'D' }

	sess := &clickerTestSession{}
	r := New(Config{
		Session: sess,
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 5, MouseClick: true},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		r.Stop()
		r.Wait()
	}()

	deadline := time.Now().Add(300 * time.Millisecond)
	var events []clickerEvent
	for time.Now().Before(deadline) {
		events = sess.snapshot()
		if len(events) >= 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events) < 4 {
		t.Fatalf("expected two cycles, got %v", events)
	}
	for i, event := range events[:4] {
		want := "key"
		if i%2 == 1 {
			want = "mouse"
		}
		if event.kind != want {
			t.Fatalf("event %d = %q, want %q: %v", i, event.kind, want, events[:4])
		}
		if event.kind == "key" && event.vk != 'D' {
			t.Fatalf("event %d used key %d, want D", i, event.vk)
		}
	}
	if gap := events[2].at.Sub(events[1].at); gap < 4*time.Millisecond {
		t.Fatalf("next cycle started before configured sleep: %v", gap)
	}
}

func TestClicker_FirstPressedKeyHasPriorityAndHandsOff(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	held := map[int32]bool{}
	PhysicalKeyDown = func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return held[vk]
	}

	sess := &clickerTestSession{}
	r := New(Config{
		Session: sess,
		Slots: [ClickerSlotCount]ClickerSlot{
			// The long owner delay makes it obvious that T is waiting for
			// ownership rather than being independently fired.
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 1000, MouseClick: false},
			{TriggerVKs: [ClickerKeysPerBind]int32{'T'}, DelayMs: 5, MouseClick: false},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		r.Stop()
		r.Wait()
	}()

	mu.Lock()
	held['D'] = true
	mu.Unlock()
	waitForKeyEvent(t, sess, 'D', 200*time.Millisecond)

	mu.Lock()
	held['T'] = true
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	for _, event := range sess.snapshot() {
		if event.kind == "key" && event.vk == 'T' {
			t.Fatalf("T fired while first-pressed D was still held: %v", sess.snapshot())
		}
	}

	mu.Lock()
	held['D'] = false
	mu.Unlock()
	waitForKeyEvent(t, sess, 'T', 200*time.Millisecond)
}

func TestClicker_PriorityKeepsMousePairsIntact(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	held := map[int32]bool{}
	PhysicalKeyDown = func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return held[vk]
	}

	sess := &clickerTestSession{}
	r := New(Config{
		Session: sess,
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 1000, MouseClick: true},
			{TriggerVKs: [ClickerKeysPerBind]int32{'T'}, DelayMs: 5, MouseClick: true},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		r.Stop()
		r.Wait()
	}()

	mu.Lock()
	held['D'] = true
	held['T'] = true
	mu.Unlock()
	waitForKeyEvent(t, sess, 'D', 200*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	for _, event := range sess.snapshot() {
		if event.kind == "key" && event.vk == 'T' {
			t.Fatalf("T interrupted D ownership: %v", sess.snapshot())
		}
	}

	mu.Lock()
	held['D'] = false
	mu.Unlock()
	waitForKeyEvent(t, sess, 'T', 200*time.Millisecond)

	events := sess.snapshot()
	for i, event := range events {
		if event.kind == "mouse" && (i == 0 || events[i-1].kind != "key") {
			t.Fatalf("mouse was not immediately paired with a key: %v", events)
		}
	}
}

func waitForKeyEvent(t *testing.T, sess *clickerTestSession, vk int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, event := range sess.snapshot() {
			if event.kind == "key" && event.vk == vk {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for key %d: %v", vk, sess.snapshot())
}

func TestClicker_FailedKeyDoesNotEmitMouse(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(int32) bool { return true }

	sess := &clickerTestSession{failKeys: true}
	r := New(Config{
		Session: sess,
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 5, MouseClick: true},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	r.Stop()
	r.Wait()
	if events := sess.snapshot(); len(events) != 0 {
		t.Fatalf("failed key emitted events: %v", events)
	}
}

func TestClicker_UsesHardenedHoldDurations(t *testing.T) {
	if ClickerKeyTapHold <= 4*time.Millisecond {
		t.Fatalf("key hold %v is too short for HID visibility", ClickerKeyTapHold)
	}
	if ClickerClickHold < 2*5*time.Millisecond {
		t.Fatalf("mouse hold %v is too short for HID visibility", ClickerClickHold)
	}
	if sleepAfter(ClickerSlot{DelayMs: 77}) != 77*time.Millisecond {
		t.Fatalf("sleepAfter must use the configured delay")
	}
	if sleepAfter(ClickerSlot{}) != DefaultDelayMs*time.Millisecond {
		t.Fatalf("invalid delay must use the default")
	}
}

func TestSharedKeyTapHoldSpansHIDPoll(t *testing.T) {
	if timing.KeyTapHold <= timing.HIDPollInterval {
		t.Fatalf("generic key hold %v must be longer than HID poll %v", timing.KeyTapHold, timing.HIDPollInterval)
	}
	if timing.KeyTapHold != 2*timing.HIDPollInterval {
		t.Fatalf("generic key hold = %v, want two HID polls (%v)", timing.KeyTapHold, 2*timing.HIDPollInterval)
	}
}

func TestKeysText(t *testing.T) {
	if got := KeysText([ClickerKeysPerBind]int32{}); got != "none" {
		t.Fatalf("empty: got %q", got)
	}
	if got := KeysText([ClickerKeysPerBind]int32{'D', 'T'}); got != "D, T" {
		t.Fatalf("two keys: got %q", got)
	}
}
