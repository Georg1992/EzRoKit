// KeyChainRunner plays a sequence of keys when its trigger key is tapped or held.
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
	KeyChainSlotCount = 7
	KeyChainCount     = 5
)

// KeyChainSwitch is one chain. Keys[0] is the trigger: pressing it starts
// a pass. It is also the first key sent, and later slots may repeat it.
// Each DelaysMs[i] is the pause after that key, before the next one
// (or before looping back when the trigger is still held).
type KeyChainSwitch struct {
	Keys     [KeyChainSlotCount]int32
	DelaysMs [KeyChainSlotCount]int
}

func (s KeyChainSwitch) Active() bool {
	return s.Keys[0] != 0
}

// KeyChainConfig is what NewKeyChain takes. Session is the canonical
// session.InputSession — same interface other runners use.
type KeyChainConfig struct {
	Session  session.InputSession
	Switches [KeyChainCount]KeyChainSwitch
	Log      func(string)
}

func (c *KeyChainConfig) applyDefaults() {
	if c.Log == nil {
		c.Log = func(string) {}
	}
}

// Active reports whether any switch has a trigger key bound.
func (c KeyChainConfig) Active() bool {
	for _, sw := range c.Switches {
		if sw.Active() {
			return true
		}
	}
	return false
}

// KeyChainRunner runs the macro.
type KeyChainRunner struct {
	lc *lifecycle.Lifecycle[KeyChainConfig]
}

// NewKeyChain constructs a KeyChainRunner. Defaults the Log callback.
func NewKeyChain(cfg KeyChainConfig) *KeyChainRunner {
	cfg.applyDefaults()
	return &KeyChainRunner{
		lc: lifecycle.New[KeyChainConfig](
			cfg,
			func(c KeyChainConfig) error {
				if c.Session == nil {
					return fmt.Errorf("input session is required")
				}
				return nil
			},
			nil,
		),
	}
}

func (k *KeyChainRunner) Running() bool { return k.lc.Running() }

func (k *KeyChainRunner) UpdateSettings(cfg KeyChainConfig) {
	// Preserve Log and Session from the existing config — the initial
	// values use Synchronize-wrapped callbacks and the live session.
	old := k.settings()
	cfg.Log = old.Log
	cfg.Session = old.Session
	cfg.applyDefaults()
	k.lc.UpdateSettings(cfg)
}

func (k *KeyChainRunner) settings() KeyChainConfig { return k.lc.Settings() }

func (k *KeyChainRunner) Start() error {
	if err := k.lc.Start(k.run); err != nil {
		return fmt.Errorf("keychain: %w", err)
	}
	return nil
}

func (k *KeyChainRunner) Stop() { k.lc.Stop() }

func (k *KeyChainRunner) Wait() { k.lc.Wait() }

// run follows one trigger the way the clicker follows a bind: lock onto the
// physical key, play every step including the trigger, then immediately
// start another pass if the key is still held. A tap, or a release mid-pass,
// still finishes A→B→C; only the next pass is skipped.
func (k *KeyChainRunner) run(ctx context.Context, _ KeyChainConfig) {
	defer k.settings().Session.Reset()
	defer SwallowPhysicalKeys(nil)
	defer SetTappingVK(0)
	var heldVK int32
	for ctx.Err() == nil {
		if emergencyDown() {
			return
		}

		current := k.settings()
		SwallowPhysicalKeys(chainTriggerVKs(current))
		vk, sw, ok := heldChainTrigger(current, heldVK)
		if !ok {
			if heldVK != 0 {
				current.Session.Reset()
				heldVK = 0
			}
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}
		heldVK = vk

		if err := k.executeChain(ctx, current.Session, sw); err != nil {
			if ctx.Err() != nil {
				return
			}
			current.Log(fmt.Sprintf("keychain stopped: %v", err))
			return
		}
	}
}

// heldChainTrigger follows one switch until that physical key is released.
// Other keys and a second switch cannot take over mid-hold.
func heldChainTrigger(current KeyChainConfig, heldVK int32) (int32, KeyChainSwitch, bool) {
	if heldVK != 0 {
		if !PhysicalKeyDown(heldVK) {
			return 0, KeyChainSwitch{}, false
		}
		if sw, ok := switchForTrigger(current, heldVK); ok {
			return heldVK, sw, true
		}
		return 0, KeyChainSwitch{}, false
	}
	return firstHeldChainTrigger(current)
}

func firstHeldChainTrigger(current KeyChainConfig) (int32, KeyChainSwitch, bool) {
	for _, sw := range current.Switches {
		if !sw.Active() {
			continue
		}
		trigger := sw.Keys[0]
		if PhysicalKeyDown(trigger) {
			return trigger, sw, true
		}
	}
	return 0, KeyChainSwitch{}, false
}

func chainTriggerVKs(cfg KeyChainConfig) []int32 {
	var vks []int32
	for _, sw := range cfg.Switches {
		if sw.Active() {
			vks = append(vks, sw.Keys[0])
		}
	}
	return vks
}

func switchForTrigger(current KeyChainConfig, vk int32) (KeyChainSwitch, bool) {
	for _, sw := range current.Switches {
		if sw.Active() && sw.Keys[0] == vk {
			return sw, true
		}
	}
	return KeyChainSwitch{}, false
}

func (k *KeyChainRunner) executeChain(ctx context.Context, sess session.InputSession, sw KeyChainSwitch) error {
	for i := 0; i < KeyChainSlotCount; i++ {
		if sw.Keys[i] == 0 {
			continue
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if emergencyDown() {
			return nil
		}
		if err := tapChainKey(sess, sw.Keys[i]); err != nil {
			return err
		}
		sleepChainDelay(ctx, time.Duration(sw.DelaysMs[i])*time.Millisecond)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

func tapChainKey(sess session.InputSession, vk int32) error {
	SetTappingVK(vk)
	defer SetTappingVK(0)
	return sess.TapKey(vk, timing.KeyTapHold)
}

// sleepChainDelay waits out a step delay the same way the clicker waits out
// DelayMs: poll so Stop and End/F12 cancel it. The trigger is not re-checked
// to abort the pass; a tap must still finish the remaining keys.
func sleepChainDelay(ctx context.Context, d time.Duration) {
	deadline := time.Now().Add(d)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		if emergencyDown() {
			return
		}
		wait := time.Until(deadline)
		if wait > timing.PollInterval {
			wait = timing.PollInterval
		}
		timing.Sleep(ctx, wait)
	}
}
