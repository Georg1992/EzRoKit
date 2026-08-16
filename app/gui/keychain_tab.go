//go:build windows

package main

import (
	"fmt"

	"ezrokit/runner"
	"github.com/lxn/walk"
)

type keyChainSlotWidgets struct {
	keyEdit   *walk.LineEdit
	delayEdit *walk.NumberEdit
}

type keychainSwitchUI struct {
	group     *walk.GroupBox
	slots     [runner.KeyChainSlotCount]keyChainSlotWidgets
	keyVKs    [runner.KeyChainSlotCount]int32
	clearBtn  *walk.PushButton
	removeBtn *walk.PushButton
}

type keychainController struct {
	switches     [runner.KeyChainCount]keychainSwitchUI
	visibleCount int
	addBtn       *walk.PushButton
}

func (c *keychainController) setKeyText(switchIdx, slotIdx int, vk int32) {
	text := "None"
	if vk != 0 {
		text = runner.KeyName(vk)
	}
	c.switches[switchIdx].slots[slotIdx].keyEdit.SetText(text)
}

func (c *keychainController) config(logFn func(string)) runner.KeyChainConfig {
	cfg := runner.KeyChainConfig{Log: logFn}
	for i := 0; i < c.visibleCount; i++ {
		sw := &c.switches[i]
		for j := 0; j < runner.KeyChainSlotCount; j++ {
			cfg.Switches[i].Keys[j] = sw.keyVKs[j]
			cfg.Switches[i].DelaysMs[j] = int(sw.slots[j].delayEdit.Value())
		}
	}
	return cfg
}

func (a *guiApp) buildKeyChainTab(page *walk.TabPage) error {
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{HNear: 4, VNear: 4, HFar: 4, VFar: 4})
	layout.SetSpacing(10)
	if err := page.SetLayout(layout); err != nil {
		return err
	}

	sv, err := walk.NewScrollView(page)
	if err != nil {
		return err
	}
	sv.SetScrollbars(false, true)
	// Cap height so adding switches scrolls instead of growing the main window.
	if err := sv.SetMinMaxSize(
		walk.Size{Width: 0, Height: keyChainScrollMinHeight},
		walk.Size{Width: keyChainScrollMaxWidth, Height: keyChainScrollMaxHeight},
	); err != nil {
		return err
	}
	svLayout := walk.NewVBoxLayout()
	svLayout.SetSpacing(10)
	if err := sv.SetLayout(svLayout); err != nil {
		return err
	}

	a.keychain.visibleCount = 1
	for i := 0; i < runner.KeyChainCount; i++ {
		if err := a.buildKeyChainGroup(sv, i); err != nil {
			return err
		}
		if i > 0 {
			a.keychain.switches[i].group.SetVisible(false)
		}
	}

	addRow, err := walk.NewComposite(page)
	if err != nil {
		return err
	}
	addLayout := walk.NewHBoxLayout()
	addLayout.SetSpacing(10)
	if err := addRow.SetLayout(addLayout); err != nil {
		return err
	}

	a.keychain.addBtn, err = walk.NewPushButton(addRow)
	if err != nil {
		return err
	}
	if err := a.keychain.addBtn.SetText("+ Add switch"); err != nil {
		return err
	}
	a.keychain.addBtn.Clicked().Attach(a.onAddKeyChainSwitch)
	a.updateKeyChainAddButton()
	a.updateKeyChainRemoveButtons()

	if _, err := newHint(page, "Key 1 is the trigger for each switch. Tap it to run the chain once; hold it to loop."); err != nil {
		return err
	}
	return nil
}

