# BELARUS CHAMP TOOLS

Windows tools suite with a Walk GUI — clicker, autopot, keychain, and timer keys. All input is routed through embedded [VIIPER](https://github.com/Alia5/VIIPER) virtual HID devices.

## Project layout

```
Belarus_Champ_Tools/
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
    BelarusChampTools-Windows-x64/
    BelarusChampTools-Windows-x64.zip
  VIIPER/                        ← git submodule
```

Open **`Belarus_Champ_Tools`** in your editor — not the `VIIPER/` folder alone.

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

Output: `release/BelarusChampTools-Windows-x64/` and `release/BelarusChampTools-Windows-x64.zip`

Users extract the ZIP and run `Install.cmd`. See `packaging/README.txt`.

## Usage

1. Launch the app — VIIPER starts automatically (takes a few seconds)
2. **Configure keys on any tab** — works anytime VIIPER is running, even before clicking Start
3. Click **Start** to enable tools, then launch your game
4. Hold a trigger key to click, or let AutoPot/KeyChain/TimerKey run
5. Press **End** to toggle tools on/off (VIIPER stays running)
6. Click **Stop** or close the app

### AutoPot tab

1. Keep the game visible with your character near **screen center** (green HP / blue SP bars under the sprite)
2. Set trigger **%** and assign **hotkeys** for HP and SP potions
3. Click **Start**

Bars under the character are detected by color in a small center region. When HP or SP drops below the threshold, the assigned key is pressed until the bar recovers.

Set `BAR_SEARCH_DEBUG=1` to save a `bar_search_debug.png` crop for calibration.

Status indicator: red **OFF**, green **ON**.

### Click loop

While the trigger key is held, each bind repeats one of these exact flows:

- **Mouse enabled:** virtual key click (held ~20 ms) → virtual mouse click
  (short, hardened hold) → sleep for **Delay ms**
- **Mouse disabled:** virtual key click (held ~20 ms) → sleep for **Delay ms**

The Delay setting is always after the final action. Each assigned key has its
own clicker state. If multiple keys are held, the first key observed pressed
gets priority; the others wait without firing until the active key is
released, then the earliest waiting key takes over. A failed key write cannot
produce a mouse-only click.

Default delay: **50 ms**. If a game misses clicks, try **50–100 ms**.

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Setup required on Start | Run `Install.cmd`, reboot if prompted |
| Clicks not registered | Start clicker before the game; increase delay |
| Loop never triggers | Check physical trigger key works |

## Source map

| Path | Purpose |
|------|---------|
| `app/gui/main.go` | Main window, VIIPER + tools lifecycle, key binding |
| `app/gui/status_badge.go` | TOOLS status badge (ON / OFF) |
| `app/gui/server.go` | Embedded VIIPER lifecycle |
| `app/gui/viiper_badge.go` | VIIPER server status badge |
| `app/gui/viiper_monitor.go` | VIIPER server health monitor |
| `app/runner/clicker.go` | Click loop (hold trigger to click) |
| `app/runner/autopot/` | AutoPot healing loop |
| `app/runner/keychain.go` | KeyChain macro runner |
| `app/runner/timer_key.go` | Timer key runner |
| `app/runner/viiper_session.go` | VIIPER session (keyboard + mouse) |
| `packaging/` | Install/Uninstall scripts and user READMEs |
| `release/` | Generated folder + ZIP (`package.ps1`) |
| `VIIPER/` | Upstream VIIPER (`replace` in `app/go.mod`) |
