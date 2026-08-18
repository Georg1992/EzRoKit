//go:build windows

package main

import (
	"math"
	"sync"

	"github.com/lxn/walk"
)

const (
	keyChainKeyFieldWidth   = 56
	keyChainDelayFieldWidth = 80
	keyChainStepWidth       = keyChainDelayFieldWidth
	keyChainHeaderHeight    = 16
	keyChainFieldHeight     = 22
	keyChainDownHeight      = 18
	keyChainLinkWidth       = 20
	keyChainLabelColWidth   = 70
	keyChainDelayMaxMs      = 999999
	keyChainScrollMaxWidth  = 9999
	// Scroll area sized for about one full switch; more switches scroll.
	keyChainScrollMinHeight = 176
	keyChainScrollMaxHeight = 260
)

var (
	keyChainArrowColor   = walk.RGB(110, 110, 110)
	keyChainTriggerColor = walk.RGB(46, 184, 70)
)

var (
	keyChainSurfaceBrush     walk.Brush
	keyChainSurfaceOnce      sync.Once
	keyChainTriggerBrush     walk.Brush
	keyChainTriggerBrushOnce sync.Once
	keyChainTriggerFont      *walk.Font
	keyChainTriggerFontOnce  sync.Once
)

func keyChainSurface() walk.Brush {
	keyChainSurfaceOnce.Do(func() {
		keyChainSurfaceBrush, _ = walk.NewSystemColorBrush(walk.SysColorBtnFace)
	})
	return keyChainSurfaceBrush
}

func applyKeyChainSurface(w walk.Window) {
	w.SetBackground(keyChainSurface())
}

func addKeyChainStepHeader(parent walk.Container, trigger bool) error {
	if !trigger {
		spacer, err := walk.NewComposite(parent)
		if err != nil {
			return err
		}
		if err := spacer.SetMinMaxSize(
			walk.Size{Width: keyChainStepWidth, Height: keyChainHeaderHeight},
			walk.Size{Width: keyChainStepWidth, Height: keyChainHeaderHeight},
		); err != nil {
			return err
		}
		applyKeyChainSurface(spacer)
		return nil
	}

	label, err := walk.NewLabel(parent)
	if err != nil {
		return err
	}
	if err := label.SetText("Trigger"); err != nil {
		return err
	}
	if err := label.SetMinMaxSize(
		walk.Size{Width: keyChainStepWidth, Height: keyChainHeaderHeight},
		walk.Size{Width: keyChainStepWidth, Height: keyChainHeaderHeight},
	); err != nil {
		return err
	}
	if err := label.SetTextAlignment(walk.AlignCenter); err != nil {
		return err
	}
	label.SetTextColor(keyChainTriggerColor)
	if font := keyChainTriggerLabelFont(); font != nil {
		label.SetFont(font)
	}
	applyKeyChainSurface(label)
	return nil
}

func styleKeyChainTriggerField(edit *walk.LineEdit) {
	edit.SetTextColor(keyChainTriggerColor)
	edit.SetBackground(keyChainTriggerBackground())
	_ = edit.SetToolTipText("Trigger key — tap once to run the chain, hold to loop")
}

func keyChainTriggerBackground() walk.Brush {
	keyChainTriggerBrushOnce.Do(func() {
		keyChainTriggerBrush, _ = walk.NewSolidColorBrush(walk.RGB(210, 242, 217))
	})
	return keyChainTriggerBrush
}

func keyChainTriggerLabelFont() *walk.Font {
	keyChainTriggerFontOnce.Do(func() {
		keyChainTriggerFont, _ = walk.NewFont("Segoe UI", 8, walk.FontBold)
	})
	return keyChainTriggerFont
}

func keyChainStepHeight() int {
	return keyChainHeaderHeight + keyChainFieldHeight + keyChainDownHeight + keyChainFieldHeight
}

func keyChainKeyCenterY(bounds walk.Rectangle) int {
	keyMid := float64(keyChainHeaderHeight) + float64(keyChainFieldHeight)/2.0
	return int(float64(bounds.Height) * keyMid / float64(keyChainStepHeight()))
}

