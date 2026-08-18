//go:build windows

package main

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ezrokit/runner"
)

type fakeRunner struct {
	startCalls atomic.Int32
	stopCalls  atomic.Int32
	waitCalls  atomic.Int32
	startErr   error
	stopped    chan struct{}
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{stopped: make(chan struct{})}
}

func (f *fakeRunner) Start() error {
	f.startCalls.Add(1)
	return f.startErr
}

func (f *fakeRunner) Stop() {
	if f.stopCalls.Add(1) == 1 {
		close(f.stopped)
	}
}

func (f *fakeRunner) Wait() {
	f.waitCalls.Add(1)
	<-f.stopped
}

var _ lifecycleRunner = (*fakeRunner)(nil)

func testSession() runner.InputSession { return testInputSession{} }

type testInputSession struct{}

func (testInputSession) TapKey(int32, time.Duration) error { return nil }
func (testInputSession) Reset()                             {}

func TestReplaceRunnerStartsAndPublishes(t *testing.T) {
	var current lifecycleRunner
	first := newFakeRunner()
	current = first

	started := replaceRunner(
		func() lifecycleRunner {
			old := current
			current = nil
			return old
		},
		func(next lifecycleRunner) { current = next },
		"fake",
		func(string) {},
		testSession,
		func() bool { return true },
		func(runner.InputSession) lifecycleRunner { return newFakeRunner() },
	)
	if !started {
		t.Fatal("replaceRunner returned false")
	}
	if first.stopCalls.Load() != 1 || first.waitCalls.Load() != 1 {
		t.Fatalf("old runner stop/wait = %d/%d, want 1/1", first.stopCalls.Load(), first.waitCalls.Load())
	}
	if current == nil || current == first {
		t.Fatal("new runner was not published")
	}
	newRunner := current.(*fakeRunner)
	if newRunner.startCalls.Load() != 1 {
		t.Fatalf("new runner Start calls = %d, want 1", newRunner.startCalls.Load())
	}
	newRunner.Stop()
	newRunner.Wait()
}

func TestReplaceRunnerSkipsWhenNotWanted(t *testing.T) {
	constructed := false
	if replaceRunner(
		func() lifecycleRunner { return nil },
		func(lifecycleRunner) {},
		"fake",
		func(string) {},
		testSession,
		func() bool { return false },
		func(runner.InputSession) lifecycleRunner {
			constructed = true
			return newFakeRunner()
		},
	) {
		t.Fatal("replaceRunner returned true when not wanted")
	}
	if constructed {
		t.Fatal("constructed a runner when not wanted")
	}
}

func TestReplaceRunnerSkipsWithoutSession(t *testing.T) {
	if replaceRunner(
		func() lifecycleRunner { return nil },
		func(lifecycleRunner) {},
		"fake",
		func(string) {},
		func() runner.InputSession { return nil },
		func() bool { return true },
		func(runner.InputSession) lifecycleRunner { return newFakeRunner() },
	) {
		t.Fatal("replaceRunner returned true without a session")
	}
}

func TestReplaceRunnerLeavesSlotEmptyOnStartFailure(t *testing.T) {
	var current lifecycleRunner
	var logs []string
	if replaceRunner(
		func() lifecycleRunner { old := current; current = nil; return old },
		func(next lifecycleRunner) { current = next },
		"fake",
		func(message string) { logs = append(logs, message) },
		testSession,
		func() bool { return true },
		func(runner.InputSession) lifecycleRunner {
			return &fakeRunner{startErr: errors.New("boom"), stopped: make(chan struct{})}
		},
	) {
		t.Fatal("replaceRunner returned true on Start failure")
	}
	if current != nil {
		t.Fatal("failed runner was published")
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %v, want one failure log", logs)
	}
}
