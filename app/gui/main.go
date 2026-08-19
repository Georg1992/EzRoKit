//go:build windows

//go:generate go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -o rsrc.syso

// Package main is the walk-based Windows GUI for the clicker. It is
// the topmost layer of a three-layer architecture; see README.md in
// this directory for the full layering rules and the import boundary.
//
// Quick rule: this package must only import `ezrokit/runner`
// (the public facade). Never import `runner/autopot`, `runner/autopot/statusui`,
// `runner/internal/...`, or `runner/platform/...` directly — add the
// missing surface to `runner` first, then consume it here.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"ezrokit/runner"

	"github.com/lxn/walk"
)

type guiApp struct {
	mainWindow *walk.MainWindow
	logList    *walk.ListBox
	logItems   []string

	clicker  clickerController
	timer    timerController
	autopot  autopotController
	keychain keychainController
	profiles toolProfileController

	// Control panel
	startBtn       *walk.PushButton // Tools Start
	stopBtn        *walk.PushButton // Tools Stop
	viiperStartBtn *walk.PushButton // VIIPER Start
	toolsBadge     *toolsBadge
	viiperBadge    *viiperBadge
	profileLogRow  *walk.Composite

	mu    sync.Mutex
	logMu sync.Mutex
	// lifecycleMu serializes GUI runner slot ownership with starts and
	// stops. Without it, a stop could observe an empty slot between a
	// runner's Start() and its publication, leaving that runner orphaned.
	lifecycleMu         sync.Mutex
	shutdownOnce        sync.Once
	bindingActive       bool
	logFile             *os.File
	starting            atomic.Int32
	stopping            atomic.Int32
	shuttingDown        atomic.Bool
	startupGeneration   atomic.Uint64
	startupCancel       context.CancelFunc
	viiperStartupCancel context.CancelFunc
	lifetimeCtx         context.Context
	lifetimeCancel      context.CancelFunc
	tools               toolsHost
	inputSession        *runner.ViiperSession
	overlay             *statusOverlay
	viiperMonitor       *viiperMonitor
	profileApplying     bool
}

func main() {
	lifetimeCtx, lifetimeCancel := context.WithCancel(context.Background())
	app := &guiApp{
		bindingActive:  false,
		lifetimeCtx:    lifetimeCtx,
		lifetimeCancel: lifetimeCancel,
	}
	defer app.shutdown()

	// Open a persistent log file in a logs/ directory next to the
	// executable so diagnostics survive GUI close. Best-effort — if
	// the file can't be created, logging still works in-memory.
	if exe, err := os.Executable(); err == nil {
		logDir := filepath.Join(filepath.Dir(exe), "logs")
		_ = os.MkdirAll(logDir, 0o755)
		logPath := filepath.Join(logDir, "app.log")
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			app.logFile = f
			// Stamp the first entry so the user knows where to look.
			_, _ = f.WriteString(fmt.Sprintf("[%s] Log file: %s\n", time.Now().Format("15:04:05"), logPath))
		}
	}

	if err := app.createWindow(); err != nil {
		walk.MsgBox(nil, "EzRoKit", err.Error(), walk.MsgBoxIconError)
	}
}

func (a *guiApp) shutdown() {
	a.shutdownOnce.Do(func() {
		a.shuttingDown.Store(true)
		a.lifecycleMu.Lock()
		a.mu.Lock()
		if a.lifetimeCancel != nil {
			a.lifetimeCancel()
			a.lifetimeCancel = nil
		}
		taken := a.tools.takeAll()
		session := a.inputSession
		a.inputSession = nil
		if a.startupCancel != nil {
			a.startupCancel()
			a.startupCancel = nil
		}
		if a.viiperStartupCancel != nil {
			a.viiperStartupCancel()
			a.viiperStartupCancel = nil
		}
		a.mu.Unlock()
		a.lifecycleMu.Unlock()

		if a.viiperMonitor != nil {
			a.viiperMonitor.stop()
			a.viiperMonitor = nil
		}

		if a.logFile != nil {
			_ = a.logFile.Close()
			a.logFile = nil
		}

		taken.stopAndWait()
		if session != nil {
			session.Close()
		}
		stopViiperServerIfStarted()

		if a.overlay != nil {
			a.overlay.Destroy()
			a.overlay = nil
		}
	})
}

