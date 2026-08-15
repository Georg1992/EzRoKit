//go:build windows

package main

import (
	"github.com/lxn/walk"
)

type toolsStatus int

const (
	toolsStatusStopped toolsStatus = iota
	toolsStatusRunning
)

var toolsColors = map[toolsStatus]walk.Color{
	toolsStatusStopped: walk.RGB(220, 53, 53),
	toolsStatusRunning: walk.RGB(46, 184, 70),
}

var toolsTexts = map[toolsStatus]string{
	toolsStatusStopped: "TOOLS OFF",
	toolsStatusRunning: "TOOLS ON",
}

const toolsBadgeWidth = 130
const toolsBadgeHeight = 40

type toolsBadge struct {
	*walk.CustomWidget
	status toolsStatus
	font   *walk.Font
}

func newToolsBadge(parent walk.Container) (*toolsBadge, error) {
	font, err := walk.NewFont("Segoe UI", 14, walk.FontBold)
	if err != nil {
		return nil, err
	}

	badge := &toolsBadge{
		status: toolsStatusStopped,
		font:   font,
	}
	cw, err := walk.NewCustomWidgetPixels(parent, 0, badge.paint)
	if err != nil {
		font.Dispose()
		return nil, err
	}
	cw.SetPaintMode(walk.PaintBuffered)
	badge.CustomWidget = cw
	if err := cw.SetMinMaxSize(
		walk.Size{Width: toolsBadgeWidth, Height: toolsBadgeHeight},
		walk.Size{Width: toolsBadgeWidth, Height: toolsBadgeHeight},
	); err != nil {
		font.Dispose()
		return nil, err
	}
	return badge, nil
}

func (b *toolsBadge) paint(canvas *walk.Canvas, bounds walk.Rectangle) error {
	color := toolsColors[b.status]
	brush, err := walk.NewSolidColorBrush(color)
	if err != nil {
		return err
	}
	defer brush.Dispose()

	// Rounded corners + a slightly darker border for a softer, badge-like look.
	radius := walk.Size{Width: 8, Height: 8}
	if err := canvas.FillRoundedRectanglePixels(brush, bounds, radius); err != nil {
		return err
	}
	borderColor := darkenColor(color, 40)
	if pen, err := walk.NewCosmeticPen(walk.PenSolid, borderColor); err == nil {
		_ = canvas.DrawRoundedRectanglePixels(pen, bounds, radius)
		pen.Dispose()
	}

	return canvas.DrawTextPixels(
		toolsTexts[b.status],
		b.font,
		walk.RGB(255, 255, 255),
		bounds,
		walk.TextCenter|walk.TextVCenter,
	)
}

// darkenColor returns a darker shade of c by subtracting d from each channel.
func darkenColor(c walk.Color, d byte) walk.Color {
	return walk.RGB(
		maxByte(0, int(c.R())-int(d)),
		maxByte(0, int(c.G())-int(d)),
		maxByte(0, int(c.B())-int(d)),
	)
}

func maxByte(a, b int) byte {
	if a > b {
		return byte(a)
	}
	return byte(b)
}

func (b *toolsBadge) SetStatus(status toolsStatus) {
	if b.status == status {
		return
	}
	b.status = status
	b.Invalidate()
}
