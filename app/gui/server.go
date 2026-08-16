//go:build windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "embed"

	"ezrokit/runner"

	"github.com/Alia5/VIIPER/viiperclient"
	"golang.org/x/sys/windows"
)

//go:embed embed/viiper.exe
var viiperBin []byte

const (
	serverWaitTime   = 30 * time.Second
	serverPollPeriod = 200 * time.Millisecond

	// maxCapturedLines is how many stdout/stderr lines we keep for
	// diagnostics when the server fails to respond.
	maxCapturedLines = 24
)

var (
	serverMu          sync.Mutex
	serverStartMu     sync.Mutex
	serverStartCancel context.CancelFunc
	serverCmd         *exec.Cmd
	serverStarted     bool
	serverPID         int
	viiperTempDir     string
)

// ---------------------------------------------------------------------------
// ring buffer for captured viiper output
// ---------------------------------------------------------------------------

type outputRing struct {
	mu    sync.Mutex
	lines []string
	pos   int
	full  bool
}

func newOutputRing(n int) *outputRing { return &outputRing{lines: make([]string, n)} }

func (r *outputRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines[r.pos] = line
	r.pos++
	if r.pos >= len(r.lines) {
		r.pos = 0
		r.full = true
	}
}

// tail returns the last lines in order. If fewer than maxCapturedLines were
// captured it returns all of them.
func (r *outputRing) tail() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string(nil), r.lines[:r.pos]...)
	}
	out := make([]string, len(r.lines))
	n := copy(out, r.lines[r.pos:])
	copy(out[n:], r.lines[:r.pos])
	return out
}

// ---------------------------------------------------------------------------
// main entry point
// ---------------------------------------------------------------------------

func ensureViiperServer(ctx context.Context, log func(string)) (started bool, err error) {
	// Serialize startup attempts without blocking stopViiperServerIfStarted.
	// The latter must be able to cancel a startup that is waiting for the
	// embedded server to become reachable.
	serverStartMu.Lock()
	defer serverStartMu.Unlock()

	startupCtx, startupCancel := context.WithCancel(ctx)
	serverMu.Lock()
	serverStartCancel = startupCancel
	serverMu.Unlock()
	defer func() {
		serverMu.Lock()
		serverStartCancel = nil
		serverMu.Unlock()
		startupCancel()
	}()

	addr := runner.DefaultAPIAddr

	pingCtx, pingCancel := context.WithTimeout(startupCtx, 2*time.Second)
	defer pingCancel()
	api := viiperclient.New(addr)
	if _, err := api.PingCtx(pingCtx); err == nil {
		log("VIIPER already running")
		return false, nil
	}

	path, dir, err := extractViiper()
	if err != nil {
		return false, err
	}
	serverMu.Lock()
	viiperTempDir = dir
	serverMu.Unlock()

	cmd := exec.Command(path, "server")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}

	ring := newOutputRing(maxCapturedLines)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return false, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("start server: %w", err)
	}
	serverMu.Lock()
	serverPID = cmd.Process.Pid
	serverMu.Unlock()

	go forwardOutput(stdout, ring, log, "viiper")
	go forwardOutput(stderr, ring, log, "viiper")

	log(fmt.Sprintf("Waiting for VIIPER (up to %s)...", serverWaitTime))
	if err := waitForServer(startupCtx, addr, serverWaitTime, log); err != nil {
		serverMu.Lock()
		pid := serverPID
		serverPID = 0
		dir := viiperTempDir
		viiperTempDir = ""
		serverMu.Unlock()
		killProcessTree(pid)
		_, _ = cmd.Process.Wait() // populate cmd.ProcessState for diagnostics
		dumpViiperDiagnostics(cmd, ring, addr, dir, log)
		_ = os.RemoveAll(dir)
		return false, err
	}

	serverMu.Lock()
	serverCmd = cmd
	serverStarted = true
	serverMu.Unlock()
	return true, nil
}

// ---------------------------------------------------------------------------
// output capture
// ---------------------------------------------------------------------------

// forwardOutput reads lines from r line-by-line. Every line (raw) is added
// to the ring buffer for diagnostics. Only key events are forwarded to the
// GUI log in clean, human-readable form — ANSI codes and timestamps are
// stripped, and internal detail lines are skipped entirely.
func forwardOutput(r io.Reader, ring *outputRing, log func(string), prefix string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		raw := scanner.Text()
		ring.add(raw)

		// Strip ANSI escape sequences so we can match on readable text.
		clean := stripANSI(raw)

		// Only forward key events; everything else is ring-only.
		switch {
		case strings.Contains(clean, "ERROR"):
			log(fmt.Sprintf("[%s] %s", prefix, clean))
		case strings.Contains(clean, "Starting VIIPER"):
			log("VIIPER server started")
		case strings.Contains(clean, "API listening"):
			// Extract port for a clean message.
			if idx := strings.LastIndex(clean, ":"); idx >= 0 {
				log("VIIPER API ready on port " + clean[idx+1:])
			} else {
				log("VIIPER API ready")
			}
		}
	}
}

