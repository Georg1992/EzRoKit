# EzRoKit

Windows tool suite for **Ragnarok Online**. It is designed to work on **every Ragnarok server** — official, private, renewal, or pre-renewal — because it talks to the game the same way you do: keys and clicks through a virtual HID keyboard/mouse, and HP/SP from the on-screen status panel.

Input is routed through embedded [VIIPER](https://github.com/Alia5/VIIPER). No server-specific packets, bots, or injected game DLLs.

## Tools

| Tab | What it does |
|-----|----------------|
| **Clicker** | Hold a trigger key to spam a key tap (optional mouse click) on a delay |
| **AutoPot** | Press your HP/SP potion keys when the bars drop below a threshold |
| **KeyChain** | Hold or tap a trigger to play a sequence of keys |
| **Timer keys** | Press a mapped key once every interval |

A mapped key is enabled. Clear the bind to turn that slot off.

## AutoPot on any server

**Visual** is the mode that works everywhere. It reads the standard Ragnarok HP/SP status panel from the screen (OCR on the numbers, pixel fill on the bars). Keep the game window visible.

**Address** reads HP/SP from the selected client process. Use it only when a matching memory profile exists for that client; it does not switch back to visual mode.

Set the HP/SP **%** thresholds, bind potion hotkeys, then **Start**.

Status: red **OFF**, green **ON**. An overlay can show the last HP/SP read.

## Usage

1. Launch EzRoKit — VIIPER starts automatically (a few seconds).
2. Bind keys on any tab while VIIPER is running (Start is not required).
3. Click **Start**, then launch the game.
4. Hold a clicker/keychain trigger, or let AutoPot and timer keys run.
5. **End** or **F12** toggles tools on/off (VIIPER stays up).
6. **Stop** or close the app to shut everything down.

### Clicker

Hold the trigger to spam. Release to stop.

- **Mouse on:** key tap → mouse click → sleep **Delay ms**
- **Mouse off:** key tap → sleep **Delay ms**

A mouse click cannot fire twice in a row. If two binds are held, the first finishes its cycle (including delay) before the next starts. **End / F12** or **Stop** clears the hold.

Default delay: **50 ms**. If the game misses clicks, try **50–100 ms**.

If VIIPER drops while a key is held, the clicker stops on the failed action. Press **End/F12** or **Start** to rebuild the session.

### KeyChain

The first key in a chain is the trigger. Tap it for one full pass (A→B→C). Hold it to loop the full pass. Releasing finishes the current pass; it does not cut mid-sequence.

### Timer keys

Each mapped timer presses its key once per interval. Keyboard only — independent of the clicker.

## Prerequisites

- Windows 64-bit
- Go 1.26+ (to build from source)
- [usbip-win2](https://github.com/vadimgrn/usbip-win2) kernel driver (one-time install + reboot)

The packaged `Install.cmd` installs the driver.

## Build

```powershell
git submodule update --init --recursive
cd app
.\build.ps1
```

Output: `..\app.exe`

Open the **EzRoKit** repo in your editor — not the `VIIPER/` submodule alone.

## Release package

```powershell
cd app
.\package.ps1
```

Output: `release/EzRoKit-Windows-x64/` and `release/EzRoKit-Windows-x64.zip`

Extract the ZIP and run `Install.cmd`. See `packaging/README.txt`.

## CI/CD

Every push/PR runs `go vet`, the test suite, the race detector, and the dead-methods guard on Windows. A version tag (e.g. `v1.0.0`) also builds the release ZIP:

```powershell
git tag v1.0.0
git push origin v1.0.0
```

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| Setup required on Start | Run `Install.cmd`, reboot if prompted |
| Clicks not registered | Start tools before the game; increase clicker delay |
| Loop never triggers | Check the physical trigger key |
| Clicker keeps going after release | Press End/F12 or Stop |
| Clicker stops mid-spam | VIIPER input failed — End/F12 or Start to rebuild |
| AutoPot does nothing | Bind an HP or SP key; keep the status panel visible in Visual mode |

## Project layout

```
EzRoKit/
  app.exe                       ← dev build output
  app/
    build.ps1                   ← build app.exe
    package.ps1                 ← build user ZIP
    gui/                        ← Walk UI + embedded VIIPER server
    runner/                     ← clicker, autopot, keychain, timer keys
  packaging/
    README.txt / README.ru.txt
    Install.cmd / Uninstall.cmd
  release/                      ← generated folder + zip
  VIIPER/                       ← git submodule
```

| Path | Purpose |
|------|---------|
| `app/gui/` | Main window, tabs, overlay, VIIPER lifecycle |
| `app/runner/clicker.go` | Click loop |
| `app/runner/autopot/` | AutoPot (visual OCR/pixel and address readers) |
| `app/runner/keychain.go` | KeyChain macros |
| `app/runner/timer_key.go` | Interval key presses |
| `packaging/` | Install/Uninstall and user READMEs |
| `.github/workflows/test.yml` | CI tests + tag-triggered release builds |
| `VIIPER/` | Upstream VIIPER (`replace` in `app/go.mod`) |