// buildKeyChainGroup creates Switch N with labels, step columns, links, Clear, and Remove.
func (a *guiApp) buildKeyChainGroup(parent walk.Container, index int) error {
	sw := &a.keychain.switches[index]

	chainGB, err := walk.NewGroupBox(parent)
	if err != nil {
		return err
	}
	sw.group = chainGB
	if err := chainGB.SetTitle(fmt.Sprintf("Switch %d", index+1)); err != nil {
		return err
	}
	chainLayout := walk.NewVBoxLayout()
	chainLayout.SetSpacing(10)
	if err := chainGB.SetLayout(chainLayout); err != nil {
		return err
	}

	chainRow, err := walk.NewComposite(chainGB)
	if err != nil {
		return err
	}
	chainHBox := walk.NewHBoxLayout()
	chainHBox.SetSpacing(0)
	if err := chainRow.SetLayout(chainHBox); err != nil {
		return err
	}
	applyKeyChainSurface(chainRow)

	if err := a.buildKeyChainLabels(chainRow); err != nil {
		return err
	}
	if err := a.buildKeyChainSteps(chainRow, index); err != nil {
		return err
	}

	btnRow, err := walk.NewComposite(chainGB)
	if err != nil {
		return err
	}
	btnLayout := walk.NewHBoxLayout()
	btnLayout.SetSpacing(10)
	if err := btnRow.SetLayout(btnLayout); err != nil {
		return err
	}
	applyKeyChainSurface(btnRow)

	sw.clearBtn, err = walk.NewPushButton(btnRow)
	if err != nil {
		return err
	}
	if err := sw.clearBtn.SetText("Clear"); err != nil {
		return err
	}
	switchIdx := index
	sw.clearBtn.Clicked().Attach(func() {
		a.clearKeyChainSwitch(switchIdx)
	})

	sw.removeBtn, err = walk.NewPushButton(btnRow)
	if err != nil {
		return err
	}
	if err := sw.removeBtn.SetText("Remove"); err != nil {
		return err
	}
	sw.removeBtn.Clicked().Attach(func() {
		a.removeKeyChainSwitch(switchIdx)
	})
	return nil
}

func (a *guiApp) buildKeyChainLabels(parent walk.Container) error {
	labelsCol, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	labelsLayout := walk.NewVBoxLayout()
	labelsLayout.SetSpacing(0)
	if err := labelsCol.SetLayout(labelsLayout); err != nil {
		return err
	}
	if err := labelsCol.SetMinMaxSize(walk.Size{Width: keyChainLabelColWidth, Height: 0}, walk.Size{Width: keyChainLabelColWidth, Height: 0}); err != nil {
		return err
	}
	applyKeyChainSurface(labelsCol)

	keysLabel, err := walk.NewLabel(labelsCol)
	if err != nil {
		return err
	}
	if err := keysLabel.SetText("Keys:"); err != nil {
		return err
	}
	if err := keysLabel.SetMinMaxSize(walk.Size{Width: keyChainLabelColWidth, Height: keyChainFieldHeight}, walk.Size{Width: keyChainLabelColWidth, Height: keyChainFieldHeight}); err != nil {
		return err
	}

	downSpacer, err := walk.NewComposite(labelsCol)
	if err != nil {
		return err
	}
	if err := downSpacer.SetMinMaxSize(walk.Size{Width: keyChainLabelColWidth, Height: keyChainDownHeight}, walk.Size{Width: keyChainLabelColWidth, Height: keyChainDownHeight}); err != nil {
		return err
	}
	applyKeyChainSurface(downSpacer)

	delaysLabel, err := walk.NewLabel(labelsCol)
	if err != nil {
		return err
	}
	if err := delaysLabel.SetText("Delay(ms):"); err != nil {
		return err
	}
	if err := delaysLabel.SetMinMaxSize(walk.Size{Width: keyChainLabelColWidth, Height: keyChainFieldHeight}, walk.Size{Width: keyChainLabelColWidth, Height: keyChainFieldHeight}); err != nil {
		return err
	}
	return nil
}

