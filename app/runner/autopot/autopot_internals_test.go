package autopot

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAutoPotDefaultsMissingLog(t *testing.T) {
	cfg := AutoPotConfig{Core: CoreConfig{Session: &recordSession{}}}
	ap := NewAutoPot(cfg)
	if ap.settings().Core.Log == nil {
		t.Fatal("NewAutoPot should default a missing logger")
	}
}

// ---------------------------------------------------------------------------
// ReaderFactory.Build tests
// ---------------------------------------------------------------------------

func TestReaderFactory_AddressMode(t *testing.T) {
	// Address mode with a real PID — attempts to create address reader,
	// but win.GetProcessBaseAddr will fail in test (no such process).
	// The reader should fall back to visual mode gracefully.
	cfg := AutoPotConfig{
		Address: &AddressConfig{
			ProcessPID: 12345,
		},
		Core: CoreConfig{
			HPThreshold: 50,
			SPThreshold: 50,
			Log:         func(string) {},
		},
	}
	ap := NewAutoPot(cfg)

	reader, pixel, ocr, isAddress := NewReaderFactory(ap.settings, ap.hpStabilizer, ap.spStabilizer).Build()

	// In test env, win.GetProcessBaseAddr fails → falls back to visual.
	// This is expected — what matters is that the fallback is clean.
	if isAddress {
		t.Log("ReaderFactory.Build: address mode succeeded (real process exists)")
		return
	}
	t.Log("ReaderFactory.Build: address mode fell back to visual (expected in test)")
	if reader == nil {
		t.Fatal("ReaderFactory.Build: expected non-nil reader after fallback")
	}
	if pixel == nil {
		t.Error("ReaderFactory.Build: expected non-nil pixelBarReader after fallback")
	}
	_ = ocr // ocr may be nil (no OCR pipeline in some envs)
}

func TestReaderFactory_AddressModeFallback(t *testing.T) {
	// Address mode with PID=0 — should fall back to visual (pixel/OCR).
	cfg := AutoPotConfig{
		Core: CoreConfig{
			HPThreshold: 50,
			SPThreshold: 50,
			Log:         func(string) {},
		},
	}
	ap := NewAutoPot(cfg)

	reader, pixel, ocr, isAddress := NewReaderFactory(ap.settings, ap.hpStabilizer, ap.spStabilizer).Build()

	if isAddress {
		t.Error("ReaderFactory.Build: expected isAddress=false for AddressMode with PID=0")
	}
	if reader == nil {
		t.Fatal("ReaderFactory.Build: expected non-nil reader")
	}
	if pixel == nil {
		t.Error("ReaderFactory.Build: expected non-nil pixelBarReader")
	}
	// OCR may be nil if NewDefaultPipeline() fails (no glyphs in some test envs).
	// That's fine — pixel-only fallback is expected.
	if ocr != nil {
		t.Logf("ReaderFactory.Build: OCR reader created (pipeline available)")
	} else {
		t.Log("ReaderFactory.Build: OCR reader nil (pipeline unavailable in test env — expected)")
	}
}

func TestReaderFactory_VisualModeNoPID(t *testing.T) {
	// Visual mode with no address config — pure pixel/OCR path.
	cfg := AutoPotConfig{
		Core: CoreConfig{
			HPThreshold: 50,
			SPThreshold: 50,
			Log:         func(string) {},
		},
	}
	ap := NewAutoPot(cfg)

	reader, pixel, ocr, isAddress := NewReaderFactory(ap.settings, ap.hpStabilizer, ap.spStabilizer).Build()

	if isAddress {
		t.Error("ReaderFactory.Build: expected isAddress=false for Visual mode")
	}
	if reader == nil {
		t.Fatal("ReaderFactory.Build: expected non-nil reader")
	}
	if pixel == nil {
		t.Error("ReaderFactory.Build: expected non-nil pixelBarReader")
	}
	_ = ocr // ocr may be nil depending on pipeline availability
}

// ---------------------------------------------------------------------------
// dispatchVisual tests
// ---------------------------------------------------------------------------

