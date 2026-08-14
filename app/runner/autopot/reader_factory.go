package autopot

import (
	"belarus-champ-tools/runner/autopot/statusui"
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
	if capture == nil {
		capture = defaultScreenCapturer()
	}
	return &ReaderFactory{
		settings: settings,
		capture:  capture,
		hpStab:   hpStab,
		spStab:   spStab,
	}
}

// Build creates the primary BarReader, the pixel fallback, and the OCR
// reader (nil if OCR is unavailable or in address mode).
//
// Returns:
//   - primary: the active BarReader
//   - fallback: pixel reader for OCR→pixel fallback; nil in address mode
//   - ocr: OCR reader for pixel→OCR recovery; nil if unavailable
//   - isAddress: true only when address reading is active (false after visual fallback)
func (f *ReaderFactory) Build() (primary BarReader, fallback *pixelBarReader, ocr *statusUIReader, isAddress bool) {
	cfg := f.settings()
	if cfg.IsAddressMode() {
		reader, err := f.buildAddressReader(cfg)
		if err != nil {
			cfg.Core.Log("autopot: " + err.Error() + " — falling back to Visual mode")
			primary, fallback, ocr = f.buildVisualReaders(cfg)
			return primary, fallback, ocr, false
		}
		setMode(cfg.Core.OnStatusUIMode, "Address reading")
		return reader, nil, nil, true
	}
	primary, fallback, ocr = f.buildVisualReaders(cfg)
	return primary, fallback, ocr, false
}

// buildVisualReaders creates pixel + optional OCR readers for visual mode.
func (f *ReaderFactory) buildVisualReaders(cfg AutoPotConfig) (primary BarReader, fallback *pixelBarReader, ocr *statusUIReader) {
	pixel := f.buildPixelReader(cfg)
	if ocr, ok := f.tryBuildOCRReader(cfg); ok {
		setMode(cfg.Core.OnStatusUIMode, "Searching...")
		return ocr, pixel, ocr
	}
	setMode(cfg.Core.OnStatusUIMode, "Pixelsearch")
	if cfg.Core.OnStatusParsed != nil {
		cfg.Core.OnStatusParsed(pixelModeSentinel, 0, pixelModeSentinel, 0, 0, 0, 0, 0)
	}
	return pixel, pixel, nil
}

func (f *ReaderFactory) buildPixelReader(cfg AutoPotConfig) *pixelBarReader {
	return &pixelBarReader{
		capture:  f.capture,
		hpStab:   f.hpStab,
		spStab:   f.spStab,
		log:      cfg.Core.Log,
		onParsed: cfg.Core.OnStatusParsed,
	}
}

func (f *ReaderFactory) tryBuildOCRReader(cfg AutoPotConfig) (*statusUIReader, bool) {
	pipeline, err := statusui.NewDefaultPipeline()
	if err != nil {
		return nil, false
	}
	return &statusUIReader{
		capture:      f.capture,
		poller:       statusui.NewStripPoller(pipeline),
		onModeChange: cfg.Core.OnStatusUIMode,
		onParsed:     cfg.Core.OnStatusParsed,
		log:          cfg.Core.Log,
		coreSettings: func() CoreConfig { return f.settings().Core },
	}, true
}

func (f *ReaderFactory) buildAddressReader(cfg AutoPotConfig) (*addressReader, error) {
	baseAddr, err := GetProcessBaseAddr(cfg.Address.ProcessPID)
	if err != nil {
		return nil, err
	}
	return &addressReader{
		pid:          cfg.Address.ProcessPID,
		profile:      cfg.Address.Profile,
		processTitle: cfg.Address.ProcessTitle,
		moduleBase:   baseAddr,
		log:          cfg.Core.Log,
		thresholdFn:  func() (hpThresh, spThresh int) { c := f.settings().Core; return c.HPThreshold, c.SPThreshold },
		onParsed:     cfg.Core.OnStatusParsed,
		onModeChange: cfg.Core.OnStatusUIMode,
	}, nil
}
