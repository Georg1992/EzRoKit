//go:build windows

package main

import (
	"fmt"
	"strconv"

	"ezrokit/runner"
	"github.com/lxn/walk"
)

// autopotController owns autopot state and config building.
type autopotController struct {
	hpEnabledCB     *walk.CheckBox
	spEnabledCB     *walk.CheckBox
	hpThresholdEdit *walk.LineEdit
	spThresholdEdit *walk.LineEdit
	hpKeyLabel      *walk.Label
	spKeyLabel      *walk.Label
	hpBindBtn       *walk.PushButton
	hpClearBtn      *walk.PushButton
	spBindBtn       *walk.PushButton
	spClearBtn      *walk.PushButton

	autopotVisualRB  *walk.RadioButton
	autopotAddressRB *walk.RadioButton
	windowCB         *walk.ComboBox
	windowRefreshBtn *walk.PushButton
	profileCB        *walk.ComboBox
	processPID       uint32
	windowList       []runner.VisibleWindow

	hpKeyVK     int32
	spKeyVK     int32
	hpThreshold int
	spThreshold int

	isRefreshingWindows    bool
	suppressWindowEvents   bool
	prevAutoPotAddressMode bool
}

func (c *autopotController) isAddressMode() bool {
	return c.autopotAddressRB != nil && c.autopotAddressRB.Checked()
}

func (c *autopotController) selectedProfile() runner.Profile {
	all := runner.AllProfiles()
	idx := c.profileCB.CurrentIndex()
	if idx >= 0 && idx < len(all) {
		return all[idx]
	}
	return runner.DefaultProfile()
}

func (c *autopotController) selectProfileByName(name string) {
	all := runner.AllProfiles()
	for i, profile := range all {
		if profile.Name == name {
			c.profileCB.SetCurrentIndex(i)
			return
		}
	}
	if len(all) > 0 {
		c.profileCB.SetCurrentIndex(0)
	}
}

func (c *autopotController) selectedWindowTitle() string {
	idx := c.windowCB.CurrentIndex()
	if idx < 0 || idx >= len(c.windowList) {
		return ""
	}
	return c.windowList[idx].Title
}

func (c *autopotController) parseThreshold(edit *walk.LineEdit) (int, bool) {
	if edit == nil {
		return 0, false
	}
	v, err := strconv.Atoi(edit.Text())
	if err != nil || v < 1 || v > 99 {
		return 0, false
	}
	return v, true
}

func (c *autopotController) config(status runner.StatusSink, logFn func(string)) runner.AutoPotConfig {
	hpName := ""
	if c.hpKeyVK != 0 {
		hpName = runner.KeyName(c.hpKeyVK)
	}
	spName := ""
	if c.spKeyVK != 0 {
		spName = runner.KeyName(c.spKeyVK)
	}
	isAddr := c.isAddressMode()
	profile := c.selectedProfile()
	cfg := runner.AutoPotConfig{
		Core: runner.CoreConfig{
			HPThreshold: c.hpThreshold,
			SPThreshold: c.spThreshold,
			HPKeyVK:     c.hpKeyVK,
			SPKeyVK:     c.spKeyVK,
			HPKeyName:   hpName,
			SPKeyName:   spName,
			HPEnabled:   c.hpEnabledCB.Checked(),
			SPEnabled:   c.spEnabledCB.Checked(),
			Log:         logFn,
			Status:      status,
		},
	}
	if isAddr {
		cfg.Address = &runner.AddressConfig{
			ProcessPID:   c.processPID,
			ProcessTitle: c.selectedWindowTitle(),
			Profile:      profile,
		}
	}
	return cfg
}

func (c *autopotController) wanted(status runner.StatusSink, logFn func(string)) runner.AutoPotConfig {
	return c.config(status, logFn)
}

