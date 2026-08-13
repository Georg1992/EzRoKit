//go:build windows

package main

import (
	"fmt"
	"strconv"

	"belarus-champ-tools/runner"
	"github.com/lxn/walk"
)

func clickerTitle(index int) string {
	return fmt.Sprintf("Bind %d", index+1)
}

type clickerSlotWidgets struct {
	row       *walk.Composite
	mouseCB   *walk.CheckBox
	keyLabel  *walk.Label
	bindBtn   *walk.PushButton
	clearBtn  *walk.PushButton
	removeBtn *walk.PushButton
	delayEdit *walk.LineEdit
}

// clickerController owns clicker UI state and config building.
type clickerController struct {
	slots           [runner.ClickerSlotCount]clickerSlotWidgets
	triggerVKs      [runner.ClickerSlotCount][runner.ClickerKeysPerBind]int32
	lastLoggedDelay [runner.ClickerSlotCount]int
	visibleCount    int
	addBtn          *walk.PushButton
}

func (c *clickerController) config(logFn func(string)) runner.Config {
	cfg := runner.Config{Log: logFn}
	for i := 0; i < c.visibleCount; i++ {
		cfg.Slots[i] = runner.ClickerSlot{
			TriggerVKs: c.triggerVKs[i],
			DelayMs:    c.delayMs(i),
			MouseClick: c.slots[i].mouseCB.Checked(),
		}
	}
	return cfg
}

func (c *clickerController) delayMs(index int) int {
	if index < 0 || index >= c.visibleCount {
		return runner.DefaultDelayMs
	}
	v, err := strconv.Atoi(c.slots[index].delayEdit.Text())
	if err != nil || v <= 0 {
		return runner.DefaultDelayMs
	}
	return v
}

func (c *clickerController) keyCount(index int) int {
	n := 0
	for _, vk := range c.triggerVKs[index] {
		if vk != 0 {
			n++
		}
	}
	return n
}

func (c *clickerController) appendKey(index int, vk int32) bool {
	for i, existing := range c.triggerVKs[index] {
		if existing == vk {
			return true // already present
		}
		if existing == 0 {
			c.triggerVKs[index][i] = vk
			return true
		}
	}
	return false // bind full
}

func (c *clickerController) removeKey(index int, vk int32) bool {
	for i, existing := range c.triggerVKs[index] {
		if existing != vk {
			continue
		}
		// Compact remaining keys forward.
		for j := i; j < runner.ClickerKeysPerBind-1; j++ {
			c.triggerVKs[index][j] = c.triggerVKs[index][j+1]
		}
		c.triggerVKs[index][runner.ClickerKeysPerBind-1] = 0
		return true
	}
	return false
}

func (a *guiApp) buildClickerTab(page *walk.TabPage) error {
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{HNear: 4, VNear: 4, HFar: 4, VFar: 4})
	layout.SetSpacing(10)
	if err := page.SetLayout(layout); err != nil {
		return err
	}

	configGB, err := walk.NewGroupBox(page)
	if err != nil {
		return err
	}
	if err := configGB.SetTitle("2. Configure key binds"); err != nil {
		return err
	}
	configLayout := walk.NewVBoxLayout()
	configLayout.SetSpacing(8)
	if err := configGB.SetLayout(configLayout); err != nil {
		return err
	}

	slotsContainer, err := walk.NewComposite(configGB)
	if err != nil {
		return err
	}
	slotsLayout := walk.NewVBoxLayout()
	slotsLayout.SetSpacing(6)
	if err := slotsContainer.SetLayout(slotsLayout); err != nil {
		return err
	}

	a.clicker.visibleCount = 1
	for i := 0; i < runner.ClickerSlotCount; i++ {
		if err := a.buildClickerSlot(slotsContainer, i); err != nil {
			return err
		}
		if i > 0 {
			a.clicker.slots[i].row.SetVisible(false)
		}
	}

	addRow, err := walk.NewComposite(configGB)
	if err != nil {
		return err
	}
	addLayout := walk.NewHBoxLayout()
	addLayout.SetSpacing(10)
	if err := addRow.SetLayout(addLayout); err != nil {
		return err
	}

	a.clicker.addBtn, err = walk.NewPushButton(addRow)
	if err != nil {
		return err
	}
	if err := a.clicker.addBtn.SetText("+ Add bind"); err != nil {
		return err
	}
	a.clicker.addBtn.Clicked().Attach(a.onAddClicker)
	a.updateClickerAddButton()
	a.updateClickerRemoveButtons()

	configHint, err := walk.NewLabel(configGB)
	if err != nil {
		return err
	}
	if err := configHint.SetText("Up to 2 binds. Enable Mouse for key → mouse → sleep, or disable it for key-only spamming. First held key has priority; End or F12 toggles start/stop."); err != nil {
		return err
	}

	return a.buildTimerKeySection(page)
}

