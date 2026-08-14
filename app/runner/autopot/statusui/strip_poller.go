package statusui

import (
	"errors"
	"image"
	"sync"
	"time"
)

// StripPoller manages the two-phase recognition loop:
//
//  1. Acquire — runs the full RecognizeScreen once to locate the status
//     panel and derive the strip rectangle. Stores those coordinates.
//
//  2. Poll — on every subsequent tick the caller captures just the tiny
//     200×11 strip region (using StripRect) and passes it to Parse.
//     No panel detection, no full-screen scan.
//
//  3. Revalidate — every ValidateEvery interval (default 5 s) the poller
//     marks itself as needing reacquisition so the caller runs a full
//     RecognizeScreen again. This catches panel drift (UI resize,
//     resolution change, game window moved).
//
// Typical caller loop:
//
//	poller := statusui.NewStripPoller(pipeline)
//
//	for autopotEnabled {
//	    if poller.NeedsValidation() {
//	        screen := platform.CaptureFullScreen()
//	        if err := poller.Validate(screen); err != nil {
//	            time.Sleep(100 * time.Millisecond)
//	            continue // panel not found yet, retry next cycle
//	        }
//	    }
//	    strip := platform.CaptureRegion(poller.StripRect())
//	    status, err := poller.Parse(strip)
//	    // act on status ...
//	    time.Sleep(100 * time.Millisecond)
//	}
type StripPoller struct {
	// ValidateEvery is how often a full RecognizeScreen is re-run to
	// confirm the strip is still where the poller expects. Default 5s.
	ValidateEvery time.Duration

	pipeline     *Pipeline
	mu           sync.Mutex
	panelRect    image.Rectangle
	stripRect    image.Rectangle
	lastValidate time.Time
}

// NewStripPoller returns a poller backed by the given pipeline.
// NeedsValidation returns true immediately so the first tick triggers
// a full acquisition.
func NewStripPoller(pipeline *Pipeline) *StripPoller {
	return &StripPoller{
		pipeline:      pipeline,
		ValidateEvery: 5 * time.Second,
	}
}

// NeedsValidation reports whether the caller should run a full
// RecognizeScreen before the next Parse. True on first call and after
// ValidateEvery has elapsed since the last successful Validate.
func (p *StripPoller) NeedsValidation() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	interval := p.ValidateEvery
	if interval == 0 {
		interval = 5 * time.Second
	}
	return p.lastValidate.IsZero() || time.Since(p.lastValidate) >= interval
}

// StripRect returns the screen-space rectangle of the HP/SP text strip
// as determined by the last successful Validate call. Zero until the
// first successful Validate.
func (p *StripPoller) StripRect() image.Rectangle {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stripRect
}

// Validate runs a full RecognizeScreen on the provided screenshot,
// updates the cached strip rectangle, and resets the revalidation
// timer. Returns an error if the panel is not found or cannot be
// verified — the caller should retry on the next cycle.
func (p *StripPoller) Validate(screen image.Image) error {
	if p == nil || p.pipeline == nil {
		return errors.New("statusui: poller is not initialized")
	}
	rec, err := p.pipeline.RecognizeScreen(screen)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.panelRect = rec.PanelRect
	p.stripRect = rec.StripRect
	p.lastValidate = time.Now()
	p.mu.Unlock()
	return nil
}

// Parse parses HP/SP values from the provided strip image. The strip
// should be captured at StripRect. Returns the parsed values and any
// parse error. The underlying Reader caches glyph hints from the
// previous successful call so repeated calls with the same values are
// significantly faster.
func (p *StripPoller) Parse(strip image.Image) (ParsedStatus, error) {
	if p == nil || p.pipeline == nil {
		return ParsedStatus{}, errors.New("statusui: poller is not initialized")
	}
	res, err := p.pipeline.ParseStrip(strip)
	return res.ParsedStatus, err
}

// Invalidate forces NeedsValidation to return true on the next call,
// triggering an immediate re-acquisition. Use when the game window is
// known to have moved or the panel may have changed.
func (p *StripPoller) PanelRect() image.Rectangle {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.panelRect
}

func (p *StripPoller) Invalidate() {
	p.mu.Lock()
	p.lastValidate = time.Time{}
	p.panelRect = image.Rectangle{}
	p.stripRect = image.Rectangle{}
	p.mu.Unlock()
}
