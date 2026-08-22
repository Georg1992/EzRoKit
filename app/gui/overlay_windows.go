//go:build windows

package main

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Additional Win32 procs not covered by lxn/win.
var (
	ovlUser32   = windows.NewLazySystemDLL("user32.dll")
	ovlGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	ovlKernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procFillRect                = ovlUser32.NewProc("FillRect")
	procSetLayeredWindowAttribs = ovlUser32.NewProc("SetLayeredWindowAttributes")
	procGetModuleHandleW        = ovlKernel32.NewProc("GetModuleHandleW")
	procCreateSolidBrush        = ovlGdi32.NewProc("CreateSolidBrush")
)

// Supplementary Win32 constants.
const (
	ovlFwBold   = 700 // LOGFONT lfWeight FW_BOLD
	ovlLwaAlpha = 0x00000002
)

// overlayClassName is the Win32 window class name for the overlay.
const overlayClassName = "ToolsStatusOverlay"

// overlayInstances maps HWND → *statusOverlay so the WndProc can access it.
var overlayInstances sync.Map

// ovlWndProcFn is the WndProc callback; stored at package level to prevent GC.
var ovlWndProcFn uintptr

// ovlRegisterOnce ensures the window class is registered exactly once.
var ovlRegisterOnce sync.Once

// statusOverlay is a small semi-transparent click-through window that floats
// above the game and displays a green/red running indicator + current mode
// + HP/SP values when available.
type statusOverlay struct {
	hwnd win.HWND
	font win.HFONT

	mu      sync.Mutex
	running bool   // true = green "Tools ON", false = red "Tools OFF"
	mode    string // e.g. "Pixelsearch", "OCR", "Address reading", "Stopped"

	// HP/SP values shown next to the running indicator.
	// For OCR and Address reading: raw values (hp/hpMax, sp/spMax).
	// For Pixelsearch: percentages (hp=50, hpMax=100, sp=30, spMax=100).
	valuesHP        int
	valuesHPMax     int
	valuesSP        int
	valuesSPMax     int
	valuesLastPaint time.Time // last InvalidateRect from SetValues; rate-limited to ~5 fps

	lastPanelX, lastPanelY, lastPanelW, lastPanelH int
}

// newStatusOverlay creates and returns a hidden overlay window.
// Must be called on the GUI thread.
func newStatusOverlay() (*statusOverlay, error) {
	o := &statusOverlay{}

	var regErr error
	ovlRegisterOnce.Do(func() {
		ovlWndProcFn = syscall.NewCallback(func(hwnd, msg, wParam, lParam uintptr) uintptr {
			switch uint32(msg) {
			case win.WM_PAINT:
				if v, ok := overlayInstances.Load(win.HWND(hwnd)); ok {
					v.(*statusOverlay).onPaint(win.HWND(hwnd))
				}
				return 0
			case win.WM_ERASEBKGND:
				return 1 // prevent default background erase
			case win.WM_DESTROY:
				overlayInstances.Delete(win.HWND(hwnd))
				return 0
			}
			return win.DefWindowProc(win.HWND(hwnd), uint32(msg), wParam, lParam)
		})

		hInst, _, _ := procGetModuleHandleW.Call(0)
		clsName, _ := syscall.UTF16PtrFromString(overlayClassName)
		wc := win.WNDCLASSEX{
			CbSize:        uint32(unsafe.Sizeof(win.WNDCLASSEX{})),
			LpfnWndProc:   ovlWndProcFn,
			HInstance:     win.HINSTANCE(hInst),
			LpszClassName: clsName,
		}
		if win.RegisterClassEx(&wc) == 0 {
			regErr = fmt.Errorf("RegisterClassEx failed")
		}
	})
	if regErr != nil {
		return nil, regErr
	}

	hInst, _, _ := procGetModuleHandleW.Call(0)
	clsName, _ := syscall.UTF16PtrFromString(overlayClassName)
	title, _ := syscall.UTF16PtrFromString("")

	exStyle := uint32(win.WS_EX_TOPMOST | win.WS_EX_LAYERED | win.WS_EX_TRANSPARENT |
		win.WS_EX_NOACTIVATE | win.WS_EX_TOOLWINDOW)
	hwnd := win.CreateWindowEx(
		exStyle, clsName, title,
		win.WS_POPUP,
		5, 60, 230, 32, // 230px — wider to avoid clipping HP/SP values
		0, 0,
		win.HINSTANCE(hInst),
		nil,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("CreateWindowEx failed")
	}

	// 85% opacity overall.
	procSetLayeredWindowAttribs.Call(uintptr(hwnd), 0, 217, ovlLwaAlpha)

	o.hwnd = hwnd
	overlayInstances.Store(hwnd, o)

	// Bold Consolas 8pt.
	lf := win.LOGFONT{LfHeight: -9, LfWeight: ovlFwBold}
	faceUTF16 := windows.StringToUTF16("Consolas")
	copy(lf.LfFaceName[:], faceUTF16)
	o.font = win.CreateFontIndirect(&lf)

	return o, nil
}

