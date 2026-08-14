package autopot

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
// Mock readers and sessions shared by the healer/controller unit tests.
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
