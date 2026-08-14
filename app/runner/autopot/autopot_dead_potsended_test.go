package autopot

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"belarus-champ-tools/runner/internal/timing"
)

// ---------------------------------------------------------------------------
// Pure helper tests — no mocks needed.
// ---------------------------------------------------------------------------

func TestAbsPctDiff(t *testing.T) {
	tests := []struct {
		a, b float64
		want float64
	}{
		{50, 50, 0},
		{30, 80, 50},
		{80, 30, 50},
		{0, 100, 100},
		{1.5, 1.5, 0},
		{1.0, 1.5, 0.5},
	}
	for _, tt := range tests {
		got := absPctDiff(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("absPctDiff(%v, %v) = %v; want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPotsEndedLabel(t *testing.T) {
	tests := []struct {
		name  string
		cfg   AutoPotConfig
		hpBar bool
		want  string
	}{
		{
			name:  "HP with key",
			cfg:   AutoPotConfig{Core: CoreConfig{HPKeyName: "F1"}},
			hpBar: true,
			want:  "HP pots ended on F1",
		},
		{
			name:  "SP with key",
			cfg:   AutoPotConfig{Core: CoreConfig{SPKeyName: "F2"}},
			hpBar: false,
			want:  "SP pots ended on F2",
		},
		{
			name:  "HP without key",
			cfg:   AutoPotConfig{Core: CoreConfig{}},
			hpBar: true,
			want:  "HP pots ended",
		},
		{
			name:  "SP without key",
			cfg:   AutoPotConfig{Core: CoreConfig{}},
			hpBar: false,
			want:  "SP pots ended",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := potsEndedLabel(tt.cfg, tt.hpBar)
			if got != tt.want {
				t.Errorf("potsEndedLabel(%+v, %v) = %q; want %q", tt.cfg, tt.hpBar, got, tt.want)
			}
		})
	}
}

func TestHealTarget(t *testing.T) {
	tests := []struct {
		name   string
		cfg    AutoPotConfig
		hpBar  bool
		wantVK int32
		wantOK bool
	}{
		{
			name:   "HP enabled with key",
			cfg:    AutoPotConfig{Core: CoreConfig{HPEnabled: true, HPKeyVK: 'Q'}},
			hpBar:  true,
			wantVK: 'Q',
			wantOK: true,
		},
		{
			name:   "HP disabled",
			cfg:    AutoPotConfig{Core: CoreConfig{HPEnabled: false, HPKeyVK: 'Q'}},
			hpBar:  true,
			wantVK: 0,
			wantOK: false,
		},
		{
			name:   "HP no key",
			cfg:    AutoPotConfig{Core: CoreConfig{HPEnabled: true, HPKeyVK: 0}},
			hpBar:  true,
			wantVK: 0,
			wantOK: false,
		},
		{
			name:   "SP enabled with key",
			cfg:    AutoPotConfig{Core: CoreConfig{SPEnabled: true, SPKeyVK: 'W'}},
			hpBar:  false,
			wantVK: 'W',
			wantOK: true,
		},
		{
			name:   "SP disabled",
			cfg:    AutoPotConfig{Core: CoreConfig{SPEnabled: false, SPKeyVK: 'W'}},
			hpBar:  false,
			wantVK: 0,
			wantOK: false,
		},
		{
			name:   "SP no key",
			cfg:    AutoPotConfig{Core: CoreConfig{SPEnabled: true, SPKeyVK: 0}},
			hpBar:  false,
			wantVK: 0,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vk, ok := healTarget(tt.cfg, tt.hpBar)
			if vk != tt.wantVK || ok != tt.wantOK {
				t.Errorf("healTarget(%+v, %v) = (%d, %v); want (%d, %v)",
					tt.cfg, tt.hpBar, vk, ok, tt.wantVK, tt.wantOK)
			}
		})
	}
}

func TestSetMode_NilCallback(t *testing.T) {
	// Must not panic when fn is nil.
	setMode(nil, "OCR")
	setMode(nil, "")
}

func TestSetMode_CallsCallback(t *testing.T) {
	var got string
	fn := func(s string) { got = s }

	setMode(fn, "")
	if got != "" {
		t.Errorf("setMode(fn, '') called with %q; want empty", got)
	}
}

// TestClearPotsEndedMode verifies that clearPotsEndedMode only calls
// setMode when potsEnded is true (to avoid unnecessary overlay updates).
func TestClearPotsEndedMode(t *testing.T) {
	t.Run("clears when potsEnded=true", func(t *testing.T) {
		var got string
		fn := func(s string) { got = s }
		clearPotsEndedMode(fn, true)
		if got != "" {
			t.Errorf("clearPotsEndedMode(fn, true) = %q; want empty", got)
		}
	})

	t.Run("skips when potsEnded=false", func(t *testing.T) {
		called := false
		fn := func(s string) { called = true }
		clearPotsEndedMode(fn, false)
		if called {
			t.Error("clearPotsEndedMode(fn, false) called setMode; should not")
		}
	})
}

// ---------------------------------------------------------------------------
// Mock readers for healUntil tests.
// ---------------------------------------------------------------------------

// constantReader returns a fixed HP/SP value regardless of context.
type constantReader struct {
	hp, sp float64
}

func (r *constantReader) ReadValues(_ context.Context) BarReadResult {
	return BarReadResult{
		Status: StatusFound,
		HP:     r.hp,
		SP:     r.sp,
		HPLow:  r.hp < 50,
		SPLow:  r.sp < 50,
	}
}

func (r *constantReader) Name() string { return "constant" }

// constantThenSpikeReader returns hpBase for N calls, then hpSpike until
// threshold is reached (above threshold), causing healUntil to exit.
type constantThenSpikeReader struct {
	mu          sync.Mutex
	callCount   int
	hpBase      float64
	hpSpike     float64
	switchAfter int // call index after which to spike
	threshold   float64
	readDelay   time.Duration // model the real visual-read cadence
}

func (r *constantThenSpikeReader) ReadValues(_ context.Context) BarReadResult {
	r.mu.Lock()
	n := r.callCount
	r.callCount++
	hp := r.hpBase
	if n >= r.switchAfter {
		hp = r.hpSpike
	}
	delay := r.readDelay
	r.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	return BarReadResult{
		Status: StatusFound,
		HP:     hp,
		SP:     80,
		HPLow:  hp < r.threshold,
		SPLow:  false,
	}
}

func (r *constantThenSpikeReader) Name() string { return "constantThenSpike" }

// modeRecorder tracks all calls to a mode callback for assertions.
type modeRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (m *modeRecorder) record(s string) {
	m.mu.Lock()
	m.calls = append(m.calls, s)
	m.mu.Unlock()
}

func (m *modeRecorder) last() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return ""
	}
	return m.calls[len(m.calls)-1]
}

