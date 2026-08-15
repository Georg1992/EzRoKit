//go:build windows

package main

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezrokit/runner"
)

// fakeRunner is a minimal lifecycleRunner for unit tests. It records
// the order of Start/Stop/Wait calls so tests can assert the pipeline.
type fakeRunner struct {
	startCalls  atomic.Int32
	stopCalls   atomic.Int32
	waitCalls   atomic.Int32
	startErr    error
	stopReturns chan struct{}
	id          int
}

func newFakeRunner(id int) *fakeRunner {
	return &fakeRunner{id: id, stopReturns: make(chan struct{}, 1)}
}

func (f *fakeRunner) Start() error {
	f.startCalls.Add(1)
	return f.startErr
}

func (f *fakeRunner) Stop() {
	f.stopCalls.Add(1)
	select {
	case f.stopReturns <- struct{}{}:
	default:
	}
}

func (f *fakeRunner) Wait() {
	f.waitCalls.Add(1)
	<-f.stopReturns
}

// fakeSession is a minimal runner.InputSession for unit tests. The
// production code treats a nil session as "not ready yet" (and bails);
// tests that exercise the rest of the pipeline pass a fakeSession so
// the helper doesn't bail at the session check.
type fakeSession struct{}

func (fakeSession) Paused() bool                          { return false }
func (fakeSession) TapKey(_ int32, _ time.Duration) error { return nil }
func (fakeSession) MouseClick(_ time.Duration) error      { return nil }

// sessionOK returns a runner.InputSession that's non-nil — used by all
// tests that exercise the path past the session-nil guard.
func sessionOK() runner.InputSession { return fakeSession{} }

func TestMakeLifecycleSlot_TakeStoreRoundTrip(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner

	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)

	// Empty slot: take returns nil directly (typed R, not wrapped
	// in an interface, so the comparison below is unambiguous).
	if got := take(); got != nil {
		t.Fatalf("expected nil from take on empty slot, got %#v", got)
	}

	// Store a fake, then take it back; type is preserved.
	want := newFakeRunner(1)
	store(want)
	if slot != want {
		t.Fatalf("store: slot mismatch: got %p want %p", slot, want)
	}
	got := take()
	if got != want {
		t.Fatalf("take: got %p want %p", got, want)
	}
	if slot != nil {
		t.Fatalf("take did not clear slot: %p", slot)
	}

	// A second take after clearing must also return nil.
	if got := take(); got != nil {
		t.Fatalf("expected nil from take after clear, got %#v", got)
	}

	// Positive case: store a non-nil fake, take it, verify the
	// returned value is non-nil (regression-lock on the typed-R
	// design that explicitly avoids the typed-nil interface gotcha).
	store(newFakeRunner(2))
	if got := take(); got == nil {
		t.Fatalf("expected non-nil from take on populated slot")
	}
}

func TestMakeLifecycleSlot_StoreNilClearsSlot(t *testing.T) {
	var mu sync.Mutex
	slot := newFakeRunner(1)
	var slotRef *fakeRunner = slot
	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slotRef)

	store(nil) // simulate a tear-down path
	if slotRef != nil {
		t.Fatalf("store(nil) did not clear slot: %p", slotRef)
	}
	_ = take() // verify it returns nil
}

// plainFake is a value-type fake used to test the runtime R-pointer
// guard. It implements lifecycleRunner on the value receiver (so it
// satisfies the interface) but is NOT a pointer type, which is what
// the guard rejects. The methods are never called (the test panics
// at makeLifecycleSlot time, before any method would run), so they
// are no-ops with no fields — using `atomic.Int32` here would
// trigger `go vet`'s copylocks warning on the value-receiver
// method copy (atomic.Int32 embeds a noCopy sentinel).
type plainFake struct{}

func (p plainFake) Start() error { return nil }
func (p plainFake) Stop()        {}
func (p plainFake) Wait()        {}

func TestMakeLifecycleSlot_NonPointerR_PanicsAtConstruction(t *testing.T) {
	// The guard fires eagerly at makeLifecycleSlot time so misuse is
	// caught at the call site (the panic stack points at the bad
	// generic arg, not deep in a take/store call).
	var mu sync.Mutex
	var slot plainFake // not a pointer R

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic when R is a non-pointer type")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", r, r)
		}
		if !strings.Contains(msg, "R must be a pointer type") {
			t.Fatalf("panic message missing pointer-type hint: %q", msg)
		}
	}()

	makeLifecycleSlot[plainFake](&mu, &slot)
}

