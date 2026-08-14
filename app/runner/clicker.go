// Package runner's clicker runner: while a physical key is held, emit
// a key tap followed by an optional mouse click. Its lifecycle is driven by
// internal/lifecycle; timing uses internal/timing; the session interface
// is internal/session.InputSession.
package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"belarus-champ-tools/runner/internal/lifecycle"
	"belarus-champ-tools/runner/internal/session"
	"belarus-champ-tools/runner/internal/timing"
)

const (
	ClickerSlotCount   = 2
	ClickerKeysPerBind = 8
	DefaultDelayMs     = 50

	// ClickerKeyTapHold is long enough to cross several HID polls, ensuring
	// the key-down report reaches the game before the key-up report.
	ClickerKeyTapHold = 4 * timing.HIDPollInterval

	// ClickerClickHold is the shortest hardened mouse hold: two HID polls
	// ensure the mouse-down report is transmitted before mouse-up.
	ClickerClickHold = 2 * timing.HIDPollInterval

	clickerTriggerCount = ClickerSlotCount * ClickerKeysPerBind
)

// ClickerSlot is one bind row. Every non-zero trigger key has its own
// clicker state and repeat deadline. The first pressed held key owns output;
// other held keys wait in press order until the owner is released.
//
//	MouseClick=true:  key click -> mouse click -> DelayMs sleep
//	MouseClick=false: key click -> DelayMs sleep
//
// DelayMs is always after the final action. A key release during a cycle does
// not interrupt that cycle; ownership is handed off after the cycle ends.
type ClickerSlot struct {
	TriggerVKs [ClickerKeysPerBind]int32
	DelayMs    int
	MouseClick bool
}

// Config holds every mutable thing the clicker loop needs.
type Config struct {
	Session session.InputSession
	Log     func(string)
	Slots   [ClickerSlotCount]ClickerSlot
}

// Runner watches trigger keys and emits clicks.
type Runner struct {
	lc *lifecycle.Lifecycle[Config]
}

// New constructs a Runner backed by a Lifecycle. The Log callback is
// defaulted to a no-op so callers don't have to provide one.
func New(cfg Config) *Runner {
	if cfg.Log == nil {
		cfg.Log = func(string) {}
	}
	r := &Runner{}
	r.lc = lifecycle.New[Config](
		cfg,
		func(c Config) error {
			if c.Session == nil {
				return fmt.Errorf("input session is required")
			}
			return nil
		},
		nil,
	)
	return r
}

// Running reports whether the clicker loop is currently active.
func (r *Runner) Running() bool { return r.lc.Running() }

// UpdateSettings replaces the live slots while preserving Session and Log.
func (r *Runner) UpdateSettings(slots [ClickerSlotCount]ClickerSlot) {
	cfg := r.lc.Settings()
	cfg.Slots = slots
	r.lc.UpdateSettings(cfg)
}

func (r *Runner) settings() Config { return r.lc.Settings() }

// Start launches the clicker loop.
func (r *Runner) Start() error {
	if err := r.lc.Start(r.run); err != nil {
		return fmt.Errorf("clicker: %w", err)
	}
	return nil
}

// Stop signals the clicker loop to exit.
func (r *Runner) Stop() { r.lc.Stop() }

// Wait blocks until the clicker goroutine has exited.
func (r *Runner) Wait() { r.lc.Wait() }

