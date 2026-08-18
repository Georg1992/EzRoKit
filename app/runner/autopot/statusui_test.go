package autopot

import (
	"context"
	"image"
	"image/draw"
	"testing"
	"time"

	"ezrokit/runner/autopot/statusui"
)

// TestStatusUIReaderCancel verifies that ReadValues returns an error
// (context.Canceled) when called with an already-cancelled context.
func TestStatusUIReaderUninitialized(t *testing.T) {
	var nilReader *statusUIReader
	if result := nilReader.ReadValues(context.Background()); result.Err == nil {
		t.Fatal("nil statusUIReader returned no error")
	}
	if result := (&statusUIReader{}).ReadValues(context.Background()); result.Err == nil {
		t.Fatal("statusUIReader without poller returned no error")
	}
}

func TestStatusUIReaderCancel(t *testing.T) {
	pipeline, err := statusui.NewDefaultPipeline()
	if err != nil {
		t.Skipf("skipping: cannot create pipeline in test env: %v", err)
	}

	reader := &statusUIReader{
		poller: statusui.NewStripPoller(pipeline),
		log:    func(string) {},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := reader.ReadValues(ctx)
	if result.Err == nil {
		t.Fatal("ReadValues with cancelled ctx: want error, got nil")
	}
}

// fixtureCapturer serves crops of a screenshot fixture as if it were the screen,
// counting how many full-screen captures the reader asks for.
type fixtureCapturer struct {
	screen     *image.RGBA
	fullCalls  int
	lastRegion Rect
}

func newFixtureCapturer(t *testing.T, name string) *fixtureCapturer {
	t.Helper()
	src := loadFixture(t, name)
	rgba := image.NewRGBA(src.Bounds())
	draw.Draw(rgba, rgba.Bounds(), src, src.Bounds().Min, draw.Src)
	return &fixtureCapturer{screen: rgba}
}

func (c *fixtureCapturer) ScreenSize() (int, int) {
	return c.screen.Bounds().Dx(), c.screen.Bounds().Dy()
}

func (c *fixtureCapturer) CaptureFullScreen() (*image.RGBA, error) {
	c.fullCalls++
	return c.screen, nil
}

func (c *fixtureCapturer) CaptureScreenRegion(roi Rect) (*image.RGBA, error) {
	c.lastRegion = roi
	out := image.NewRGBA(image.Rect(0, 0, roi.W, roi.H))
	draw.Draw(out, out.Bounds(), c.screen, image.Pt(roi.X, roi.Y), draw.Src)
	return out, nil
}

func newFixtureStatusUIReader(t *testing.T, name string) (*statusUIReader, *fixtureCapturer) {
	t.Helper()
	pipeline, err := statusui.NewDefaultPipeline()
	if err != nil {
		t.Skipf("skipping: cannot create pipeline in test env: %v", err)
	}
	capture := newFixtureCapturer(t, name)
	return &statusUIReader{
		capture:      capture,
		poller:       statusui.NewStripPoller(pipeline),
		log:          func(string) {},
		coreSettings: func() CoreConfig { return CoreConfig{HPThreshold: 50, SPThreshold: 50} },
	}, capture
}

// TestStatusUIReaderRevalidatesFromCachedPanel is the guard on the cost of
// revalidation. Scanning the screen for the panel costs ~95ms inside the healing
// loop; confirming the cached rect costs ~0.4ms. Once the panel has been located,
// no later revalidation may reach for a full screenshot.
func TestStatusUIReaderRevalidatesFromCachedPanel(t *testing.T) {
	reader, capture := newFixtureStatusUIReader(t, "aa.png")

	if result := reader.ReadValues(context.Background()); result.Status != StatusFound {
		t.Fatalf("first read: status %v err %v", result.Status, result.Err)
	}
	if capture.fullCalls != 1 {
		t.Fatalf("acquisition took %d full-screen captures, want 1", capture.fullCalls)
	}

	// Force the next read to revalidate.
	reader.poller.ValidateEvery = time.Nanosecond
	if result := reader.ReadValues(context.Background()); result.Status != StatusFound {
		t.Fatalf("read after revalidation: status %v err %v", result.Status, result.Err)
	}
	if capture.fullCalls != 1 {
		t.Fatalf("revalidation scanned the screen again (%d full captures)", capture.fullCalls)
	}

	panel := reader.poller.PanelRect()
	if capture.lastRegion.W != panel.Dx() && capture.lastRegion.W != 200 {
		t.Fatalf("revalidation captured %+v, expected the panel or strip rect", capture.lastRegion)
	}
}

// TestStatusUIReaderReadsFixtureValues pins the percentages the healing layer
// acts on to the values printed in the game's status panel.
func TestStatusUIReaderReadsFixtureValues(t *testing.T) {
	reader, _ := newFixtureStatusUIReader(t, "aa.png")

	result := reader.ReadValues(context.Background())
	if result.Status != StatusFound {
		t.Fatalf("status %v err %v", result.Status, result.Err)
	}
	// aa.png shows HP 751/1290 and SP 102/201.
	if wantHP := 751.0 * 100 / 1290.0; result.HP < wantHP-0.01 || result.HP > wantHP+0.01 {
		t.Errorf("HP %.2f%%, want %.2f%%", result.HP, wantHP)
	}
	if wantSP := 102.0 * 100 / 201.0; result.SP < wantSP-0.01 || result.SP > wantSP+0.01 {
		t.Errorf("SP %.2f%%, want %.2f%%", result.SP, wantSP)
	}
	if result.HPLow || result.SPLow {
		t.Errorf("HP 58%% and SP 50.7%% are above a 50 threshold: HPLow=%t SPLow=%t", result.HPLow, result.SPLow)
	}

	thirsty, _ := newFixtureStatusUIReader(t, "aa.png")
	thirsty.coreSettings = func() CoreConfig { return CoreConfig{HPThreshold: 60, SPThreshold: 60} }
	low := thirsty.ReadValues(context.Background())
	if !low.HPLow || !low.SPLow {
		t.Errorf("the same read is below a 60 threshold: HPLow=%t SPLow=%t", low.HPLow, low.SPLow)
	}
}

func TestAcceptMaximaHoldsSteadyAgainstAMisreadDigit(t *testing.T) {
	r := &statusUIReader{}

	if !r.acceptMaxima(statusui.ParsedStatus{HP: 751, HPMax: 1290, SP: 102, SPMax: 201}) {
		t.Fatal("first read should be adopted")
	}
	if !r.acceptMaxima(statusui.ParsedStatus{HP: 700, HPMax: 1290, SP: 100, SPMax: 201}) {
		t.Fatal("read with the same maxima should be accepted")
	}
	// A dropped digit in hpMax would leave HP looking ten times healthier.
	if r.acceptMaxima(statusui.ParsedStatus{HP: 129, HPMax: 129, SP: 102, SPMax: 201}) {
		t.Fatal("read with changed maxima should be refused")
	}
	if !r.acceptMaxima(statusui.ParsedStatus{HP: 700, HPMax: 1290, SP: 100, SPMax: 201}) {
		t.Fatal("refusing one frame must not disturb the established maxima")
	}
}

func TestAcceptMaximaAdoptsAConfirmedChange(t *testing.T) {
	r := &statusUIReader{}
	r.acceptMaxima(statusui.ParsedStatus{HP: 751, HPMax: 1290, SP: 102, SPMax: 201})

	// A level-up: the same new maxima repeat until they are believed.
	levelled := statusui.ParsedStatus{HP: 900, HPMax: 1400, SP: 110, SPMax: 220}
	for i := 1; i < maxChangeConfirm; i++ {
		if r.acceptMaxima(levelled) {
			t.Fatalf("adopted new maxima after only %d reads", i)
		}
	}
	if !r.acceptMaxima(levelled) {
		t.Fatalf("new maxima should be adopted after %d consecutive reads", maxChangeConfirm)
	}
	if r.hpMax != 1400 || r.spMax != 220 {
		t.Fatalf("adopted maxima HP/%d SP/%d, want HP/1400 SP/220", r.hpMax, r.spMax)
	}
}

func TestAcceptMaximaNeverAdoptsUnrepeatedGarbage(t *testing.T) {
	r := &statusUIReader{}
	r.acceptMaxima(statusui.ParsedStatus{HP: 751, HPMax: 1290, SP: 102, SPMax: 201})

	for i := 0; i < 10; i++ {
		garbage := statusui.ParsedStatus{HP: 1, HPMax: 100 + i, SP: 1, SPMax: 20 + i}
		if r.acceptMaxima(garbage) {
			t.Fatalf("accepted maxima that never repeated: %+v", garbage)
		}
	}
	if r.hpMax != 1290 || r.spMax != 201 {
		t.Fatalf("maxima drifted to HP/%d SP/%d", r.hpMax, r.spMax)
	}
}

// TestConfirmMaximaDiscardsACorruptFrame checks the reader answers with what the
// screen actually says, not with the frame that disagreed with it.
func TestConfirmMaximaDiscardsACorruptFrame(t *testing.T) {
	reader, _ := newFixtureStatusUIReader(t, "aa.png")
	if result := reader.ReadValues(context.Background()); result.Status != StatusFound {
		t.Fatalf("first read: status %v err %v", result.Status, result.Err)
	}

	// A digit lost from hpMax: 1290 arriving as 129.
	corrupt := statusui.ParsedStatus{HP: 129, HPMax: 129, SP: 102, SPMax: 201}
	status, ok := reader.confirmMaxima(corrupt)
	if !ok {
		t.Fatal("re-reading the strip should have recovered a usable read")
	}
	if status.HPMax != 1290 || status.HP != 751 {
		t.Fatalf("recovered HP=%d/%d, want 751/1290", status.HP, status.HPMax)
	}
}

func TestAcceptMaximaRejectsNonPositiveMaxima(t *testing.T) {
	r := &statusUIReader{}
	if r.acceptMaxima(statusui.ParsedStatus{HP: 0, HPMax: 0, SP: 0, SPMax: 0}) {
		t.Fatal("zero maxima would make every percentage 0 and drink pots forever")
	}
}
