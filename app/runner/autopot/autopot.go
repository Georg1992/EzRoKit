// Package autopot is the HP/SP auto-potion runner.
//
// Architecture:
//
//   - BarReader interface — HP/SP detectors produce percentage readings.
//     Two implementations: pixelBarReader (colour-based, always-available)
//     and statusUIReader (OCR-based, higher precision).
//
//   - AutoPotRunner.run() — orchestrator that picks the active reader,
//     reads HP/SP, and calls healUntil when values drop below thresholds.
//
//   - healUntil() — unified heal loop that presses a potion key and
//     spin-reads via the active BarReader until the stat rises above
//     threshold. Replaces two duplicate ~140-line heal functions.
//
// Lifecycle bookkeeping is in internal/lifecycle; timing constants in
// internal/timing; InputSession interface in internal/session.
package autopot

import (
	"context"
	"fmt"
	"time"

	"belarus-champ-tools/runner/internal/lifecycle"
	"belarus-champ-tools/runner/internal/timing"
)

// AutoPotConfig is defined in config.go (composite of CoreConfig + optional AddressConfig).

// AutoPotRunner heals HP/SP based on readings from the active BarReader.
type AutoPotRunner struct {
	lc *lifecycle.Lifecycle[AutoPotConfig]

	hpStabilizer *BarStabilizer
	spStabilizer *BarStabilizer
}

