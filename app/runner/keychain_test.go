package runner

import (
	"context"
	"sync"
	"testing"
	"time"
)

type keychainSession struct {
	mu     sync.Mutex
	keys   []int32
	resets int
}

func (s *keychainSession) TapKey(vk int32, _ time.Duration) error {
	s.mu.Lock()
	s.keys = append(s.keys, vk)
	s.mu.Unlock()
	return nil
}
func (s *keychainSession) Reset() {
	s.mu.Lock()
	s.resets++
	s.mu.Unlock()
}
func (s *keychainSession) snapshot() []int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int32(nil), s.keys...)
}
func (s *keychainSession) resetCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resets
}

func stubPhysicalKey(t *testing.T, fn func(int32) bool) {
	t.Helper()
	orig := PhysicalKeyDown
	PhysicalKeyDown = fn
	t.Cleanup(func() { PhysicalKeyDown = orig })
}

func stubSwallowKeys(t *testing.T, fn func([]int32)) {
	t.Helper()
	orig := SwallowPhysicalKeys
	SwallowPhysicalKeys = fn
	t.Cleanup(func() { SwallowPhysicalKeys = orig })
}

func startKeyChain(t *testing.T, sess *keychainSession, sw KeyChainSwitch) *KeyChainRunner {
	t.Helper()
	stubSwallowKeys(t, func([]int32) {})
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			sw,
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		r.Stop()
		r.Wait()
	})
	return r
}

func waitForKeyChainKeys(t *testing.T, sess *keychainSession, n int, timeout time.Duration) []int32 {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got := sess.snapshot()
		if len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d keys: %v", n, sess.snapshot())
	return nil
}

func TestKeyChain_TapSendsEveryKeyOnce(t *testing.T) {
	var mu sync.Mutex
	held := true
	stubPhysicalKey(t, func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return vk == 'A' && held
	})

	sess := &keychainSession{}
	startKeyChain(t, sess, KeyChainSwitch{
		Keys:     [KeyChainSlotCount]int32{'A', 'B', 'C'},
		DelaysMs: [KeyChainSlotCount]int{20, 20, 20},
	})

	waitForKeyChainKeys(t, sess, 1, 200*time.Millisecond)
	mu.Lock()
	held = false
	mu.Unlock()

	got := waitForKeyChainKeys(t, sess, 3, 200*time.Millisecond)
	if got[0] != 'A' || got[1] != 'B' || got[2] != 'C' {
		t.Fatalf("tap sequence = %v, want [A B C]", got[:3])
	}
	time.Sleep(80 * time.Millisecond)
	if later := sess.snapshot(); len(later) != 3 {
		t.Fatalf("tap looped the chain: %v", later)
	}
}

func TestKeyChain_HoldLoopsFullChain(t *testing.T) {
	stubPhysicalKey(t, func(vk int32) bool { return vk == 'A' })

	sess := &keychainSession{}
	startKeyChain(t, sess, KeyChainSwitch{
		Keys:     [KeyChainSlotCount]int32{'A', 'B', 'C'},
		DelaysMs: [KeyChainSlotCount]int{0, 0, 0},
	})

	got := waitForKeyChainKeys(t, sess, 6, 300*time.Millisecond)
	want := []int32{'A', 'B', 'C', 'A', 'B', 'C'}
	for i, vk := range want {
		if got[i] != vk {
			t.Fatalf("hold sequence = %v, want %v", got[:6], want)
		}
	}
}

func TestKeyChain_ReleaseFinishesCurrentPass(t *testing.T) {
	var mu sync.Mutex
	held := true
	stubPhysicalKey(t, func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return vk == 'A' && held
	})

	sess := &keychainSession{}
	startKeyChain(t, sess, KeyChainSwitch{
		Keys:     [KeyChainSlotCount]int32{'A', 'B', 'C'},
		DelaysMs: [KeyChainSlotCount]int{30, 30, 0},
	})

	waitForKeyChainKeys(t, sess, 1, 200*time.Millisecond)
	mu.Lock()
	held = false
	mu.Unlock()

	got := waitForKeyChainKeys(t, sess, 3, 200*time.Millisecond)
	if got[0] != 'A' || got[1] != 'B' || got[2] != 'C' {
		t.Fatalf("released mid-pass sequence = %v, want [A B C]", got[:3])
	}
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && sess.resetCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	if sess.resetCount() == 0 {
		t.Fatal("release did not send key-up")
	}
	time.Sleep(50 * time.Millisecond)
	if later := sess.snapshot(); len(later) != 3 {
		t.Fatalf("started another pass after release: %v", later)
	}
}