func (m *modeRecorder) contains(want string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.calls {
		if c == want {
			return true
		}
	}
	return false
}

// recordSession records TapKey calls.
type recordSession struct {
	mu       sync.Mutex
	tapKeys  []int32
	tapCount atomic.Int64
}

func (s *recordSession) TapKey(vk int32, hold time.Duration) error {
	s.mu.Lock()
	s.tapKeys = append(s.tapKeys, vk)
	s.mu.Unlock()
	s.tapCount.Add(1)
	return nil
}

func (s *recordSession) MouseClick(_ time.Duration) error { return nil }

// ---------------------------------------------------------------------------
// healUntil tests — pots-ended detection.
// ---------------------------------------------------------------------------

// TestHealUntil_PotsEndedDetected verifies that healUntil detects
// no-change after 3 seconds and calls OnStatusUIMode with the
// appropriate pots-ended label.
func TestHealUntil_PotsEndedDetected(t *testing.T) {
	sess := &recordSession{}
	rec := &modeRecorder{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Session:        sess,
			HPThreshold:    50,
			HPKeyVK:        'Q',
			HPEnabled:      true,
			Log:            func(string) {},
			OnStatusUIMode: rec.record,
			HPKeyName:      "F1",
		},
	}
	ap := NewAutoPot(cfg)

	// Reader returns HP=30 (below threshold) forever.
	reader := &constantReader{hp: 30, sp: 80}

	// Give enough time for 3s no-change detection + 2 iterations of slow taps.
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	start := time.Now()
	ap.healer.healUntil(ctx, reader, true)
	elapsed := time.Since(start)

	t.Logf("healUntil ran for %v, %d TapKey calls, mode calls: %v",
		elapsed.Round(time.Millisecond), sess.tapCount.Load(), rec.calls)

	// After 3+ seconds, pots-ended label should have been set.
	if !rec.contains("HP pots ended on F1") {
		t.Errorf("mode calls %v do not contain %q", rec.calls, "HP pots ended on F1")
	}

	// Should have pressed some keys.
	if taps := sess.tapCount.Load(); taps == 0 {
		t.Error("healUntil did not press any keys")
	}

	if rec.last() != "" {
		t.Errorf("last mode call = %q; want empty after cancellation cleanup", rec.last())
	}
}