func TestStartLifecycle_HappyPath(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner

	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)
	var logs []string
	log := func(s string) { logs = append(logs, s) }

	ok := startLifecycle(
		take, store,
		"fake", log,
		sessionOK,
		func() bool { return true },
		func(_ runner.InputSession) *fakeRunner { return newFakeRunner(1) },
	)
	if !ok {
		t.Fatalf("startLifecycle returned false on happy path")
	}
	if slot == nil {
		t.Fatalf("slot not populated after successful start")
	}
	if slot.startCalls.Load() != 1 {
		t.Fatalf("expected 1 Start call, got %d", slot.startCalls.Load())
	}
	if len(logs) != 1 || logs[0] != "fake started" {
		t.Fatalf("expected one 'fake started' log, got %v", logs)
	}

	// Tear down the fake so the test goroutine doesn't leak.
	slot.Stop()
	slot.Wait()
}

func TestStartLifecycle_IsWantedFalse_NoOp(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner

	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)

	ok := startLifecycle(
		take, store,
		"fake", func(string) {},
		sessionOK,
		func() bool { return false },
		func(_ runner.InputSession) *fakeRunner {
			t.Fatalf("construct must not be called when isWanted is false")
			return nil
		},
	)
	if ok {
		t.Fatalf("expected false when isWanted is false")
	}
	if slot != nil {
		t.Fatalf("slot should remain nil when isWanted is false")
	}
}

func TestStartLifecycle_NilSession_NoOp(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner

	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)

	ok := startLifecycle(
		take, store,
		"fake", func(string) {},
		func() runner.InputSession { return nil },
		func() bool { return true },
		func(_ runner.InputSession) *fakeRunner {
			t.Fatalf("construct must not be called when session is nil")
			return nil
		},
	)
	if ok {
		t.Fatalf("expected false when session is nil")
	}
	if slot != nil {
		t.Fatalf("slot should remain nil when session is nil")
	}
}

func TestStartLifecycle_StartFailureLogsAndLeavesSlotNil(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner

	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)

	var logs []string
	ok := startLifecycle(
		take, store,
		"fake", func(s string) { logs = append(logs, s) },
		sessionOK,
		func() bool { return true },
		func(_ runner.InputSession) *fakeRunner {
			return &fakeRunner{startErr: errors.New("boom"), stopReturns: make(chan struct{}, 1)}
		},
	)
	if ok {
		t.Fatalf("expected false on Start failure")
	}
	if slot != nil {
		t.Fatalf("slot should be nil after Start failure (old was taken; new was rejected)")
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log line, got %d: %v", len(logs), logs)
	}
}

func TestStartLifecycle_ReplacesOldRunner(t *testing.T) {
	var mu sync.Mutex
	var slot *fakeRunner
	take, store := makeLifecycleSlot[*fakeRunner](&mu, &slot)

	// Install a first runner.
	first := newFakeRunner(1)
	store(first)
	if slot != first {
		t.Fatalf("first install: slot mismatch")
	}

	// Build a second one via startLifecycle. The first must be
	// Stop()+Wait()'d and replaced.
	ok := startLifecycle(
		take, store,
		"fake", func(string) {},
		sessionOK,
		func() bool { return true },
		func(_ runner.InputSession) *fakeRunner { return newFakeRunner(2) },
	)
	if !ok {
		t.Fatalf("replacement start returned false")
	}
	if first.stopCalls.Load() != 1 || first.waitCalls.Load() != 1 {
		t.Fatalf("expected old runner to be Stop+Wait once; got stop=%d wait=%d",
			first.stopCalls.Load(), first.waitCalls.Load())
	}
	if slot == first {
		t.Fatalf("slot still points to old runner after replacement")
	}
	if slot == nil || slot.id != 2 {
		t.Fatalf("expected slot to be the new runner (id=2), got %v", slot)
	}

	// Cleanup.
	slot.Stop()
	slot.Wait()
}
