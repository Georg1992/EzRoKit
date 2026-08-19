//go:build windows

package main

import (
	"testing"

	"github.com/lxn/walk"
)

func TestClearKeyChainSwitchDoesNotPanic(t *testing.T) {
	mw, err := walk.NewMainWindow()
	if err != nil {
		t.Fatal(err)
	}
	defer mw.Dispose()

	tabs, err := walk.NewTabWidget(mw)
	if err != nil {
		t.Fatal(err)
	}
	page, err := walk.NewTabPage()
	if err != nil {
		t.Fatal(err)
	}
	app := &guiApp{mainWindow: mw}
	if err := app.buildKeyChainTab(page); err != nil {
		t.Fatal(err)
	}
	if err := tabs.Pages().Add(page); err != nil {
		t.Fatal(err)
	}

	app.keychain.switches[0].keyVKs[0] = '1'
	app.keychain.switches[0].keyVKs[1] = '2'
	app.keychain.setKeyText(0, 0, '1')
	app.keychain.setKeyText(0, 1, '2')
	if err := app.keychain.switches[0].slots[0].delayEdit.SetValue(200); err != nil {
		t.Fatal(err)
	}

	app.hookChainDelayCommits()
	app.clearKeyChainSwitch(0)

	if app.keychain.switches[0].keyVKs[0] != 0 {
		t.Fatalf("trigger after clear = %d, want 0", app.keychain.switches[0].keyVKs[0])
	}
	if app.keychain.switches[0].keyVKs[1] != 0 {
		t.Fatalf("key 2 after clear = %d, want 0", app.keychain.switches[0].keyVKs[1])
	}
	if got := app.keychain.switches[0].slots[0].delayEdit.Value(); got != 0 {
		t.Fatalf("delay after clear = %v, want 0", got)
	}
	if app.keychain.visibleCount != 1 {
		t.Fatalf("visibleCount after clear = %d, want 1", app.keychain.visibleCount)
	}
}