func (a *guiApp) buildClickerSlot(parent walk.Container, index int) error {
	row, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	rowLayout := walk.NewHBoxLayout()
	rowLayout.SetSpacing(10)
	if err := row.SetLayout(rowLayout); err != nil {
		return err
	}

	w := &a.clicker.slots[index]
	w.row = row

	w.mouseCB, err = walk.NewCheckBox(row)
	if err != nil {
		return err
	}
	if err := w.mouseCB.SetText("Mouse"); err != nil {
		return err
	}
	w.mouseCB.SetChecked(index == 0)
	w.mouseCB.CheckedChanged().Attach(a.syncRunnerSettings)

	title, err := walk.NewLabel(row)
	if err != nil {
		return err
	}
	if err := title.SetText(clickerTitle(index) + ":"); err != nil {
		return err
	}

	keyText, err := walk.NewLabel(row)
	if err != nil {
		return err
	}
	if err := keyText.SetText("Keys:"); err != nil {
		return err
	}

	w.keyLabel, err = walk.NewLabel(row)
	if err != nil {
		return err
	}
	if err := w.keyLabel.SetText(runner.KeysText(a.clicker.triggerVKs[index])); err != nil {
		return err
	}

	w.bindBtn, err = walk.NewPushButton(row)
	if err != nil {
		return err
	}
	if err := w.bindBtn.SetText("Add key..."); err != nil {
		return err
	}
	slot := index
	w.bindBtn.Clicked().Attach(func() {
		a.bindClickerKey(slot)
	})

	w.clearBtn, err = walk.NewPushButton(row)
	if err != nil {
		return err
	}
	if err := w.clearBtn.SetText("Clear"); err != nil {
		return err
	}
	w.clearBtn.Clicked().Attach(func() {
		a.clearClickerKey(slot)
	})

	w.removeBtn, err = walk.NewPushButton(row)
	if err != nil {
		return err
	}
	if err := w.removeBtn.SetText("Remove"); err != nil {
		return err
	}
	w.removeBtn.Clicked().Attach(func() {
		a.removeClicker(slot)
	})

	delayLabel, err := walk.NewLabel(row)
	if err != nil {
		return err
	}
	if err := delayLabel.SetText("Delay (ms):"); err != nil {
		return err
	}

	w.delayEdit, err = walk.NewLineEdit(row)
	if err != nil {
		return err
	}
	w.delayEdit.SetMaxLength(6)
	if err := w.delayEdit.SetMinMaxSize(walk.Size{Width: 80, Height: 0}, walk.Size{Width: 80, Height: 0}); err != nil {
		return err
	}
	if err := w.delayEdit.SetText(strconv.Itoa(runner.DefaultDelayMs)); err != nil {
		return err
	}
	a.clicker.lastLoggedDelay[index] = runner.DefaultDelayMs
	w.delayEdit.TextChanged().Attach(a.syncRunnerSettings)
	w.delayEdit.EditingFinished().Attach(func() {
		a.logClickerDelayIfChanged(slot)
	})

	return nil
}

func (a *guiApp) onAddClicker() {
	if a.clicker.visibleCount >= runner.ClickerSlotCount {
		return
	}
	a.clicker.slots[a.clicker.visibleCount].row.SetVisible(true)
	a.clicker.visibleCount++
	a.updateClickerAddButton()
	a.updateClickerRemoveButtons()
	a.setClickerConfigEnabled(a.isViiperReady())
}

func (a *guiApp) updateClickerAddButton() {
	if a.clicker.addBtn == nil {
		return
	}
	atMax := a.clicker.visibleCount >= runner.ClickerSlotCount
	a.clicker.addBtn.SetVisible(!atMax)
}

func (a *guiApp) updateClickerRemoveButtons() {
	canRemove := a.clicker.visibleCount > 1
	for i := 0; i < runner.ClickerSlotCount; i++ {
		btn := a.clicker.slots[i].removeBtn
		if btn == nil {
			continue
		}
		btn.SetVisible(i < a.clicker.visibleCount && canRemove)
	}
}

func (a *guiApp) logClickerDelayIfChanged(index int) {
	if index < 0 || index >= a.clicker.visibleCount {
		return
	}
	delay := a.clicker.delayMs(index)
	if delay == a.clicker.lastLoggedDelay[index] {
		return
	}
	a.clicker.lastLoggedDelay[index] = delay
	a.appendLog(fmt.Sprintf("%s delay: %d ms", clickerTitle(index), delay))
}

