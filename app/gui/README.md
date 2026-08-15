# `app/gui` — Windows GUI (presentation layer)

This package is the **walk-based Windows GUI** for the clicker. It is the
topmost layer of a three-layer architecture; the other two layers live in
sibling packages.

## Layered architecture

```
┌─────────────────────────────────────────────────────────────┐
│  app/gui             ← you are here (presentation)         │
│  walk widgets, tabs, log panel, bind-key flow               │
└────────────────────────┬────────────────────────────────────┘
                         │ imports
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  app/runner           ← PUBLIC FACADE                       │
│  Runner, AutoPotRunner, KeyChainRunner, TimerKeyRunner,     │
│  ViiperSession, InputSession, OpenViiperSession,           │
│  timing constants (PollInterval, KeyBindTimeout, ...),      │
│  slot counts (ClickerSlotCount, KeyChainSlotCount, ...),    │
│  KeyBindTimeout, WaitForKeyPress, KeyName, VKToHID, ...     │
└────────────────────────┬────────────────────────────────────┘
                         │ imports
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  app/runner/autopot, statusui, internal/*, platform/*       │
│  Vision detection, color thresholds, ROI geometry,          │
│  status-bar parsing, goroutine bookkeeping, Windows input.  │
│  Internal — not part of the public API.                     │
└─────────────────────────────────────────────────────────────┘
```

## The import boundary rule

**`app/gui` must only import `app/runner` (the public facade).**
It must never import any of the internal subpackages directly:

- ❌ `ezrokit/runner/autopot`
- ❌ `ezrokit/runner/autopot/statusui`
- ❌ `ezrokit/runner/internal/...`
- ❌ `ezrokit/runner/platform/...`

If you find yourself wanting to import one of these, you have hit a
genuine API gap. **Add the missing surface to `runner` first**, then
import it from the GUI. The internal packages can move freely
(rename, split, merge) as long as `runner` re-exports the public bits.

## Why this matters

1. **Decoupling.** A color threshold tune in the `autopot` package
   (e.g. `hpFillMinGreen` 35 → 40) must require zero changes here. If
   the GUI re-implements detection, every tweak becomes a cross-package
   edit.
2. **Refactor freedom.** The internal subpackages are allowed to
   reorganize freely. Only `runner` is the stability contract.
3. **Testability.** The GUI tests should not need the vision stack to
   exercise the bind-key flow, the start/stop lifecycle, or the log
   panel.

## What lives in each `app/gui` file

| File | Role |
|---|---|
| `main.go` | `guiApp` struct, `main()`, shutdown, tool lifecycle |
| `logging.go` | Persistent GUI log, bounded log model, and log-trimmer lifecycle |
| `viiper_lifecycle.go` | Cancellable VIIPER startup and session wiring |
| `control_panel.go` | Top Start/Stop buttons + status badge + tool profiles |
| `tool_profiles.go` | Persistent profiles for all tool bindings and settings |
| `clicker_tab.go` | Clicker slot rows, trigger-key binding, delay config |
| `autopot_tab.go` | HP/SP enable, threshold, hotkey binding |
| `keychain_tab.go` | KeyChain slot rows + hotkey binding |
| `timer_key_tab.go` | Timer-key slot rows + interval + hotkey binding |
| `runner_control.go` | Shared helpers: `makeLifecycleSlot`, `startLifecycle`, `bindKeyFlow` |
| `server.go` | Embedded VIIPER HTTP server start/stop |
| `status_badge.go` | Stopped/Running pill widget |
| `keychain_arrows.go` | Arrow icons on the KeyChain tab |
| `branding.go` | EzRoKit window icon |
| `style.go` | Shared styling: gray hints, aligned labels, fixed-width buttons |
| `prereq_windows.go` | Windows-specific prereq checks |
| `app.manifest` | Windows manifest (HiDPI, etc.) |
| `embed/` | Embedded resources |

## Adding a new feature

1. **Pure GUI change** (new tab, new widget, new layout): stay in
   `app/gui`. No new imports needed.
2. **Needs a new runner capability** (e.g. a new config field exposed
   to the user): add the field to the relevant config struct in
   `runner/*` and re-export through `runner` if it isn't already.
3. **Needs a new detection primitive** (e.g. a new color threshold
   the GUI should display): add it to the `autopot` package,
   re-export from `runner`, then consume the re-export here.
4. **Never** copy a detection threshold into a `app/gui` file as
   a literal. Always go through the constant.

## How to verify the boundary

A one-liner that should return **zero** results when the boundary is
intact:

```bash
# Should print nothing.
grep -rn 'ezrokit/runner/' --include='*.go' app/gui/ \
  | grep -v 'ezrokit/runner"' \
  | grep -v 'ezrokit/runner/auto' \
  | grep -v 'ezrokit/runner/status' \
  | grep -v 'ezrokit/runner/internal' \
  | grep -v 'ezrokit/runner/platform'
```

(Replace the grep with the exact import paths you forbid.)