func (a *guiApp) buildAutoPotTab(page *walk.TabPage) error {
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{HNear: 4, VNear: 4, HFar: 4, VFar: 4})
	layout.SetSpacing(10)
	if err := page.SetLayout(layout); err != nil {
		return err
	}

	if err := a.buildAutoPotModeSection(page); err != nil {
		return err
	}
	if err := a.buildHPPotionSection(page); err != nil {
		return err
	}
	if err := a.buildSPPotionSection(page); err != nil {
		return err
	}

	if _, err := newHint(page, "When HP or SP drops below the threshold, one potion is pressed at a time; the bar is polled until it recovers before using another."); err != nil {
		return err
	}

	// Initial state: address controls disabled (default is Visual mode).
	a.setAutoPotAddressModeEnabled(false)
	return nil
}

// buildAutoPotModeSection creates the Detection mode group box with
// Visual/Address radio buttons, address controls, and wires their events.
func (a *guiApp) buildAutoPotModeSection(page *walk.TabPage) error {
	modeGB, err := walk.NewGroupBox(page)
	if err != nil {
		return err
	}
	if err := modeGB.SetTitle("Detection mode"); err != nil {
		return err
	}
	modeLayout := walk.NewVBoxLayout()
	modeLayout.SetSpacing(4)
	if err := modeGB.SetLayout(modeLayout); err != nil {
		return err
	}

	modeRow, err := walk.NewComposite(modeGB)
	if err != nil {
		return err
	}
	modeHBox := walk.NewHBoxLayout()
	modeHBox.SetSpacing(16)
	if err := modeRow.SetLayout(modeHBox); err != nil {
		return err
	}

	a.autopot.autopotVisualRB, err = walk.NewRadioButton(modeRow)
	if err != nil {
		return err
	}
	if err := a.autopot.autopotVisualRB.SetText("Visual (screen capture)"); err != nil {
		return err
	}
	a.autopot.autopotVisualRB.SetChecked(true)

	a.autopot.autopotAddressRB, err = walk.NewRadioButton(modeRow)
	if err != nil {
		return err
	}
	if err := a.autopot.autopotAddressRB.SetText("Address reading"); err != nil {
		return err
	}

	if err := a.buildAddressControls(modeGB); err != nil {
		return err
	}

	// Wire mode toggle.
	a.autopot.autopotVisualRB.CheckedChanged().Attach(func() {
		isAddress := a.autopot.autopotAddressRB.Checked()
		a.setAutoPotAddressModeEnabled(isAddress)
		if isAddress && !a.profileApplying {
			a.appendLog("AutoPot mode: Address reading — select a game window and bind potion keys")
		}
	})
	a.autopot.autopotAddressRB.CheckedChanged().Attach(func() {
		isAddress := a.autopot.autopotAddressRB.Checked()
		a.setAutoPotAddressModeEnabled(isAddress)
	})
	return nil
}

