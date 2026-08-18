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

// run cycles one bind for as long as the user holds its key. A cycle leaves
// nothing pressed, so a release only has to stop the loop; Reset covers a cycle
// that failed halfway through.
func (r *Runner) run(ctx context.Context, _ Config) {
	defer r.settings().Session.Reset()
	var heldVK int32
	for ctx.Err() == nil {
		if emergencyDown() {
			return
		}

		current := r.settings()
		vk, slot, ok := heldTrigger(current, heldVK)
		if !ok {
			if heldVK != 0 {
				current.Session.Reset()
				heldVK = 0
			}
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}
		heldVK = vk

		if err := r.fireCycle(current.Session, slot, vk); err != nil {
			current.Log(fmt.Sprintf("clicker stopped: %v", err))
			return
		}
		sleepCycleDelay(ctx, slotDelay(slot), vk)
	}
}

// heldTrigger follows one bind until that physical key is released.
// Other keys and a second bind cannot take over mid-hold.
func heldTrigger(current Config, heldVK int32) (int32, ClickerSlot, bool) {
	if heldVK != 0 {
		if !PhysicalKeyDown(heldVK) {
			return 0, ClickerSlot{}, false
		}
		if slot, ok := slotForTrigger(current, heldVK); ok {
			return heldVK, slot, true
		}
		return 0, ClickerSlot{}, false
	}
	return firstHeldTrigger(current)
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

func slotForTrigger(current Config, vk int32) (ClickerSlot, bool) {
	for bi := range current.Slots {
		slot := current.Slots[bi]
		for _, trigger := range slot.TriggerVKs {
			if trigger == vk {
				return slot, true
			}
		}
	}
	return ClickerSlot{}, false
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

// fireCycle sends one cycle. Whether a cycle may run at all is decided here, by
// the physical hold; the session itself only decides what the game receives.
func (r *Runner) fireCycle(sess session.InputSession, slot ClickerSlot, vk int32) error {
	if !PhysicalKeyDown(vk) {
		return nil
	}
	if !slot.MouseClick {
		return sess.TapKey(vk, ClickerHold)
	}
	cycle, ok := sess.(session.ClickerInputSession)
	if !ok {
		return fmt.Errorf("input session cannot run a clicker cycle")
	}
	return cycle.TapKeyWithClick(vk, ClickerHold)
}

func slotDelay(slot ClickerSlot) time.Duration {
	delayMs := slot.DelayMs
	if delayMs <= 0 {
		delayMs = DefaultDelayMs
	}
	return time.Duration(delayMs) * time.Millisecond
}
