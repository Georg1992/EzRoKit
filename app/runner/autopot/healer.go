package autopot

import (
	"context"
	"fmt"
	"time"

	"ezrokit/runner/internal/timing"
)

const (
	potsEndedDelay  = 1 * time.Second // tap interval when pots appear empty
	noChangeTimeout = 3 * time.Second // no value change → assume pots ended
	valueChangeTol  = 1.0             // tolerance for value-change detection (%)
)

// healer owns potion policy: target selection, fast tapping, and the
// conservative empty-pot fallback. It depends on live settings but knows
// nothing about reader selection or lifecycle management.
type healer struct {
	settings func() AutoPotConfig
}

func (h *healer) healUntil(ctx context.Context, reader BarReader, hpBar bool) {
	h.healUntilWithInitial(ctx, reader, hpBar, nil)
}

// healUntilWithInitial is healUntil with an optional already-validated
// reading from mainLoop. Reusing that snapshot avoids a duplicate capture/
// OCR parse before the first potion tap.
func (h *healer) healUntilWithInitial(ctx context.Context, reader BarReader, hpBar bool, initial *BarReadResult) {
	var (
		healStart time.Time
		lastPct   = -1.0
		potsEnded bool
	)
	defer func() {
		cfg := h.settings()
		clearPotsEndedMode(cfg.Core.OnStatusUIMode, potsEnded)
	}()

	for {
		if ctx.Err() != nil {
			return
		}

		cfg := h.settings()
		if cfg.Core.Session == nil {
			timing.Sleep(ctx, timing.PollInterval)
			continue
		}

		vk, ok := healTarget(cfg, hpBar)
		if !ok {
			return
		}

		var result BarReadResult
		if initial != nil {
			result = *initial
			initial = nil
		} else {
			result = reader.ReadValues(ctx)
		}
		if result.Status != StatusFound {
			return
		}

		pct := result.HP
		threshold := float64(cfg.Core.HPThreshold)
		if !hpBar {
			pct = result.SP
			threshold = float64(cfg.Core.SPThreshold)
		}
		if pct >= threshold {
			return
		}

		if healStart.IsZero() {
			healStart = time.Now()
			lastPct = pct
		}

		elapsed := time.Since(healStart)
		potsEnded, healStart = h.potsEndedStep(cfg, hpBar, elapsed, pct, lastPct, potsEnded, healStart)
		if potsEnded {
			recovered, ok := h.potsEndedTap(ctx, cfg, vk, reader, hpBar, pct)
			if !ok {
				return
			}
			if recovered {
				potsEnded, healStart = false, time.Now()
			}
			continue
		}

		lastPct = pct
		if !h.healTap(ctx, cfg, vk) {
			return
		}
	}
}

func (h *healer) potsEndedStep(cfg AutoPotConfig, hpBar bool, elapsed time.Duration, pct, lastPct float64, potsEnded bool, healStart time.Time) (bool, time.Time) {
	if !potsEnded && elapsed >= noChangeTimeout && absPctDiff(pct, lastPct) < valueChangeTol {
		cfg.Core.Log(fmt.Sprintf("autopot: %s — slowing to 1s taps", potsEndedLabel(cfg, hpBar)))
		potsEnded = true
	}
	if !potsEnded {
		return false, healStart
	}
	if absPctDiff(pct, lastPct) >= valueChangeTol {
		cfg.Core.Log("autopot: potion took effect, resuming normal speed")
		setMode(cfg.Core.OnStatusUIMode, "")
		return false, time.Now()
	}
	setMode(cfg.Core.OnStatusUIMode, potsEndedLabel(cfg, hpBar))
	return true, healStart
}

func (h *healer) potsEndedTap(ctx context.Context, cfg AutoPotConfig, vk int32, reader BarReader, hpBar bool, beforePct float64) (bool, bool) {
	if cfg.Core.Session == nil {
		return false, false
	}
	if err := cfg.Core.Session.TapKey(vk, timing.KeyTapHold); err != nil {
		cfg.Core.Log(fmt.Sprintf("Key VK_0x%02X failed: %v", vk, err))
		return false, false
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
			return true, true
		}
	}
	timing.Sleep(ctx, potsEndedDelay)
	return false, true
}

func (h *healer) healTap(ctx context.Context, cfg AutoPotConfig, vk int32) bool {
	if cfg.Core.Session == nil {
		return false
	}
	if err := cfg.Core.Session.TapKey(vk, timing.KeyTapHold); err != nil {
		cfg.Core.Log(fmt.Sprintf("Key VK_0x%02X failed: %v", vk, err))
		return false
	}
	return true
}

func clearPotsEndedMode(fn func(string), potsEnded bool) {
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
