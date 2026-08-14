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
// readerController tests
// ---------------------------------------------------------------------------

func TestReaderController_OCRSwitchToPixel(t *testing.T) {
	// When OCR returns StatusInvalid, readerController should switch
	// to pixel and return false (iteration consumed).
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	controller := newReaderController(ocr, pixel, ocr, false)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("ocr failed")}

	proceed := controller.process(context.Background(), cfg, result)
	if proceed {
		t.Error("readerController: expected invalid OCR result to be consumed")
	}
	if controller.reader() != pixel {
		t.Error("readerController: reader was not switched to pixel on OCR failure")
	}
}

// ---------------------------------------------------------------------------
// readerController OCR tests
// ---------------------------------------------------------------------------

func TestReaderController_OCRFound(t *testing.T) {
	pixel := &pixelBarReader{}
	ocr := &statusUIReader{}
	controller := newReaderController(ocr, pixel, ocr, false)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusFound, HP: 80, SP: 80}

	if !controller.process(context.Background(), cfg, result) {
		t.Error("readerController: expected valid result to proceed")
	}
	if controller.reader() != ocr {
		t.Error("readerController: reader should not change on valid result")
	}
}

func TestReaderController_OCRFailureSwitch(t *testing.T) {
	pixel := &pixelBarReader{log: func(string) {}}
	ocr := &statusUIReader{}
	controller := newReaderController(ocr, pixel, ocr, false)

	modeFns := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Log:            func(string) {},
			OnStatusUIMode: func(s string) { modeFns = append(modeFns, s) },
		},
	}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("ocr lost panel")}

	if controller.process(context.Background(), cfg, result) {
		t.Error("readerController: expected invalid OCR result to be consumed")
	}
	if controller.reader() != pixel {
		t.Error("readerController: reader should switch to pixel on failure")
	}
	if len(modeFns) == 0 || modeFns[len(modeFns)-1] != "Pixelsearch" {
		t.Errorf("readerController: expected Pixelsearch mode, got %v", modeFns)
	}
	if controller.nextOCRRetry.IsZero() {
		t.Error("readerController: nextOCRRetry should be set")
	}
}

// ---------------------------------------------------------------------------
// potsEndedStep tests
// ---------------------------------------------------------------------------

func TestPotsEndedStep_NotEnded(t *testing.T) {
	h := &healer{}
	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	now := time.Now()

	ended, hs := h.potsEndedStep(cfg, true, time.Second, 30, 30, false, now)
	if ended {
		t.Error("potsEndedStep: should not detect pots-ended after 1s")
	}
	if hs != now {
		t.Error("potsEndedStep: should return unchanged healStart")
	}
}

func TestPotsEndedStep_DetectsEnded(t *testing.T) {
	h := &healer{}
	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}, HPKeyName: "F1"}}

	logged := ""
	cfg.Core.Log = func(s string) { logged = s }
	now := time.Now()

	ended, _ := h.potsEndedStep(cfg, true, 4*time.Second, 30, 30, false, now)
	if !ended {
		t.Error("potsEndedStep: should detect pots-ended after 3s with no change")
	}
	if logged != "autopot: HP pots ended on F1 — slowing to 1s taps" {
		t.Errorf("potsEndedStep: log = %q; want %q", logged, "autopot: HP pots ended on F1 — slowing to 1s taps")
	}
}

func TestPotsEndedStep_Recovers(t *testing.T) {
	h := &healer{}
	modeCalls := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Log:            func(string) {},
			OnStatusUIMode: func(s string) { modeCalls = append(modeCalls, s) },
		},
	}
	now := time.Now()

	ended, hs := h.potsEndedStep(cfg, true, 4*time.Second, 60, 30, true, now)
	if ended {
		t.Error("potsEndedStep: should detect recovery (30→60)")
	}
	// On recovery, healStart is reset to time.Now() which should be >= now.
	if hs.Before(now) {
		t.Error("potsEndedStep: healStart should be reset to current time on recovery")
	}
}

func TestPotsEndedStep_ReAppliesLabel(t *testing.T) {
	h := &healer{}
	modeCalls := []string{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Log:            func(string) {},
			OnStatusUIMode: func(s string) { modeCalls = append(modeCalls, s) },
			HPKeyName:      "F1",
		},
	}
	now := time.Now()

	ended, hs := h.potsEndedStep(cfg, true, 4*time.Second, 30, 30, true, now)
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
	// When the value changes >= 1% after tap, potsEndedTap reports recovery.
	sess := &recordSession{}
	reader := &constantReader{hp: 70, sp: 80} // reader returns value after tap
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	h := &healer{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	recovered, ok := h.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if !ok || !recovered {
		t.Error("potsEndedTap: expected successful recovery when value rises (30→70)")
	}
	if taps := sess.tapCount.Load(); taps != 1 {
		t.Errorf("potsEndedTap: expected 1 TapKey call, got %d", taps)
	}
}