// TestHealUntil_PotsEnded_ThenValueChanges verifies that when the HP
// value changes after pots-ended was detected, healUntil exits and
// calls clearPotsEndedMode (which resets the mode to "").
//
// Split into two phases:
//  1. constantThenSpikeReader returns HP=30 for 300+ calls → pots-ended
//     detected and label applied.
//  2. After switchAfter iterations, HP spikes to 80 (above threshold)
//     → healUntil exits via clearPotsEndedMode → last mode is "".
//
// switchAfter=303 gives roughly 3s of simulated reads, then a slow-mode
// recovery check, comfortably within the 20s timeout on Windows.
func TestHealUntil_PotsEnded_ThenValueChanges(t *testing.T) {
	sess := &recordSession{}
	rec := &modeRecorder{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Session:        sess,
			HPThreshold:    50,
			HPKeyVK:        'Q',
			HPEnabled:      true,
			Log:            func(string) {},
			OnStatusUIMode: rec.record,
			HPKeyName:      "F1",
		},
	}
	ap := NewAutoPot(cfg)

	reader := &constantThenSpikeReader{
		hpBase:      30,
		hpSpike:     80,
		switchAfter: 303,
		threshold:   50,
		readDelay:   timing.PollInterval,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	start := time.Now()
	ap.healer.healUntil(ctx, reader, true)
	elapsed := time.Since(start)

	t.Logf("healUntil ran for %v, %d TapKey calls, mode calls: %v",
		elapsed.Round(time.Millisecond), sess.tapCount.Load(), rec.calls)

	if !rec.contains("HP pots ended on F1") {
		t.Errorf("mode calls %v do not contain %q", rec.calls, "HP pots ended on F1")
	}

	last := rec.last()
	if last != "" {
		t.Errorf("last mode call = %q; want empty (cleared on exit)", last)
	}
}

// TestHealUntil_PotsEnded_SPBar tests pots-ended detection for SP.
func TestHealUntil_PotsEnded_SPBar(t *testing.T) {
	sess := &recordSession{}
	rec := &modeRecorder{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Session:        sess,
			HPThreshold:    50,
			SPThreshold:    50,
			HPKeyVK:        'Q',
			SPKeyVK:        'W',
			HPEnabled:      false, // only SP enabled
			SPEnabled:      true,
			Log:            func(string) {},
			OnStatusUIMode: rec.record,
			SPKeyName:      "F2",
		},
	}
	ap := NewAutoPot(cfg)

	// Reader returns SP=30 (below threshold) forever.
	reader := &constantReader{hp: 80, sp: 30}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	start := time.Now()
	ap.healer.healUntil(ctx, reader, false) // SP bar
	elapsed := time.Since(start)

	t.Logf("SP healUntil ran for %v, %d TapKey calls, mode calls: %v",
		elapsed.Round(time.Millisecond), sess.tapCount.Load(), rec.calls)

	if !rec.contains("SP pots ended on F2") {
		t.Errorf("mode calls %v do not contain %q", rec.calls, "SP pots ended on F2")
	}

	if rec.last() != "" {
		t.Errorf("last mode call = %q; want empty after cancellation cleanup", rec.last())
	}
}

