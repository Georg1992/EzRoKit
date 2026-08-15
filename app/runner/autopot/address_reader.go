package autopot

import (
	"context"
	"fmt"
	"time"

	"ezrokit/runner/profiles"
	"golang.org/x/sys/windows"
)

// addressReader is a BarReader that reads HP/SP values from a game
// process's memory at the offsets defined by a server profile.
//
// Opens a fresh process handle on every ReadValues() call and closes it
// after all 4 values are read. This avoids the stale-handle problem that
// occurs when anti-cheat invalidates persistent handles.
//
// When reads fail persistently, the reader attempts to auto-reconnect by
// finding a window whose title matches processTitle and resolving its PID.
type addressReader struct {
	pid          uint32
	profile      profiles.Profile
	processTitle string // window title for auto-reconnect
	moduleBase   uintptr // base address of the exe in the target process

	thresholdFn  func() (hpThresh, spThresh int)
	onParsed     func(hp, hpMax, sp, spMax, stripX, stripY, stripW, stripH int)
	onModeChange func(string)

	lastLog  time.Time
	log      func(string)
	loggedFirstFail bool

	hadError      bool       // true when the last ReadValues returned an error
	lastReconn    time.Time  // last auto-reconnect attempt time
}

// reconnectInterval is how long the reader waits before trying to find
// the process again after persistent read failures.
const reconnectInterval = 5 * time.Second

func (r *addressReader) Name() string { return "Address" }

func (r *addressReader) ReadValues(ctx context.Context) BarReadResult {
	if ctx.Err() != nil {
		return BarReadResult{Status: StatusInvalid, Err: ctx.Err()}
	}
	if r.pid == 0 {
		return BarReadResult{Status: StatusInvalid, Err: fmt.Errorf("address reader: no process selected (PID=0)")}
	}

	h, err := OpenProcessHandle(r.pid)
	if err != nil {
		r.setError("address: OpenProcess(%d) failed: %v", r.pid, err)
		return BarReadResult{Status: StatusInvalid, Err: err}
	}
	defer CloseProcessHandle(h)

	base := r.moduleBase
	curHP, maxHP, err := r.readValues(h, base, r.profile.CurrentHPAddr, r.profile.MaxHPAddr)
	if err != nil {
		r.setError("address: %v", err)
		return BarReadResult{Status: StatusInvalid, Err: err}
	}
	curSP, maxSP, err := r.readValues(h, base, r.profile.CurrentSPAddr, r.profile.MaxSPAddr)
	if err != nil {
		r.setError("address: %v", err)
		return BarReadResult{Status: StatusInvalid, Err: err}
	}

	// All reads succeeded — clear error state and restore normal mode.
	if r.hadError {
		r.hadError = false
		if r.onModeChange != nil {
			r.onModeChange("Address reading")
		}
	}

	hpPct, spPct := r.pct(curHP, maxHP, curSP, maxSP)

	if r.onParsed != nil {
		r.onParsed(int(curHP), int(maxHP), int(curSP), int(maxSP), 0, 0, 0, 0)
	}

	hpLow, spLow := r.lowFlags(hpPct, spPct)
	return BarReadResult{
		HP: hpPct, SP: spPct,
		HPLow: hpLow, SPLow: spLow,
		Status: StatusFound,
	}
}

// readValues reads two uint32 values from the process memory at
// (base+addr1) and (base+addr2) using the open handle h.
func (r *addressReader) readValues(h windows.Handle, base uintptr, addr1, addr2 uintptr) (uint32, uint32, error) {
	v1, err := ReadProcessUint32ByHandle(h, base+addr1)
	if err != nil {
		return 0, 0, err
	}
	v2, err := ReadProcessUint32ByHandle(h, base+addr2)
	if err != nil {
		return 0, 0, err
	}
	return v1, v2, nil
}

// pct converts raw HP/SP values to percentages.
func (r *addressReader) pct(curHP, maxHP, curSP, maxSP uint32) (hpPct, spPct float64) {
	if maxHP > 0 {
		hpPct = float64(curHP) * 100.0 / float64(maxHP)
	}
	if maxSP > 0 {
		spPct = float64(curSP) * 100.0 / float64(maxSP)
	}
	return
}

// lowFlags checks HP/SP percentages against the live config thresholds.
func (r *addressReader) lowFlags(hpPct, spPct float64) (hpLow, spLow bool) {
	if r.thresholdFn == nil {
		return false, false
	}
	hpThresh, spThresh := r.thresholdFn()
	return hpPct < float64(hpThresh), spPct < float64(spThresh)
}

// setError marks the reader as in error state, updates the overlay mode
// to "Address err" on first failure, logs the error (rate-limited), and
// periodically attempts to auto-reconnect to the game process by searching
// for a visible window whose title matches processTitle.
func (r *addressReader) setError(format string, args ...interface{}) {
	now := time.Now()

	// First failure after a success — switch overlay to error indicator.
	if !r.hadError {
		r.hadError = true
		if r.onModeChange != nil {
			r.onModeChange("Address err")
		}
	}

	// Auto-reconnect: try to find a new PID by window title periodically.
	if r.pid != 0 && now.Sub(r.lastReconn) >= reconnectInterval {
		r.lastReconn = now

		newPID := r.pid
		if r.processTitle != "" {
			if found := FindVisibleWindowPID(r.processTitle); found != 0 {
				newPID = found
			}
		}

		if newPID != r.pid {
			base, err := GetProcessBaseAddr(newPID)
			if err != nil {
				if r.log != nil {
					r.log(fmt.Sprintf("address: found PID %d but base addr failed: %v", newPID, err))
				}
			} else {
				r.pid = newPID
				r.moduleBase = base
				if r.log != nil {
					r.log(fmt.Sprintf("address: reconnected to PID %d", newPID))
				}
				// Reconnect succeeded — clear error and restore normal mode.
				// The next ReadValues will open a fresh handle to the new PID.
				r.hadError = false
				r.loggedFirstFail = false
				if r.onModeChange != nil {
					r.onModeChange("Address reading")
				}
				return
			}
		}
	}

	// Log the error (rate-limited to once per 10s).
	if r.log == nil {
		return
	}
	if !r.loggedFirstFail || now.Sub(r.lastLog) > 10*time.Second {
		r.log(fmt.Sprintf(format, args...))
		r.loggedFirstFail = true
		r.lastLog = now
	}
}
