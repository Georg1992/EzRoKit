package statusui

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"sync"
)

//go:embed assets/StatusPanel.png
var statusPanelTemplatePNG []byte

var (
	statusPanelTemplateOnce sync.Once
	statusPanelTemplate     image.Image
)

// DefaultStatusPanelTemplate returns the embedded StatusPanel.png decoded as
// an image.Image. Loaded once on first call; safe for concurrent use.
//
// The template is embedded into the binary so the recognition pipeline
// doesn't depend on a runtime file path. Callers that want to ship their
// own template can still pass any image.Image to FindStatusPanel directly.
func DefaultStatusPanelTemplate() image.Image {
	statusPanelTemplateOnce.Do(func() {
		if len(statusPanelTemplatePNG) == 0 {
			return
		}
		img, err := png.Decode(bytes.NewReader(statusPanelTemplatePNG))
		if err == nil {
			statusPanelTemplate = img
		}
	})
	return statusPanelTemplate
}

// FindStatusPanelOptions configures the panel search.
type FindStatusPanelOptions struct {
	// TopLeftRegion is the first region to scan, in screen coordinates.
	// Defaults to image.Rect(0, 0, 400, 200) when empty — the
	// typical Ragnarok status window location in the upper-left.
	TopLeftRegion image.Rectangle
	// MaxScore is the maximum acceptable SAD score (0 = perfect,
	// 1 = worst). Defaults to 0.45 when zero. 0.45 sits well above
	// the worst top-left-panel SAD observed in the regression set
	// (≈0.25 on captures with content drift) and below
	// false-positive SAD observed on flat unrelated regions
	// (≈0.99) while allowing nearby UI elements (skill hotbar,
	// chat window trim, etc.) that slightly raise the SAD score
	// to still pass through to VerifyPanel.
	MaxScore float64
}

// FindStatusPanel searches for the status panel inside img using
// grayscale sum-of-absolute-differences (SAD) against the template.
//
// The search uses a fast two-phase approach:
//
//  1. Top-left region (default 400×200) at stride=1. The Ragnarok status
//     panel lives in the upper-left corner of the screen in most cases,
//     so this finds it immediately with pixel accuracy in a few ms.
//
//  2. Full-screen at stride=8 as fallback. Scanning every 8th pixel
//     position evaluates ~27K positions instead of ~1.7M — ~60× fewer
//     than a pixel-accurate full scan — and completes in ~100ms. The
//     best coarse position is refined with stride=1 in a ±8px
//     neighborhood for pixel-accurate placement.
//
// For *image.RGBA (the format produced by the screen-capture path)
// the hot loop accesses the underlying Pix slice directly rather
// than going through image.At(); for any other format it falls
// back to At().
//
// Returns the best match location, the normalized 0..1 score (0 =
// perfect, 1 = worst), and ok=true iff the best score is at or
// below MaxScore. If no match is found, rect is the zero
// Rectangle and ok=false.
func FindStatusPanel(img, template image.Image, opts FindStatusPanelOptions) (image.Rectangle, float64, bool) {
	maxScore := opts.MaxScore
	if maxScore == 0 {
		maxScore = 0.45
	}
	tb := template.Bounds()

	// Phase 1: top-left region at stride=1 (common case, instant).
	region := opts.TopLeftRegion
	if region.Empty() {
		region = image.Rect(0, 0, 400, 200)
	}
	rect, score, ok := findPanelInRegion(img, template, region, maxScore, 1)
	if ok {
		return rect, score, true
	}

	// Phase 2: full-screen at stride=8 (coarse scan, ~60× fewer positions).
	fullRegion := img.Bounds()
	coarseRect, coarseScore, found := findPanelInRegion(img, template, fullRegion, maxScore, 8)
	if !found {
		return image.Rectangle{}, coarseScore, false
	}

	// Phase 3: refine at stride=1 in ±8px neighborhood around coarse best.
	radius := 8
	refineRegion := image.Rect(
		coarseRect.Min.X-radius,
		coarseRect.Min.Y-radius,
		coarseRect.Min.X+radius+tb.Dx(),
		coarseRect.Min.Y+radius+tb.Dy(),
	)
	return findPanelInRegion(img, template, refineRegion, maxScore, 1)
}

// panelBoundaryOverflowPx is the maximum number of pixels the template may
// extend beyond the top or left image boundary during the panel search.
// This allows panels partially clipped at the screen top-left corner to be
// found at the correct position. The value is kept small (20 px) to prevent
// false-positive matches from tiny visible slivers far outside the image.
const panelBoundaryOverflowPx = 20

