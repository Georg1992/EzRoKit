package autopot

import (
	"context"
	"fmt"
	"time"

	"ezrokit/runner/internal/timing"
)

const (
	ocrProbeInterval      = 2 * time.Second
	statusUIRetryInterval = 5 * time.Second
)

// readerController owns visual-reader selection and recovery state. The main
// autopot loop only asks it whether a result is usable and which reader is
// currently active; it does not need to know OCR/pixel transition details.
type readerController struct {
	active   BarReader
	pixel    *pixelBarReader
	ocr      *statusUIReader
	address  bool
	usingOCR bool

	nextOCRRetry    time.Time
	pixelFailStart  time.Time
	loggedPixelFail bool
}

func newReaderController(reader BarReader, pixel *pixelBarReader, ocr *statusUIReader, address bool) *readerController {
	return &readerController{
		active:   reader,
		pixel:    pixel,
		ocr:      ocr,
		address:  address,
		usingOCR: !address && reader == ocr,
	}
}

func (c *readerController) initialMode() (mode string, clearValues bool) {
	if c.address {
		return "Address reading", false
	}
	if c.usingOCR {
		return "Searching...", false
	}
	return "Pixelsearch", true
}

// process returns true when result is valid for normal HP/SP processing.
// Failure delays and reader transitions are kept inside this controller.
func (c *readerController) process(ctx context.Context, cfg AutoPotConfig, result BarReadResult) bool {
	if c.address {
		return result.Status == StatusFound
	}
	if c.usingOCR {
		if result.Status == StatusFound {
			return true
		}
		c.switchToPixel(cfg, result)
		return false
	}
	if result.Status == StatusFound {
		return true
	}

	c.handlePixelFailure(ctx, cfg, result)
	return false
}

func (c *readerController) switchToPixel(cfg AutoPotConfig, result BarReadResult) {
	cfg.Core.Log(fmt.Sprintf("autopot: statusui issue, switching to pixel-bar: %v", result.Err))
	c.active = c.pixel
	c.usingOCR = false
	setMode(cfg.Core.Status, "Pixelsearch")
	if cfg.Core.Status != nil {
		cfg.Core.Status.ClearValues()
	}
	c.nextOCRRetry = time.Now().Add(ocrProbeInterval)
}

func (c *readerController) handlePixelFailure(ctx context.Context, cfg AutoPotConfig, result BarReadResult) {
	if c.pixelFailStart.IsZero() {
		c.pixelFailStart = time.Now()
	}
	if time.Since(c.pixelFailStart) < statusUIRetryInterval {
		timing.Sleep(ctx, timing.CaptureRetryDelay)
		return
	}
	if !c.loggedPixelFail {
		cfg.Core.Log(fmt.Sprintf("autopot: pixel bars not found for 5s — retrying every 5s: %v", result.Err))
		c.loggedPixelFail = true
	}
	timing.Sleep(ctx, statusUIRetryInterval)
}

// probeOCR checks for recovery after a pixel result has been processed. It is
// deliberately separate from process so a valid pixel result is never delayed.
func (c *readerController) probeOCR(ctx context.Context, cfg AutoPotConfig) {
	if !c.isPixel() || c.ocr == nil || !time.Now().After(c.nextOCRRetry) {
		return
	}
	c.nextOCRRetry = time.Now().Add(ocrProbeInterval)
	if probe := c.ocr.ReadValues(ctx); probe.Status == StatusFound {
		cfg.Core.Log("autopot: statusui recovered, switching back")
		c.active = c.ocr
		c.usingOCR = true
		setMode(cfg.Core.Status, "OCR")
		publishStatus(cfg.Core.Status, probe)
		c.loggedPixelFail = false
	}
}

func (c *readerController) markValid() {
	c.pixelFailStart = time.Time{}
	c.loggedPixelFail = false
}

func (c *readerController) reader() BarReader { return c.active }
func (c *readerController) isAddress() bool   { return c.address }
func (c *readerController) isPixel() bool     { return !c.address && !c.usingOCR }
