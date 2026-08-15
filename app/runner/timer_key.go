// TimerKeyRunner fires each enabled slot's key on its interval.
// Lifecycle driven by internal/lifecycle.
package runner

import (
	"context"
	"fmt"
	"time"

	"ezrokit/runner/internal/lifecycle"
	"ezrokit/runner/internal/session"
	"ezrokit/runner/internal/timing"
)

const (
	TimerKeySlotCount          = 5
	DefaultTimerKeyIntervalSec = 1
	DefaultTimerKeyIntervalMs  = DefaultTimerKeyIntervalSec * 1000
)

type TimerSlot struct {
	Enabled    bool
	KeyVK      int32
	IntervalMs int
}

// TimerKeyConfig is what NewTimerKey takes. Session is the canonical
// session.InputSession.
type TimerKeyConfig struct {
	Session session.InputSession
	Slots   [TimerKeySlotCount]TimerSlot
	Log     func(string)
}

func (c *TimerKeyConfig) applyDefaults() {
	for i := range c.Slots {
		if c.Slots[i].IntervalMs <= 0 {
			c.Slots[i].IntervalMs = DefaultTimerKeyIntervalMs
		}
	}
	if c.Log == nil {
		c.Log = func(string) {}
	}
}

func (c TimerKeyConfig) AnyActive() bool {
	for _, slot := range c.Slots {
		if slot.Enabled && slot.KeyVK != 0 {
			return true
		}
	}
	return false
}

// TimerKeyRunner fires each enabled slot on its interval.
type TimerKeyRunner struct {
	lc *lifecycle.Lifecycle[TimerKeyConfig]
}

// NewTimerKey constructs a TimerKeyRunner. Applies defaults.
func NewTimerKey(cfg TimerKeyConfig) *TimerKeyRunner {
	cfg.applyDefaults()
	return &TimerKeyRunner{
		lc: lifecycle.New[TimerKeyConfig](
			cfg,
			func(c TimerKeyConfig) error {
				if !c.AnyActive() {
					return nil
				}
				if c.Session == nil {
					return fmt.Errorf("input session is required")
				}
				return nil
			},
			nil,
		),
	}
}

func (t *TimerKeyRunner) Running() bool { return t.lc.Running() }

func (t *TimerKeyRunner) UpdateSettings(cfg TimerKeyConfig) {
	// Preserve Log and Session from the existing config — the initial
	// values use Synchronize-wrapped callbacks and the live session.
	old := t.settings()
	cfg.Log = old.Log
	cfg.Session = old.Session
	cfg.applyDefaults()
	t.lc.UpdateSettings(cfg)
}

func (t *TimerKeyRunner) settings() TimerKeyConfig { return t.lc.Settings() }

func (t *TimerKeyRunner) Start() error {
	if err := t.lc.Start(t.run); err != nil {
		return fmt.Errorf("timer key: %w", err)
	}
	return nil
}

func (t *TimerKeyRunner) Stop() { t.lc.Stop() }

func (t *TimerKeyRunner) Wait() { t.lc.Wait() }

func (t *TimerKeyRunner) run(ctx context.Context, cfg TimerKeyConfig) {
	session := cfg.Session

	var lastSlots [TimerKeySlotCount]TimerSlot
	var nextDue [TimerKeySlotCount]time.Time

	for {
		if ctx.Err() != nil {
			return
		}
		current := t.settings()
		now := time.Now()
		var earliest time.Time
		anyActive := false

		for i := range current.Slots {
			slot := current.Slots[i]
			if !slot.Enabled || slot.KeyVK == 0 {
				lastSlots[i] = TimerSlot{}
				nextDue[i] = time.Time{}
				continue
			}
			anyActive = true

			if slot != lastSlots[i] {
				lastSlots[i] = slot
				nextDue[i] = time.Time{}
			}

			interval := time.Duration(slot.IntervalMs) * time.Millisecond
			if nextDue[i].IsZero() {
				nextDue[i] = now
			}
			if !now.Before(nextDue[i]) {
				if err := session.TapKey(slot.KeyVK, timing.KeyTapHold); err != nil {
					current.Log(fmt.Sprintf("Timer %d key %s failed: %v", i+1, KeyName(slot.KeyVK), err))
					nextDue[i] = now.Add(interval)
					continue
				}
				nextDue[i] = now.Add(interval)
			}
			if earliest.IsZero() || nextDue[i].Before(earliest) {
				earliest = nextDue[i]
			}
		}

		if !anyActive {
			timing.Sleep(ctx, timing.CaptureRetryDelay)
			continue
		}

		wait := time.Until(earliest)
		if wait < timing.MinPollWait {
			wait = timing.MinPollWait
		}
		if wait > timing.PollInterval {
			wait = timing.PollInterval
		}
		timing.Sleep(ctx, wait)
	}
}