func TestKeyChain_HeldSwitchIgnoresLaterTrigger(t *testing.T) {
	var mu sync.Mutex
	held := map[int32]bool{}
	stubPhysicalKey(t, func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return held[vk]
	})

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'D', 'A'}, DelaysMs: [KeyChainSlotCount]int{5, 0}},
			{Keys: [KeyChainSlotCount]int32{'T', 'B'}, DelaysMs: [KeyChainSlotCount]int{5, 0}},
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
	waitForKeyChainKeys(t, sess, 1, 200*time.Millisecond)

	mu.Lock()
	held['T'] = true
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	for _, vk := range sess.snapshot() {
		if vk == 'B' {
			t.Fatalf("T took over while D was still held: %v", sess.snapshot())
		}
	}

	mu.Lock()
	held['D'] = false
	mu.Unlock()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, vk := range sess.snapshot() {
			if vk == 'B' {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("T never ran after D released: %v", sess.snapshot())
}

func TestKeyChain_RepeatedKeysIncludingTrigger(t *testing.T) {
	stubPhysicalKey(t, func(vk int32) bool { return vk == '1' })

	sess := &keychainSession{}
	k := NewKeyChain(KeyChainConfig{Session: sess, Log: func(string) {}})
	err := k.executeChain(context.Background(), sess, KeyChainSwitch{
		Keys: [KeyChainSlotCount]int32{'1', '2', '1', '3', '1', '4'},
	})
	if err != nil {
		t.Fatalf("executeChain: %v", err)
	}
	got := sess.snapshot()
	want := []int32{'1', '2', '1', '3', '1', '4'}
	if len(got) != len(want) {
		t.Fatalf("sequence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
}

func TestKeyChain_ExecuteChainSendsEveryKey(t *testing.T) {
	stubPhysicalKey(t, func(vk int32) bool { return vk == 'A' })

	sess := &keychainSession{}
	k := NewKeyChain(KeyChainConfig{Session: sess, Log: func(string) {}})
	err := k.executeChain(context.Background(), sess, KeyChainSwitch{
		Keys: [KeyChainSlotCount]int32{'A', 'B', 'C'},
	})
	if err != nil {
		t.Fatalf("executeChain: %v", err)
	}
	got := sess.snapshot()
	if len(got) != 3 || got[0] != 'A' || got[1] != 'B' || got[2] != 'C' {
		t.Fatalf("sequence = %v, want [A B C]", got)
	}
}

func TestKeyChain_StopCancelsScheduledSleep(t *testing.T) {
	stubPhysicalKey(t, func(int32) bool { return true })

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'T', 'A'}, DelaysMs: [KeyChainSlotCount]int{1000, 0}},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitForKeyChainKeys(t, sess, 1, 200*time.Millisecond)
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

func TestKeyChain_EmergencyToggleStopsRunawayChain(t *testing.T) {
	var mu sync.Mutex
	emergency := false
	stubPhysicalKey(t, func(int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return true
	})
	origEmergency := EmergencyKeyDown
	EmergencyKeyDown = func(int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return emergency
	}
	t.Cleanup(func() { EmergencyKeyDown = origEmergency })

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'T', 'A'}, DelaysMs: [KeyChainSlotCount]int{5, 0}},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	waitForKeyChainKeys(t, sess, 1, 200*time.Millisecond)
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
		t.Fatal("emergency toggle did not stop keychain")
	}
	stoppedAt := len(sess.snapshot())
	time.Sleep(50 * time.Millisecond)
	if got := len(sess.snapshot()); got != stoppedAt {
		t.Fatalf("keychain emitted after emergency stop: %d then %d events", stoppedAt, got)
	}
	r.Wait()
}

func TestChainTriggerVKs_ActiveTriggersOnly(t *testing.T) {
	cfg := KeyChainConfig{
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'1', '2'}},
			{Keys: [KeyChainSlotCount]int32{0, 'A'}},
			{Keys: [KeyChainSlotCount]int32{'B'}},
		},
	}
	got := chainTriggerVKs(cfg)
	if len(got) != 2 || got[0] != '1' || got[1] != 'B' {
		t.Fatalf("chainTriggerVKs = %v, want [1 B]", got)
	}
}

func TestKeyChain_TapChainKeyMarksTappingVK(t *testing.T) {
	var mu sync.Mutex
	var got []int32
	orig := SetTappingVK
	SetTappingVK = func(vk int32) {
		mu.Lock()
		got = append(got, vk)
		mu.Unlock()
	}
	t.Cleanup(func() { SetTappingVK = orig })

	sess := &keychainSession{}
	if err := tapChainKey(sess, '1'); err != nil {
		t.Fatalf("tapChainKey: %v", err)
	}
	mu.Lock()
	seq := append([]int32(nil), got...)
	mu.Unlock()
	if len(seq) != 2 || seq[0] != '1' || seq[1] != 0 {
		t.Fatalf("SetTappingVK sequence = %v, want [1 0]", seq)
	}
	if keys := sess.snapshot(); len(keys) != 1 || keys[0] != '1' {
		t.Fatalf("tapped %v, want [1]", keys)
	}
}

func TestKeyChain_SwallowsTriggerWhileRunning(t *testing.T) {
	var mu sync.Mutex
	var last []int32
	stubSwallowKeys(t, func(vks []int32) {
		mu.Lock()
		last = append([]int32(nil), vks...)
		mu.Unlock()
	})
	stubPhysicalKey(t, func(int32) bool { return false })

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'1', '2', '1'}},
		},
		Log: func(string) {},
	})
	if err := r.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := append([]int32(nil), last...)
		mu.Unlock()
		if len(got) == 1 && got[0] == '1' {
			r.Stop()
			r.Wait()
			mu.Lock()
			cleared := last
			mu.Unlock()
			if len(cleared) != 0 {
				t.Fatalf("stop left swallow set %v", cleared)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	r.Stop()
	r.Wait()
	t.Fatal("runner never swallowed trigger 1")
}

func TestKeyChain_HoldLoopsWithoutIdlePollGap(t *testing.T) {
	stubPhysicalKey(t, func(vk int32) bool { return vk == 'T' })

	sess := &keychainSession{}
	startKeyChain(t, sess, KeyChainSwitch{
		Keys:     [KeyChainSlotCount]int32{'T', 'A'},
		DelaysMs: [KeyChainSlotCount]int{0, 0},
	})

	// The previous loop slept PollInterval after every pass, so 8 taps needed
	// ~80ms. Clicker-style hold tracking with a 0ms loop delay must keep up.
	got := waitForKeyChainKeys(t, sess, 8, 50*time.Millisecond)
	for i, vk := range got[:8] {
		want := int32('T')
		if i%2 == 1 {
			want = 'A'
		}
		if vk != want {
			t.Fatalf("hold sequence[%d] = %v, want %c: %v", i, vk, want, got[:8])
		}
	}
}