// onPaint is called from the WndProc on WM_PAINT.
func (o *statusOverlay) onPaint(hwnd win.HWND) {
	o.mu.Lock()
	running := o.running
	mode := o.mode
	hp, hpMax, sp, spMax := o.valuesHP, o.valuesHPMax, o.valuesSP, o.valuesSPMax
	font := o.font
	o.mu.Unlock()

	var ps win.PAINTSTRUCT
	hdc := win.BeginPaint(hwnd, &ps)
	if hdc == 0 {
		return
	}
	defer win.EndPaint(hwnd, &ps)

	var rc win.RECT
	win.GetClientRect(hwnd, &rc)

	// Background: near-black.
	bgBrush, _, _ := procCreateSolidBrush.Call(uintptr(win.RGB(15, 15, 20)))
	procFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&rc)), bgBrush)
	win.DeleteObject(win.HGDIOBJ(bgBrush))

	oldFont := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldFont)
	win.SetBkMode(hdc, 1) // TRANSPARENT

	// Row 1: ● Tools ON (green) / ● Tools OFF (red) + HP/SP values
	titleText := runningText(running)

	// Format values based on mode:
	// - Pixelsearch: percentages (HP:50% SP:30%)
	// - OCR / Address reading: raw values (HP:5000/10000 SP:3000/6000)
	valuesText := ""
	if hpMax > 0 && spMax > 0 && running {
		if mode == "Pixelsearch" {
			valuesText = fmt.Sprintf(" HP:%d%% SP:%d%%", hp, sp)
		} else {
			valuesText = fmt.Sprintf(" HP:%d/%d SP:%d/%d", hp, hpMax, sp, spMax)
		}
	}

	fullText := titleText + valuesText
	if running {
		win.SetTextColor(hdc, win.RGB(80, 220, 100)) // green
	} else {
		win.SetTextColor(hdc, win.RGB(255, 80, 80)) // red
	}
	fullUTF16, _ := syscall.UTF16PtrFromString(fullText)
	fullLen := int32(len([]rune(fullText)))
	win.TextOut(hdc, 4, 2, fullUTF16, fullLen)

	// Row 2: mode label in brackets, e.g. [Address reading], [Pixelsearch]
	if mode != "" {
		modeText := "[" + mode + "]"
		isAlert := mode != "OCR" && mode != "Pixelsearch" && mode != "Address reading" && mode != "Searching..." && mode != "Stopped"
		if isAlert {
			win.SetTextColor(hdc, win.RGB(255, 160, 70)) // orange for alerts
		} else {
			win.SetTextColor(hdc, win.RGB(180, 180, 190)) // dim grey for normal
		}
		modeUTF16, _ := syscall.UTF16PtrFromString(modeText)
		modeLen := int32(len([]rune(modeText)))
		win.TextOut(hdc, 4, 17, modeUTF16, modeLen)
	}
}

func runningText(running bool) string {
	if running {
		return "● Tools ON"
	}
	return "● Tools OFF"
}

func (o *statusOverlay) sameMode(mode string) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hwnd != 0 && o.running && o.mode == mode
}

func (o *statusOverlay) sameValues(hp, hpMax, sp, spMax int) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hwnd != 0 &&
		o.valuesHP == hp && o.valuesHPMax == hpMax &&
		o.valuesSP == sp && o.valuesSPMax == spMax
}

func (o *statusOverlay) samePanelRect(x, y, w, h int) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hwnd != 0 &&
		o.lastPanelX == x && o.lastPanelY == y &&
		o.lastPanelW == w && o.lastPanelH == h
}

func (o *statusOverlay) valuesCleared() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.hwnd != 0 && o.valuesHP == 0 && o.valuesHPMax == 0 && o.valuesSP == 0 && o.valuesSPMax == 0
}

