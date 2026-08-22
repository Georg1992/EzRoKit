//go:build windows

package main

import (
	"testing"
)

func TestOverlayShowStopped(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.ShowStopped()

	ovl.mu.Lock()
	running := ovl.running
	mode := ovl.mode
	hp := ovl.valuesHP
	sp := ovl.valuesSP
	ovl.mu.Unlock()

	if running {
		t.Errorf("ShowStopped() running = true; want false")
	}
	if mode != "Stopped" {
		t.Errorf("ShowStopped() mode = %q; want %q", mode, "Stopped")
	}
	if hp != 0 || sp != 0 {
		t.Errorf("ShowStopped() values not cleared: HP=%d SP=%d", hp, sp)
	}
}

func TestOverlayShowStopped_RunningText(t *testing.T) {
	got := runningText(true)
	want := "● Tools ON"
	if got != want {
		t.Errorf("runningText(true) = %q; want %q", got, want)
	}

	got = runningText(false)
	want = "● Tools OFF"
	if got != want {
		t.Errorf("runningText(false) = %q; want %q", got, want)
	}
}

func TestOverlaySetMode_AddressReading(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("Address reading")

	ovl.mu.Lock()
	running := ovl.running
	mode := ovl.mode
	ovl.mu.Unlock()

	if !running {
		t.Errorf("SetMode('Address reading') running = false; want true")
	}
	if mode != "Address reading" {
		t.Errorf("SetMode('Address reading') mode = %q; want %q", mode, "Address reading")
	}
}

func TestOverlaySetMode_Pixelsearch(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("Pixelsearch")

	ovl.mu.Lock()
	got := ovl.mode
	ovl.mu.Unlock()

	want := "Pixelsearch"
	if got != want {
		t.Errorf("SetMode('Pixelsearch') mode = %q; want %q", got, want)
	}
}

func TestOverlaySetMode_Replaces(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("Address reading")
	ovl.SetMode("Pixelsearch")

	ovl.mu.Lock()
	got := ovl.mode
	ovl.mu.Unlock()

	want := "Pixelsearch"
	if got != want {
		t.Errorf("SetMode Address→Pixelsearch mode = %q; want %q", got, want)
	}
}

func TestOverlaySetMode_KeepsRunning(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("OCR")
	ovl.SetMode("Pixelsearch")

	ovl.mu.Lock()
	running := ovl.running
	ovl.mu.Unlock()

	if !running {
		t.Errorf("running after two SetMode calls = false; want true")
	}
}

func TestOverlayShowStopped_ClearsRunning(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("Address reading")
	ovl.SetValues(5000, 10000, 3000, 6000)
	ovl.ShowStopped()

	ovl.mu.Lock()
	running := ovl.running
	mode := ovl.mode
	hp := ovl.valuesHP
	sp := ovl.valuesSP
	ovl.mu.Unlock()

	if running {
		t.Errorf("ShowStopped after SetMode: running = true; want false")
	}
	if mode != "Stopped" {
		t.Errorf("ShowStopped after SetMode: mode = %q; want %q", mode, "Stopped")
	}
	if hp != 0 {
		t.Errorf("ShowStopped after SetValues: HP = %d; want 0", hp)
	}
	if sp != 0 {
		t.Errorf("ShowStopped after SetValues: SP = %d; want 0", sp)
	}
}

func TestOverlaySetMode_Empty(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("")

	ovl.mu.Lock()
	got := ovl.mode
	running := ovl.running
	ovl.mu.Unlock()

	if got != "" {
		t.Errorf("SetMode('') mode = %q; want empty", got)
	}
	if !running {
		t.Errorf("SetMode('') running = false; want true")
	}
}

func TestOverlaySetValues_OcrStoresRaw(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("OCR")
	ovl.SetValues(5000, 10000, 3000, 6000) // raw values stored as-is

	ovl.mu.Lock()
	hp := ovl.valuesHP
	hpMax := ovl.valuesHPMax
	sp := ovl.valuesSP
	spMax := ovl.valuesSPMax
	ovl.mu.Unlock()

	if hp != 5000 || hpMax != 10000 {
		t.Errorf("OCR SetValues: HP = %d/%d; want 5000/10000", hp, hpMax)
	}
	if sp != 3000 || spMax != 6000 {
		t.Errorf("OCR SetValues: SP = %d/%d; want 3000/6000", sp, spMax)
	}
}

func TestOverlaySetValues_PixelStoresPercent(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetMode("Pixelsearch")
	ovl.SetValues(50, 100, 30, 100) // percentages stored as-is

	ovl.mu.Lock()
	hp := ovl.valuesHP
	hpMax := ovl.valuesHPMax
	sp := ovl.valuesSP
	spMax := ovl.valuesSPMax
	ovl.mu.Unlock()

	if hp != 50 || hpMax != 100 {
		t.Errorf("Pixelsearch SetValues: HP = %d/%d; want 50/100", hp, hpMax)
	}
	if sp != 30 || spMax != 100 {
		t.Errorf("Pixelsearch SetValues: SP = %d/%d; want 30/100", sp, spMax)
	}
}

func TestOverlaySetValues_ZeroMax(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetValues(5000, 0, 3000, 0) // zero max → stored as-is, onPaint hides

	ovl.mu.Lock()
	hp := ovl.valuesHP
	hpMax := ovl.valuesHPMax
	sp := ovl.valuesSP
	spMax := ovl.valuesSPMax
	ovl.mu.Unlock()

	if hp != 5000 {
		t.Errorf("SetValues(5000,0): HP = %d; want 5000", hp)
	}
	if hpMax != 0 {
		t.Errorf("SetValues(5000,0): HP max = %d; want 0", hpMax)
	}
	if sp != 3000 {
		t.Errorf("SetValues(3000,0): SP = %d; want 3000", sp)
	}
	if spMax != 0 {
		t.Errorf("SetValues(3000,0): SP max = %d; want 0", spMax)
	}
}

func TestOverlayClearValues(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	ovl.SetValues(5000, 10000, 3000, 6000)
	ovl.ClearValues()

	ovl.mu.Lock()
	hp := ovl.valuesHP
	sp := ovl.valuesSP
	ovl.mu.Unlock()

	if hp != 0 || sp != 0 {
		t.Errorf("ClearValues: HP=%d SP=%d; want 0 0", hp, sp)
	}
}

func TestOverlayHide(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	// Just ensure it doesn't panic.
	ovl.Hide()
}

func TestOverlaySameModeAndValues(t *testing.T) {
	ovl, err := newStatusOverlay()
	if err != nil {
		t.Fatal(err)
	}
	defer ovl.Destroy()

	if ovl.sameMode("OCR") {
		t.Fatal("empty overlay should not report OCR as current")
	}
	ovl.SetMode("OCR")
	if !ovl.sameMode("OCR") {
		t.Fatal("SetMode(OCR) should match sameMode(OCR)")
	}
	if ovl.sameMode("Pixelsearch") {
		t.Fatal("OCR overlay should not match Pixelsearch")
	}

	if ovl.sameValues(10, 100, 20, 100) {
		t.Fatal("empty values should not match")
	}
	ovl.SetValues(10, 100, 20, 100)
	if !ovl.sameValues(10, 100, 20, 100) {
		t.Fatal("SetValues should match sameValues")
	}
	if ovl.sameValues(11, 100, 20, 100) {
		t.Fatal("changed HP should not match")
	}
	ovl.ClearValues()
	if !ovl.valuesCleared() {
		t.Fatal("ClearValues should report valuesCleared")
	}
}