// ---------------------------------------------------------------------------
// createWindow and initialisation phases
// ---------------------------------------------------------------------------

func (a *guiApp) createWindow() error {
	mw := a.initMainWindow()
	if err := a.setupMainWindow(mw); err != nil {
		return err
	}
	a.startBackgroundMonitors()
	if err := a.initTabs(mw); err != nil {
		return err
	}
	if err := a.initLogArea(mw); err != nil {
		return err
	}
	if err := a.initToolProfiles(); err != nil {
		return err
	}
	a.wireClosingHandler(mw)
	a.setInitialState()
	a.onStartViiper()

	mw.Starting().Attach(a.hookChainDelayCommits)
	mw.Show()
	mw.Run()
	return nil
}

// initMainWindow creates the main window and the HP/SP overlay.
func (a *guiApp) initMainWindow() *walk.MainWindow {
	mw, err := walk.NewMainWindow()
	if err != nil {
		panic(err) // must not fail
	}
	a.mainWindow = mw

	if ovl, ovlErr := newStatusOverlay(); ovlErr == nil {
		a.overlay = ovl
	}
	return mw
}

// setupMainWindow sets title, size, layout, icon, header, and control panel.
func (a *guiApp) setupMainWindow(mw *walk.MainWindow) error {
	if err := a.setupLogLimit(); err != nil {
		return err
	}
	if err := mw.SetTitle("EzRoKit"); err != nil {
		return err
	}
	if err := mw.SetMinMaxSize(walk.Size{Width: 780, Height: 600}, walk.Size{}); err != nil {
		return err
	}
	if err := mw.SetSize(walk.Size{Width: 780, Height: 600}); err != nil {
		return err
	}

	root := walk.NewVBoxLayout()
	root.SetMargins(walk.Margins{HNear: 10, VNear: 10, HFar: 10, VFar: 10})
	root.SetSpacing(10)
	if err := mw.SetLayout(root); err != nil {
		return err
	}

	icon, err := walk.NewIconFromImageForDPI(ezrokitIconImage(), 96)
	if err != nil {
		return err
	}
	if err := mw.SetIcon(icon); err != nil {
		return err
	}
	return a.buildControlPanel(mw)
}

// startBackgroundMonitors starts the VIIPER connectivity monitor and the
// start/stop toggle-key watcher. Both run for the lifetime of the app.
func (a *guiApp) startBackgroundMonitors() {
	ctx := a.lifetimeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	runner.SetKeyboardLog(a.fileLog())
	if err := runner.StartPhysicalKeyboard(ctx); err != nil {
		a.appendLog(fmt.Sprintf("Physical keyboard tracking failed: %v", err))
	}
	a.viiperMonitor = startViiperMonitor(ctx, func(active bool) {
		a.mainWindow.Synchronize(func() {
			if active {
				a.viiperBadge.SetStatus(viiperActive)
				return
			}
			a.viiperBadge.SetStatus(viiperInactive)
			if a.isStarted() {
				a.appendLog("VIIPER server disconnected — stopping tools")
				a.onStop()
			}
			a.mu.Lock()
			if a.inputSession != nil {
				a.inputSession.Close()
				a.inputSession = nil
			}
			a.mu.Unlock()
			stopViiperServerIfStarted()
			a.startBtn.SetEnabled(false)
			a.stopBtn.SetEnabled(false)
			a.setConfigEnabled(false)
			a.viiperStartBtn.SetEnabled(true)
		})
	})

	runner.StartToggleKeyWatcher(ctx, func(vk int32) {
		a.mainWindow.Synchronize(func() {
			if a.isStarted() {
				a.appendLog(fmt.Sprintf("%s pressed — stopping tools", runner.KeyName(vk)))
				a.onStop()
			} else {
				a.onStart()
			}
		})
	})
}