// buildAddressControls creates the window selector, profile combo, refresh
// button, and wires their events (selection change, refresh click).
func (a *guiApp) buildAddressControls(modeGB *walk.GroupBox) error {
	addrRow, err := walk.NewComposite(modeGB)
	if err != nil {
		return err
	}
	addrHBox := walk.NewHBoxLayout()
	addrHBox.SetSpacing(8)
	if err := addrRow.SetLayout(addrHBox); err != nil {
		return err
	}

	if _, err := newFieldLabel(addrRow, "Game window:", 80); err != nil {
		return err
	}

	a.autopot.windowCB, err = walk.NewComboBox(addrRow)
	if err != nil {
		return err
	}
	if err := a.autopot.windowCB.SetMinMaxSize(walk.Size{Width: 260, Height: 0}, walk.Size{Width: 260, Height: 0}); err != nil {
		return err
	}

	a.autopot.windowRefreshBtn, err = walk.NewPushButton(addrRow)
	if err != nil {
		return err
	}
	if err := a.autopot.windowRefreshBtn.SetText("Refresh"); err != nil {
		return err
	}

	if _, err := newFieldLabel(addrRow, "Profile:", 50); err != nil {
		return err
	}

	a.autopot.profileCB, err = walk.NewComboBox(addrRow)
	if err != nil {
		return err
	}
	if err := a.autopot.profileCB.SetMinMaxSize(walk.Size{Width: 120, Height: 0}, walk.Size{Width: 120, Height: 0}); err != nil {
		return err
	}
	allProfiles := runner.AllProfiles()
	profileNames := make([]string, 0, len(allProfiles))
	for _, p := range allProfiles {
		profileNames = append(profileNames, p.Name)
	}
	if err := a.autopot.profileCB.SetModel(profileNames); err != nil {
		return err
	}
	if len(profileNames) > 0 {
		a.autopot.profileCB.SetCurrentIndex(0)
	}

	// Wire window selection.
	a.autopot.windowCB.CurrentIndexChanged().Attach(func() {
		if a.autopot.isRefreshingWindows || a.profileApplying || a.autopot.suppressWindowEvents {
			return
		}
		if a.autopot.windowCB.CurrentIndex() < 0 {
			a.autopot.processPID = 0
			a.clearAutoPotKeys()
			a.appendLog("Game window cleared — potion keys reset")
		} else if err := a.openSelectedProcessHandle(); err != nil {
			a.appendLog(fmt.Sprintf("Failed to open process: %v", err))
		} else {
			a.syncAutoPotSettings()
		}
	})

	// Wire refresh button.
	a.autopot.windowRefreshBtn.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button != walk.LeftButton {
			return
		}
		a.autopot.isRefreshingWindows = true
		windows, err := populateWindowComboBox(a.autopot.windowCB)
		if err != nil {
			a.appendLog(fmt.Sprintf("Window list refresh failed: %v", err))
		} else if len(windows) == 0 {
			a.appendLog("Window list refresh found no visible windows")
		}
		a.autopot.windowList = windows
		a.autopot.isRefreshingWindows = false
	})
	return nil
}

// potionSectionConfig carries the varying details for buildPotionSection.
type potionSectionConfig struct {
	title            string
	defaultThreshold int
	enabledCB        **walk.CheckBox
	thresholdEdit    **walk.LineEdit
	keyLabel         **walk.Label
	bindBtn          **walk.PushButton
	clearBtn         **walk.PushButton
	threshold        *int
	onBind           func()
	onClear          func()
	commitThresh     func()
}

// buildPotionSection creates a potion (HP/SP) group box with enable checkbox,
// threshold input, key label, bind button, and clear button. Two thin wrappers
// (buildHPPotionSection / buildSPPotionSection) call this with the right config.
func (a *guiApp) buildPotionSection(page *walk.TabPage, cfg potionSectionConfig) error {
	gb, err := walk.NewGroupBox(page)
	if err != nil {
		return err
	}
	if err := gb.SetTitle(cfg.title); err != nil {
		return err
	}
	layout := walk.NewHBoxLayout()
	layout.SetSpacing(8)
	if err := gb.SetLayout(layout); err != nil {
		return err
	}

	*cfg.enabledCB, err = walk.NewCheckBox(gb)
	if err != nil {
		return err
	}
	if err := (*cfg.enabledCB).SetText("Enabled"); err != nil {
		return err
	}
	(*cfg.enabledCB).SetChecked(true)
	(*cfg.enabledCB).CheckedChanged().Attach(a.syncAutoPotSettings)

	if _, err := newFieldLabel(gb, "Trigger below %:", 100); err != nil {
		return err
	}

	*cfg.thresholdEdit, err = walk.NewLineEdit(gb)
	if err != nil {
		return err
	}
	(*cfg.thresholdEdit).SetMaxLength(2)
	if err := (*cfg.thresholdEdit).SetMinMaxSize(walk.Size{Width: 40, Height: 0}, walk.Size{Width: 40, Height: 0}); err != nil {
		return err
	}
	thresholdStr := strconv.Itoa(cfg.defaultThreshold)
	if err := (*cfg.thresholdEdit).SetText(thresholdStr); err != nil {
		return err
	}
	*cfg.threshold = cfg.defaultThreshold
	(*cfg.thresholdEdit).EditingFinished().Attach(func() {
		cfg.commitThresh()
		a.syncAutoPotSettings()
	})

	if _, err := newFieldLabel(gb, "Key:", 30); err != nil {
		return err
	}

	*cfg.keyLabel, err = walk.NewLabel(gb)
	if err != nil {
		return err
	}
	if err := (*cfg.keyLabel).SetText("none"); err != nil {
		return err
	}
	// Stable width so the buttons stay aligned.
	if err := (*cfg.keyLabel).SetMinMaxSize(walk.Size{Width: 70, Height: 0}, walk.Size{Width: 9999, Height: 0}); err != nil {
		return err
	}

	*cfg.bindBtn, err = newFixedButton(gb, "Set key...", 85)
	if err != nil {
		return err
	}
	(*cfg.bindBtn).Clicked().Attach(cfg.onBind)

	*cfg.clearBtn, err = newFixedButton(gb, "Clear", 55)
	if err != nil {
		return err
	}
	(*cfg.clearBtn).Clicked().Attach(cfg.onClear)
	return nil
}