// NewAutoPot constructs an AutoPotRunner with the given initial config.
func NewAutoPot(cfg AutoPotConfig) *AutoPotRunner {
	cfg.applyDefaults()
	return &AutoPotRunner{
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

// pixelModeSentinel is passed to OnStatusParsed when switching from OCR
// to pixel mode. The -1 values signal "no OCR data available" to the
// overlay, which displays an appropriate fallback indicator.
const (
	pixelModeSentinel      = -1
	ocrProbeInterval       = 2 * time.Second
	statusUIRetryInterval  = 5 * time.Second
)

// run is the main autopot loop.
//
//  1. Builds readers via ReaderFactory based on config.
//  2. Each tick: read HP/SP from the active reader. If HP or SP drops
//     below its threshold, call healUntil to press the potion key and
//     wait for the value to rise.
//  3. If the OCR reader fails (panel lost, parse error), switch
//     immediately to pixel-bar. Every 5 s, probe the OCR reader and
//     switch back if it recovers.
func (a *AutoPotRunner) run(ctx context.Context, cfg AutoPotConfig) {
	defer a.resetStabilizers()

	factory := NewReaderFactory(a.settings, a.hpStabilizer, a.spStabilizer)
	reader, pixel, ocr, isAddress := factory.Build()
	a.mainLoop(ctx, reader, pixel, ocr, isAddress)
}

// mainLoop is the core autopot polling loop. It reads HP/SP, dispatches
// to the active reader's handler, and calls healUntil when thresholds
// are breached.
func (a *AutoPotRunner) mainLoop(ctx context.Context, reader BarReader, pixel *pixelBarReader, ocr *statusUIReader, isAddress bool) {
	nextOCRRetry := time.Time{}
	loggedPixelFail := false
	pixelFailStart := time.Time{}
	dead := false

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

		result := reader.ReadValues(ctx)

		if a.handleDead(ctx, cfg, result, &dead) {
			continue
		}

		if dead && result.Status == StatusFound {
			dead = false
			setMode(cfg.Core.OnStatusUIMode, reader.Name())
		}

		// Dispatch to the active reader's handler.
		if isAddress {
			if result.Status != StatusFound {
				timing.Sleep(ctx, timing.PollInterval)
				continue
			}
		} else if !a.dispatchVisual(ctx, cfg, &reader, pixel, ocr, &result, &nextOCRRetry, &pixelFailStart, &loggedPixelFail) {
			continue
		}

		// Normal processing — result is valid (StatusFound).
		pixelFailStart = time.Time{}
		loggedPixelFail = false

		if cfg.Core.HPEnabled && result.HPLow {
			a.healUntil(ctx, reader, true)
			continue
		}
		if cfg.Core.SPEnabled && result.SPLow {
			a.healUntil(ctx, reader, false)
			continue
		}

		if isAddress {
			timing.Sleep(ctx, timing.PollInterval) // 10ms for address mode
		} else {
			timing.Sleep(ctx, timing.CaptureRetryDelay) // 50ms for pixel/OCR
		}
	}
}

// dispatchVisual handles OCR→pixel fallback and pixel→OCR recovery.
// Returns true if the result can proceed to normal processing.
func (a *AutoPotRunner) dispatchVisual(ctx context.Context, cfg AutoPotConfig, reader *BarReader, pixel BarReader, ocr *statusUIReader, result *BarReadResult, nextOCRRetry *time.Time, pixelFailStart *time.Time, loggedPixelFail *bool) bool {
	if *reader == ocr {
		return !a.handleOCR(cfg, reader, pixel, *result, nextOCRRetry)
	}
	return !a.handlePixel(ctx, cfg, reader, pixel, ocr, result, nextOCRRetry, pixelFailStart, loggedPixelFail)
}

// handleDead handles the StatusDead case. Returns true if the iteration was
// consumed (caller should continue the loop). The dead block has already slept.
func (a *AutoPotRunner) handleDead(ctx context.Context, cfg AutoPotConfig, result BarReadResult, dead *bool) bool {
	if result.Status != StatusDead {
		return false
	}
	if !*dead {
		cfg.Core.Log("autopot: character dead (HP=1)")
		*dead = true
	}
	setMode(cfg.Core.OnStatusUIMode, "Dead")
	if cfg.Core.HPEnabled && cfg.Core.HPKeyVK != 0 {
		if err := cfg.Core.Session.TapKey(cfg.Core.HPKeyVK, timing.KeyTapHold); err != nil {
			cfg.Core.Log(fmt.Sprintf("Key VK_0x%02X failed: %v", cfg.Core.HPKeyVK, err))
		}
	}
	timing.Sleep(ctx, potsEndedDelay)
	return true
}

// handleOCR handles the case when the OCR reader is active and the result is
// not StatusDead. If the result is valid (StatusFound), returns false so the
// main loop proceeds to normal processing. If the OCR reader failed, switches
// to pixel-bar mode, sends the sentinel to the overlay, and returns true.
func (a *AutoPotRunner) handleOCR(cfg AutoPotConfig, reader *BarReader, pixel BarReader, result BarReadResult, nextOCRRetry *time.Time) bool {
	if result.Status == StatusFound {
		return false
	}
	// OCR reader failed — switch to pixel-bar fallback.
	cfg.Core.Log(fmt.Sprintf("autopot: statusui issue, switching to pixel-bar: %v", result.Err))
	*reader = pixel
	setMode(cfg.Core.OnStatusUIMode, "Pixelsearch")
	if cfg.Core.OnStatusParsed != nil {
		cfg.Core.OnStatusParsed(pixelModeSentinel, 0, pixelModeSentinel, 0, 0, 0, 0, 0)
	}
	*nextOCRRetry = time.Now().Add(ocrProbeInterval)
	return true
}

// handlePixel handles the case when the pixel reader is active and the result
// is not StatusDead. It performs two tasks:
//
//  1. OCR recovery probe: periodically probes the OCR reader. If OCR recovers
//     (StatusFound or StatusDead), switches back to OCR and updates *result.
//     For StatusDead, returns true so the next iteration handles dead state.
//     For StatusFound, returns false so normal processing uses the probe data.
//
//  2. Pixel failure: if bars are not found, tracks failure duration. For the
//     first 5 seconds polls fast (50ms); after 5s slows to 5s polling. Logs
//     the transition once.
//
// Returns true if the iteration was consumed (caller should continue loop).
func (a *AutoPotRunner) handlePixel(ctx context.Context, cfg AutoPotConfig, reader *BarReader, pixel BarReader, ocr *statusUIReader, result *BarReadResult, nextOCRRetry *time.Time, pixelFailStart *time.Time, loggedPixelFail *bool) bool {
	// OCR recovery probe — runs every ocrProbeInterval.
	if ocr != nil && time.Now().After(*nextOCRRetry) {
		*nextOCRRetry = time.Now().Add(ocrProbeInterval)
		probe := ocr.ReadValues(ctx)
		if probe.Status == StatusFound {
			cfg.Core.Log("autopot: statusui recovered, switching back")
			*reader = ocr
			setMode(cfg.Core.OnStatusUIMode, "OCR")
			*loggedPixelFail = false
			*result = probe
			return false // fall through to normal processing
		}
		if probe.Status == StatusDead {
			cfg.Core.Log("autopot: statusui recovered (dead)")
			*reader = ocr
			*loggedPixelFail = false
			*result = probe
			return true // next iteration: StatusDead check at top
		}
		// Probe didn't find anything — continue with pixel.
	}

	if result.Status == StatusFound {
		return false // normal processing
	}

	// Pixel bars not found.
	if pixelFailStart.IsZero() {
		*pixelFailStart = time.Now()
	}
	if time.Since(*pixelFailStart) < statusUIRetryInterval {
		timing.Sleep(ctx, timing.CaptureRetryDelay)
		return true
	}
	if !*loggedPixelFail {
		cfg.Core.Log(fmt.Sprintf("autopot: pixel bars not found for 5s — retrying every 5s: %v", result.Err))
		*loggedPixelFail = true
	}
	timing.Sleep(ctx, statusUIRetryInterval)
	return true
}

const (
	potsEndedDelay    = 1 * time.Second  // tap interval when pots appear empty
	noChangeTimeout   = 3 * time.Second  // no value change → assume pots ended
	valueChangeTol    = 1.0              // tolerance for value-change detection (%)
)

// healUntil presses the potion key and keeps pressing until the relevant
// stat rises above threshold or ctx is cancelled.
//
// Pots-ended detection: if the stat doesn't change for `noChangeTimeout`
// while we're spamming below threshold, assume the potion stack is empty.
// In this state the tap interval slows to `potsEndedDelay` (1s) and the
// overlay shows "HP pots ended" / "SP pots ended".
//
// Recovery in slow mode is checked immediately after each tap: we read the
// value before the tap, tap the key, then read the value again (before the
// 1s sleep). If the value changed >= 1%, the potion took effect and we
// exit slow mode instantly — no need to wait for the next loop iteration.
func (a *AutoPotRunner) healUntil(ctx context.Context, reader BarReader, hpBar bool) {
	var (
		healStart time.Time
		lastPct   = -1.0
		potsEnded bool
	)

	for {
		if ctx.Err() != nil {
			return
		}

		cfg := a.settings()
		if cfg.Core.Session == nil {
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}

		vk, ok := healTarget(cfg, hpBar)
		if !ok {
			return
		}

		result := reader.ReadValues(ctx)
		if result.Status != StatusFound {
			a.healExit(cfg, potsEnded)
			return
		}

		pct := result.HP
		threshold := float64(cfg.Core.HPThreshold)
		if !hpBar {
			pct = result.SP
			threshold = float64(cfg.Core.SPThreshold)
		}
		if pct >= threshold {
			a.healExit(cfg, potsEnded)
			return
		}

		// Initialise tracking on first iteration.
		if healStart.IsZero() {
			healStart = time.Now()
			lastPct = pct
		}

		// Manage pots-ended state and perform the heal tap.
		elapsed := time.Since(healStart)
		potsEnded, healStart = a.potsEndedStep(cfg, hpBar, elapsed, pct, lastPct, potsEnded, healStart)
		if potsEnded {
			if a.potsEndedTap(ctx, cfg, vk, reader, hpBar, pct) {
				potsEnded, healStart = false, time.Now()
			}
			continue
		}

		lastPct = pct

		if !a.healTap(ctx, cfg, vk) {
			return
		}
	}
}

// potsEndedStep checks if pots are empty (no value change after timeout)
// or if they've recovered. Returns (potsEnded, healStart).
func (a *AutoPotRunner) potsEndedStep(cfg AutoPotConfig, hpBar bool, elapsed time.Duration, pct, lastPct float64, potsEnded bool, healStart time.Time) (bool, time.Time) {
	if !potsEnded && elapsed >= noChangeTimeout && absPctDiff(pct, lastPct) < valueChangeTol {
		cfg.Core.Log(fmt.Sprintf("autopot: %s — slowing to 1s taps", potsEndedLabel(cfg, hpBar)))
		potsEnded = true
	}
	if !potsEnded {
		return false, healStart
	}
	// In pots-ended mode: check for recovery.
	if absPctDiff(pct, lastPct) >= valueChangeTol {
		cfg.Core.Log("autopot: potion took effect, resuming normal speed")
		setMode(cfg.Core.OnStatusUIMode, "")
		return false, time.Now()
	}
	setMode(cfg.Core.OnStatusUIMode, potsEndedLabel(cfg, hpBar))
	return true, healStart
}

// potsEndedTap taps the key, reads the value after, and returns true if
// the potion took effect (value changed >= valueChangeTol).
func (a *AutoPotRunner) potsEndedTap(ctx context.Context, cfg AutoPotConfig, vk int32, reader BarReader, hpBar bool, beforePct float64) bool {
	if err := cfg.Core.Session.TapKey(vk, timing.KeyTapHold); err != nil {
		cfg.Core.Log(fmt.Sprintf("Key VK_0x%02X failed: %v", vk, err))
		return false
	}
	afterResult := reader.ReadValues(ctx)
	if afterResult.Status == StatusFound {
		afterPct := afterResult.HP
		if !hpBar {
			afterPct = afterResult.SP
		}
		if absPctDiff(afterPct, beforePct) >= valueChangeTol {
			cfg.Core.Log("autopot: potion took effect, resuming normal speed")
			setMode(cfg.Core.OnStatusUIMode, "")
			return true
		}
	}
	timing.Sleep(ctx, potsEndedDelay)
	return false
}

// healTap presses the potion key and sleeps for PollInterval. Returns true
// on success; false on TapKey error or nil session (caller should return
// from healUntil).
func (a *AutoPotRunner) healTap(ctx context.Context, cfg AutoPotConfig, vk int32) bool {
	if cfg.Core.Session == nil {
		return false
	}
	if err := cfg.Core.Session.TapKey(vk, timing.KeyTapHold); err != nil {
		cfg.Core.Log(fmt.Sprintf("Key VK_0x%02X failed: %v", vk, err))
		return false
	}
	timing.Sleep(ctx, timing.PollInterval)
	return true
}

// healExit clears the pots-ended overlay mode when leaving healUntil.
// Called before every return in the heal loop.
func (a *AutoPotRunner) healExit(cfg AutoPotConfig, potsEnded bool) {
	a.clearPotsEndedMode(cfg.Core.OnStatusUIMode, potsEnded)
}

// clearPotsEndedMode clears the "Pots ended" overlay mode when leaving
// healUntil (regardless of exit reason: recovered, reader failed, ctx done).
func (a *AutoPotRunner) clearPotsEndedMode(fn func(string), potsEnded bool) {
	if potsEnded && fn != nil {
		fn("")
	}
}

func potsEndedLabel(cfg AutoPotConfig, hpBar bool) string {
	label := "HP pots ended"
	keyName := cfg.Core.HPKeyName
	if !hpBar {
		label = "SP pots ended"
		keyName = cfg.Core.SPKeyName
	}
	if keyName != "" {
		label += " on " + keyName
	}
	return label
}

func absPctDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func healTarget(cfg AutoPotConfig, hpBar bool) (vk int32, ok bool) {
	if hpBar {
		if !cfg.Core.HPEnabled || cfg.Core.HPKeyVK == 0 {
			return 0, false
		}
		return cfg.Core.HPKeyVK, true
	}
	if !cfg.Core.SPEnabled || cfg.Core.SPKeyVK == 0 {
		return 0, false
	}
	return cfg.Core.SPKeyVK, true
}

func setMode(fn func(string), mode string) {
	if fn != nil {
		fn(mode)
	}
}
