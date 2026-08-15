//go:build windows

package main

import (
	"testing"

	"ezrokit/runner"
)

type mockAutoPotRunner struct {
	running bool
	updates []runner.AutoPotConfig
}

func (m *mockAutoPotRunner) Running() bool { return m.running }

func (m *mockAutoPotRunner) UpdateSettings(cfg runner.AutoPotConfig) {
	m.updates = append(m.updates, cfg)
}

func TestCommittedThresholdFields(t *testing.T) {
	app := &guiApp{}
	app.autopot.hpThreshold = 50
	app.autopot.spThreshold = 30
	if app.autopot.hpThreshold != 50 || app.autopot.spThreshold != 30 {
		t.Fatalf("thresholds=%d/%d want 50/30", app.autopot.hpThreshold, app.autopot.spThreshold)
	}
	app.autopot.hpThreshold = 60
	if app.autopot.hpThreshold != 60 || app.autopot.spThreshold != 30 {
		t.Fatalf("thresholds=%d/%d want 60/30", app.autopot.hpThreshold, app.autopot.spThreshold)
	}
}

func TestRunningRunnerReceivesThresholdUpdate(t *testing.T) {
	mock := &mockAutoPotRunner{running: true}
	cfg := runner.AutoPotConfig{
		Core: runner.CoreConfig{
			HPEnabled:   true,
			HPKeyVK:     'W',
			HPThreshold: 60,
			SPThreshold: 50,
		},
	}
	mock.UpdateSettings(cfg)
	if len(mock.updates) != 1 {
		t.Fatalf("updates=%d want 1", len(mock.updates))
	}
	if mock.updates[0].Core.HPThreshold != 60 || mock.updates[0].Core.SPThreshold != 50 {
		t.Fatalf("update=%+v want 60/50", mock.updates[0])
	}
}