// stripANSI removes ANSI escape codes (CSI sequences like \x1b[90m) from s.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inEscape := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			inEscape = true
			i++ // skip '['
			continue
		}
		if inEscape {
			if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') {
				inEscape = false
			}
			continue
		}
		_ = b.WriteByte(s[i])
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// ping loop
// ---------------------------------------------------------------------------

func waitForServer(ctx context.Context, addr string, timeout time.Duration, log func(string)) error {
	deadline := time.Now().Add(timeout)
	api := viiperclient.New(addr)
	attempt := 0

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempt++
		pingCtx, pingCancel := context.WithTimeout(ctx, 1*time.Second)
		_, err := api.PingCtx(pingCtx)
		pingCancel()

		if err == nil {
			return nil
		}

		// Log every 10th failed attempt so the user sees activity.
		if attempt%10 == 0 {
			log(fmt.Sprintf("ping attempt %d: %v", attempt, err))
		}

		timer := time.NewTimer(serverPollPeriod)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("server ping timed out after %s (%d attempts)", timeout, attempt)
}

// ---------------------------------------------------------------------------
// timeout diagnostics
// ---------------------------------------------------------------------------

func dumpViiperDiagnostics(cmd *exec.Cmd, ring *outputRing, addr, tempDir string, log func(string)) {
	log("--- VIIPER startup diagnostics ---")

	// 1. Process status.
	if cmd.ProcessState != nil {
		log(fmt.Sprintf("Process exited with code %d", cmd.ProcessState.ExitCode()))
	} else if cmd.Process != nil {
		// On Windows we can check the exit code without Wait by
		// polling GetExitCodeProcess, but a simpler approach is to
		// call Wait in a non-blocking way. Since we already know
		// pings failed, the process is either running or dead.
		log("Process still alive (no exit code)")
	} else {
		log("No process handle")
	}

	// 2. Tested address.
	log(fmt.Sprintf("Tested address: %s", addr))

	// 3. Last captured output.
	lines := ring.tail()
	if len(lines) == 0 {
		log("No stdout/stderr captured from viiper.exe")
	} else {
		log(fmt.Sprintf("Last %d viiper lines:", len(lines)))
		for _, l := range lines {
			log(fmt.Sprintf("  | %s", l))
		}
	}

	// 4. Suggested actions.
	var suggestions []string
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		suggestions = append(suggestions, fmt.Sprintf("viiper.exe exited with code %d — check [viiper] logs above for crash details", code))
	}
	if len(lines) == 0 {
		suggestions = append(suggestions, "No output captured — viiper.exe may have failed to start (missing DLL, permissions, or antivirus block)")
	}
	suggestions = append(suggestions, "Run '"+filepath.Join(tempDir, "viiper.exe")+" server' in a terminal to see startup output directly")
	if idx := strings.LastIndex(addr, ":"); idx >= 0 {
		suggestions = append(suggestions, "Check Windows Firewall — port "+addr[idx+1:]+" must be allowed for localhost TCP")
	} else {
		suggestions = append(suggestions, "Check Windows Firewall — port must be allowed for localhost TCP")
	}
	suggestions = append(suggestions, "If port is wrong, update DefaultAPIAddr in runner/internal/timing/timing.go")

	log("Suggested actions:")
	for _, s := range suggestions {
		log("  → " + s)
	}
	log("--- end diagnostics ---")
}

// ---------------------------------------------------------------------------
// process lifecycle
// ---------------------------------------------------------------------------

func stopViiperServerIfStarted() {
	serverMu.Lock()
	startupCancel := serverStartCancel
	serverStartCancel = nil
	pid := serverPID
	started := serverStarted
	cmd := serverCmd
	serverPID = 0
	serverStarted = false
	serverCmd = nil
	dir := viiperTempDir
	viiperTempDir = ""
	serverMu.Unlock()

	if startupCancel != nil {
		startupCancel()
	}
	if !started || pid <= 0 {
		return
	}

	killProcessTree(pid)
	if cmd != nil && cmd.Process != nil {
		// Best-effort wait; process may have already terminated
		_, _ = cmd.Process.Wait()
	}
	_ = os.RemoveAll(dir)
}

func killProcessTree(pid int) {
	if pid <= 0 {
		return
	}
	// Best-effort kill; process may have already exited or be unkillable
	_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func extractViiper() (string, string, error) {
	dir, err := os.MkdirTemp("", "viiper-clicker-*")
	if err != nil {
		return "", "", fmt.Errorf("create temp dir: %w", err)
	}
	path := filepath.Join(dir, "viiper.exe")
	if err := os.WriteFile(path, viiperBin, 0o755); err != nil {
		// Best-effort cleanup on write failure
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("write viiper.exe: %w", err)
	}
	return path, dir, nil
}
