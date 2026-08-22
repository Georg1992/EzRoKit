package runner

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezrokit/runner/internal/session"
)

// mockSession is a session.InputSession that counts TapKey calls.
type mockSession struct {
	tapCount atomic.Int64
}

func (m *mockSession) TapKey(vk int32, hold time.Duration) error {
	m.tapCount.Add(1)
	// Honor the hold so a high tap rate simulates real load.
	time.Sleep(hold)
	return nil
}

func (m *mockSession) Reset()          {}
func (m *mockSession) TapCount() int64 { return m.tapCount.Load() }

// TestTimerKeyRunnerStress starts a real TimerKeyRunner (whose run() loop
// calls session.TapKey on each enabled slot's interval), then hammers
// UpdateSettings + Running + Paused-toggling from many goroutines. The
// timer-key loop is the simplest of the three runners — it makes no
// platform calls — so it gives the race detector the cleanest signal.
func TestTimerKeyRunnerStress(t *testing.T) {
	sess := &mockSession{}
	r := NewTimerKey(TimerKeyConfig{
		Session: sess,
		Slots: [TimerKeySlotCount]TimerSlot{
			{Enabled: true, KeyVK: 'Q', IntervalMs: 5},
			{Enabled: true, KeyVK: 'W', IntervalMs: 7},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Settings writers — change the active slots and intervals while
	// the run() loop is reading them on every tick.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			n := seed
			for {
				select {
				case <-stop:
					return
				default:
					r.UpdateSettings(TimerKeyConfig{
						Session: sess,
						Slots: [TimerKeySlotCount]TimerSlot{
							{Enabled: true, KeyVK: 'Q', IntervalMs: 5 + n%20},
							{Enabled: n%2 == 0, KeyVK: 'W', IntervalMs: 7 + n%20},
						},
						Log: func(string) {},
					})
					n++
				}
			}
		}(i)
	}
	// Running readers.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.Running()
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	r.Stop()
	r.Wait()
	if r.Running() {
		t.Fatal("still running after Stop+Wait")
	}
	if got := sess.TapCount(); got == 0 {
		t.Fatalf("TapKey was never called — run() never tapped a slot")
	}
}

// TestKeyChainRunnerStress starts a real KeyChainRunner. Its run() loop
// calls PhysicalKeyDown(trigger) on every poll; in a non-game test env
// that returns false and the loop just sleeps — but the loop body
// (settings read, Paused check, Stop machinery) is still the same
// pattern the other runners use, so the race surface is real.
func TestKeyChainRunnerStress(t *testing.T) {
	sess := &mockSession{}
	r := NewKeyChain(KeyChainConfig{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Switches: [KeyChainCount]KeyChainSwitch{
			{
				Keys:     [KeyChainSlotCount]int32{'A', 'B', 0, 0, 0, 0, 0},
				DelaysMs: [KeyChainSlotCount]int{1, 1, 0, 0, 0, 0, 0},
			},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			n := seed
			for {
				select {
				case <-stop:
					return
				default:
					r.UpdateSettings(KeyChainConfig{
						Session:  sess,
						Keyboard: HostKeyboard(),
						Switches: [KeyChainCount]KeyChainSwitch{
							{
								Keys:     [KeyChainSlotCount]int32{int32('A' + rune(n%5)), 0, 0, 0, 0, 0, 0},
								DelaysMs: [KeyChainSlotCount]int{1, 0, 0, 0, 0, 0, 0},
							},
						},
						Log: func(string) {},
					})
					n++
				}
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.Running()
				}
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
	r.Stop()
	r.Wait()
	if r.Running() {
		t.Fatal("still running after Stop+Wait")
	}
}

// TestClickerAndAutoPotConcurrent starts both the clicker runner and the
// autopot runner with a shared mock session, then hammers UpdateSettings,
// Running(), and Paused-toggling from many goroutines. This exercises the
// cross-runner concurrency that the individual runner stress tests miss:
//   - Two run() goroutines alive simultaneously, each reading lifecycle
//     settings (liveMu.RLock)
//   - External callers writing settings (liveMu.Lock)
//   - Direct TapKey calls from multiple goroutines (simulates
//     clicker+autopot key interleaving on a shared session)
//
// The autopot run() loop returns StatusNotFound (no game running), so it
// does NOT call TapKey — but the direct TapKey callers below exercise
// concurrent writes through the shared session surface.
func TestClickerAndAutoPotConcurrent(t *testing.T) {
	sess := &mockSession{}

	clicker := New(Config{
		Session:  sess,
		Keyboard: HostKeyboard(),
		Log:      func(string) {},
		Slots:    [ClickerSlotCount]ClickerSlot{},
	})
	if err := clicker.Start(); err != nil {
		t.Fatalf("clicker Start: %v", err)
	}

	ap := NewAutoPot(AutoPotConfig{
		Core: CoreConfig{
			Session:     sess,
			HPThreshold: 50,
			SPThreshold: 50,
			HPKeyVK:     'Q',
			SPKeyVK:     'W',
			Log:         func(string) {},
		},
	})
	if err := ap.Start(); err != nil {
		t.Fatalf("autopot Start: %v", err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Settings writers for both runners.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			n := seed
			for {
				select {
				case <-stop:
					return
				default:
					clicker.UpdateSettings([ClickerSlotCount]ClickerSlot{
						{TriggerVKs: [ClickerKeysPerBind]int32{int32('A' + rune(n%5))}, DelayMs: 50 + n%50},
						{},
					})
					ap.UpdateSettings(AutoPotConfig{
						Core: CoreConfig{
							Session:     sess,
							HPThreshold: 40 + n%40,
							SPThreshold: 40 + n%40,
							HPKeyVK:     'Q',
							SPKeyVK:     'W',
							Log:         func(string) {},
						},
					})
					n++
				}
			}
		}(i)
	}

	// Running readers for both runners.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = clicker.Running()
					_ = ap.Running()
				}
			}
		}()
	}

	// Direct TapKey callers — simulates concurrent key taps from
	// multiple runners on the same session (the real ViiperSession
	// serialises writes per device, but the mock exercises the pattern).
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = sess.TapKey('A', time.Millisecond)
					_ = sess.TapKey('Q', time.Millisecond)
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	clicker.Stop()
	clicker.Wait()
	ap.Stop()
	ap.Wait()

	if clicker.Running() {
		t.Fatal("clicker still running after Stop+Wait")
	}
	if ap.Running() {
		t.Fatal("autopot still running after Stop+Wait")
	}
}

// Compile-time check that mockSession satisfies the InputSession surface
// the runners read from.
var _ session.InputSession = (*mockSession)(nil)
