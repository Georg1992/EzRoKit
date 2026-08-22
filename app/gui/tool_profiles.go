//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ezrokit/runner"
	"github.com/lxn/walk"
)

const newToolProfileName = "New Profile"

type toolProfileController struct {
	combo       *walk.ComboBox
	saveBtn     *walk.PushButton
	removeBtn   *walk.PushButton
	template    toolProfile
	saved       []toolProfile
	path        string
	activeIndex int
	suppress    bool
}

// toolProfile is the complete user-facing tool configuration. It deliberately
// stores virtual-key codes rather than display names so a profile can restore
// bindings exactly, even if the key-name formatting changes later.
type toolProfile struct {
	Name     string          `json:"name"`
	Clicker  clickerProfile  `json:"clicker"`
	Timer    timerProfile    `json:"timer"`
	AutoPot  autoPotProfile  `json:"auto_pot"`
	KeyChain keyChainProfile `json:"key_chain"`
}

type clickerProfile struct {
	VisibleCount int                                         `json:"visible_count"`
	Slots        [runner.ClickerSlotCount]runner.ClickerSlot `json:"slots"`
}

type timerProfile struct {
	VisibleCount int                                        `json:"visible_count"`
	Slots        [runner.TimerKeySlotCount]timerProfileSlot `json:"slots"`
}

type timerProfileSlot struct {
	KeyVK       int32 `json:"key_vk"`
	IntervalSec int   `json:"interval_sec"`
}

type autoPotProfile struct {
	HPThreshold    int    `json:"hp_threshold"`
	SPThreshold    int    `json:"sp_threshold"`
	HPKeyVK        int32  `json:"hp_key_vk"`
	SPKeyVK        int32  `json:"sp_key_vk"`
	AddressMode    bool   `json:"address_mode"`
	AddressProfile string `json:"address_profile"`
	WindowTitle    string `json:"window_title"`
}

type keyChainProfile struct {
	VisibleCount int                                         `json:"visible_count"`
	Switches     [runner.KeyChainCount]keyChainProfileSwitch `json:"switches"`
}

type keyChainProfileSwitch struct {
	Keys     [runner.KeyChainSlotCount]int32 `json:"keys"`
	DelaysMs [runner.KeyChainSlotCount]int   `json:"delays_ms"`
}

type storedToolProfiles struct {
	Profiles []toolProfile `json:"profiles"`
}

func (a *guiApp) initToolProfiles() error {
	a.profiles.path = toolProfilesPath()
	stored, err := loadToolProfiles(a.profiles.path)
	if err != nil {
		a.appendLog(fmt.Sprintf("Could not load profiles: %v", err))
		stored = nil
	}
	a.profiles.saved = stored
	a.profiles.template = a.captureToolProfile(newToolProfileName)
	a.profiles.activeIndex = 0
	a.refreshToolProfileCombo(0)
	return nil
}

func toolProfilesPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "EzRoKit", "profiles.json")
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "profiles.json")
	}
	return "profiles.json"
}

func loadToolProfiles(path string) ([]toolProfile, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	var stored storedToolProfiles
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("parse profiles: %w", err)
	}
	out := make([]toolProfile, 0, len(stored.Profiles))
	seen := make(map[string]bool, len(stored.Profiles))
	for _, profile := range stored.Profiles {
		profile.Name = strings.TrimSpace(profile.Name)
		if profile.Name == "" || profile.Name == newToolProfileName || seen[profile.Name] {
			continue
		}
		seen[profile.Name] = true
		normalizeToolProfile(&profile)
		out = append(out, profile)
	}
	return out, nil
}

func saveToolProfiles(path string, profiles []toolProfile) error {
	data, err := json.MarshalIndent(storedToolProfiles{Profiles: profiles}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write profiles: %w", err)
	}
	return nil
}