func TestDispatchVisual_OCRSwitchToPixel(t *testing.T) {
	// When OCR returns StatusInvalid, dispatchVisual should switch
	// to pixel and return false (iteration consumed).
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	reader := BarReader(ocr) // reader == ocr initially

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("ocr failed")}
	nextOCRRetry := time.Time{}

	proceed := ap.handleOCR(cfg, &reader, pixel, result, &nextOCRRetry)
	if !proceed {
		t.Error("handleOCR: expected true (iteration consumed) on OCR failure")
	}
	// Reader should now point to pixel.
	if reader != pixel {
		t.Error("handleOCR: reader was not switched to pixel on OCR failure")
	}
}

func TestDispatchVisual_OCRSuccess(t *testing.T) {
	// When OCR returns StatusFound, dispatchVisual should return true
	// (proceed to normal processing).
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{}
	ocr := &statusUIReader{}
	reader := BarReader(ocr)

	cfg := AutoPotConfig{Core: CoreConfig{}}
	result := BarReadResult{Status: StatusFound, HP: 80, SP: 80}
	nextOCRRetry := time.Time{}

	proceed := ap.handleOCR(cfg, &reader, pixel, result, &nextOCRRetry)
	if proceed {
		t.Error("handleOCR: expected false (not consumed) on OCR success")
	}
	if reader != ocr {
		t.Error("handleOCR: reader should still be ocr on success")
	}
}

// ---------------------------------------------------------------------------
// handleDead tests
// ---------------------------------------------------------------------------

func TestHandleDead_NotDead(t *testing.T) {
	ap := &AutoPotRunner{}
	dead := false
	ctx := context.Background()
	cfg := AutoPotConfig{Core: CoreConfig{HPEnabled: true, HPKeyVK: 'Q', Log: func(string) {}}}
	result := BarReadResult{Status: StatusFound, HP: 80}

	consumed := ap.handleDead(ctx, cfg, result, &dead)
	if consumed {
		t.Error("handleDead: expected false for StatusFound")
	}
	if dead {
		t.Error("handleDead: dead should remain false for StatusFound")
	}
}

func TestHandleDead_FirstDead(t *testing.T) {
	ap := &AutoPotRunner{}
	dead := false
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	logged := ""
	cfg := AutoPotConfig{
		Core: CoreConfig{
		Session:   &recordSession{},
		HPEnabled: true,
		HPKeyVK:   'Q',
		Log:       func(s string) { logged = s },
		},
	}
	result := BarReadResult{Status: StatusDead}

	consumed := ap.handleDead(ctx, cfg, result, &dead)
	if !consumed {
		t.Error("handleDead: expected true for StatusDead")
	}
	if !dead {
		t.Error("handleDead: dead should be true after StatusDead")
	}
	if logged != "autopot: character dead (HP=1)" {
		t.Errorf("handleDead: log = %q; want %q", logged, "autopot: character dead (HP=1)")
	}
}

func TestHandleDead_RepeatDead(t *testing.T) {
	ap := &AutoPotRunner{}
	dead := true // already marked dead
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	logCount := 0
	cfg := AutoPotConfig{
		Core: CoreConfig{
		Session:   &recordSession{},
		HPEnabled: true,
		HPKeyVK:   'Q',
		Log:       func(s string) { logCount++ },
		},
	}
	result := BarReadResult{Status: StatusDead}

	consumed := ap.handleDead(ctx, cfg, result, &dead)
	if !consumed {
		t.Error("handleDead: expected true for StatusDead (already dead)")
	}
	if logCount != 0 {
		t.Error("handleDead: should not log on repeat dead")
	}
}

// ---------------------------------------------------------------------------
// handleOCR tests
// ---------------------------------------------------------------------------

func TestHandleOCR_Found(t *testing.T) {
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{}
	ocr := &statusUIReader{}
	reader := BarReader(ocr)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusFound, HP: 80, SP: 80}
	nextOCRRetry := time.Time{}

	proceed := ap.handleOCR(cfg, &reader, pixel, result, &nextOCRRetry)
	if proceed {
		t.Error("handleOCR: expected false for StatusFound")
	}
	if reader != ocr {
		t.Error("handleOCR: reader should not change on StatusFound")
	}
}

