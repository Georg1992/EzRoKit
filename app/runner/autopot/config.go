package autopot

import (
	"fmt"

	"ezrokit/runner/internal/session"
	"ezrokit/runner/profiles"
)

// CoreConfig holds the shared configuration used by all BarReader
// implementations and the AutoPotRunner orchestrator.
type CoreConfig struct {
	Session     session.InputSession
	HPThreshold int
	SPThreshold int
	HPKeyVK     int32
	SPKeyVK     int32
	HPKeyName   string // human-readable key name for overlay (e.g. "F1")
	SPKeyName   string
	Log         func(string)
	Status      StatusSink
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

// IsAddressMode reports whether address-reading mode is selected.
func (c AutoPotConfig) IsAddressMode() bool {
	return c.Address != nil
}

// validate checks that required fields are present.
// HP and SP are independent: a single mapped potion is enough to start.
func (c AutoPotConfig) validate() error {
	if c.Core.Session == nil {
		return fmt.Errorf("input session is required")
	}
	if c.Core.Log == nil {
		return fmt.Errorf("log callback is required")
	}
	if c.Address != nil && c.Address.ProcessPID == 0 {
		return fmt.Errorf("address mode: no game window selected")
	}
	return nil
}

func (c AutoPotConfig) hpBound() bool {
	return c.Core.HPKeyVK != 0
}

func (c AutoPotConfig) spBound() bool {
	return c.Core.SPKeyVK != 0
}

// HasBoundPotion reports whether at least one potion key is assigned.
func (c AutoPotConfig) HasBoundPotion() bool {
	return c.hpBound() || c.spBound()
}
