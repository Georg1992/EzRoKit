// Package autopot is the HP/SP auto-potion runner.
//
// The runner coordinates lifecycle and high-level priority only. Reader
// failover lives in readerController; potion timing and empty-pot policy live
// in healer. This keeps platform acquisition, reader recovery, and healing
// policy independently testable without changing the public runner API.
package autopot

import (
	"context"
	"fmt"

	"ezrokit/runner/internal/lifecycle"
	"ezrokit/runner/internal/timing"
)

// AutoPotRunner heals HP/SP based on readings from the active BarReader.
type AutoPotRunner struct {
	lc *lifecycle.Lifecycle[AutoPotConfig]

	hpStabilizer *BarStabilizer
	spStabilizer *BarStabilizer
	healer       *healer
}

// NewAutoPot constructs an AutoPotRunner with the given initial config.
func NewAutoPot(cfg AutoPotConfig) *AutoPotRunner {
	cfg.applyDefaults()
	a := &AutoPotRunner{
		lc: lifecycle.New(
			cfg,
			func(c AutoPotConfig) error {
				return c.validate()
			},
			nil, // cleanup is handled by defer resetStabilizers() inside run()
		),
		hpStabilizer: NewBarStabilizer(true, cfg.Core.HPThreshold),
		spStabilizer: NewBarStabilizer(false, cfg.Core.SPThreshold),
	}
	a.healer = &healer{settings: a.settings}
	return a
}

// Running reports whether the heal loop is currently active.
func (a *AutoPotRunner) Running() bool { return a.lc.Running() }

// UpdateSettings propagates new settings to the stabilisers.
//
// IMPORTANT: Log, OnStatusParsed, OnStatusUIMode, and Session are
// preserved from the existing config. The GUI layer passes bare
// callbacks (a.appendLog, a.onStatusParsed) without Synchronize;
// the initial startup replaces them with Synchronize-wrapped
// versions. We must keep those wrappers so UI calls from the
// autopot goroutine always marshal to the GUI thread.
func (a *AutoPotRunner) UpdateSettings(cfg AutoPotConfig) {
	old := a.settings()
	cfg.Core.Log = old.Core.Log
	cfg.applyDefaults()
	cfg.Core.OnStatusParsed = old.Core.OnStatusParsed
	cfg.Core.OnStatusUIMode = old.Core.OnStatusUIMode
	cfg.Core.Session = old.Core.Session
	a.lc.UpdateSettings(cfg)
	a.hpStabilizer.SetThreshold(cfg.Core.HPThreshold)
	a.spStabilizer.SetThreshold(cfg.Core.SPThreshold)
}

// Start launches the healer.
func (a *AutoPotRunner) Start() error {
	if err := a.lc.Start(a.run); err != nil {
		return fmt.Errorf("autopot: %w", err)
	}
	return nil
}

// Stop signals the healer to exit.
func (a *AutoPotRunner) Stop() { a.lc.Stop() }

// Wait blocks until the healer goroutine has exited.
func (a *AutoPotRunner) Wait() { a.lc.Wait() }

func (a *AutoPotRunner) settings() AutoPotConfig { return a.lc.Settings() }

func (a *AutoPotRunner) resetStabilizers() {
	a.hpStabilizer.Reset()
	a.spStabilizer.Reset()
}

// run builds the reader set once, then delegates visual recovery and healing
// policy to their focused controllers.
func (a *AutoPotRunner) run(ctx context.Context, _ AutoPotConfig) {
	defer a.resetStabilizers()

	factory := NewReaderFactory(a.settings, a.hpStabilizer, a.spStabilizer)
	reader, pixel, ocr, isAddress := factory.Build()
	a.mainLoop(ctx, reader, pixel, ocr, isAddress)
}

// mainLoop owns only the high-level decision order: read one coherent HP/SP
// snapshot, heal HP first, then SP, and maintain the normal poll cadence.
func (a *AutoPotRunner) mainLoop(ctx context.Context, reader BarReader, pixel *pixelBarReader, ocr *statusUIReader, isAddress bool) {
	controller := newReaderController(reader, pixel, ocr, isAddress)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		cfg := a.settings()
		if cfg.Core.Session == nil {
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}

		result := controller.reader().ReadValues(ctx)
		if !controller.process(ctx, cfg, result) {
			if controller.isAddress() {
				timing.Sleep(ctx, timing.PollInterval)
			} else {
				controller.probeOCR(ctx, cfg)
			}
			continue
		}
		controller.markValid()

		if cfg.Core.HPEnabled && result.HPLow {
			a.healer.healUntilWithInitial(ctx, controller.reader(), true, &result)
			continue
		}
		if cfg.Core.SPEnabled && result.SPLow {
			a.healer.healUntilWithInitial(ctx, controller.reader(), false, &result)
			continue
		}

		// Recovery is deliberately after normal processing so it cannot
		// delay acting on a valid HP/SP snapshot.
		controller.probeOCR(ctx, cfg)
		timing.Sleep(ctx, timing.PollInterval)
	}
}
