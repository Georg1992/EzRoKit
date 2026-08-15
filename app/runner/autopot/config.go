package autopot

import (
	"fmt"

	"ezrokit/runner/internal/session"
	"ezrokit/runner/profiles"
)

// CoreConfig holds the shared configuration used by all BarReader
// implementations and the AutoPotRunner orchestrator.
type CoreConfig struct {
	Session        session.InputSession
	HPThreshold    int
	SPThreshold    int
	HPKeyVK        int32
	SPKeyVK        int32
	HPKeyName      string // human-readable key name for overlay (e.g. "F1")
	SPKeyName      string
	HPEnabled      bool
	SPEnabled      bool
	Log            func(string)
	OnStatusParsed func(hp, hpMax, sp, spMax, stripX, stripY, stripW, stripH int)
	OnStatusUIMode func(mode string)
}

// AddressConfig holds configuration specific to the address-reading
// BarReader. When nil, the runner uses visual detection (pixel/OCR).
type AddressConfig struct {
	ProcessPID   uint32
	ProcessTitle string // game window title for auto-reconnect on error
	Profile      profiles.Profile
}

// AutoPotConfig is the composite config passed by the GUI layer.
// It decomposes into CoreConfig (always required) and an optional
// AddressConfig for memory-reading mode.
type AutoPotConfig struct {
	Core    CoreConfig
	Address *AddressConfig // nil = visual mode (pixel/OCR)
}

func (c *AutoPotConfig) applyDefaults() {
	if c.Core.Log == nil {
		c.Core.Log = func(string) {}
	}
}

// IsAddressMode reports whether address-reading mode is active.
func (c AutoPotConfig) IsAddressMode() bool {
	return c.Address != nil && c.Address.ProcessPID != 0
}

// validate checks that required fields are present.
func (c AutoPotConfig) validate() error {
	if c.Core.Session == nil {
		return fmt.Errorf("input session is required")
	}
	if c.Core.Log == nil {
		return fmt.Errorf("log callback is required")
	}
	if c.Core.HPEnabled && c.Core.HPKeyVK == 0 {
		return fmt.Errorf("HP potion key is not set")
	}
	if c.Core.SPEnabled && c.Core.SPKeyVK == 0 {
		return fmt.Errorf("SP potion key is not set")
	}
	return nil
}
