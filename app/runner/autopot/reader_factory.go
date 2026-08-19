package autopot

import (
	"ezrokit/runner/autopot/statusui"
)

// ReaderFactory constructs BarReader instances based on the provided config.
// It owns reader construction so the orchestrator depends on the BarReader
// contract and readerController rather than platform-specific setup details.
type ReaderFactory struct {
	settings func() AutoPotConfig // live config for runtime threshold lookups
	capture  screenCapturer
	hpStab   *BarStabilizer
	spStab   *BarStabilizer
}

// NewReaderFactory creates a factory for the given settings getter and stabilizers.
// The settings function provides live access to the config (thresholds can change
// via UpdateSettings mid-run).
func NewReaderFactory(settings func() AutoPotConfig, hpStab, spStab *BarStabilizer) *ReaderFactory {
	return NewReaderFactoryWithCapture(settings, hpStab, spStab, defaultScreenCapturer())
}

// NewReaderFactoryWithCapture creates a factory with an explicit screen
// source. Production callers normally use NewReaderFactory; tests and other
// hosts can provide deterministic frames without replacing package globals.
func NewReaderFactoryWithCapture(settings func() AutoPotConfig, hpStab, spStab *BarStabilizer, capture screenCapturer) *ReaderFactory {
	return &ReaderFactory{
		settings: settings,
		capture:  capture,
		hpStab:   hpStab,
		spStab:   spStab,
	}
}

// Build creates the primary BarReader plus the visual recovery pair.
//
// Address mode always returns an address reader. Visual mode returns OCR as
// primary when the embedded pipeline loads, with the pixel reader used for
// runtime recovery if the status panel is lost.
//
// Returns:
//   - primary: the active BarReader
//   - pixel: pixel reader for OCR recovery; nil in address mode
//   - ocr: OCR reader for pixel→OCR recovery; nil if unavailable
//   - isAddress: true when address reading is the selected mode
func (f *ReaderFactory) Build() (primary BarReader, pixel *pixelBarReader, ocr *statusUIReader, isAddress bool) {
	cfg := f.settings()
	if cfg.IsAddressMode() {
		return f.buildAddressReader(cfg), nil, nil, true
	}
	primary, pixel, ocr = f.buildVisualReaders(cfg)
	return primary, pixel, ocr, false
}

func (f *ReaderFactory) buildVisualReaders(cfg AutoPotConfig) (primary BarReader, fallback *pixelBarReader, ocr *statusUIReader) {
	pixel := f.buildPixelReader(cfg)
	if ocr, ok := f.tryBuildOCRReader(cfg); ok {
		return ocr, pixel, ocr
	}
	return pixel, pixel, nil
}

func (f *ReaderFactory) buildPixelReader(cfg AutoPotConfig) *pixelBarReader {
	return &pixelBarReader{
		capture: f.capture,
		hpStab:  f.hpStab,
		spStab:  f.spStab,
		log:     cfg.Core.Log,
	}
}

func (f *ReaderFactory) tryBuildOCRReader(cfg AutoPotConfig) (*statusUIReader, bool) {
	pipeline, err := statusui.NewDefaultPipeline()
	if err != nil {
		if cfg.Core.Log != nil {
			cfg.Core.Log("autopot: OCR pipeline unavailable: " + err.Error())
		}
		return nil, false
	}
	return &statusUIReader{
		capture:      f.capture,
		poller:       statusui.NewStripPoller(pipeline),
		log:          cfg.Core.Log,
		coreSettings: func() CoreConfig { return f.settings().Core },
	}, true
}

func (f *ReaderFactory) buildAddressReader(cfg AutoPotConfig) *addressReader {
	return &addressReader{
		pid:          cfg.Address.ProcessPID,
		profile:      cfg.Address.Profile,
		processTitle: cfg.Address.ProcessTitle,
		log:          cfg.Core.Log,
		thresholdFn:  func() (hpThresh, spThresh int) { c := f.settings().Core; return c.HPThreshold, c.SPThreshold },
	}
}