func TestPotsEndedTap_NoRecovery(t *testing.T) {
	// When the value stays the same after tap, potsEndedTap reports no recovery.
	sess := &recordSession{}
	reader := &constantReader{hp: 30, sp: 80} // same value
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	h := &healer{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	recovered, ok := h.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if !ok || recovered {
		t.Error("potsEndedTap: expected a successful non-recovery when value is unchanged (30→30)")
	}
}

func TestPotsEndedTap_TapKeyError(t *testing.T) {
	// When TapKey returns error, potsEndedTap reports a terminal failure.
	sess := &errorSession{}
	reader := &constantReader{hp: 70, sp: 80}
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}
	h := &healer{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	recovered, ok := h.potsEndedTap(ctx, cfg, 'Q', reader, true, 30)
	if ok || recovered {
		t.Error("potsEndedTap: expected a terminal failure when TapKey fails")
	}
}

// errorSession returns error on TapKey.
type errorSession struct{}

func (s *errorSession) TapKey(_ int32, _ time.Duration) error { return fmt.Errorf("session error") }
func (s *errorSession) MouseClick(_ time.Duration) error      { return nil }

// initialSnapshotReader returns a recovered value on its first actual read.
// The initial low result supplied to healUntilWithInitial must still trigger
// one immediate tap before that read occurs.
type initialSnapshotReader struct {
	calls int
}

func (r *initialSnapshotReader) ReadValues(_ context.Context) BarReadResult {
	r.calls++
	return BarReadResult{Status: StatusFound, HP: 80, SP: 80}
}

func (r *initialSnapshotReader) Name() string { return "initialSnapshot" }

func TestHealUntilWithInitial_UsesSnapshotBeforeReadingAgain(t *testing.T) {
	sess := &recordSession{}
	cfg := AutoPotConfig{Core: CoreConfig{
		Session:     sess,
		HPEnabled:   true,
		HPKeyVK:     'Q',
		HPThreshold: 50,
		Log:         func(string) {},
	}}
	ap := NewAutoPot(cfg)
	reader := &initialSnapshotReader{}
	initial := BarReadResult{Status: StatusFound, HP: 30, SP: 80, HPLow: true}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ap.healer.healUntilWithInitial(ctx, reader, true, &initial)

	if taps := sess.tapCount.Load(); taps != 1 {
		t.Fatalf("expected one immediate potion tap, got %d", taps)
	}
	if reader.calls != 1 {
		t.Fatalf("expected one post-tap read, got %d (initial snapshot was reread)", reader.calls)
	}
}

// ---------------------------------------------------------------------------
// readerController full path tests
// ---------------------------------------------------------------------------

func TestReaderController_OCRProceed(t *testing.T) {
	pixel := &pixelBarReader{}
	ocr := &statusUIReader{}
	controller := newReaderController(ocr, pixel, ocr, false)

	cfg := AutoPotConfig{Core: CoreConfig{}}
	result := BarReadResult{Status: StatusFound, HP: 80}

	if !controller.process(context.Background(), cfg, result) {
		t.Error("readerController: expected valid OCR result to proceed")
	}
}

func TestReaderController_PixelBarsFound(t *testing.T) {
	pixel := &pixelBarReader{}
	controller := newReaderController(pixel, pixel, nil, false)

	cfg := AutoPotConfig{Core: CoreConfig{}}
	result := BarReadResult{Status: StatusFound, HP: 80}

	if !controller.process(context.Background(), cfg, result) {
		t.Error("readerController: expected valid pixel result to proceed")
	}
}

func TestReaderController_PixelBarsNotFound(t *testing.T) {
	pixel := &pixelBarReader{log: func(string) {}}
	controller := newReaderController(pixel, pixel, nil, false)

	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	result := BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("no bars found")}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if controller.process(ctx, cfg, result) {
		t.Error("readerController: expected invalid pixel result to be consumed")
	}
}

// ---------------------------------------------------------------------------
// Concurrent safety: ReaderFactory.Build under -race
// ---------------------------------------------------------------------------

func TestReaderFactoryConcurrentRace(t *testing.T) {
	cfg := AutoPotConfig{
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