func normalizeToolProfile(profile *toolProfile) {
	if profile.Clicker.VisibleCount < 1 || profile.Clicker.VisibleCount > runner.ClickerSlotCount {
		profile.Clicker.VisibleCount = 1
	}
	if profile.Timer.VisibleCount < 1 || profile.Timer.VisibleCount > runner.TimerKeySlotCount {
		profile.Timer.VisibleCount = 1
	}
	if profile.KeyChain.VisibleCount < 1 || profile.KeyChain.VisibleCount > runner.KeyChainCount {
		profile.KeyChain.VisibleCount = 1
	}
	if profile.AutoPot.HPThreshold < 1 || profile.AutoPot.HPThreshold > 99 {
		profile.AutoPot.HPThreshold = 50
	}
	if profile.AutoPot.SPThreshold < 1 || profile.AutoPot.SPThreshold > 99 {
		profile.AutoPot.SPThreshold = 30
	}
	for i := range profile.Clicker.Slots {
		if profile.Clicker.Slots[i].DelayMs <= 0 {
			profile.Clicker.Slots[i].DelayMs = runner.DefaultDelayMs
		}
	}
	for i := range profile.Timer.Slots {
		if profile.Timer.Slots[i].IntervalSec <= 0 {
			profile.Timer.Slots[i].IntervalSec = runner.DefaultTimerKeyIntervalSec
		}
	}
}

func (a *guiApp) refreshToolProfileCombo(selected int) {
	if a.profiles.combo == nil {
		return
	}
	names := make([]string, 1, len(a.profiles.saved)+1)
	names[0] = newToolProfileName
	for _, profile := range a.profiles.saved {
		names = append(names, profile.Name)
	}
	if selected < 0 || selected >= len(names) {
		selected = 0
	}
	a.profiles.suppress = true
	_ = a.profiles.combo.SetModel(names)
	_ = a.profiles.combo.SetCurrentIndex(selected)
	a.profiles.activeIndex = selected
	if a.profiles.removeBtn != nil {
		a.profiles.removeBtn.SetEnabled(selected > 0)
	}
	a.profiles.suppress = false
}

func (a *guiApp) onToolProfileSelected() {
	if a.profiles.suppress || a.profiles.combo == nil {
		return
	}
	idx := a.profiles.combo.CurrentIndex()
	if idx < 0 || idx >= len(a.profiles.saved)+1 {
		return
	}
	if idx == a.profiles.activeIndex {
		return
	}
	a.profiles.activeIndex = idx
	if a.profiles.removeBtn != nil {
		a.profiles.removeBtn.SetEnabled(idx > 0)
	}
	if idx == 0 {
		a.applyToolProfile(a.profiles.template)
		return
	}
	a.applyToolProfile(a.profiles.saved[idx-1])
	a.appendLog(fmt.Sprintf("Profile loaded: %s", a.profiles.saved[idx-1].Name))
}

func (a *guiApp) saveToolProfile() {
	if a.profiles.combo == nil {
		return
	}
	// Commit text fields before taking the snapshot. This also restores an
	// invalid threshold to its last valid value.
	a.commitHPThresholdEdit()
	a.commitSPThresholdEdit()

	name := strings.TrimSpace(a.profiles.combo.Text())
	if name == "" {
		walk.MsgBox(a.mainWindow, "Save profile", "Profile name cannot be empty.", walk.MsgBoxIconWarning)
		return
	}
	if name == newToolProfileName {
		walk.MsgBox(a.mainWindow, "Save profile", "Choose a name other than New Profile.", walk.MsgBoxIconWarning)
		return
	}

	idx := a.profiles.activeIndex
	if idx < 0 || idx > len(a.profiles.saved) {
		idx = 0
	}
	for i, profile := range a.profiles.saved {
		if strings.EqualFold(profile.Name, name) && i != idx-1 {
			walk.MsgBox(a.mainWindow, "Save profile", "That profile name already exists. Edit the name above and press Save again.", walk.MsgBoxIconWarning)
			return
		}
	}

	profile := a.captureToolProfile(name)
	if idx > 0 {
		a.profiles.saved[idx-1] = profile
	} else {
		a.profiles.saved = append(a.profiles.saved, profile)
		idx = len(a.profiles.saved)
	}
	if err := saveToolProfiles(a.profiles.path, a.profiles.saved); err != nil {
		walk.MsgBox(a.mainWindow, "Save profile", err.Error(), walk.MsgBoxIconError)
		return
	}
	a.refreshToolProfileCombo(idx)
	a.appendLog(fmt.Sprintf("Profile saved: %s", name))
}

