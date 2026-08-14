package statusui

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func statusUIDirForPoller(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}

func TestStripPoller_UninitializedReturnsErrors(t *testing.T) {
	var poller *StripPoller
	if err := poller.Validate(nil); err == nil {
		t.Fatal("Validate on nil poller returned nil error")
	}
	if _, err := poller.Parse(nil); err == nil {
		t.Fatal("Parse on nil poller returned nil error")
	}
}

func TestStripPoller_AcquireThenPoll(t *testing.T) {
	pipeline, err := NewPipeline(
		filepath.Join(statusUIDirForPoller(t), "glyphs"), 0.70,
	)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	poller := NewStripPoller(pipeline)

	// Before any Validate the rect is zero and NeedsValidation is true.
	if !poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=true before first Validate")
	}
	if !poller.StripRect().Empty() {
		t.Fatal("expected empty StripRect before first Validate")
	}

	src := loadPNGImage(t, filepath.Join(
		statusUIDirForPoller(t), "..", "testdata", "aa.png",
	))

	// Validate acquires the strip rect.
	if err := poller.Validate(src); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if poller.StripRect().Empty() {
		t.Fatal("StripRect still empty after Validate")
	}
	if poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=false immediately after Validate")
	}

	// Poll: crop the strip from the source image and parse.
	strip := ExtractROI(src, poller.StripRect())
	status, err := poller.Parse(strip)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if status.HP != 751 || status.HPMax != 1290 || status.SP != 102 || status.SPMax != 201 {
		t.Fatalf("parsed HP=%d/%d SP=%d/%d, want HP=751/1290 SP=102/201",
			status.HP, status.HPMax, status.SP, status.SPMax)
	}
}

func TestStripPoller_NeedsValidationAfterInterval(t *testing.T) {
	pipeline, err := NewPipeline(
		filepath.Join(statusUIDirForPoller(t), "glyphs"), 0.70,
	)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	poller := NewStripPoller(pipeline)
	poller.ValidateEvery = 50 * time.Millisecond

	src := loadPNGImage(t, filepath.Join(
		statusUIDirForPoller(t), "..", "testdata", "aa.png",
	))
	if err := poller.Validate(src); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=false immediately after Validate")
	}
	time.Sleep(60 * time.Millisecond)
	if !poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=true after interval elapsed")
	}
}

func TestStripPoller_Invalidate(t *testing.T) {
	pipeline, err := NewPipeline(
		filepath.Join(statusUIDirForPoller(t), "glyphs"), 0.70,
	)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	poller := NewStripPoller(pipeline)
	src := loadPNGImage(t, filepath.Join(
		statusUIDirForPoller(t), "..", "testdata", "aa.png",
	))
	if err := poller.Validate(src); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=false after Validate")
	}

	poller.Invalidate()
	if !poller.NeedsValidation() {
		t.Fatal("expected NeedsValidation=true after Invalidate")
	}
	if !poller.StripRect().Empty() {
		t.Fatal("expected empty StripRect after Invalidate")
	}
}