// initTabs creates the Clicker, AutoPot, and KeyChain tab pages and wires
// tab-change and deactivating handlers for threshold blur.
func (a *guiApp) initTabs(mw *walk.MainWindow) error {
	tabs, err := walk.NewTabWidget(mw)
	if err != nil {
		return err
	}

	tabDefs := []struct {
		title string
		build func(*walk.TabPage) error
	}{
		{"Clicker", a.buildClickerTab},
		{"AutoPot", a.buildAutoPotTab},
		{"KeyChain", a.buildKeyChainTab},
	}

	for _, td := range tabDefs {
		page, err := walk.NewTabPage()
		if err != nil {
			return err
		}
		if err := page.SetTitle(td.title); err != nil {
			return err
		}
		if err := td.build(page); err != nil {
			return err
		}
		if err := tabs.Pages().Add(page); err != nil {
			return err
		}
	}

	tabs.CurrentIndexChanged().Attach(a.finishThresholdInput)
	mw.Deactivating().Attach(a.finishThresholdInput)
	return nil
}

// initLogArea creates the Logs panel beside the Profiles controls.
func (a *guiApp) initLogArea(mw *walk.MainWindow) error {
	parent := walk.Container(mw)
	if a.profileLogRow != nil {
		parent = a.profileLogRow
	}

	logGB, err := walk.NewGroupBox(parent)
	if err != nil {
		return err
	}
	if err := logGB.SetTitle("Logs"); err != nil {
		return err
	}
	logLayout := walk.NewVBoxLayout()
	logLayout.SetSpacing(4)
	if err := logGB.SetLayout(logLayout); err != nil {
		return err
	}

	a.logList, err = walk.NewListBox(logGB)
	if err != nil {
		return err
	}
	if err := a.logList.SetMinMaxSize(walk.Size{Width: 300, Height: 140}, walk.Size{}); err != nil {
		return err
	}
	a.logItems = make([]string, 0, maxLogItems)
	if err := a.logList.SetModel(a.logItems); err != nil {
		return err
	}
	a.wireThresholdBlurOnClick(mw)
	return nil
}

// wireClosingHandler attaches the shutdown handler to the window close event.
func (a *guiApp) wireClosingHandler(mw *walk.MainWindow) {
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		a.shutdown()
	})
}

// setInitialState disables everything except Start VIIPER.
func (a *guiApp) setInitialState() {
	a.viiperBadge.SetStatus(viiperInactive)
	a.toolsBadge.SetStatus(toolsStatusStopped)
	a.viiperStartBtn.SetEnabled(true)
	a.startBtn.SetEnabled(false)
	a.stopBtn.SetEnabled(false)
	a.setConfigEnabled(false)
}

// isViiperReady reports whether VIIPER is running with an active session.
// This is the minimum requirement for key binding and tools operation.
func (a *guiApp) isViiperReady() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inputSession != nil
}

