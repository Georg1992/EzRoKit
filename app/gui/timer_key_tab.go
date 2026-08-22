//go:build windows

package main

import (
	"fmt"
	"strconv"

	"ezrokit/runner"
	"github.com/lxn/walk"
)

type timerSlotWidgets struct {
	row          *walk.Composite
	keyLabel     *walk.Label
	bindBtn      *walk.PushButton
	clearBtn     *walk.PushButton
	intervalEdit *walk.LineEdit
}

// timerController owns timer-key UI state and config building.
type timerController struct {
	slots        [runner.TimerKeySlotCount]timerSlotWidgets
	keyVKs       [runner.TimerKeySlotCount]int32
	visibleCount int
	addBtn       *walk.PushButton
}

func (c *timerController) config(logFn func(string)) runner.TimerKeyConfig {
	cfg := runner.TimerKeyConfig{Log: logFn}
	for i := 0; i < c.visibleCount; i++ {
		cfg.Slots[i] = runner.TimerSlot{
			KeyVK:      c.keyVKs[i],
			IntervalMs: c.intervalMs(i),
		}
	}
	return cfg
}

func (c *timerController) wanted(logFn func(string)) runner.TimerKeyConfig {
	return c.config(logFn)
}

func (c *timerController) intervalMs(index int) int {
	if index < 0 || index >= c.visibleCount {
		return runner.DefaultTimerKeyIntervalMs
	}
	v, err := strconv.Atoi(c.slots[index].intervalEdit.Text())
	if err != nil || v <= 0 {
		return runner.DefaultTimerKeyIntervalMs
	}
	return v * 1000
}

func (a *guiApp) buildTimerKeySection(page *walk.TabPage) error {
	timerGB, err := walk.NewGroupBox(page)
	if err != nil {
		return err
	}
	if err := timerGB.SetTitle("3. Timer keys"); err != nil {
		return err
	}
	timerLayout := walk.NewVBoxLayout()
	timerLayout.SetSpacing(8)
	if err := timerGB.SetLayout(timerLayout); err != nil {
		return err
	}

	slotsContainer, err := walk.NewComposite(timerGB)
	if err != nil {
		return err
	}
	slotsLayout := walk.NewVBoxLayout()
	slotsLayout.SetSpacing(6)
	if err := slotsContainer.SetLayout(slotsLayout); err != nil {
		return err
	}

	a.timer.visibleCount = 1
	for i := 0; i < runner.TimerKeySlotCount; i++ {
		if err := a.buildTimerSlotRow(slotsContainer, i); err != nil {
			return err
		}
		if i > 0 {
			a.timer.slots[i].row.SetVisible(false)
		}
	}

	addRow, err := walk.NewComposite(timerGB)
	if err != nil {
		return err
	}
	addLayout := walk.NewHBoxLayout()
	addLayout.SetSpacing(10)
	if err := addRow.SetLayout(addLayout); err != nil {
		return err
	}

	a.timer.addBtn, err = walk.NewPushButton(addRow)
	if err != nil {
		return err
	}
	if err := a.timer.addBtn.SetText("+ Add timer"); err != nil {
		return err
	}
	a.timer.addBtn.Clicked().Attach(a.onAddTimer)

	if _, err := newHint(timerGB, "Each mapped timer presses its key once every interval. Keyboard only — separate from the clicker above."); err != nil {
		return err
	}

	return nil
}

func (a *guiApp) buildTimerSlotRow(parent walk.Container, index int) error {
	row, err := walk.NewComposite(parent)
	if err != nil {
		return err
	}
	rowLayout := walk.NewHBoxLayout()
	rowLayout.SetSpacing(8)
	if err := row.SetLayout(rowLayout); err != nil {
		return err
	}

	w := &a.timer.slots[index]
	w.row = row

	if _, err := newFieldLabel(row, fmt.Sprintf("Timer %d:", index+1), 55); err != nil {
		return err
	}

	if _, err := newFieldLabel(row, "Key:", 30); err != nil {
		return err
	}

	w.keyLabel, err = walk.NewLabel(row)
	if err != nil {
		return err
	}
	if err := w.keyLabel.SetText("none"); err != nil {
		return err
	}
	// Stable width so the Interval column stays aligned.
	if err := w.keyLabel.SetMinMaxSize(walk.Size{Width: 80, Height: 0}, walk.Size{Width: 9999, Height: 0}); err != nil {
		return err
	}

	w.bindBtn, err = newFixedButton(row, "Set key...", 85)
	if err != nil {
		return err
	}
	slot := index
	w.bindBtn.Clicked().Attach(func() {
		a.bindTimerKey(slot)
	})

	w.clearBtn, err = newFixedButton(row, "Clear", 55)
	if err != nil {
		return err
	}
	w.clearBtn.Clicked().Attach(func() {
		a.clearTimerKey(slot)
	})

	if _, err := newFieldLabel(row, "Interval (s):", 80); err != nil {
		return err
	}

	w.intervalEdit, err = walk.NewLineEdit(row)
	if err != nil {
		return err
	}
	w.intervalEdit.SetMaxLength(6)
	if err := w.intervalEdit.SetMinMaxSize(walk.Size{Width: 60, Height: 0}, walk.Size{Width: 60, Height: 0}); err != nil {
		return err
	}
	if err := w.intervalEdit.SetText(strconv.Itoa(runner.DefaultTimerKeyIntervalSec)); err != nil {
		return err
	}
	w.intervalEdit.TextChanged().Attach(a.syncTimerKeySettings)

	return nil
}