func (a *guiApp) setClickerConfigEnabled(enabled bool) {
	for i := 0; i < a.clicker.visibleCount; i++ {
		w := &a.clicker.slots[i]
		w.mouseCB.SetEnabled(enabled)
		w.delayEdit.SetEnabled(enabled)
		full := a.clicker.keyCount(i) >= runner.ClickerKeysPerBind
		w.bindBtn.SetEnabled(enabled && !full)
		w.clearBtn.SetEnabled(enabled)
		if w.removeBtn != nil {
			w.removeBtn.SetEnabled(enabled && a.clicker.visibleCount > 1)
		}
	}
	if a.clicker.addBtn != nil {
		a.clicker.addBtn.SetEnabled(enabled && a.clicker.visibleCount < runner.ClickerSlotCount)
	}
}

func (a *guiApp) updateClickerKeyLabel(index int) {
	a.clicker.slots[index].keyLabel.SetText(runner.KeysText(a.clicker.triggerVKs[index]))
}

func (a *guiApp) clearClickerKey(index int) {
	if index < 0 || index >= a.clicker.visibleCount {
		return
	}
	a.clicker.triggerVKs[index] = [runner.ClickerKeysPerBind]int32{}
	a.updateClickerKeyLabel(index)
	a.appendLog(fmt.Sprintf("%s keys cleared", clickerTitle(index)))
	a.setClickerConfigEnabled(a.isViiperReady())
	a.syncRunnerSettings()
}

func (a *guiApp) copyClickerSlotData(from, to int) {
	src := &a.clicker.slots[from]
	dst := &a.clicker.slots[to]
	a.clicker.triggerVKs[to] = a.clicker.triggerVKs[from]
	a.updateClickerKeyLabel(to)
	dst.mouseCB.SetChecked(src.mouseCB.Checked())
	dst.delayEdit.SetText(src.delayEdit.Text())
	a.clicker.lastLoggedDelay[to] = a.clicker.lastLoggedDelay[from]
}

func (a *guiApp) resetClickerSlotData(index int) {
	a.clicker.triggerVKs[index] = [runner.ClickerKeysPerBind]int32{}
	a.updateClickerKeyLabel(index)
	a.clicker.slots[index].mouseCB.SetChecked(false)
	a.clicker.slots[index].delayEdit.SetText(strconv.Itoa(runner.DefaultDelayMs))
	a.clicker.lastLoggedDelay[index] = runner.DefaultDelayMs
}

func (a *guiApp) removeClicker(index int) {
	if a.clicker.visibleCount <= 1 {
		return
	}
	if index < 0 || index >= a.clicker.visibleCount {
		return
	}
	for i := index; i < a.clicker.visibleCount-1; i++ {
		a.copyClickerSlotData(i+1, i)
	}
	last := a.clicker.visibleCount - 1
	a.resetClickerSlotData(last)
	a.clicker.slots[last].row.SetVisible(false)
	a.clicker.visibleCount--
	a.updateClickerAddButton()
	a.updateClickerRemoveButtons()
	a.setClickerConfigEnabled(a.isViiperReady())
	a.syncRunnerSettings()
	a.appendLog(fmt.Sprintf("%s removed", clickerTitle(index)))
}

func (a *guiApp) bindClickerKey(index int) {
	a.bindKeyFlow(
		func() bool {
			if !a.isViiperReady() || a.bindingActive || index < 0 || index >= a.clicker.visibleCount {
				return false
			}
			if a.clicker.keyCount(index) >= runner.ClickerKeysPerBind {
				return false
			}
			a.bindingActive = true
			a.clicker.slots[index].bindBtn.SetEnabled(false)
			return true
		},
		fmt.Sprintf("Press a key to add for %s (%s timeout)...", clickerTitle(index), runner.KeyBindTimeout),
		func() { a.bindingActive = false },
		func() { a.setClickerConfigEnabled(a.isViiperReady()) },
		func(vk int32) {
			a.unsetKeyBinding(vk)
			if !a.clicker.appendKey(index, vk) {
				a.appendLog(fmt.Sprintf("%s is full (max %d keys)", clickerTitle(index), runner.ClickerKeysPerBind))
				return
			}
			a.updateClickerKeyLabel(index)
			a.appendLog(fmt.Sprintf("%s added key %s", clickerTitle(index), runner.KeyName(vk)))
			a.setClickerConfigEnabled(a.isViiperReady())
			a.syncRunnerSettings()
		},
	)
}