// buildHPPotionSection builds the HP potion group box using the shared builder.
func (a *guiApp) buildHPPotionSection(page *walk.TabPage) error {
	return a.buildPotionSection(page, potionSectionConfig{
		title:            "HP potion",
		defaultThreshold: 50,
		enabledCB:        &a.autopot.hpEnabledCB,
		thresholdEdit:    &a.autopot.hpThresholdEdit,
		keyLabel:         &a.autopot.hpKeyLabel,
		bindBtn:          &a.autopot.hpBindBtn,
		clearBtn:         &a.autopot.hpClearBtn,
		threshold:        &a.autopot.hpThreshold,
		onBind:           a.onBindHPKey,
		onClear:          a.onClearHPKey,
		commitThresh:     a.commitHPThresholdEdit,
	})
}

// buildSPPotionSection builds the SP potion group box using the shared builder.
func (a *guiApp) buildSPPotionSection(page *walk.TabPage) error {
	return a.buildPotionSection(page, potionSectionConfig{
		title:            "SP potion",
		defaultThreshold: 30,
		enabledCB:        &a.autopot.spEnabledCB,
		thresholdEdit:    &a.autopot.spThresholdEdit,
		keyLabel:         &a.autopot.spKeyLabel,
		bindBtn:          &a.autopot.spBindBtn,
		clearBtn:         &a.autopot.spClearBtn,
		threshold:        &a.autopot.spThreshold,
		onBind:           a.onBindSPKey,
		onClear:          a.onClearSPKey,
		commitThresh:     a.commitSPThresholdEdit,
	})
}

// setAutoPotAddressModeEnabled enables or disables the address-mode
// UI elements (window selector, profile). When switching away from
// address mode (back to Visual), clears the PID so the old handle
// isn't reused. Potion keys are preserved — they work for both modes.
func (a *guiApp) setAutoPotAddressModeEnabled(enabled bool) {
	a.autopot.windowCB.SetEnabled(enabled)
	a.autopot.windowRefreshBtn.SetEnabled(enabled)
	a.autopot.profileCB.SetEnabled(enabled)
	if !enabled {
		a.autopot.processPID = 0
	}
	a.syncAutoPotSettings()
}

// clearAutoPotKeys resets both HP and SP key bindings and logs it.
func (a *guiApp) clearAutoPotKeys() {
	a.autopot.hpKeyVK = 0
	a.autopot.spKeyVK = 0
	a.autopot.hpKeyLabel.SetText("none")
	a.autopot.spKeyLabel.SetText("none")
	a.syncAutoPotSettings()
}

// openSelectedProcessHandle stores the selected window's PID for
// use by the address reader (which opens/closes handles per read).
func (a *guiApp) openSelectedProcessHandle() error {
	idx := a.autopot.windowCB.CurrentIndex()
	if idx < 0 || idx >= len(a.autopot.windowList) {
		return nil // nothing selected
	}

	win := a.autopot.windowList[idx]
	// Only log and update PID if it actually changed (guards against
	// spurious CurrentIndexChanged firings from Walk's combo box).
	if a.autopot.processPID != win.PID {
		a.autopot.processPID = win.PID
		a.logToFile(fmt.Sprintf("Selected %q (PID %d)", win.Title, win.PID))
	}
	return nil
}