// SetMode updates the mode label shown in the overlay (e.g. "Pixelsearch", "OCR",
// "Address reading"). Sets running=true. Shows the overlay and repaints immediately.
// Pass "" to hide the label. Safe from any goroutine.
func (o *statusOverlay) SetMode(mode string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	hwnd := o.hwnd
	if hwnd != 0 {
		o.running = true
		o.mode = mode
	}
	o.mu.Unlock()
	if hwnd == 0 {
		return
	}
	win.ShowWindow(hwnd, win.SW_SHOWNOACTIVATE)
	win.InvalidateRect(hwnd, nil, true)
}

// SetValues stores HP/SP values to display next to the running indicator.
// Values are stored as-is — onPaint formats them based on the current mode:
// - Pixelsearch: hp=50 hpMax=100 → "HP:50% SP:30%"
// - OCR/Address: hp=5000 hpMax=10000 → "HP:5000/10000 SP:3000/6000"
// Safe from any goroutine.
//
// InvalidateRect is rate-limited to ~5 fps (once per 200ms) to prevent
// flickering from rapid address-mode polling (10ms per tick). When the
// game window is focused, Windows processes WM_PAINT at full speed and
// 100 fps repaints cause visible text flicker in the overlay.
func (o *statusOverlay) SetValues(hp, hpMax, sp, spMax int) {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.hwnd == 0 {
		o.mu.Unlock()
		return
	}
	hwnd := o.hwnd
	o.valuesHP = hp
	o.valuesHPMax = hpMax
	o.valuesSP = sp
	o.valuesSPMax = spMax
	now := time.Now()
	if now.Sub(o.valuesLastPaint) < 200*time.Millisecond {
		o.mu.Unlock()
		return
	}
	o.valuesLastPaint = now
	o.mu.Unlock()
	win.InvalidateRect(hwnd, nil, true)
}

// ClearValues resets the HP/SP display.
func (o *statusOverlay) ClearValues() {
	if o == nil {
		return
	}
	o.mu.Lock()
	if o.hwnd == 0 {
		o.mu.Unlock()
		return
	}
	hwnd := o.hwnd
	o.valuesHP = 0
	o.valuesHPMax = 0
	o.valuesSP = 0
	o.valuesSPMax = 0
	o.mu.Unlock()
	win.InvalidateRect(hwnd, nil, true)
}

// SetPanelRect repositions the overlay directly below the given panel rect
// (x, y, w, h) with a 3px gap. When panel is empty (w==0), the overlay
// stays at its current position. Safe from any goroutine.
func (o *statusOverlay) SetPanelRect(x, y, w, h int) {
	if o == nil || w == 0 || h == 0 {
		return
	}
	o.mu.Lock()
	hwnd := o.hwnd
	o.lastPanelX, o.lastPanelY, o.lastPanelW, o.lastPanelH = x, y, w, h
	o.mu.Unlock()
	if hwnd == 0 {
		return
	}
	// Position directly below the panel with a 12px gap to avoid overlapping
	// the in-game status panel.
	newY := int32(y + h + 12)
	win.SetWindowPos(hwnd, 0, int32(x), newY, 230, 32, win.SWP_NOACTIVATE|win.SWP_NOZORDER|win.SWP_SHOWWINDOW)
}

// ShowStopped sets running=false and mode="Stopped" so the overlay displays
// "● Tools OFF" in red with "[Stopped]" below. Shows the overlay and repaints
// immediately.
func (o *statusOverlay) ShowStopped() {
	if o == nil {
		return
	}
	o.mu.Lock()
	hwnd := o.hwnd
	if hwnd == 0 {
		o.mu.Unlock()
		return
	}
	o.running = false
	o.mode = "Stopped"
	o.valuesHP = 0
	o.valuesHPMax = 0
	o.valuesSP = 0
	o.valuesSPMax = 0
	o.mu.Unlock()
	win.ShowWindow(hwnd, win.SW_SHOWNOACTIVATE)
	win.InvalidateRect(hwnd, nil, true)
}

// Hide hides the overlay without destroying it.
func (o *statusOverlay) Hide() {
	if o == nil {
		return
	}
	o.mu.Lock()
	hwnd := o.hwnd
	o.mu.Unlock()
	if hwnd != 0 {
		win.ShowWindow(hwnd, win.SW_HIDE)
	}
}

// Destroy releases the overlay window and its GDI resources.
func (o *statusOverlay) Destroy() {
	if o == nil {
		return
	}
	o.mu.Lock()
	font := o.font
	hwnd := o.hwnd
	o.font = 0
	o.hwnd = 0
	o.mu.Unlock()
	if font != 0 {
		win.DeleteObject(win.HGDIOBJ(font))
	}
	if hwnd != 0 {
		win.DestroyWindow(hwnd)
	}
}