func (a *guiApp) buildKeyChainSteps(parent walk.Container, switchIdx int) error {
	stepsRow, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	stepsHBox := walk.NewHBoxLayout()
	stepsHBox.SetSpacing(0)
	if err := stepsRow.SetLayout(stepsHBox); err != nil {
		return err
	}
	applyKeyChainSurface(stepsRow)

	stepHeight := keyChainStepHeight()
	for i := 0; i < runner.KeyChainSlotCount; i++ {
		if err := a.buildKeyChainStep(stepsRow, switchIdx, i, stepHeight); err != nil {
			return err
		}
		if i < runner.KeyChainSlotCount-1 {
			if err := a.buildKeyChainStepLink(stepsRow, stepHeight); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *guiApp) buildKeyChainStep(parent walk.Container, switchIdx, slotIdx, height int) error {
	step, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	stepLayout := walk.NewVBoxLayout()
	stepLayout.SetSpacing(0)
	if err := step.SetLayout(stepLayout); err != nil {
		return err
	}
	if err := step.SetMinMaxSize(walk.Size{Width: keyChainStepWidth, Height: height}, walk.Size{Width: keyChainStepWidth, Height: height}); err != nil {
		return err
	}
	applyKeyChainSurface(step)

	w := &a.keychain.switches[switchIdx].slots[slotIdx]
	w.keyEdit, err = walk.NewLineEdit(step)
	if err != nil {
		return err
	}
	if err := w.keyEdit.SetReadOnly(true); err != nil {
		return err
	}
	if err := w.keyEdit.SetMinMaxSize(walk.Size{Width: keyChainKeyFieldWidth, Height: keyChainFieldHeight}, walk.Size{Width: keyChainKeyFieldWidth, Height: keyChainFieldHeight}); err != nil {
		return err
	}
	a.keychain.setKeyText(switchIdx, slotIdx, 0)
	si, slot := switchIdx, slotIdx
	w.keyEdit.MouseDown().Attach(func(_ int, _ int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.bindKeyChainKey(si, slot)
		}
	})

	downArrow, err := newKeyChainDownArrow(step)
	if err != nil {
		return err
	}
	if err := downArrow.SetMinMaxSize(walk.Size{Width: keyChainStepWidth, Height: keyChainDownHeight}, walk.Size{Width: keyChainStepWidth, Height: keyChainDownHeight}); err != nil {
		return err
	}

	w.delayEdit, err = walk.NewNumberEdit(step)
	if err != nil {
		return err
	}
	if err := w.delayEdit.SetRange(0, keyChainDelayMaxMs); err != nil {
		return err
	}
	if err := w.delayEdit.SetDecimals(0); err != nil {
		return err
	}
	if err := w.delayEdit.SetIncrement(1); err != nil {
		return err
	}
	if err := w.delayEdit.SetSpinButtonsVisible(true); err != nil {
		return err
	}
	if err := w.delayEdit.SetValue(0); err != nil {
		return err
	}
	if err := w.delayEdit.SetMinMaxSize(walk.Size{Width: keyChainDelayFieldWidth, Height: keyChainFieldHeight}, walk.Size{Width: keyChainDelayFieldWidth, Height: keyChainFieldHeight}); err != nil {
		return err
	}
	w.delayEdit.ValueChanged().Attach(a.syncKeyChainSettings)

	return nil
}

func (a *guiApp) buildKeyChainStepLink(parent walk.Container, height int) error {
	link, err := newKeyChainStepLink(parent)
	if err != nil {
		return err
	}
	return link.SetMinMaxSize(
		walk.Size{Width: keyChainLinkWidth, Height: height},
		walk.Size{Width: keyChainLinkWidth, Height: height},
	)
}

func (a *guiApp) onAddKeyChainSwitch() {
	if a.keychain.visibleCount >= runner.KeyChainCount {
		return
	}
	a.keychain.switches[a.keychain.visibleCount].group.SetVisible(true)
	a.keychain.visibleCount++
	a.updateKeyChainAddButton()
	a.updateKeyChainRemoveButtons()
	a.setKeyChainConfigEnabled(a.isViiperReady())
}

func (a *guiApp) updateKeyChainAddButton() {
	if a.keychain.addBtn == nil {
		return
	}
	atMax := a.keychain.visibleCount >= runner.KeyChainCount
	a.keychain.addBtn.SetVisible(!atMax)
}

func (a *guiApp) updateKeyChainRemoveButtons() {
	canRemove := a.keychain.visibleCount > 1
	for i := 0; i < runner.KeyChainCount; i++ {
		btn := a.keychain.switches[i].removeBtn
		if btn == nil {
			continue
		}
		btn.SetVisible(i < a.keychain.visibleCount && canRemove)
	}
}

func (a *guiApp) syncKeyChainSettings() {
	if a.profileApplying {
		return
	}
	if !a.isStarted() {
		return
	}

	cfg := a.keychain.config(a.appendLog)
	a.mu.Lock()
	kc := a.keychainRunner
	a.mu.Unlock()

	if !cfg.Active() {
		a.stopKeyChainRunner()
		return
	}

	a.mu.Lock()
	cfg.Session = a.inputSession
	a.mu.Unlock()

	if kc != nil && kc.Running() {
		kc.UpdateSettings(cfg)
		return
	}

	a.startKeyChainRunner(cfg, a.guiLog(a.appendLog))
}

func (a *guiApp) setKeyChainConfigEnabled(enabled bool) {
	for i := 0; i < a.keychain.visibleCount; i++ {
		sw := &a.keychain.switches[i]
		for j := 0; j < runner.KeyChainSlotCount; j++ {
			sw.slots[j].keyEdit.SetEnabled(enabled)
			sw.slots[j].delayEdit.SetEnabled(enabled)
		}
		if sw.clearBtn != nil {
			sw.clearBtn.SetEnabled(enabled)
		}
		if sw.removeBtn != nil {
			sw.removeBtn.SetEnabled(enabled && a.keychain.visibleCount > 1)
		}
	}
	if a.keychain.addBtn != nil {
		a.keychain.addBtn.SetEnabled(enabled && a.keychain.visibleCount < runner.KeyChainCount)
	}
}

func (a *guiApp) startKeyChainRunner(cfg runner.KeyChainConfig, log func(string)) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.shuttingDown.Load() {
		return
	}
	take := func() lifecycleRunner {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.keychainRunner == nil {
			return nil
		}
		old := a.keychainRunner
		a.keychainRunner = nil
		return old
	}
	store := func(r lifecycleRunner) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.keychainRunner = r.(*runner.KeyChainRunner)
	}
	replaceRunner(
		take,
		store,
		"KeyChain",
		log,
		func() runner.InputSession {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.inputSession
		},
		func() bool { return cfg.Active() },
		func(sess runner.InputSession) lifecycleRunner {
			cfg.Session = sess
			cfg.Log = log
			return runner.NewKeyChain(cfg)
		},
	)
}