func (a *guiApp) commitHPThresholdEdit() {
	v, ok := a.autopot.parseThreshold(a.autopot.hpThresholdEdit)
	if !ok {
		a.autopot.hpThresholdEdit.SetText(strconv.Itoa(a.autopot.hpThreshold))
		return
	}
	if v == a.autopot.hpThreshold {
		return
	}
	a.autopot.hpThreshold = v
	a.logToFile(fmt.Sprintf("AutoPot HP threshold: %d%%", v))
}

func (a *guiApp) commitSPThresholdEdit() {
	v, ok := a.autopot.parseThreshold(a.autopot.spThresholdEdit)
	if !ok {
		a.autopot.spThresholdEdit.SetText(strconv.Itoa(a.autopot.spThreshold))
		return
	}
	if v == a.autopot.spThreshold {
		return
	}
	a.autopot.spThreshold = v
	a.logToFile(fmt.Sprintf("AutoPot SP threshold: %d%%", v))
}

func (a *guiApp) syncAutoPotSettings() {
	if a.profileApplying {
		return
	}
	cfg := a.autopot.wanted(a.autopotStatus(), a.fileLog())
	a.mu.Lock()
	cfg.Core.Session = a.inputSession
	cfg.Core.Log = a.fileLog()
	r := a.tools.autopot
	a.mu.Unlock()

	if cfg.Core.Session == nil || cfg.Core.Log == nil {
		return
	}

	if r != nil && r.Running() {
		// If neither HP nor SP keys are bound, stop the runner
		// instead of letting it spin doing nothing.
		if !cfg.HasBoundPotion() {
			a.lifecycleMu.Lock()
			a.mu.Lock()
			a.tools.autopot = nil
			a.mu.Unlock()
			a.lifecycleMu.Unlock()
			stopRunnerAsync(r)
			if a.overlay != nil {
				a.overlay.SetMode("AutoPot off")
			}
			return
		}

		// If AddressMode changed (Visual→Address or Address→Visual),
		// we must stop the runner and start a new one. The reader
		// (addressReader / statusUIReader / pixelBarReader) is created
		// once inside run() — UpdateSettings only changes the config,
		// it doesn't recreate the reader.
		if cfg.IsAddressMode() != a.autopot.prevAutoPotAddressMode {
			a.autopot.prevAutoPotAddressMode = cfg.IsAddressMode()
			a.lifecycleMu.Lock()
			a.mu.Lock()
			a.tools.autopot = nil
			a.mu.Unlock()
			a.lifecycleMu.Unlock()
			// Stop the old reader before starting the replacement so
			// both runners do not overlap on the same session.
			stopRunnerAsync(r)
			a.startAutoPotRunner(cfg, a.fileLog())
			return
		}

		r.UpdateSettings(cfg)
		return
	}

	if !a.isStarted() {
		return
	}

	a.autopot.prevAutoPotAddressMode = cfg.IsAddressMode()
	a.startAutoPotRunner(cfg, a.fileLog())
}

func (a *guiApp) setAutoPotConfigEnabled(enabled bool) {
	a.autopot.hpEnabledCB.SetEnabled(enabled)
	a.autopot.spEnabledCB.SetEnabled(enabled)
	a.autopot.hpThresholdEdit.SetEnabled(enabled)
	a.autopot.spThresholdEdit.SetEnabled(enabled)
	a.autopot.hpBindBtn.SetEnabled(enabled)
	a.autopot.hpClearBtn.SetEnabled(enabled)
	a.autopot.spBindBtn.SetEnabled(enabled)
	a.autopot.spClearBtn.SetEnabled(enabled)
	a.autopot.autopotVisualRB.SetEnabled(enabled)
	a.autopot.autopotAddressRB.SetEnabled(enabled)
	// Address-mode sub-controls follow the mode+enabled state.
	isAddress := a.autopot.isAddressMode()
	a.autopot.windowCB.SetEnabled(enabled && isAddress)
	a.autopot.windowRefreshBtn.SetEnabled(enabled && isAddress)
	a.autopot.profileCB.SetEnabled(enabled && isAddress)
}

