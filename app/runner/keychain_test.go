package runner

import (
	"context"
	"sync"
	"testing"
	"time"
)

type keychainSession struct {
	mu   sync.Mutex
	keys []int32
}

func (s *keychainSession) TapKey(vk int32, _ time.Duration) error {
	s.mu.Lock()
	s.keys = append(s.keys, vk)
	s.mu.Unlock()
	return nil
}
func (s *keychainSession) Reset() {}
func (s *keychainSession) snapshot() []int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int32(nil), s.keys...)
}

func TestKeyChain_DoesNotTapTriggerKey(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'T' }

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'T', 'Q', 'W'}},
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
	for time.Now().Before(deadline) {
		if len(sess.snapshot()) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	for _, vk := range sess.snapshot() {
		if vk == 'T' {
			t.Fatalf("keychain tapped the trigger: %v", sess.snapshot())
		}
	}
}

func TestKeyChain_StopsWhenTriggerReleased(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()

	var mu sync.Mutex
	held := true
	PhysicalKeyDown = func(vk int32) bool {
		mu.Lock()
		defer mu.Unlock()
		return vk == 'T' && held
	}

	sess := &keychainSession{}
	r := NewKeyChain(KeyChainConfig{
		Session: sess,
		Switches: [KeyChainCount]KeyChainSwitch{
			{Keys: [KeyChainSlotCount]int32{'T', 'Q'}, DelaysMs: [KeyChainSlotCount]int{0, 0}},
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
	for time.Now().Before(deadline) {
		if len(sess.snapshot()) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	held = false
	mu.Unlock()
	time.Sleep(40 * time.Millisecond)
	stoppedAt := len(sess.snapshot())
	time.Sleep(40 * time.Millisecond)
	if got := len(sess.snapshot()); got != stoppedAt {
		t.Fatalf("released trigger kept the chain running: %d then %d", stoppedAt, got)
	}
}

func TestKeyChain_ExecuteChainSkipsTrigger(t *testing.T) {
	orig := PhysicalKeyDown
	defer func() { PhysicalKeyDown = orig }()
	PhysicalKeyDown = func(vk int32) bool { return vk == 'T' }

	sess := &keychainSession{}
	k := NewKeyChain(KeyChainConfig{Session: sess, Log: func(string) {}})
	err := k.executeChain(context.Background(), sess, KeyChainSwitch{
		Keys: [KeyChainSlotCount]int32{'T', 'A', 'B'},
	})
	if err != nil {
		t.Fatalf("executeChain: %v", err)
	}
	got := sess.snapshot()
	if len(got) != 2 || got[0] != 'A' || got[1] != 'B' {
		t.Fatalf("sequence = %v, want [A B]", got)
	}
}