// findPanelInRegion scans the given region of img at the given stride
// against the template and returns the lowest-SAD match below maxScore,
// or ok=false if no match qualifies.
//
// Stride controls the sampling density: 1 = every pixel position (slowest,
// highest accuracy), 8 = every 8th position (~60× faster, used for coarse
// full-screen searches). The best match is refined at stride=1 in a small
// neighborhood by the caller (FindStatusPanel).
//
// The search min boundary is extended by panelBoundaryOverflowPx so a panel
// partially clipped at the screen top-left is found at its real position.
// Only the visible intersection is compared; the SAD is normalized by that
// area so a partly-clipped match competes fairly with fully-visible candidates.
func findPanelInRegion(img, template image.Image, region image.Rectangle, maxScore float64, stride int) (image.Rectangle, float64, bool) {
	ib := img.Bounds()
	tb := template.Bounds()
	tw, th := tb.Dx(), tb.Dy()
	if tw <= 0 || th <= 0 {
		return image.Rectangle{}, 0, false
	}

	region = region.Intersect(ib)
	// Extend the search start upward by up to panelBoundaryOverflowPx so a
	// panel partially clipped above the screen top is found at its real
	// position. minX is NOT extended: allowing the template to start before
	// the image left edge creates false positives where a small sliver of
	// the left screen content happens to match the template well enough to
	// beat a genuine right-side panel. The typical clipped-panel case
	// (window flush with display top) only needs a top overflow.
	minX := region.Min.X
	minY := region.Min.Y - panelBoundaryOverflowPx
	if minY < region.Min.Y-(th-1) {
		minY = region.Min.Y - (th - 1)
	}
	maxX := region.Max.X - tw
	maxY := region.Max.Y - th
	if maxX < minX || maxY < minY {
		return image.Rectangle{}, 0, false
	}
	// Minimum visible pixels: 75% of the full template. Positions with less
	// visible area are skipped — tiny slivers can't produce reliable scores.
	minVisPx := int(float64(tw*th) * 0.75)

	tplGray := precomputeGrayscale(template, tb)

	imgGray, fastOK := precomputeImageGrayscale(img, ib)

	// sadPartial computes the SAD over only the rows and columns where the
	// template overlaps the image. Normalizes by the pixel count of that
	// visible intersection so scores remain in the same 0..1 range as a
	// full-template match.
	sadPartial := func(x0, y0 int, earlyExit float64) float64 {
		return computeSadPartial(ib, tw, th, minVisPx, fastOK, imgGray, tplGray, img, x0, y0, earlyExit)
	}

	maxPossible := float64(tw*th) * 255.0
	bestScore := maxPossible + 1
	bestX, bestY := minX, minY
scan:
	for y := minY; y <= maxY; y += stride {
		for x := minX; x <= maxX; x += stride {
			score := sadPartial(x, y, bestScore)
			if score < bestScore {
				bestScore = score
				bestX, bestY = x, y
				if bestScore == 0 {
					break scan
				}
			}
		}
	}

	normalized := bestScore / maxPossible
	if normalized > maxScore {
		return image.Rectangle{}, normalized, false
	}
	return image.Rect(bestX, bestY, bestX+tw, bestY+th), normalized, true
}

// computeSadPartial computes the SAD for a single candidate position (x0, y0)
// accounting for only the visible portion of the template that overlaps the
// image. Returns a normalized score on the full-template scale.
func computeSadPartial(ib image.Rectangle, tw, th, minVisPx int, fastOK bool, imgGray, tplGray [][]uint8, img image.Image, x0, y0 int, earlyExit float64) float64 {
	dyStart := ib.Min.Y - y0
	if dyStart < 0 {
		dyStart = 0
	}
	dyEnd := ib.Max.Y - y0
	if dyEnd > th {
		dyEnd = th
	}
	dxStart := ib.Min.X - x0
	if dxStart < 0 {
		dxStart = 0
	}
	dxEnd := ib.Max.X - x0
	if dxEnd > tw {
		dxEnd = tw
	}
	visH := dyEnd - dyStart
	visW := dxEnd - dxStart
	if visH <= 0 || visW <= 0 || visH*visW < minVisPx {
		return earlyExit + 1
	}
	visPixels := float64(visH * visW)
	scaledExit := earlyExit * (visPixels / float64(tw*th))

	var rawSum float64
	if fastOK {
		rawSum = sadOnGrayscalePartial(imgGray, tplGray, ib, x0, y0, dxStart, dyStart, dxEnd, dyEnd, scaledExit)
	} else {
		rawSum = sadWithEarlyExitPartial(img, tplGray, x0, y0, dxStart, dyStart, dxEnd, dyEnd, scaledExit)
	}
	return rawSum / visPixels * float64(tw*th)
}

