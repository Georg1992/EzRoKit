package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ezrokit/runner/internal/lifecycle"
	"ezrokit/runner/internal/session"
	"ezrokit/runner/internal/timing"
)

const (
	ClickerSlotCount   = 2
	ClickerKeysPerBind = 8
	DefaultDelayMs     = 50
	// 10 ms down is long enough for a 60 fps game to see the press.
	ClickerHold = 10 * time.Millisecond
)

// ClickerSlot is one bind row.
//
//	MouseClick=true:  key down -> mouse click -> key up -> DelayMs
//	MouseClick=false: key down -> key up -> DelayMs
type ClickerSlot struct {
	TriggerVKs [ClickerKeysPerBind]int32
	DelayMs    int
	MouseClick bool
}

type Config struct {
	Session session.InputSession
	Log     func(string)
	Slots   [ClickerSlotCount]ClickerSlot
}

type Runner struct {
	lc *lifecycle.Lifecycle[Config]
}

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

func (r *Runner) Running() bool { return r.lc.Running() }

func (r *Runner) UpdateSettings(slots [ClickerSlotCount]ClickerSlot) {
	cfg := r.lc.Settings()
	cfg.Slots = slots
	r.lc.UpdateSettings(cfg)
}

func (r *Runner) settings() Config { return r.lc.Settings() }

func (r *Runner) Start() error {
	if err := r.lc.Start(r.run); err != nil {
		return fmt.Errorf("clicker: %w", err)
	}
	return nil
}

func (r *Runner) Stop() { r.lc.Stop() }

func (r *Runner) Wait() { r.lc.Wait() }

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

func (r *Runner) run(ctx context.Context, _ Config) {
	defer r.settings().Session.Reset()
	armed := false
	for ctx.Err() == nil {
		if emergencyDown() {
			return
		}

		current := r.settings()
		vk, slot, ok := firstHeldTrigger(current)
		if !ok {
			if armed {
				current.Session.Reset()
				armed = false
			}
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}

		if err := r.fireCycle(current.Session, slot, vk); err != nil {
			current.Log(fmt.Sprintf("clicker stopped: %v", err))
			return
		}
		armed = true
		if ctx.Err() != nil || emergencyDown() {
			return
		}
		if !PhysicalKeyDown(vk) {
			current.Session.Reset()
			armed = false
			continue
		}
		sleepCycleDelay(ctx, slotDelay(slot), vk)
		if ctx.Err() != nil || emergencyDown() || !PhysicalKeyDown(vk) {
			current.Session.Reset()
			armed = false
		}
	}
}

func firstHeldTrigger(current Config) (int32, ClickerSlot, bool) {
	for bi := range current.Slots {
		slot := current.Slots[bi]
		for _, vk := range slot.TriggerVKs {
			if vk != 0 && PhysicalKeyDown(vk) {
				return vk, slot, true
			}
		}
	}
	return 0, ClickerSlot{}, false
}

func emergencyDown() bool {
	for _, vk := range timing.ToggleVKs {
		if EmergencyKeyDown(vk) {
			return true
		}
	}
	return false
}

func sleepCycleDelay(ctx context.Context, d time.Duration, vk int32) {
	deadline := time.Now().Add(d)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if emergencyDown() || !PhysicalKeyDown(vk) {
			return
		}
		wait := time.Until(deadline)
		if wait > timing.PollInterval {
			wait = timing.PollInterval
		}
		timing.Sleep(ctx, wait)
	}
}

func (r *Runner) fireCycle(sess session.InputSession, slot ClickerSlot, vk int32) error {
	if !slot.MouseClick {
		return sess.TapKey(vk, ClickerHold)
	}
	ordered, ok := sess.(session.OrderedInputSession)
	if !ok {
		return fmt.Errorf("input session does not support ordered key+mouse cycles")
	}
	return ordered.KeyDownThenMouseClick(vk, ClickerHold, ClickerHold)
}

func slotDelay(slot ClickerSlot) time.Duration {
	delayMs := slot.DelayMs
	if delayMs <= 0 {
		delayMs = DefaultDelayMs
	}
	return time.Duration(delayMs) * time.Millisecond
}
