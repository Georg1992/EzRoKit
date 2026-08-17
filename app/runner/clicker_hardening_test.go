package runner

import (
	"sync"
	"testing"
	"time"
)

type orderedClickerSession struct {
	mu     sync.Mutex
	events []string
	holds  []time.Duration
}

func (s *orderedClickerSession) TapKey(vk int32, hold time.Duration) error {
	s.mu.Lock()
	s.events = append(s.events, "key")
	s.holds = append(s.holds, hold)
	s.mu.Unlock()
	_ = vk
	return nil
}
func (s *orderedClickerSession) MouseClick(hold time.Duration) error {
	s.mu.Lock()
	s.events = append(s.events, "mouse")
	s.holds = append(s.holds, hold)
	s.mu.Unlock()
	return nil
}
func (s *orderedClickerSession) TapKeyThenMouseClick(_ int32, keyHold, mouseHold time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "key", "mouse")
	s.holds = append(s.holds, keyHold, mouseHold)
	return nil
}
func (s *orderedClickerSession) snapshot() ([]string, []time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...), append([]time.Duration(nil), s.holds...)
}

func TestClicker_FlowHasNoExtraActionBetweenMouseAndSleep(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'D' }

	sess := &orderedClickerSession{}
	r := New(Config{
		Session: sess,
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
		want := ClickerKeyTapHold
		if i%2 == 1 {
			want = ClickerClickHold
		}
		if hold != want {
			t.Fatalf("hold %d = %v, want %v", i, hold, want)
		}
	}
}

type concurrentOrderSession struct {
	mu           sync.Mutex
	events       []string
	atomicCycles int
}

func (s *concurrentOrderSession) TapKey(_ int32, _ time.Duration) error {
	s.mu.Lock()
	s.events = append(s.events, "other-key")
	s.mu.Unlock()
	return nil
}

func (s *concurrentOrderSession) MouseClick(_ time.Duration) error {
	s.mu.Lock()
	s.events = append(s.events, "mouse")
	s.mu.Unlock()
	return nil
}

func (s *concurrentOrderSession) TapKeyThenMouseClick(_ int32, _, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.atomicCycles++
	s.events = append(s.events, "key")
	// Keep the critical section occupied long enough for competing input to
	// contend with it; the pair must still remain adjacent in the event log.
	time.Sleep(time.Millisecond)
	s.events = append(s.events, "mouse")
	return nil
}

func (s *concurrentOrderSession) snapshot() ([]string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...), s.atomicCycles
}

func TestClicker_AtomicKeyMouseCycleCannotBeInterleaved(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'D' }

	sess := &concurrentOrderSession{}
	r := New(Config{
		Session: sess,
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

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, cycles := sess.snapshot()
		if cycles >= 4 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(stopOther)
	other.Wait()
	r.Stop()
	r.Wait()

	events, cycles := sess.snapshot()
	if cycles < 4 {
		t.Fatalf("expected atomic clicker cycles, got %d: %v", cycles, events)
	}
	for i, event := range events {
		if event == "mouse" && (i == 0 || events[i-1] != "key") {
			t.Fatalf("mouse was not immediately preceded by its key: %v", events)
		}
	}
}

func TestClicker_StopCancelsScheduledSleep(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(int32) bool { return true }

	sess := &orderedClickerSession{}
	r := New(Config{
		Session: sess,
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
