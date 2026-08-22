package autopot

import (
	"context"
	"image"
	"testing"
)

type recordingScreenCapturer struct {
	sizeCalls   int
	regionCalls int
	fullCalls   int
}

func (c *recordingScreenCapturer) ScreenSize() (int, int) {
	c.sizeCalls++
	return 1920, 1080
}

func (c *recordingScreenCapturer) CaptureScreenRegion(Rect) (*image.RGBA, error) {
	c.regionCalls++
	return image.NewRGBA(image.Rect(0, 0, 32, 16)), nil
}

func (c *recordingScreenCapturer) CaptureFullScreen() (*image.RGBA, error) {
	c.fullCalls++
	return image.NewRGBA(image.Rect(0, 0, 1920, 1080)), nil
}

func TestPixelReaderUsesInjectedCapture(t *testing.T) {
	capture := &recordingScreenCapturer{}
	reader := &pixelBarReader{
		capture:  capture,
		hpStab:   NewBarStabilizer(true, 50),
		spStab:   NewBarStabilizer(false, 50),
		settings: func() AutoPotConfig { return AutoPotConfig{Core: CoreConfig{HPKeyVK: 'Q'}} },
	}

	result := reader.ReadValues(context.Background())
	if result.Status == StatusFound {
		t.Fatal("empty injected frame unexpectedly produced a valid bar reading")
	}
	if capture.sizeCalls != 1 || capture.regionCalls != 1 {
		t.Fatalf("capture calls = size:%d region:%d; want 1 each", capture.sizeCalls, capture.regionCalls)
	}
}

func TestReaderFactoryPassesCaptureToVisualReaders(t *testing.T) {
	capture := &recordingScreenCapturer{}
	cfg := AutoPotConfig{Core: CoreConfig{Log: func(string) {}}}
	ap := NewAutoPot(cfg)

	_, pixel, ocr, _ := NewReaderFactoryWithCapture(ap.settings, ap.hpStabilizer, ap.spStabilizer, capture).Build()
	if pixel == nil {
		t.Fatal("factory returned nil pixel reader")
	}
	if pixel.capture != capture {
		t.Fatal("factory did not inject capture into pixel reader")
	}
	if ocr != nil && ocr.capture != capture {
		t.Fatal("factory did not inject capture into OCR reader")
	}
}