func TestHandleOCR_FailureSwitch(t *testing.T) {
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	reader := BarReader(ocr)

	modeFns := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
		Log:           func(string) {},
		OnStatusUIMode: func(s string) { modeFns = append(modeFns, s) },
		},
	}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("ocr lost panel")}
	nextOCRRetry := time.Time{}

	proceed := ap.handleOCR(cfg, &reader, pixel, result, &nextOCRRetry)
	if !proceed {
		t.Error("handleOCR: expected true (consumed) on OCR failure")
	}
	if reader != pixel {
		t.Error("handleOCR: reader should switch to pixel on failure")
	}
	if len(modeFns) == 0 || modeFns[len(modeFns)-1] != "Pixelsearch" {
		t.Errorf("handleOCR: expected Pixelsearch mode, got %v", modeFns)
	}
	if nextOCRRetry.IsZero() {
		t.Error("handleOCR: nextOCRRetry should be set")
	}
}

func TestHandleOCR_DeadSwitchesToPixel(t *testing.T) {
	// StatusDead should also trigger the pixel fallback.
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	reader := BarReader(ocr)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusDead, Err: fmt.Errorf("HP=1")}
	nextOCRRetry := time.Time{}

	proceed := ap.handleOCR(cfg, &reader, pixel, result, &nextOCRRetry)
	if !proceed {
		t.Error("handleOCR: expected true for StatusDead")
	}
	if reader != pixel {
		t.Error("handleOCR: reader should switch to pixel on StatusDead")
	}
}

// ---------------------------------------------------------------------------
// potsEndedStep tests
// ---------------------------------------------------------------------------

func TestPotsEndedStep_NotEnded(t *testing.T) {
	ap := &AutoPotRunner{}
	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	now := time.Now()

	ended, hs := ap.potsEndedStep(cfg, true, time.Second, 30, 30, false, now)
	if ended {
		t.Error("potsEndedStep: should not detect pots-ended after 1s")
	}
	if hs != now {
		t.Error("potsEndedStep: should return unchanged healStart")
	}
}

func TestPotsEndedStep_DetectsEnded(t *testing.T) {
	ap := &AutoPotRunner{}
	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}, HPKeyName: "F1"}}

	logged := ""
	cfg.Core.Log = func(s string) { logged = s }
	now := time.Now()

	ended, _ := ap.potsEndedStep(cfg, true, 4*time.Second, 30, 30, false, now)
	if !ended {
		t.Error("potsEndedStep: should detect pots-ended after 3s with no change")
	}
	if logged != "autopot: HP pots ended on F1 — slowing to 1s taps" {
		t.Errorf("potsEndedStep: log = %q; want %q", logged, "autopot: HP pots ended on F1 — slowing to 1s taps")
	}
}

func TestPotsEndedStep_Recovers(t *testing.T) {
	ap := &AutoPotRunner{}
	modeCalls := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
		Log:           func(string) {},
		OnStatusUIMode: func(s string) { modeCalls = append(modeCalls, s) },
		},
	}
	now := time.Now()

	ended, hs := ap.potsEndedStep(cfg, true, 4*time.Second, 60, 30, true, now)
	if ended {
		t.Error("potsEndedStep: should detect recovery (30→60)")
	}
	// On recovery, healStart is reset to time.Now() which should be >= now.
	if hs.Before(now) {
		t.Error("potsEndedStep: healStart should be reset to current time on recovery")
	}
}

func TestPotsEndedStep_ReAppliesLabel(t *testing.T) {
	ap := &AutoPotRunner{}
	modeCalls := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
		Log:           func(string) {},
		OnStatusUIMode: func(s string) { modeCalls = append(modeCalls, s) },
		HPKeyName:     "F1",
		},
	}
	now := time.Now()

	ended, hs := ap.potsEndedStep(cfg, true, 4*time.Second, 30, 30, true, now)
	if !ended {
		t.Error("potsEndedStep: should still be in pots-ended mode")
	}
	if hs != now {
		t.Error("potsEndedStep: healStart should not change when still ended")
	}
	if len(modeCalls) == 0 || modeCalls[len(modeCalls)-1] != "HP pots ended on F1" {
		t.Errorf("potsEndedStep: expected HP pots ended on F1 mode, got %v", modeCalls)
	}
}

// ---------------------------------------------------------------------------
// potsEndedTap tests
// ---------------------------------------------------------------------------

