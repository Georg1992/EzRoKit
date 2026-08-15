//go:build windows

package main

import (
	"ezrokit/runner"

	"github.com/lxn/walk"
)

func (a *guiApp) buildControlPanel(parent walk.Container) error {
	runGB, err := walk.NewGroupBox(parent)
	if err != nil {
		return err
	}
	if err := runGB.SetTitle("1. Tools Control"); err != nil {
		return err
	}
	runLayout := walk.NewVBoxLayout()
	runLayout.SetSpacing(8)
	if err := runGB.SetLayout(runLayout); err != nil {
		return err
	}

	controlRow, err := walk.NewComposite(runGB)
	if err != nil {
		return err
	}
	controlHBox := walk.NewHBoxLayout()
	controlHBox.SetSpacing(16)
	if err := controlRow.SetLayout(controlHBox); err != nil {
		return err
	}

	if err := a.buildViiperSection(controlRow); err != nil {
		return err
	}
	if _, err := walk.NewHSpacer(controlRow); err != nil {
		return err
	}
	if err := a.buildToolsSection(controlRow); err != nil {
		return err
	}

	a.profileLogRow, err = walk.NewComposite(runGB)
	if err != nil {
		return err
	}
	profileLogLayout := walk.NewHBoxLayout()
	profileLogLayout.SetSpacing(12)
	if err := profileLogLayout.SetAlignment(walk.AlignHNearVNear); err != nil {
		return err
	}
	if err := a.profileLogRow.SetLayout(profileLogLayout); err != nil {
		return err
	}

	profileGB, err := walk.NewGroupBox(a.profileLogRow)
	if err != nil {
		return err
	}
	if err := profileGB.SetTitle(""); err != nil {
		return err
	}
	profileLayout := walk.NewVBoxLayout()
	profileLayout.SetSpacing(4)
	if err := profileLayout.SetAlignment(walk.AlignHNearVNear); err != nil {
		return err
	}
	if err := profileGB.SetLayout(profileLayout); err != nil {
		return err
	}

	profileRow, err := walk.NewComposite(profileGB)
	if err != nil {
		return err
	}
	profileHBox := walk.NewHBoxLayout()
	profileHBox.SetSpacing(8)
	if err := profileHBox.SetAlignment(walk.AlignHNearVCenter); err != nil {
		return err
	}
	if err := profileRow.SetLayout(profileHBox); err != nil {
		return err
	}
	a.profiles.combo, err = walk.NewComboBox(profileRow)
	if err != nil {
		return err
	}
	if err := a.profiles.combo.SetMinMaxSize(walk.Size{Width: 220, Height: 0}, walk.Size{Width: 220, Height: 0}); err != nil {
		return err
	}
	if err := a.profiles.combo.SetModel([]string{newToolProfileName}); err != nil {
		return err
	}
	a.profiles.combo.CurrentIndexChanged().Attach(a.onToolProfileSelected)

	profileButtons, err := walk.NewComposite(profileGB)
	if err != nil {
		return err
	}
	buttonHBox := walk.NewHBoxLayout()
	buttonHBox.SetSpacing(8)
	if err := buttonHBox.SetAlignment(walk.AlignHNearVCenter); err != nil {
		return err
	}
	if err := profileButtons.SetLayout(buttonHBox); err != nil {
		return err
	}
	a.profiles.saveBtn, err = newFixedButton(profileButtons, "Save", 70)
	if err != nil {
		return err
	}
	a.profiles.saveBtn.Clicked().Attach(a.saveToolProfile)
	a.profiles.removeBtn, err = newFixedButton(profileButtons, "Remove", 70)
	if err != nil {
		return err
	}
	a.profiles.removeBtn.Clicked().Attach(a.removeToolProfile)

	return nil
}

// buildViiperSection creates the VIIPER status badge, Start button, and hint.
func (a *guiApp) buildViiperSection(parent walk.Container) error {
	panel, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	vbox := walk.NewVBoxLayout()
	vbox.SetSpacing(4)
	if err := panel.SetLayout(vbox); err != nil {
		return err
	}

	a.viiperBadge, err = newViiperBadge(panel)
	if err != nil {
		return err
	}

	a.viiperStartBtn, err = newFixedButton(panel, "Start VIIPER", 110)
	if err != nil {
		return err
	}
	a.viiperStartBtn.Clicked().Attach(a.onStartViiper)

	if _, err := newHint(panel, "VIIPER starts automatically."); err != nil {
		return err
	}
	return nil
}

// buildToolsSection creates the TOOLS status badge, Start/Stop buttons, and hint.
func (a *guiApp) buildToolsSection(parent walk.Container) error {
	panel, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	vbox := walk.NewVBoxLayout()
	vbox.SetSpacing(4)
	if err := panel.SetLayout(vbox); err != nil {
		return err
	}

	a.toolsBadge, err = newToolsBadge(panel)
	if err != nil {
		return err
	}

	btnRow, err := walk.NewComposite(panel)
	if err != nil {
		return err
	}
	btnHBox := walk.NewHBoxLayout()
	btnHBox.SetSpacing(10)
	if err := btnRow.SetLayout(btnHBox); err != nil {
		return err
	}

	a.startBtn, err = newFixedButton(btnRow, "Start", 70)
	if err != nil {
		return err
	}
	a.startBtn.Clicked().Attach(a.onStart)

	a.stopBtn, err = newFixedButton(btnRow, "Stop", 70)
	if err != nil {
		return err
	}
	a.stopBtn.SetEnabled(false)
	a.stopBtn.Clicked().Attach(a.onStop)

	if _, err := newHint(panel, "Toggle: "+runner.ToggleKeyLabel()); err != nil {
		return err
	}
	return nil
}