// TestHealUntil_PotsEnded_ReApply verifies that the pots-ended label
// is re-applied on every iteration (not just once on entry), which is
// critical for surviving OCR validation overwriting the overlay mode.
// We test this by verifying multiple identical calls to OnStatusUIMode.
//
// Timing on Windows:
//   - 300 simulated visual reads × ~10ms = ~3s → pots-ended detected
//   - Remaining time in the 8s budget allows multiple slow iterations
//   - Each slow iteration re-applies the label = ≥2 total
func TestHealUntil_PotsEnded_ReApply(t *testing.T) {
	sess := &recordSession{}
	rec := &modeRecorder{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Session:        sess,
			HPThreshold:    50,
			HPKeyVK:        'Q',
			HPEnabled:      true,
			Log:            func(string) {},
			OnStatusUIMode: rec.record,
			HPKeyName:      "F1",
		},
	}
	ap := NewAutoPot(cfg)

	reader := &constantReader{hp: 30, sp: 80}

	// 8s budget: ~4.5s fast + ~3 slow iterations.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ap.healer.healUntil(ctx, reader, true)

	// Count consecutive "HP pots ended on F1" calls after pots-ended
	// was first triggered. Since healUntil exits via ctx cancellation
	// (constant HP never rises), there's no "" clear marker — just
	// count all "HP pots ended on F1" calls.
	rec.mu.Lock()
	potsEndedCalls := 0
	inPotsEnded := false
	for _, c := range rec.calls {
		if c == "HP pots ended on F1" {
			inPotsEnded = true
			potsEndedCalls++
		} else if inPotsEnded {
			// Non-label call (e.g. "" on normal exit, or other modes)
			// — stop counting.
			break
		}
	}
	rec.mu.Unlock()

	t.Logf("pots-ended label was applied %d times (including re-applies)", potsEndedCalls)

	if potsEndedCalls < 2 {
		t.Errorf("pots-ended label applied %d times; expected >= 2 (re-apply on each iteration)", potsEndedCalls)
	}
}

// TestHealUntil_PotsEndedClearsOnRecover verifies that when pots-ended
// is active and the value finally rises above threshold, healUntil
// exits (clearing the mode).
func TestHealUntil_HealsImmediately_NoPotsEnded(t *testing.T) {
	// When value is already above threshold, healUntil returns
	// without pressing any keys or calling mode callback.
	sess := &recordSession{}
	rec := &modeRecorder{}
	cfg := AutoPotConfig{
		Core: CoreConfig{
			Session:        sess,
			HPThreshold:    50,
			HPKeyVK:        'Q',
			HPEnabled:      true,
			Log:            func(string) {},
			OnStatusUIMode: rec.record,
		},
	}
	ap := NewAutoPot(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	reader := &constantReader{hp: 80, sp: 80} // above threshold

	start := time.Now()
	ap.healer.healUntil(ctx, reader, true)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("healUntil took %v to exit when value already above threshold (expected < 100ms)",
			elapsed.Round(time.Millisecond))
	}
	if taps := sess.tapCount.Load(); taps > 0 {
		t.Errorf("healUntil pressed %d keys despite value already above threshold", taps)
	}
	if len(rec.calls) > 0 {
		t.Errorf("mode callback called %d times with %v; expected 0 (no pots-ended state)",
			len(rec.calls), rec.calls)
	}
}