func (a *guiApp) stopKeyChainRunner() {
	a.lifecycleMu.Lock()
	a.mu.Lock()
	kc := a.keychainRunner
	a.keychainRunner = nil
	a.mu.Unlock()
	a.lifecycleMu.Unlock()
	stopRunnerAsync(kc)
}

func (a *guiApp) resetKeyChainSwitchData(switchIdx int) {
	sw := &a.keychain.switches[switchIdx]
	for i := 0; i < runner.KeyChainSlotCount; i++ {
		sw.keyVKs[i] = 0
		a.keychain.setKeyText(switchIdx, i, 0)
		sw.slots[i].delayEdit.SetValue(0)
	}
}

func (a *guiApp) copyKeyChainSwitchData(from, to int) {
	src := &a.keychain.switches[from]
	dst := &a.keychain.switches[to]
	for i := 0; i < runner.KeyChainSlotCount; i++ {
		dst.keyVKs[i] = src.keyVKs[i]
		a.keychain.setKeyText(to, i, src.keyVKs[i])
		dst.slots[i].delayEdit.SetValue(src.slots[i].delayEdit.Value())
	}
}

func (a *guiApp) clearKeyChainSwitch(switchIdx int) {
	if switchIdx < 0 || switchIdx >= a.keychain.visibleCount {
		return
	}
	a.resetKeyChainSwitchData(switchIdx)
	a.syncKeyChainSettings()
	a.appendLog(fmt.Sprintf("KeyChain switch %d cleared", switchIdx+1))
}

func (a *guiApp) removeKeyChainSwitch(switchIdx int) {
	if a.keychain.visibleCount <= 1 {
		return
	}
	if switchIdx < 0 || switchIdx >= a.keychain.visibleCount {
		return
	}
	for i := switchIdx; i < a.keychain.visibleCount-1; i++ {
		a.copyKeyChainSwitchData(i+1, i)
	}
	last := a.keychain.visibleCount - 1
	a.resetKeyChainSwitchData(last)
	a.keychain.switches[last].group.SetVisible(false)
	a.keychain.visibleCount--
	a.updateKeyChainAddButton()
	a.updateKeyChainRemoveButtons()
	a.setKeyChainConfigEnabled(a.isViiperReady())
	a.syncKeyChainSettings()
	a.appendLog(fmt.Sprintf("KeyChain switch %d removed", switchIdx+1))
}

func (a *guiApp) bindKeyChainKey(switchIdx, slotIdx int) {
	a.bindKeyFlow(
		func() bool {
			if !a.isViiperReady() || a.bindingActive ||
				switchIdx < 0 || switchIdx >= a.keychain.visibleCount ||
				slotIdx < 0 || slotIdx >= runner.KeyChainSlotCount {
				return false
			}
			a.bindingActive = true
			a.keychain.switches[switchIdx].slots[slotIdx].keyEdit.SetEnabled(false)
			return true
		},
		fmt.Sprintf("Press a key for switch %d slot %d (%s timeout)...", switchIdx+1, slotIdx+1, runner.KeyBindTimeout),
		func() { a.bindingActive = false },
		func() { a.setKeyChainConfigEnabled(a.isViiperReady()) },
		func(vk int32) {
			a.unsetKeyBinding(vk)
			a.keychain.switches[switchIdx].keyVKs[slotIdx] = vk
			a.keychain.setKeyText(switchIdx, slotIdx, vk)
			a.appendLog(fmt.Sprintf("Switch %d key %d: %s", switchIdx+1, slotIdx+1, runner.KeyName(vk)))
			a.syncKeyChainSettings()
		},
	)
}