func (a *guiApp) isStarted() bool {
	// Fast path: startup in flight — no mutex needed (atomic load).
	if a.starting.Load() != 0 {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tools.anyRunning()
}

// setConfigEnabled enables or disables all tab configuration (clicker slots,
// autopot, timer keys, keychain). Called when VIIPER state changes — config
// is enabled when VIIPER is running, disabled when VIIPER is down.
func (a *guiApp) setConfigEnabled(enabled bool) {
	a.setClickerConfigEnabled(enabled)
	a.setAutoPotConfigEnabled(enabled)
	a.setTimerKeyConfigEnabled(enabled)
	a.setKeyChainConfigEnabled(enabled)
}

// setToolsStarted updates the TOOLS badge and Start/Stop button states.
// Does NOT touch config enable/disable — that's managed by VIIPER state.
// MUST be called on the GUI thread.
func (a *guiApp) setToolsStarted(started bool) {
	a.startBtn.SetEnabled(!started)
	a.stopBtn.SetEnabled(started)
	if started {
		a.toolsBadge.SetStatus(toolsStatusRunning)
	} else {
		a.toolsBadge.SetStatus(toolsStatusStopped)
	}
}

// ---------------------------------------------------------------------------
// Tools lifecycle
// ---------------------------------------------------------------------------

// onStart is the Tools Start button click handler. It assumes VIIPER is
// already running (inputSession is non-nil) and starts all runners.
// The blocking portion runs on a background goroutine so the GUI stays
// responsive during session wiring.
func (a *guiApp) onStart() {
	a.mu.Lock()
	if a.inputSession == nil {
		a.mu.Unlock()
		a.appendLog("Cannot start tools — VIIPER is not running. Start VIIPER first.")
		return
	}
	if a.shuttingDown.Load() || a.stopping.Load() != 0 || a.starting.Load() != 0 || a.tools.anyRunning() {
		a.mu.Unlock()
		return
	}
	if ready, msg := inputDriverReady(); !ready {
		a.mu.Unlock()
		a.appendLog("Input driver not ready — see Setup required dialog.")
		walk.MsgBox(a.mainWindow, "Setup required", msg, walk.MsgBoxIconWarning)
		return
	}
	// Cancel any previous startup goroutine that is still running.
	if a.startupCancel != nil {
		a.startupCancel()
		a.startupCancel = nil
	}
	generation := a.startupGeneration.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	a.startupCancel = cancel
	a.starting.Store(1)
	a.mu.Unlock()

	// Immediate UI feedback.
	a.setToolsStarted(true)
	a.appendLog("Starting tools...")

	go a.startInBackground(ctx, generation)
}

// startInBackground runs the long-running tools startup work off the GUI
// thread. VIIPER is already running — this only wires up the runners.
func (a *guiApp) startInBackground(ctx context.Context, generation uint64) {
	diag := a.fileLog()
	user := a.guiLog(a.appendLog)
	isStillStarting := func() bool {
		if ctx.Err() != nil {
			return false
		}
		return a.starting.Load() != 0 && a.startupGeneration.Load() == generation
	}
	finishFailure := func() {
		if ctx.Err() != nil {
			return // superseded by a newer Start
		}
		a.starting.Swap(0)
		a.mainWindow.Synchronize(func() { a.setToolsStarted(false) })
	}

	// Use the existing VIIPER session (already set up by onStartViiper).
	a.mu.Lock()
	session := a.inputSession
	a.mu.Unlock()

	if session == nil {
		user("Cannot start — VIIPER session is nil")
		finishFailure()
		return
	}

	diag("Reusing VIIPER session...")
	session.Reset()

	if !isStillStarting() {
		return
	}

	cfg := a.clicker.config(diag)
	cfg.Session = session
	cfg.Log = diag

	r := runner.New(cfg)
	// Publish the clicker under the same ownership lock used by onStop and
	// the other runner starters. Otherwise onStop could take an empty slot
	// between Start() and publication, orphaning this live goroutine.
	a.lifecycleMu.Lock()
	if !isStillStarting() || a.shuttingDown.Load() {
		a.lifecycleMu.Unlock()
		r.Stop()
		r.Wait()
		return
	}
	if err := r.Start(); err != nil {
		a.lifecycleMu.Unlock()
		user(fmt.Sprintf("Start failed: %v", err))
		finishFailure()
		return
	}
	a.mu.Lock()
	a.tools.clicker = r
	a.mu.Unlock()
	a.lifecycleMu.Unlock()
	if !isStillStarting() {
		a.lifecycleMu.Lock()
		a.mu.Lock()
		if a.tools.clicker == r {
			a.tools.clicker = nil
		}
		a.mu.Unlock()
		a.lifecycleMu.Unlock()
		r.Stop()
		r.Wait()
		return
	}

	if !a.startRemainingRunners(ctx, generation, session, diag) {
		return
	}

	// atomically read+clear the starting flag so onStop can't race
	// between the two operations.
	wasStarting := a.starting.Swap(0)
	if wasStarting == 0 {
		return // onStop already cleared starting before we could finish
	}

	a.mainWindow.Synchronize(func() { a.setToolsStarted(true) })
	user("Tools started")
}

// startRemainingRunners starts AutoPot, TimerKey, and KeyChain runners.
func (a *guiApp) startRemainingRunners(ctx context.Context, generation uint64, session runner.InputSession, logFn func(string)) bool {
	stillStarting := func() bool {
		return ctx.Err() == nil && a.starting.Load() != 0 && a.startupGeneration.Load() == generation
	}
	stopIfCancelled := func() bool {
		if stillStarting() {
			return false
		}
		a.stopStartedRunners()
		return true
	}

	if stopIfCancelled() {
		return false
	}
	autopotCfg := a.autopot.wanted(a.autopotStatus(), logFn)
	autopotCfg.Core.Session = session
	autopotCfg.Core.Log = logFn
	timerCfg := a.timer.wanted(logFn)
	timerCfg.Session = session
	timerCfg.Log = logFn

	keyChainCfg := a.keychain.config(logFn)
	keyChainCfg.Session = session
	keyChainCfg.Log = logFn

	a.autopot.prevAutoPotAddressMode = autopotCfg.IsAddressMode()
	a.startAutoPotRunner(autopotCfg, logFn)
	if stopIfCancelled() {
		return false
	}

	// If no autopot keys are bound, show "AutoPot off" instead of a stale mode.
	if !autopotCfg.HasBoundPotion() {
		a.mainWindow.Synchronize(func() {
			if a.overlay != nil {
				a.overlay.SetMode("AutoPot off")
			}
		})
	}

	a.startTimerKeyRunner(timerCfg, logFn)
	if stopIfCancelled() {
		return false
	}
	a.startKeyChainRunner(keyChainCfg, logFn)
	if stopIfCancelled() {
		return false
	}
	return true
}

// stopStartedRunners takes ownership of every currently published runner and
// waits for each one to finish. It is used when startup is cancelled after a
// subset of the tools has already been started.
func (a *guiApp) stopStartedRunners() {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.mu.Lock()
	taken := a.tools.takeAll()
	a.mu.Unlock()
	taken.stopAndWait()
}

// onStop stops all tools but keeps the VIIPER session alive so the next
// Start reuses it. The blocking Stop+Wait runs on a background goroutine.
// VIIPER server is NOT stopped — it stays running for reuse.
func (a *guiApp) onStop() {
	a.stopping.Store(1)
	a.lifecycleMu.Lock()
	a.mu.Lock()
	taken := a.tools.takeAll()
	session := a.inputSession
	// Keep a.inputSession alive so the next Start reuses it.
	// Full cleanup (Close) happens in shutdown().
	// Invalidate any in-flight startup goroutine before releasing the lock.
	a.startupGeneration.Add(1)
	a.starting.Store(0)
	if a.startupCancel != nil {
		a.startupCancel()
		a.startupCancel = nil
	}
	a.mu.Unlock()
	a.lifecycleMu.Unlock()

	a.setToolsStarted(false)
	a.appendLog("Stopping tools...")

	go func() {
		taken.stopAndWait()
		if session != nil {
			session.Reset()
			// Keep the session alive; the next Start reuses it.
			// Full cleanup (Close) happens in shutdown().
		}
		a.mainWindow.Synchronize(func() {
			a.appendLog("Tools stopped — Start to relaunch")
			if a.overlay != nil {
				a.overlay.ShowStopped()
			}
			a.stopping.Store(0)
		})
	}()
}

func (a *guiApp) startAutoPotRunner(cfg runner.AutoPotConfig, log func(string)) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.shuttingDown.Load() {
		return
	}
	take := func() lifecycleRunner {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.tools.autopot == nil {
			return nil
		}
		old := a.tools.autopot
		a.tools.autopot = nil
		return old
	}
	store := func(r lifecycleRunner) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.tools.autopot = r.(*runner.AutoPotRunner)
	}
	replaceRunner(
		take,
		store,
		"AutoPot",
		log,
		a.guiLog(a.appendLog),
		func() runner.InputSession {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.inputSession
		},
		func() bool { return cfg.HasBoundPotion() },
		func(sess runner.InputSession) lifecycleRunner {
			cfg.Core.Session = sess
			cfg.Core.Log = log
			return runner.NewAutoPot(cfg)
		},
	)
}

