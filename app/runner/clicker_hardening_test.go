package runner

import (
	"sync"
	"testing"
	"time"

	"ezrokit/runner/internal/timing"
)

// cycleClickerSession records what the game would receive. A cycle is one call,
// so an interleaved event from another runner cannot land inside it.
type cycleClickerSession struct {
	mu     sync.Mutex
	events []string
	holds  []time.Duration
	cycles int
	resets int
}

func (s *cycleClickerSession) TapKey(int32, time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "other-key")
	return nil
}

func (s *cycleClickerSession) Reset() {
	s.mu.Lock()
	s.resets++
	s.mu.Unlock()
}

func (s *cycleClickerSession) TapKeyWithClick(_ int32, hold time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cycles++
	s.events = append(s.events, "key")
	s.holds = append(s.holds, hold)
	s.events = append(s.events, "mouse")
	s.holds = append(s.holds, hold)
	return nil
}

func (s *cycleClickerSession) snapshot() ([]string, []time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...), append([]time.Duration(nil), s.holds...)
}

func (s *cycleClickerSession) cycleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cycles
}

func (s *cycleClickerSession) resetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resets
}

func TestClicker_FlowHasNoExtraActionBetweenMouseAndSleep(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'D' }

	sess := &cycleClickerSession{}
	r := New(Config{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 25, MouseClick: true},
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
	var events []string
	var holds []time.Duration
	for time.Now().Before(deadline) {
		events, holds = sess.snapshot()
		if len(events) >= 6 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(events) < 6 {
		t.Fatalf("expected three complete cycles, got %v", events)
	}
	for i := 0; i < 6; i++ {
		want := "key"
		if i%2 == 1 {
			want = "mouse"
		}
		if events[i] != want {
			t.Fatalf("event %d = %q, want %q: %v", i, events[i], want, events)
		}
	}
	for i, hold := range holds[:6] {
		if hold != timing.KeyTapHold {
			t.Fatalf("hold %d = %v, want %v", i, hold, timing.KeyTapHold)
		}
	}
}

// A cycle only starts while the bind is physically held, so a release stops the
// clicker without needing the session to re-check anything mid-write.
func TestClicker_NoCycleStartsAfterTheHoldDrops(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	checks := 0
	PhysicalKeyDown = func(vk int32) bool {
		if vk != 'D' {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		checks++
		return checks <= 3
	}

	sess := &cycleClickerSession{}
	r := New(Config{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 1, MouseClick: true},
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
	for time.Now().Before(deadline) {
		if _, _ = sess.snapshot(); sess.cycleCount() >= 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if sess.cycleCount() != 1 {
		t.Fatalf("expected exactly one cycle before the hold dropped, got %d", sess.cycleCount())
	}
	time.Sleep(50 * time.Millisecond)
	if got := sess.cycleCount(); got != 1 {
		t.Fatalf("a cycle started after the hold dropped: %d cycles", got)
	}
	if sess.resetCount() == 0 {
		t.Fatal("dropped hold did not release the virtual key")
	}
}

func TestClicker_CycleCannotBeInterleavedByAnotherRunner(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'D' }

	sess := &cycleClickerSession{}
	r := New(Config{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 1, MouseClick: true},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stopOther := make(chan struct{})
	var other sync.WaitGroup
	other.Add(1)
	go func() {
		defer other.Done()
		for {
			select {
			case <-stopOther:
				return
			default:
				_ = sess.TapKey('X', time.Millisecond)
			}
		}
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && sess.cycleCount() < 4 {
		time.Sleep(time.Millisecond)
	}
	close(stopOther)
	other.Wait()
	r.Stop()
	r.Wait()

	events, _ := sess.snapshot()
	if cycles := sess.cycleCount(); cycles < 4 {
		t.Fatalf("expected cycles alongside another runner's taps, got %d", cycles)
	}
	for i, event := range events {
		if event == "mouse" && (i == 0 || events[i-1] != "key") {
			t.Fatalf("another runner's key landed inside a cycle: %v", events)
		}
	}
}

func TestClicker_StopCancelsScheduledSleep(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(int32) bool { return true }

	sess := &cycleClickerSession{}
	r := New(Config{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Slots: [ClickerSlotCount]ClickerSlot{
			{TriggerVKs: [ClickerKeysPerBind]int32{'D'}, DelayMs: 1000, MouseClick: true},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Wait for the first mouse action, then verify Stop does not wait for the
	// configured one-second sleep.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		events, _ := sess.snapshot()
		if len(events) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	r.Stop()
	done := make(chan struct{})
	go func() {
		r.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Stop did not cancel the scheduled sleep")
	}
}
