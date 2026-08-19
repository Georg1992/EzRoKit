# EzRoKit

Windows tools suite with a Walk GUI — clicker, autopot, keychain, and timer keys. All input is routed through embedded [VIIPER](https://github.com/Alia5/VIIPER) virtual HID devices.

## Project layout

```
EzRoKit/
  app.exe                       ← dev build output
  app/
    build.ps1                    ← build app.exe
    package.ps1                  ← build user ZIP
    gui/                         ← Walk UI + embedded VIIPER server
    runner/                      ← click loop, autopot, keychain, timer keys
  packaging/
    README.txt / README.ru.txt
    Install.cmd / Uninstall.cmd
  release/                           ← build output only (folder + zip)
    EzRoKit-Windows-x64/
    EzRoKit-Windows-x64.zip
  VIIPER/                        ← git submodule
```

Open **`EzRoKit`** in your editor — not the `VIIPER/` folder alone.

## Prerequisites

- Windows 64-bit
- Go 1.26+ (for building)
- [usbip-win2](https://github.com/vadimgrn/usbip-win2) kernel driver (one-time install + reboot)

The packaged `Install.cmd` installs the driver automatically.

## Build

```powershell
git submodule update --init --recursive
cd app
.\build.ps1
```

Output: `..\app.exe`

## Release package

```powershell
cd app
.\package.ps1
```

Output: `release/EzRoKit-Windows-x64/` and `release/EzRoKit-Windows-x64.zip`

Users extract the ZIP and run `Install.cmd`. See `packaging/README.txt`.

## CI/CD

Every push/PR runs `go vet`, the full test suite, the race detector, and the dead-methods guard on Windows. Pushing a version tag (e.g. `v1.0.0`) additionally builds the release ZIP and uploads it as a workflow artifact:

```powershell
git tag v1.0.0
git push origin v1.0.0
```

## Usage

1. Launch the app — VIIPER starts automatically (takes a few seconds)
2. **Configure keys on any tab** — works anytime VIIPER is running, even before clicking Start
3. Click **Start** to enable tools, then launch your game
4. Hold a trigger key to click, or let AutoPot/KeyChain/TimerKey run
5. Press **End** to toggle tools on/off (VIIPER stays running)
6. Click **Stop** or close the app

### AutoPot tab

Two reading modes are available:

- **Visual (screen capture)** — OCR reads the HP/SP numbers from the in-game
  status panel. If the panel cannot be read, pixel-bar recovery is used
  until OCR finds the panel again. Keep the game visible so the panel/bars
  can be found.
- **Address reading** — reads HP/SP directly from the game process memory.
  Select the game window and a profile, then assign the potion hotkeys.
  Address mode stays in address mode; it does not switch to visual reading.

Set the trigger **%** and assign **hotkeys** for HP and SP potions, then click **Start**. When HP or SP drops below its threshold, the assigned key is pressed until the value recovers.

Status indicator: red **OFF**, green **ON**.

### Click loop

Hold the trigger to spam. Release it to stop. Same loop as a simple AHK
script, through the virtual keyboard and mouse:

- **Mouse enabled:** key tap → mouse click → sleep **Delay ms**
- **Mouse disabled:** key tap → sleep **Delay ms**

A mouse click cannot happen twice in a row, and a failed key write cannot
produce a mouse-only click. If two binds are held, the first one finishes its
cycle (including Delay) before the next one starts.

Releasing the trigger stops that bind. **End / F12** or **Stop** clear
the hold state and stop every tool.

If the VIIPER connection breaks while a key is held, the clicker stops on the
failed action instead of retrying a dead device. Press **End/F12** or **Start**
to rebuild the session and continue.

Default delay: **50 ms**. If a game misses clicks, try **50–100 ms**.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Setup required on Start | Run `Install.cmd`, reboot if prompted |
| Clicks not registered | Start clicker before the game; increase delay |
| Loop never triggers | Check physical trigger key works |
| Clicker keeps going after key release | Press End/F12 or Stop; rebuild if it happens again |
| Clicker stops itself mid-spam | VIIPER input failed — press End/F12 or Start to rebuild the session |

## Source map

| Path | Purpose |
|------|---------|
| `app/gui/main.go` | Main window, VIIPER + tools lifecycle, key binding |
| `app/gui/status_badge.go` | TOOLS status badge (ON / OFF) |
| `app/gui/server.go` | Embedded VIIPER lifecycle |
| `app/gui/viiper_badge.go` | VIIPER server status badge |
| `app/gui/viiper_monitor.go` | VIIPER server health monitor |
| `app/gui/overlay_windows.go` | On-screen HP/SP status overlay |
| `app/gui/runner_control.go` | Shared runner start/stop and key-bind helpers |
| `app/runner/clicker.go` | Click loop (hold trigger to click) |
| `app/runner/autopot/` | AutoPot healing loop (OCR/pixel/address readers) |
| `app/runner/autopot/healer.go` | Potion timing and empty-pot policy |
| `app/runner/autopot/reader_controller.go` | OCR ↔ pixel failover |
| `app/runner/keychain.go` | KeyChain macro runner |
| `app/runner/timer_key.go` | Timer key runner |
| `app/runner/viiper_session.go` | VIIPER session (keyboard + mouse) |
| `packaging/` | Install/Uninstall scripts and user READMEs |
| `release/` | Generated folder + ZIP (`package.ps1`) |
| `.github/workflows/test.yml` | CI tests + tag-triggered release builds |
| `VIIPER/` | Upstream VIIPER (`replace` in `app/go.mod`) |