func (a *guiApp) removeToolProfile() {
	idx := a.profiles.activeIndex
	if idx <= 0 || idx > len(a.profiles.saved) {
		a.appendLog("New Profile cannot be removed")
		return
	}

	name := a.profiles.saved[idx-1].Name
	previous := append([]toolProfile(nil), a.profiles.saved...)
	a.profiles.saved = append(a.profiles.saved[:idx-1], a.profiles.saved[idx:]...)
	if err := saveToolProfiles(a.profiles.path, a.profiles.saved); err != nil {
		a.profiles.saved = previous
		walk.MsgBox(a.mainWindow, "Remove profile", err.Error(), walk.MsgBoxIconError)
		return
	}

	a.refreshToolProfileCombo(0)
	a.applyToolProfile(a.profiles.template)
	a.appendLog(fmt.Sprintf("Profile removed: %s", name))
}

func (a *guiApp) captureToolProfile(name string) toolProfile {
	a.commitHPThresholdEdit()
	a.commitSPThresholdEdit()
	profile := toolProfile{
		Name:    name,
		Clicker: clickerProfile{VisibleCount: a.clicker.visibleCount},
		Timer:   timerProfile{VisibleCount: a.timer.visibleCount},
		AutoPot: autoPotProfile{
			HPThreshold:    a.autopot.hpThreshold,
			SPThreshold:    a.autopot.spThreshold,
			HPKeyVK:        a.autopot.hpKeyVK,
			SPKeyVK:        a.autopot.spKeyVK,
			AddressMode:    a.autopot.isAddressMode(),
			AddressProfile: a.autopot.selectedProfile().Name,
			WindowTitle:    a.autopot.selectedWindowTitle(),
		},
		KeyChain: keyChainProfile{VisibleCount: a.keychain.visibleCount},
	}
	for i := range profile.Clicker.Slots {
		profile.Clicker.Slots[i] = runner.ClickerSlot{
			TriggerVKs: a.clicker.triggerVKs[i],
			DelayMs:    a.clicker.delayMs(i),
			MouseClick: a.clicker.slots[i].mouseCB.Checked(),
		}
	}
	for i := range profile.Timer.Slots {
		profile.Timer.Slots[i] = timerProfileSlot{
			KeyVK:       a.timer.keyVKs[i],
			IntervalSec: a.intervalSeconds(i),
		}
	}
	for i := range profile.KeyChain.Switches {
		for j := 0; j < runner.KeyChainSlotCount; j++ {
			profile.KeyChain.Switches[i].Keys[j] = a.keychain.switches[i].keyVKs[j]
			profile.KeyChain.Switches[i].DelaysMs[j] = int(a.keychain.switches[i].slots[j].delayEdit.Value())
		}
	}
	normalizeToolProfile(&profile)
	return profile
}