func keyChainDelayCenterY(bounds walk.Rectangle) int {
	top := float64(keyChainHeaderHeight + keyChainFieldHeight + keyChainDownHeight)
	return int(float64(bounds.Height) * (top + float64(keyChainFieldHeight)/2.0) / float64(keyChainStepHeight()))
}

func newKeyChainPen() (walk.Pen, error) {
	return walk.NewCosmeticPen(walk.PenSolid, keyChainArrowColor)
}

func drawArrowHead(canvas *walk.Canvas, pen walk.Pen, tip, from walk.Point) error {
	dx := float64(tip.X - from.X)
	dy := float64(tip.Y - from.Y)
	length := math.Hypot(dx, dy)
	if length < 1 {
		return nil
	}
	dx /= length
	dy /= length
	perpX := -dy
	perpY := dx
	size := 3.5
	p1 := walk.Point{
		X: tip.X - int(dx*size+perpX*size*0.6),
		Y: tip.Y - int(dy*size+perpY*size*0.6),
	}
	p2 := walk.Point{
		X: tip.X - int(dx*size-perpX*size*0.6),
		Y: tip.Y - int(dy*size-perpY*size*0.6),
	}
	if err := canvas.DrawLinePixels(pen, tip, p1); err != nil {
		return err
	}
	return canvas.DrawLinePixels(pen, tip, p2)
}

func drawLineArrow(canvas *walk.Canvas, pen walk.Pen, from, to walk.Point) error {
	if err := canvas.DrawLinePixels(pen, from, to); err != nil {
		return err
	}
	return drawArrowHead(canvas, pen, to, from)
}

func fillKeyChainSurface(canvas *walk.Canvas, bounds walk.Rectangle) error {
	return canvas.FillRectanglePixels(keyChainSurface(), bounds)
}

func initKeyChainArrowWidget(w *walk.CustomWidget) {
	applyKeyChainSurface(w)
	w.SetPaintMode(walk.PaintNoErase)
	w.SetInvalidatesOnResize(true)
}

func newKeyChainDownArrow(parent walk.Container) (*walk.CustomWidget, error) {
	w, err := walk.NewCustomWidgetPixels(parent, 0, func(canvas *walk.Canvas, bounds walk.Rectangle) error {
		if err := fillKeyChainSurface(canvas, bounds); err != nil {
			return err
		}

		pen, err := newKeyChainPen()
		if err != nil {
			return err
		}
		defer pen.Dispose()

		cx := bounds.Width / 2
		from := walk.Point{X: cx, Y: 0}
		to := walk.Point{X: cx, Y: bounds.Height - 1}
		return drawLineArrow(canvas, pen, from, to)
	})
	if err != nil {
		return nil, err
	}
	initKeyChainArrowWidget(w)
	return w, nil
}

func newKeyChainStepLink(parent walk.Container) (*walk.CustomWidget, error) {
	w, err := walk.NewCustomWidgetPixels(parent, 0, func(canvas *walk.Canvas, bounds walk.Rectangle) error {
		if err := fillKeyChainSurface(canvas, bounds); err != nil {
			return err
		}

		pen, err := newKeyChainPen()
		if err != nil {
			return err
		}
		defer pen.Dispose()

		w := bounds.Width
		keyY := keyChainKeyCenterY(bounds)
		delayY := keyChainDelayCenterY(bounds)
		midX := w * 2 / 5

		if err := canvas.DrawLinePixels(pen, walk.Point{X: 0, Y: keyY}, walk.Point{X: w, Y: keyY}); err != nil {
			return err
		}

		points := []walk.Point{
			{X: 0, Y: delayY},
			{X: midX, Y: delayY},
			{X: midX, Y: keyY},
			{X: w, Y: keyY},
		}
		if err := canvas.DrawPolylinePixels(pen, points); err != nil {
			return err
		}
		return drawArrowHead(canvas, pen, walk.Point{X: w, Y: keyY}, walk.Point{X: midX, Y: keyY})
	})
	if err != nil {
		return nil, err
	}
	initKeyChainArrowWidget(w)
	return w, nil
}
