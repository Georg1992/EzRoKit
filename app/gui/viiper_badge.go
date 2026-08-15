//go:build windows

package main

import (
	"github.com/lxn/walk"
)

type viiperStatus int

const (
	viiperInactive viiperStatus = iota
	viiperActive
)

var viiperColors = map[viiperStatus]walk.Color{
	viiperInactive: walk.RGB(220, 53, 53), // red
	viiperActive:   walk.RGB(46, 184, 70), // green
}

var viiperTexts = map[viiperStatus]string{
	viiperInactive: "VIIPER OFF",
	viiperActive:   "VIIPER ON",
}

const viiperBadgeWidth = 120
const viiperBadgeHeight = 40

type viiperBadge struct {
	*walk.CustomWidget
	status viiperStatus
	font   *walk.Font
}

func newViiperBadge(parent walk.Container) (*viiperBadge, error) {
	font, err := walk.NewFont("Segoe UI", 14, walk.FontBold)
	if err != nil {
		return nil, err
	}

	badge := &viiperBadge{
		status: viiperInactive,
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
		walk.Size{Width: viiperBadgeWidth, Height: viiperBadgeHeight},
		walk.Size{Width: viiperBadgeWidth, Height: viiperBadgeHeight},
	); err != nil {
		font.Dispose()
		return nil, err
	}
	return badge, nil
}

func (b *viiperBadge) paint(canvas *walk.Canvas, bounds walk.Rectangle) error {
	color := viiperColors[b.status]
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
		viiperTexts[b.status],
		b.font,
		walk.RGB(255, 255, 255),
		bounds,
		walk.TextCenter|walk.TextVCenter,
	)
}

func (b *viiperBadge) SetStatus(status viiperStatus) {
	if b.status == status {
		return
	}
	b.status = status
	b.Invalidate()
}
