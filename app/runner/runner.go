// Package runner is the public facade. It re-exports a small set of
// types from runner/autopot and runner/internal/session (see also
// timing.go for timing constants) so consumers (gui, tests) can import
// a single package instead of reaching into the internal subpackages.
//
// The facade is intentionally lean — only symbols with a current
// consumer are re-exported. Add a new re-export inside the type/var
// blocks below when something needs it; remove it when nothing uses it.
//
// Note: this is the ONLY place in the package that imports runner/autopot
// (autopot does not import runner to keep the graph cycle-free). All
// other files in runner/* import from internal/{lifecycle,session,
// timing} directly.
package runner

import (
	"ezrokit/runner/autopot"
	"ezrokit/runner/internal/session"
)

// InputSession is the canonical input-device interface any runner uses.
// Re-exported so gui/* and tests can keep writing runner.InputSession.
type InputSession = session.InputSession

// AutoPot types re-exported for callers that import only this package.
type (
	AutoPotRunner = autopot.AutoPotRunner
	AutoPotConfig = autopot.AutoPotConfig
	CoreConfig    = autopot.CoreConfig
	AddressConfig = autopot.AddressConfig
)

var (
	NewAutoPot = autopot.NewAutoPot
)

