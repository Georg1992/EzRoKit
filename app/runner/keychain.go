// KeyChainRunner plays a sequence of keys when its trigger key is held down.
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

// KeyChainSwitch is one chain: Keys[0] is the trigger, remaining slots are
// the sequence to tap while the trigger is held.
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
				if !c.Active() {
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

func (k *KeyChainRunner) run(ctx context.Context, _ KeyChainConfig) {
	for {
		if ctx.Err() != nil {
			return
		}
		current := k.settings()
		if !current.Active() {
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}

		for i, sw := range current.Switches {
			if !sw.Active() {
				continue
			}
			trigger := sw.Keys[0]
			if !PhysicalKeyDown(trigger) {
				continue
			}
			if err := k.executeChain(ctx, current.Session, sw); err != nil {
				if ctx.Err() != nil {
					return
				}
				current.Log(fmt.Sprintf("KeyChain switch %d failed: %v", i+1, err))
			}
		}
		timing.Sleep(ctx, timing.PollInterval)
	}
}

func (k *KeyChainRunner) executeChain(ctx context.Context, sess session.InputSession, sw KeyChainSwitch) error {
	trigger := sw.Keys[0]
	for i := 1; i < KeyChainSlotCount; i++ {
		if sw.Keys[i] == 0 {
			continue
		}
		if !PhysicalKeyDown(trigger) {
			return nil
		}
		if err := sess.TapKey(sw.Keys[i], timing.KeyTapHold); err != nil {
			return err
		}
		delay := time.Duration(sw.DelaysMs[i]) * time.Millisecond
		timing.Sleep(ctx, delay)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}