// unsetKeyBinding searches every key storage location in the app for vk.
// If found, it clears the old binding (UI label + state), syncs the
// affected runner, and logs the change. Call this from any onPress
// handler BEFORE assigning the key to the new slot so a key can only
// ever be bound in one place at a time, except that one keychain switch
// may repeat the same key (including the trigger).
func (a *guiApp) unsetKeyBinding(vk int32) {
	a.unsetKeyBindingExceptChain(vk, -1)
}

func (a *guiApp) unsetKeyBindingExceptChain(vk int32, keepSwitch int) {
	// Check clicker binds (each may hold multiple independent keys).
	for i := 0; i < a.clicker.visibleCount; i++ {
		if a.clicker.removeKey(i, vk) {
			a.updateClickerKeyLabel(i)
			a.logToFile(fmt.Sprintf("Key %s removed from %s (reassigned)", runner.KeyName(vk), clickerTitle(i)))
			a.setClickerConfigEnabled(a.isViiperReady())
			a.syncRunnerSettings()
			return
		}
	}
	// Check timer keys.
	for i := 0; i < a.timer.visibleCount; i++ {
		if a.timer.keyVKs[i] == vk {
			a.timer.keyVKs[i] = 0
			a.timer.slots[i].keyLabel.SetText("none")
			a.logToFile(fmt.Sprintf("Key %s removed from Timer %d (reassigned)", runner.KeyName(vk), i+1))
			a.syncTimerKeySettings()
			return
		}
	}
	// Check HP potion.
	if a.autopot.hpKeyVK == vk {
		a.autopot.hpKeyVK = 0
		a.autopot.hpKeyLabel.SetText("none")
		a.logToFile(fmt.Sprintf("Key %s removed from HP potion (reassigned)", runner.KeyName(vk)))
		a.syncAutoPotSettings()
		return
	}
	// Check SP potion.
	if a.autopot.spKeyVK == vk {
		a.autopot.spKeyVK = 0
		a.autopot.spKeyLabel.SetText("none")
		a.logToFile(fmt.Sprintf("Key %s removed from SP potion (reassigned)", runner.KeyName(vk)))
		a.syncAutoPotSettings()
		return
	}
	// Other keychain switches cannot keep this key. Slots on keepSwitch stay
	// so a chain like 1-2-1-3-1-4 can reuse the trigger.
	cleared := false
	for si := 0; si < a.keychain.visibleCount; si++ {
		if si == keepSwitch {
			continue
		}
		sw := &a.keychain.switches[si]
		for i := 0; i < runner.KeyChainSlotCount; i++ {
			if sw.keyVKs[i] != vk {
				continue
			}
			sw.keyVKs[i] = 0
			a.keychain.setKeyText(si, i, 0)
			a.logToFile(fmt.Sprintf("Key %s removed from Switch %d slot %d (reassigned)", runner.KeyName(vk), si+1, i+1))
			cleared = true
		}
	}
	if cleared {
		a.syncKeyChainSettings()
	}
}

func (a *guiApp) syncRunnerSettings() {
	if a.profileApplying {
		return
	}
	cfg := a.clicker.config(a.fileLog())
	a.mu.Lock()
	r := a.tools.clicker
	a.mu.Unlock()

	if r != nil && r.Running() {
		r.UpdateSettings(cfg.Slots)
	}
}