// precomputeGrayscale returns a 2-D slice of 8-bit luminance values
// for the pixels in the given image bounds.
func precomputeGrayscale(img image.Image, b image.Rectangle) [][]uint8 {
	w, h := b.Dx(), b.Dy()
	out := make([][]uint8, h)
	for y := 0; y < h; y++ {
		row := make([]uint8, w)
		for x := 0; x < w; x++ {
			row[x] = luma8(img.At(b.Min.X+x, b.Min.Y+y))
		}
		out[y] = row
	}
	return out
}

// precomputeImageGrayscale converts an *image.RGBA (the format produced
// by the screen-capture path) to a 2-D slice of 8-bit luminance values
// by reading the Pix slice directly. For other image formats it returns
// ok=false so the caller can fall back to per-pixel At().
//
// Accessing Pix directly is ~10× faster than calling image.At() for
// every pixel, which is the difference between a sub-second full-screen
// scan and a 5-minute timeout.
//
// Note on alpha pre-multiplication: image.At().RGBA() returns
// pre-multiplied 16-bit values, while reading Pix directly gives the
// un-pre-multiplied 8-bit values. For fully-opaque pixels (which is
// true for screen captures from win.CapturePlayerBarSearch) the two
// are identical; if a non-opaque image is ever passed in, the fast
// path and the At() path will disagree.
func precomputeImageGrayscale(img image.Image, b image.Rectangle) ([][]uint8, bool) {
	rgba, ok := img.(*image.RGBA)
	if !ok {
		return nil, false
	}
	w, h := b.Dx(), b.Dy()
	out := make([][]uint8, h)
	pix := rgba.Pix
	for y := 0; y < h; y++ {
		row := make([]uint8, w)
		srcY := b.Min.Y + y
		for x := 0; x < w; x++ {
			srcX := b.Min.X + x
			off := rgba.PixOffset(srcX, srcY)
			row[x] = luma8FromRGBA(pix[off], pix[off+1], pix[off+2])
		}
		out[y] = row
	}
	return out, true
}

// sadOnGrayscalePartial computes the SAD over only the visible sub-rectangle
// [dxStart,dxEnd) × [dyStart,dyEnd) of the template against the grayscale image.
func sadOnGrayscalePartial(imgGray, tpl [][]uint8, ib image.Rectangle, x0, y0, dxStart, dyStart, dxEnd, dyEnd int, earlyExit float64) float64 {
	sum := 0.0
	for dy := dyStart; dy < dyEnd; dy++ {
		irow := imgGray[y0+dy-ib.Min.Y]
		trow := tpl[dy]
		for dx := dxStart; dx < dxEnd; dx++ {
			diff := float64(trow[dx]) - float64(irow[x0+dx-ib.Min.X])
			if diff < 0 {
				diff = -diff
			}
			sum += diff
			if sum >= earlyExit {
				return sum
			}
		}
	}
	return sum
}

// sadWithEarlyExitPartial is the At()-based fallback for non-RGBA images,
// matching only the visible sub-rectangle of the template.
func sadWithEarlyExitPartial(img image.Image, tpl [][]uint8, x0, y0, dxStart, dyStart, dxEnd, dyEnd int, earlyExit float64) float64 {
	sum := 0.0
	for dy := dyStart; dy < dyEnd; dy++ {
		row := tpl[dy]
		yy := y0 + dy
		for dx := dxStart; dx < dxEnd; dx++ {
			diff := float64(row[dx]) - float64(luma8(img.At(x0+dx, yy)))
			if diff < 0 {
				diff = -diff
			}
			sum += diff
			if sum >= earlyExit {
				return sum
			}
		}
	}
	return sum
}

// luma8 returns an 8-bit grayscale value (Rec.601-style weights) for
// any color. This is the canonical formula used across statusui
// (template load, runtime capture, and the slow path all go through
// here). Matches the formula in statusui's PreprocessImage so every
// grayscale conversion in the package agrees to ±1.
func luma8(c color.Color) uint8 {
	r, g, b, _ := c.RGBA()
	return uint8((r*299 + g*587 + b*114) / 1000 >> 8)
}

// luma8FromRGBA computes the same 8-bit grayscale for raw 8-bit RGB
// inputs read directly from an *image.RGBA Pix slice. Used by the
// fast path (precomputeImageGrayscale) to avoid the At()→RGBA() round
// trip. Assumes fully-opaque pixels — for non-opaque inputs the At()
// path (which goes through pre-multiplied 16-bit RGBA()) will differ
// from this by the alpha factor, so call the color.Color version
// instead.
func luma8FromRGBA(r, g, b uint8) uint8 {
	// Expand 8-bit channels the same way color.RGBA() does before
	// applying the canonical luma formula, so the fast and At() paths
	// produce identical grayscale values.
	r16 := uint32(r) * 257
	g16 := uint32(g) * 257
	b16 := uint32(b) * 257
	return uint8(((r16*299 + g16*587 + b16*114) / 1000) >> 8)
}