func (a *guiApp) onClearHPKey() {
	a.autopot.hpKeyVK = 0
	a.autopot.hpKeyLabel.SetText("none")
	a.logToFile("HP potion key cleared")
	a.syncAutoPotSettings()
}

func (a *guiApp) onClearSPKey() {
	a.autopot.spKeyVK = 0
	a.autopot.spKeyLabel.SetText("none")
	a.logToFile("SP potion key cleared")
	a.syncAutoPotSettings()
}

type overlayStatusSink struct {
	app *guiApp
}

func (a *guiApp) autopotStatus() runner.StatusSink {
	return overlayStatusSink{app: a}
}

func (s overlayStatusSink) SetMode(mode string) {
	s.app.mainWindow.Synchronize(func() {
		if s.app.overlay != nil {
			s.app.overlay.SetMode(mode)
		}
	})
}

func (s overlayStatusSink) SetValues(v runner.OverlayValues) {
	s.app.mainWindow.Synchronize(func() {
		if s.app.overlay == nil {
			return
		}
		s.app.overlay.SetValues(v.HP, v.HPMax, v.SP, v.SPMax)
		if v.PanelW > 0 && v.PanelH > 0 {
			s.app.overlay.SetPanelRect(v.PanelX, v.PanelY, v.PanelW, v.PanelH)
		}
	})
}

func (s overlayStatusSink) ClearValues() {
	s.app.mainWindow.Synchronize(func() {
		if s.app.overlay != nil {
			s.app.overlay.ClearValues()
		}
	})
}

func (a *guiApp) finishThresholdInput() {
	a.commitHPThresholdEdit()
	a.commitSPThresholdEdit()
	a.syncAutoPotSettings()
	a.blurThresholdEdits()
}

func (a *guiApp) wireThresholdBlurOnClick(container walk.Container) {
	if container == nil {
		return
	}
	children := container.Children()
	if children == nil {
		return
	}
	for i := 0; i < children.Len(); i++ {
		child := children.At(i)
		if child == a.autopot.hpThresholdEdit || child == a.autopot.spThresholdEdit || child == a.logList {
			continue
		}
		if win, ok := child.(walk.Window); ok {
			win.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
				a.finishThresholdInput()
			})
		}
		if c, ok := child.(walk.Container); ok {
			a.wireThresholdBlurOnClick(c)
		}
	}
}

func (a *guiApp) blurThresholdEdits() {
	if a.mainWindow != nil {
		_ = a.mainWindow.SetFocus()
	}
}

func (a *guiApp) onBindHPKey() {
	a.bindAutoPotKey(true)
}

func (a *guiApp) onBindSPKey() {
	a.bindAutoPotKey(false)
}

func (a *guiApp) bindAutoPotKey(hp bool) {
	a.bindKeyFlow(
		func() bool {
			if !a.isViiperReady() || a.bindingActive {
				return false
			}
			if a.autopot.isAddressMode() && a.autopot.windowCB.CurrentIndex() < 0 {
				a.appendLog("Cannot bind — select a game window first for Address Reading mode")
				return false
			}
			a.bindingActive = true
			if hp {
				a.autopot.hpBindBtn.SetEnabled(false)
			} else {
				a.autopot.spBindBtn.SetEnabled(false)
			}
			return true
		},
		fmt.Sprintf("Press a potion hotkey to assign (%s timeout)...", runner.KeyBindTimeout),
		func() { a.bindingActive = false },
		func() { a.setAutoPotConfigEnabled(a.isViiperReady()) },
		func(vk int32) {
			a.unsetKeyBinding(vk)
			if hp {
				a.autopot.hpKeyVK = vk
				a.autopot.hpKeyLabel.SetText(runner.KeyName(vk))
				a.logToFile(fmt.Sprintf("HP potion key: %s", runner.KeyName(vk)))
			} else {
				a.autopot.spKeyVK = vk
				a.autopot.spKeyLabel.SetText(runner.KeyName(vk))
				a.logToFile(fmt.Sprintf("SP potion key: %s", runner.KeyName(vk)))
			}
			a.syncAutoPotSettings()
		},
	)
}
