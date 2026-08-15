//go:build windows

package main

import (
	"github.com/lxn/walk"
)

// Shared style helpers for a consistent, readable layout: small gray
// hint labels, right-aligned fixed-width field labels (so inputs line
// up in columns across rows), and fixed-width buttons.

// newHint creates a small gray helper label under a section.
func newHint(parent walk.Container, text string) (*walk.Label, error) {
	l, err := walk.NewLabel(parent)
	if err != nil {
		return nil, err
	}
	if err := l.SetText(text); err != nil {
		return nil, err
	}
	font, err := walk.NewFont("Segoe UI", 8, 0)
	if err != nil {
		return nil, err
	}
	l.SetFont(font)
	l.SetTextColor(walk.RGB(100, 100, 100))
	return l, nil
}

// newFieldLabel creates a right-aligned label with a fixed width so the
// input that follows it starts at the same column on every row.
func newFieldLabel(parent walk.Container, text string, width int) (*walk.Label, error) {
	l, err := walk.NewLabel(parent)
	if err != nil {
		return nil, err
	}
	if err := l.SetText(text); err != nil {
		return nil, err
	}
	if err := l.SetMinMaxSize(walk.Size{Width: width, Height: 0}, walk.Size{Width: width, Height: 0}); err != nil {
		return nil, err
	}
	if err := l.SetTextAlignment(walk.AlignFar); err != nil {
		return nil, err
	}
	return l, nil
}

// newFixedButton creates a push button with a fixed width so buttons
// line up across rows.
func newFixedButton(parent walk.Container, text string, width int) (*walk.PushButton, error) {
	b, err := walk.NewPushButton(parent)
	if err != nil {
		return nil, err
	}
	if err := b.SetText(text); err != nil {
		return nil, err
	}
	if err := b.SetMinMaxSize(walk.Size{Width: width, Height: 0}, walk.Size{Width: width, Height: 0}); err != nil {
		return nil, err
	}
	return b, nil
}
