//go:build windows

package main

import (
	"path/filepath"
	"testing"

	"ezrokit/runner"
)

func TestToolProfilesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	want := toolProfile{
		Name: "Raid",
		Clicker: clickerProfile{
			VisibleCount: 2,
			Slots: [runner.ClickerSlotCount]runner.ClickerSlot{
				{TriggerVKs: [runner.ClickerKeysPerBind]int32{0x41}, DelayMs: 125, MouseClick: true},
			},
		},
		Timer: timerProfile{
			VisibleCount: 1,
			Slots: [runner.TimerKeySlotCount]timerProfileSlot{
				{Enabled: true, KeyVK: 0x70, IntervalSec: 7},
			},
		},
		AutoPot: autoPotProfile{
			HPEnabled: true, SPThreshold: 30, HPThreshold: 42, HPKeyVK: 0x71,
			AddressMode: true, AddressProfile: "Revenant", WindowTitle: "Game",
		},
		KeyChain: keyChainProfile{
			VisibleCount: 1,
			Switches: [runner.KeyChainCount]keyChainProfileSwitch{
				{Keys: [runner.KeyChainSlotCount]int32{0x72, 0x31}, DelaysMs: [runner.KeyChainSlotCount]int{10, 20}},
			},
		},
	}
	if err := saveToolProfiles(path, []toolProfile{want}); err != nil {
		t.Fatalf("saveToolProfiles: %v", err)
	}
	got, err := loadToolProfiles(path)
	if err != nil {
		t.Fatalf("loadToolProfiles: %v", err)
	}
	if len(got) != 1 || got[0].Name != want.Name {
		t.Fatalf("loaded profiles=%+v", got)
	}
	if got[0].Clicker.Slots[0] != want.Clicker.Slots[0] {
		t.Fatalf("clicker slot=%+v want %+v", got[0].Clicker.Slots[0], want.Clicker.Slots[0])
	}
	if got[0].AutoPot != want.AutoPot {
		t.Fatalf("autopot=%+v want %+v", got[0].AutoPot, want.AutoPot)
	}
	if got[0].KeyChain.Switches[0] != want.KeyChain.Switches[0] {
		t.Fatalf("keychain switch=%+v want %+v", got[0].KeyChain.Switches[0], want.KeyChain.Switches[0])
	}
}

func TestNormalizeToolProfileRestoresDefaults(t *testing.T) {
	profile := toolProfile{}
	normalizeToolProfile(&profile)
	if profile.Clicker.VisibleCount != 1 || profile.Timer.VisibleCount != 1 || profile.KeyChain.VisibleCount != 1 {
		t.Fatalf("visible counts=%d/%d/%d", profile.Clicker.VisibleCount, profile.Timer.VisibleCount, profile.KeyChain.VisibleCount)
	}
	if profile.AutoPot.HPThreshold != 50 || profile.AutoPot.SPThreshold != 30 {
		t.Fatalf("thresholds=%d/%d", profile.AutoPot.HPThreshold, profile.AutoPot.SPThreshold)
	}
	if profile.Clicker.Slots[0].DelayMs != runner.DefaultDelayMs || profile.Timer.Slots[0].IntervalSec != runner.DefaultTimerKeyIntervalSec {
		t.Fatalf("defaults=%+v/%+v", profile.Clicker.Slots[0], profile.Timer.Slots[0])
	}
}
