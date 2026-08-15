//go:build windows

package main

import (
	"image"
	"image/color"
)

// ezrokitIconImage draws the EzRoKit "EZ" monogram as block letters.
func ezrokitIconImage() image.Image {
	const w, h = 54, 36
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bg := color.RGBA{R: 0x16, G: 0x1E, B: 0x2A, A: 255} // dark slate
	fg := color.RGBA{R: 0xED, G: 0xF2, B: 0xF7, A: 255} // near white
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, bg)
		}
	}

	drawLetter := func(pattern []string, x0, y0, cell int, c color.Color) {
		for r, row := range pattern {
			for col, ch := range row {
				if ch != '#' {
					continue
				}
				for dy := 0; dy < cell; dy++ {
					for dx := 0; dx < cell; dx++ {
						img.Set(x0+col*cell+dx, y0+r*cell+dy, c)
					}
				}
			}
		}
	}

	const cell = 4
	// "E"
	drawLetter([]string{
		"#####",
		"#....",
		"#####",
		"#....",
		"#####",
	}, 4, 8, cell, fg)
	// "Z"
	drawLetter([]string{
		"#####",
		"...##",
		"..##.",
		".##..",
		"#####",
	}, 30, 8, cell, fg)
	return img
}