// KeysText formats bind trigger keys for UI labels.
func KeysText(vks [ClickerKeysPerBind]int32) string {
	names := make([]string, 0, ClickerKeysPerBind)
	for _, vk := range vks {
		if vk != 0 {
			names = append(names, KeyName(vk))
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

type clickerKeyState struct {
	vk        int32
	down      bool
	pressedAt time.Time
	nextDue   time.Time
	pressSeq  uint64
}

// run maintains independent state for every configured key. Only owner may
// emit input; owner is selected by first observed press and remains stable
// until that key is observed released. This prevents keys from racing while
// preserving a deterministic handoff to the next held key.
func (r *Runner) run(ctx context.Context, _ Config) {
	var states [clickerTriggerCount]clickerKeyState
	var pressSeq uint64
	owner := -1

	for ctx.Err() == nil {
		current := r.settings()
		now := time.Now()
		anyMapped := false

		for i := range states {
			bi := i / ClickerKeysPerBind
			ki := i % ClickerKeysPerBind
			vk := current.Slots[bi].TriggerVKs[ki]
			state := &states[i]

			if vk != state.vk {
				*state = clickerKeyState{vk: vk}
				if owner == i {
					owner = -1
				}
			}
			if vk == 0 {
				continue
			}
			anyMapped = true

			down := PhysicalKeyDown(vk)
			if down && !state.down {
				pressSeq++
				state.down = true
				state.pressedAt = now
				state.nextDue = now
				state.pressSeq = pressSeq
			} else if !down && state.down {
				state.down = false
				state.pressedAt = time.Time{}
				state.nextDue = time.Time{}
				if owner == i {
					owner = -1
				}
			}
		}

		if owner < 0 {
			owner = firstPressedKey(states[:])
		}
		if owner < 0 {
			if anyMapped {
				timing.Sleep(ctx, timing.PollInterval)
			} else {
				timing.Sleep(ctx, timing.CaptureRetryDelay)
			}
			continue
		}

		state := &states[owner]
		slot := current.Slots[owner/ClickerKeysPerBind]
		if state.nextDue.IsZero() || !now.Before(state.nextDue) {
			if r.fireCycle(ctx, current.Session, current.Log, slot, state.vk) {
				return
			}
			// This deadline belongs only to the owner. Waiting keys keep
			// their own press sequence and are not reset or advanced.
			state.nextDue = time.Now().Add(slotDelay(slot))
		}

		wait := time.Until(state.nextDue)
		if wait < timing.MinPollWait {
			wait = timing.MinPollWait
		}
		if wait > timing.PollInterval {
			wait = timing.PollInterval
		}
		timing.Sleep(ctx, wait)
	}
}

func firstPressedKey(states []clickerKeyState) int {
	owner := -1
	var earliest uint64
	for i, state := range states {
		if !state.down || state.pressSeq == 0 {
			continue
		}
		if owner < 0 || state.pressSeq < earliest {
			owner = i
			earliest = state.pressSeq
		}
	}
	return owner
}

// fireCycle emits either key click -> mouse click or the key-only variant.
// It returns true when cancellation was observed and the runner should stop
// without beginning another action.
func (r *Runner) fireCycle(ctx context.Context, sess session.InputSession, log func(string), slot ClickerSlot, vk int32) bool {
	if !slot.MouseClick {
		if err := sess.TapKey(vk, ClickerKeyTapHold); err != nil {
			if ctx.Err() != nil {
				return true
			}
			log(fmt.Sprintf("clicker key %s failed: %v", KeyName(vk), err))
		}
		return false
	}

	if cycle, ok := sess.(session.ClickerCycleSession); ok {
		if err := cycle.ClickerCycle(vk, ClickerKeyTapHold, ClickerClickHold); err != nil {
			if ctx.Err() != nil {
				return true
			}
			log(fmt.Sprintf("clicker cycle for key %s failed: %v", KeyName(vk), err))
		}
		return false
	}

	if err := sess.TapKey(vk, ClickerKeyTapHold); err != nil {
		if ctx.Err() != nil {
			return true
		}
		log(fmt.Sprintf("clicker key %s failed: %v", KeyName(vk), err))
		return false
	}
	if ctx.Err() != nil {
		return true
	}
	if err := sess.MouseClick(ClickerClickHold); err != nil {
		if ctx.Err() != nil {
			return true
		}
		log(fmt.Sprintf("clicker mouse click failed: %v", err))
	}
	return false
}

func slotDelay(slot ClickerSlot) time.Duration {
	delayMs := slot.DelayMs
	if delayMs <= 0 {
		delayMs = DefaultDelayMs
	}
	return time.Duration(delayMs) * time.Millisecond
}
