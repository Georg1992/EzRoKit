package autopot

import (
	"context"
	"fmt"
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

type recordSink struct {
	modes  []string
	values []OverlayValues
	clears int
}

func (s *recordSink) SetMode(mode string)       { s.modes = append(s.modes, mode) }
func (s *recordSink) SetValues(v OverlayValues) { s.values = append(s.values, v) }
func (s *recordSink) ClearValues()              { s.clears++ }

func TestSetMode_NilCallback(t *testing.T) {
	setMode(nil, "OCR")
	setMode(nil, "")
}

func TestSetMode_CallsCallback(t *testing.T) {
	sink := &recordSink{}
	setMode(sink, "OCR")
	if len(sink.modes) != 1 || sink.modes[0] != "OCR" {
		t.Errorf("setMode(sink, OCR) modes = %v; want [OCR]", sink.modes)
	}
	setMode(sink, "")
	if len(sink.modes) != 2 || sink.modes[1] != "" {
		t.Errorf("setMode(sink, '') modes = %v; want [OCR, \"\"]", sink.modes)
	}
}

func TestClearPotsEndedMode(t *testing.T) {
	t.Run("clears when potsEnded=true", func(t *testing.T) {
		sink := &recordSink{}
		clearPotsEndedMode(sink, true)
		if len(sink.modes) != 1 || sink.modes[0] != "" {
			t.Errorf("clearPotsEndedMode(sink, true) modes = %v; want [\"\"]", sink.modes)
		}
	})

	t.Run("skips when potsEnded=false", func(t *testing.T) {
		sink := &recordSink{}
		clearPotsEndedMode(sink, false)
		if len(sink.modes) != 0 {
			t.Errorf("clearPotsEndedMode(sink, false) modes = %v; want none", sink.modes)
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

func (s *recordSession) Reset() {}

type guardedTapSession struct {
	mu      sync.Mutex
	events  []string
	started chan struct{}
	once    sync.Once
}

func (s *guardedTapSession) TapKey(vk int32, hold time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	label := fmt.Sprintf("key-%c", vk)
	s.events = append(s.events, label+"-start")
	s.once.Do(func() { close(s.started) })
	time.Sleep(hold)
	s.events = append(s.events, label+"-end")
	return nil
}

func (s *guardedTapSession) Reset() {}

func (s *guardedTapSession) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

func TestHealer_TapCannotBeInterruptedByAnotherKey(t *testing.T) {
	sess := &guardedTapSession{started: make(chan struct{})}
	h := &healer{}
	cfg := AutoPotConfig{Core: CoreConfig{Session: sess, Log: func(string) {}}}

	done := make(chan struct{})
	go func() {
		h.healTap(context.Background(), cfg, 'Q')
		close(done)
	}()
	<-sess.started
	if err := sess.TapKey('X', time.Millisecond); err != nil {
		t.Fatalf("competing key: %v", err)
	}
	<-done

	events := sess.snapshot()
	want := []string{"key-Q-start", "key-Q-end", "key-X-start", "key-X-end"}
	if len(events) != len(want) {
		t.Fatalf("another key interrupted AutoPot tap: got %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %q, want %q: %v", i, events[i], want[i], events)
		}
	}
}