func (a *guiApp) onAddTimer() {
	if a.timer.visibleCount >= runner.TimerKeySlotCount {
		return
	}
	a.timer.slots[a.timer.visibleCount].row.SetVisible(true)
	a.timer.visibleCount++
	a.updateTimerAddButton()
}

func (a *guiApp) updateTimerAddButton() {
	if a.timer.addBtn == nil {
		return
	}
	atMax := a.timer.visibleCount >= runner.TimerKeySlotCount
	a.timer.addBtn.SetVisible(!atMax)
}

func (a *guiApp) syncTimerKeySettings() {
	if a.profileApplying {
		return
	}
	cfg := a.timer.wanted(a.fileLog())
	a.mu.Lock()
	t := a.tools.timer
	a.mu.Unlock()

	if t != nil && t.Running() {
		if !cfg.AnyActive() {
			// Nil the runner immediately so isStarted() and
			// subsequent sync calls see a stopped state.
			a.lifecycleMu.Lock()
			a.mu.Lock()
			a.tools.timer = nil
			a.mu.Unlock()
			a.lifecycleMu.Unlock()
			stopRunnerAsync(t)
			return
		}
		t.UpdateSettings(cfg)
		return
	}

	if a.isStarted() {
		a.startTimerKeyRunner(cfg, a.fileLog())
	}
}

func (a *guiApp) setTimerKeyConfigEnabled(enabled bool) {
	for i := 0; i < a.timer.visibleCount; i++ {
		a.timer.slots[i].intervalEdit.SetEnabled(enabled)
		a.timer.slots[i].bindBtn.SetEnabled(enabled)
		a.timer.slots[i].clearBtn.SetEnabled(enabled)
	}
	if a.timer.addBtn != nil {
		a.timer.addBtn.SetEnabled(enabled && a.timer.visibleCount < runner.TimerKeySlotCount)
	}
}

func (a *guiApp) startTimerKeyRunner(cfg runner.TimerKeyConfig, log func(string)) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.shuttingDown.Load() {
		return
	}
	take := func() lifecycleRunner {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.tools.timer == nil {
			return nil
		}
		old := a.tools.timer
		a.tools.timer = nil
		return old
	}
	store := func(r lifecycleRunner) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.tools.timer = r.(*runner.TimerKeyRunner)
	}
	replaceRunner(
		take,
		store,
		"Timer keys",
		log,
		a.guiLog(a.appendLog),
		func() runner.InputSession {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.inputSession
		},
		func() bool { return cfg.AnyActive() },
		func(sess runner.InputSession) lifecycleRunner {
			cfg.Session = sess
			cfg.Log = log
			return runner.NewTimerKey(cfg)
		},
	)
}

func (a *guiApp) clearTimerKey(index int) {
	if index < 0 || index >= a.timer.visibleCount {
		return
	}
	a.timer.keyVKs[index] = 0
	a.timer.slots[index].keyLabel.SetText("none")
	a.logToFile(fmt.Sprintf("Timer %d key cleared", index+1))
	a.syncTimerKeySettings()
}

func (a *guiApp) bindTimerKey(index int) {
	a.bindKeyFlow(
		func() bool {
			if !a.isViiperReady() || a.bindingActive || index < 0 || index >= a.timer.visibleCount {
				return false
			}
			a.bindingActive = true
			a.timer.slots[index].bindBtn.SetEnabled(false)
			return true
		},
		fmt.Sprintf("Press a key for timer %d (%s timeout)...", index+1, runner.KeyBindTimeout),
		func() { a.bindingActive = false },
		func() { a.setTimerKeyConfigEnabled(a.isViiperReady()) },
		func(vk int32) {
			a.unsetKeyBinding(vk)
			a.timer.keyVKs[index] = vk
			a.timer.slots[index].keyLabel.SetText(runner.KeyName(vk))
			a.logToFile(fmt.Sprintf("Timer %d key: %s", index+1, runner.KeyName(vk)))
			a.syncTimerKeySettings()
		},
	)
}
