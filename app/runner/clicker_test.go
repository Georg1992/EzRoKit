package runner

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"ezrokit/runner/internal/timing"
)

type clickerEvent struct {
	kind string
	vk   int32
	at   time.Time
}

type clickerTestSession struct {
	mu         sync.Mutex
	events     []clickerEvent
	failKeys   bool
	resetCount int
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
func (s *clickerTestSession) Reset() {
	s.mu.Lock()
	s.resetCount++
	s.mu.Unlock()
}
func (s *clickerTestSession) KeyDownThenMouseClick(vk int32, _, _ time.Duration) error {
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
func (s *clickerTestSession) resets() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resetCount
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

func TestClicker_ReleaseSendsKeyUp(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	held := true
	PhysicalKeyDown = func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return held && vk == 'D'
	}

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

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && len(sess.snapshot()) < 2 {
		time.Sleep(time.Millisecond)
	}
	if len(sess.snapshot()) < 2 {
		t.Fatal("clicker never fired while held")
	}

	mu.Lock()
	held = false
	mu.Unlock()

	deadline = time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && sess.resets() == 0 {
		time.Sleep(time.Millisecond)
	}
	if sess.resets() == 0 {
		t.Fatal("release did not send key-up")
	}

	n := len(sess.snapshot())
	time.Sleep(40 * time.Millisecond)
	if got := len(sess.snapshot()); got != n {
		t.Fatalf("spam continued after release: %d events then %d", n, got)
	}
}

func TestClicker_SecondBindWaitsForFirstCycle(t *testing.T) {
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
	held['T'] = true
	mu.Unlock()

	waitForKeyEvent(t, sess, 'D', 200*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	if events := sess.snapshot(); countKeyEvents(events, 'T') != 0 {
		t.Fatalf("T ran during D's delay: %v", events)
	}
}

func TestClicker_UnboundPhysicalKeyDoesNotInterruptTrigger(t *testing.T) {
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
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 5, MouseClick: false},
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

	before := countKeyEvents(sess.snapshot(), 'D')
	mu.Lock()
	held['X'] = true
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	held['X'] = false
	mu.Unlock()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if countKeyEvents(sess.snapshot(), 'D') > before {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("unbound X interrupted held D: before=%d after=%d events=%v", before, countKeyEvents(sess.snapshot(), 'D'), sess.snapshot())
}

func countKeyEvents(events []clickerEvent, vk int32) int {
	n := 0
	for _, event := range events {
		if event.kind == "key" && event.vk == vk {
			n++
		}
	}
	return n
}

func TestClicker_NeverEmitsTwoMiceInARow(t *testing.T) {
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
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 5, MouseClick: true},
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

	deadline := time.Now().Add(200 * time.Millisecond)
	var events []clickerEvent
	for time.Now().Before(deadline) {
		events = sess.snapshot()
		if len(events) >= 8 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events) < 4 {
		t.Fatalf("expected several cycles, got %v", events)
	}
	for i, event := range events {
		if event.kind == "mouse" && (i == 0 || events[i-1].kind != "key") {
			t.Fatalf("mouse was not immediately preceded by a key: %v", events)
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

func TestClicker_ReleasedTriggerStopsImmediately(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	held := true
	PhysicalKeyDown = func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return vk == 'D' && held
	}

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

	waitForKeyEvent(t, sess, 'D', 200*time.Millisecond)
	mu.Lock()
	held = false
	mu.Unlock()
	time.Sleep(timing.PollInterval + 20*time.Millisecond)
	stoppedAt := countKeyEvents(sess.snapshot(), 'D')
	time.Sleep(50 * time.Millisecond)
	if got := countKeyEvents(sess.snapshot(), 'D'); got != stoppedAt {
		t.Fatalf("released trigger kept cycling: %d then %d events", stoppedAt, got)
	}
}

func TestClicker_EmergencyToggleStopsRunawayClicker(t *testing.T) {
	origPhysical := PhysicalKeyDown
	origEmergency := EmergencyKeyDown
	defer func() {
		PhysicalKeyDown = origPhysical
		EmergencyKeyDown = origEmergency
	}()

	var mu sync.Mutex
	emergency := false
	PhysicalKeyDown = func(int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return true
	}
	EmergencyKeyDown = func(int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return emergency
	}

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

	waitForKeyEvent(t, sess, 'D', 200*time.Millisecond)
	mu.Lock()
	emergency = true
	mu.Unlock()

	deadline := time.Now().Add(200 * time.Millisecond)
	for r.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Running() {
		r.Stop()
		r.Wait()
		t.Fatal("emergency toggle did not stop clicker")
	}
	stoppedAt := len(sess.snapshot())
	time.Sleep(50 * time.Millisecond)
	if got := len(sess.snapshot()); got != stoppedAt {
		t.Fatalf("clicker emitted after emergency stop: %d then %d events", stoppedAt, got)
	}
	r.Wait()
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
	deadline := time.Now().Add(200 * time.Millisecond)
	for r.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Running() {
		r.Stop()
		r.Wait()
		t.Fatal("clicker kept retrying after a failed key")
	}
	r.Wait()
	if events := sess.snapshot(); len(events) != 0 {
		t.Fatalf("failed key emitted events: %v", events)
	}
}

func TestClicker_UsesConfiguredDelay(t *testing.T) {
	if ClickerHold != 10*time.Millisecond {
		t.Fatalf("clicker hold = %v, want 10ms so a 60fps game can see the down", ClickerHold)
	}
	if slotDelay(ClickerSlot{DelayMs: 77}) != 77*time.Millisecond {
		t.Fatalf("slotDelay must use the configured delay")
	}
	if slotDelay(ClickerSlot{}) != DefaultDelayMs*time.Millisecond {
		t.Fatalf("invalid delay must use the default")
	}
}

func TestSharedKeyTapHoldSpansHIDPoll(t *testing.T) {
	if timing.KeyTapHold != timing.HIDPollInterval {
		t.Fatalf("generic key hold = %v, want one HID poll (%v)", timing.KeyTapHold, timing.HIDPollInterval)
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

func TestVKToHID_CoversEveryBindableKey(t *testing.T) {
	for vk, name := range keyNames {
		if _, ok := VKToHID(vk); !ok {
			t.Errorf("bindable key %s (VK 0x%02X) has no HID mapping", name, vk)
		}
	}
}

type splitOnlySession struct {
	keys int
}

func (s *splitOnlySession) TapKey(int32, time.Duration) error { s.keys++; return nil }
func (s *splitOnlySession) Reset()                            {}

func TestClicker_MouseClickRequiresOrderedSession(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(int32) bool { return true }

	sess := &splitOnlySession{}
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
	deadline := time.Now().Add(200 * time.Millisecond)
	for r.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if r.Running() {
		r.Stop()
		r.Wait()
		t.Fatal("clicker kept running without an ordered session")
	}
	r.Wait()
	if sess.keys != 0 {
		t.Fatalf("unordered path sent input: keys=%d", sess.keys)
	}
}