func TestPotsEndedTap_SuccessfulHeal(t *testing.T) {
	// When the value changes >= 1% after tap, potsEndedTap returns true.
	sess := &recordSession{}
	reader := &constantReader{hp: 70, sp: 80} // reader returns value after tap
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	ap := &AutoPotRunner{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	recovered := ap.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if !recovered {
		t.Error("potsEndedTap: expected true when value rises (30→70)")
	}
	if taps := sess.tapCount.Load(); taps != 1 {
		t.Errorf("potsEndedTap: expected 1 TapKey call, got %d", taps)
	}
}

func TestPotsEndedTap_NoRecovery(t *testing.T) {
	// When the value stays the same after tap, potsEndedTap returns false.
	sess := &recordSession{}
	reader := &constantReader{hp: 30, sp: 80} // same value
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	ap := &AutoPotRunner{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	recovered := ap.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if recovered {
		t.Error("potsEndedTap: expected false when value unchanged (30→30)")
	}
}

func TestPotsEndedTap_TapKeyError(t *testing.T) {
	// When TapKey returns error, potsEndedTap returns false.
	sess := &errorSession{}
	reader := &constantReader{hp: 70, sp: 80}
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	ap := &AutoPotRunner{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	recovered := ap.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if recovered {
		t.Error("potsEndedTap: expected false when TapKey fails")
	}
}

// errorSession returns error on TapKey.
type errorSession struct{}

func (s *errorSession) TapKey(_ int32, _ time.Duration) error { return fmt.Errorf("session error") }
func (s *errorSession) MouseClick(_ time.Duration) error       { return nil }

// ---------------------------------------------------------------------------
// dispatchVisual: full path tests
// ---------------------------------------------------------------------------

func TestDispatchVisual_OCR_Proceed(t *testing.T) {
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{}
	ocr := &statusUIReader{}
	reader := BarReader(ocr)

	cfg := AutoPotConfig{Core: CoreConfig{}}
	result := BarReadResult{Status: StatusFound, HP: 80}
	nextOCRRetry := time.Time{}
	var pfs time.Time
	lpf := false

	proceed := ap.dispatchVisual(context.Background(), cfg, &reader, pixel, ocr, &result, &nextOCRRetry, &pfs, &lpf)
	if !proceed {
		t.Error("dispatchVisual: expected true for OCR StatusFound")
	}
}

func TestDispatchVisual_Pixel_BarsFound_Proceed(t *testing.T) {
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{}
	reader := BarReader(pixel)

	cfg := AutoPotConfig{Core: CoreConfig{}}
	result := BarReadResult{Status: StatusFound, HP: 80}
	nextOCRRetry := time.Now().Add(time.Hour) // don't probe OCR
	var pfs time.Time
	lpf := false

	// Passing nil for ocr — handlePixel checks ocr != nil before probing.
	proceed := ap.dispatchVisual(context.Background(), cfg, &reader, pixel, nil, &result, &nextOCRRetry, &pfs, &lpf)
	if !proceed {
		t.Error("dispatchVisual: expected true for pixel StatusFound")
	}
}

func TestDispatchVisual_Pixel_BarsNotFound(t *testing.T) {
	ap := &AutoPotRunner{}
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	reader := BarReader(pixel)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("no bars found")}
	nextOCRRetry := time.Now().Add(time.Hour) // don't probe OCR
	var pfs time.Time
	lpf := false

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	proceed := ap.dispatchVisual(ctx, cfg, &reader, pixel, ocr, &result, &nextOCRRetry, &pfs, &lpf)
	if proceed {
		t.Error("dispatchVisual: expected false when pixel bars not found")
	}
}

// ---------------------------------------------------------------------------
// Concurrent safety: ReaderFactory.Build + handleDead under -race
// ---------------------------------------------------------------------------

func TestReaderFactoryConcurrentRace(t *testing.T) {	cfg := AutoPotConfig{
		Core: CoreConfig{
			HPThreshold: 50,
			SPThreshold: 50,
			Log:         func(string) {},
		},
	}
	ap := NewAutoPot(cfg)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, _ = NewReaderFactory(ap.settings, ap.hpStabilizer, ap.spStabilizer).Build()
		}()
	}
	wg.Wait()
}

func TestHandleDeadConcurrentRace(t *testing.T) {
	ap := &AutoPotRunner{}
	dead := false
	ctx := context.Background()
	cfg := AutoPotConfig{Core: CoreConfig{HPEnabled: true, HPKeyVK: 'Q', Log: func(string) {}}}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ap.handleDead(ctx, cfg, BarReadResult{Status: StatusFound, HP: 80}, &dead)
		}()
	}
	wg.Wait()
}