func (a *guiApp) applyToolProfile(profile toolProfile) {
	normalizeToolProfile(&profile)
	a.profileApplying = true
	a.autopot.suppressWindowEvents = true
	a.clicker.visibleCount = profile.Clicker.VisibleCount
	for i := 0; i < runner.ClickerSlotCount; i++ {
		a.clicker.triggerVKs[i] = profile.Clicker.Slots[i].TriggerVKs
		a.clicker.slots[i].mouseCB.SetChecked(profile.Clicker.Slots[i].MouseClick)
		a.clicker.slots[i].delayEdit.SetText(strconv.Itoa(profile.Clicker.Slots[i].DelayMs))
		a.clicker.lastLoggedDelay[i] = profile.Clicker.Slots[i].DelayMs
		a.clicker.slots[i].row.SetVisible(i < a.clicker.visibleCount)
		a.updateClickerKeyLabel(i)
	}
	a.updateClickerAddButton()
	a.updateClickerRemoveButtons()

	a.timer.visibleCount = profile.Timer.VisibleCount
	for i := 0; i < runner.TimerKeySlotCount; i++ {
		slot := profile.Timer.Slots[i]
		a.timer.keyVKs[i] = slot.KeyVK
		a.timer.slots[i].intervalEdit.SetText(strconv.Itoa(slot.IntervalSec))
		a.timer.slots[i].row.SetVisible(i < a.timer.visibleCount)
		a.timer.slots[i].keyLabel.SetText(keyNameOrNone(slot.KeyVK))
	}
	a.updateTimerAddButton()

	a.autopot.hpThreshold = profile.AutoPot.HPThreshold
	a.autopot.spThreshold = profile.AutoPot.SPThreshold
	a.autopot.hpThresholdEdit.SetText(strconv.Itoa(profile.AutoPot.HPThreshold))
	a.autopot.spThresholdEdit.SetText(strconv.Itoa(profile.AutoPot.SPThreshold))
	a.autopot.hpKeyVK = profile.AutoPot.HPKeyVK
	a.autopot.spKeyVK = profile.AutoPot.SPKeyVK
	a.autopot.hpKeyLabel.SetText(keyNameOrNone(profile.AutoPot.HPKeyVK))
	a.autopot.spKeyLabel.SetText(keyNameOrNone(profile.AutoPot.SPKeyVK))
	if profile.AutoPot.AddressMode {
		a.autopot.autopotAddressRB.SetChecked(true)
	} else {
		a.autopot.autopotVisualRB.SetChecked(true)
	}
	a.autopot.processPID = 0
	a.autopot.selectProfileByName(profile.AutoPot.AddressProfile)
	windowIndex := -1
	for i, window := range a.autopot.windowList {
		if window.Title == profile.AutoPot.WindowTitle {
			windowIndex = i
			break
		}
	}
	a.autopot.windowCB.SetCurrentIndex(windowIndex)
	if windowIndex >= 0 {
		_ = a.openSelectedProcessHandle()
	}

	a.keychain.visibleCount = profile.KeyChain.VisibleCount
	for i := 0; i < runner.KeyChainCount; i++ {
		sw := profile.KeyChain.Switches[i]
		for j := 0; j < runner.KeyChainSlotCount; j++ {
			a.keychain.switches[i].keyVKs[j] = sw.Keys[j]
			a.keychain.setKeyText(i, j, sw.Keys[j])
			a.keychain.switches[i].slots[j].delayEdit.SetValue(float64(sw.DelaysMs[j]))
		}
		a.keychain.switches[i].group.SetVisible(i < a.keychain.visibleCount)
	}
	a.updateKeyChainAddButton()
	a.updateKeyChainRemoveButtons()
	a.profileApplying = false
	a.syncRunnerSettings()
	a.syncAutoPotSettings()
	a.syncTimerKeySettings()
	a.syncKeyChainSettings()
	a.mainWindow.Synchronize(func() {
		a.autopot.suppressWindowEvents = false
	})
}

func (a *guiApp) intervalSeconds(index int) int {
	if index < 0 || index >= a.timer.visibleCount {
		return runner.DefaultTimerKeyIntervalSec
	}
	value, err := strconv.Atoi(a.timer.slots[index].intervalEdit.Text())
	if err != nil || value <= 0 {
		return runner.DefaultTimerKeyIntervalSec
	}
	return value
}

func keyNameOrNone(vk int32) string {
	if vk == 0 {
		return "none"
	}
	return runner.KeyName(vk)
}
