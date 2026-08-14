package autopot

import (
	"image"
	"sort"
)

// BarRead holds the fill percentage and pixel counts for a single bar.
type BarRead struct {
	Percent     float64
	FilledWidth int
	FullWidth   int
	Found       bool
}

// ReadMappedBars reads the fill percentage of HP and SP bars from the given image.
// Uses cached bar rectangles from the last successful pair detection.
func ReadMappedBars(img image.Image, bars MappedBars) (hp BarRead, sp BarRead) {
	if !bars.Valid {
		return BarRead{}, BarRead{}
	}
	if bars.HP.W < 1 || bars.SP.W < 1 {
		return BarRead{FullWidth: bars.HP.W}, BarRead{FullWidth: bars.SP.W}
	}
	hp = ReadHPFill(img, bars.HP)
	sp = ReadSPFill(img, bars.SP)
	return hp, sp
}

// ReadHPFill reads the fill percentage of an HP bar from the image.
// Returns a BarRead with the fill percentage and pixel counts.
// When no fill pixels are found (the bar is empty), returns Found=true
// with Percent=0 so the stabiliser can accumulate low readings and heal.
func ReadHPFill(img image.Image, hp Rect) BarRead {
	if hp.W < 1 || hp.H < 1 {
		return BarRead{Found: false}
	}
	hp = trimBarEdges(img, hp, true)
	if hp.W < 1 {
		return BarRead{Found: false}
	}
	best := BarRead{Found: true, FullWidth: hp.W}
	for row := 0; row < hp.H; row++ {
		br := readBarFillSingleRow(img, hp.X, hp.Y+row, hp.W, isHPFillRead)
		if br.FilledWidth > best.FilledWidth {
			best = br
		}
	}
	if best.FilledWidth == 0 {
		return BarRead{Found: true, Percent: 0, FullWidth: hp.W}
	}
	return normalizeBarRead(img, hp, true, best)
}

// ReadSPFill reads the fill percentage of an SP bar from the image.
// Similar to ReadHPFill but uses SP color detection.
// When no fill pixels are found (empty bar), returns Found=true with
// Percent=0 so the stabiliser can accumulate low readings.
func ReadSPFill(img image.Image, sp Rect) BarRead {
	if sp.W < 1 || sp.H < 1 {
		return BarRead{Found: false}
	}
	sp = trimBarEdges(img, sp, false)
	if sp.W < 1 {
		return BarRead{Found: false}
	}
	best := BarRead{Found: true, FullWidth: sp.W}
	for row := 0; row < sp.H; row++ {
		br := readBarFillSingleRow(img, sp.X, sp.Y+row, sp.W, isSPFill)
		if br.FilledWidth > best.FilledWidth {
			best = br
		}
	}
	if best.FilledWidth == 0 {
		return BarRead{Found: true, Percent: 0, FullWidth: sp.W}
	}
	return normalizeBarRead(img, sp, false, best)
}

func readBarFillSingleRow(img image.Image, x0, y, w int, isPixel func(r, g, b uint8) bool) BarRead {
	filled := 0
	for col := 0; col < w; col++ {
		rp, gp, bp := pixelAt(img, x0+col, y)
		if isPixel(rp, gp, bp) {
			filled++
			continue
		}
		if filled > 0 {
			break
		}
	}
	return barReadFromFill(filled, w)
}

func barReadFromFill(filled, full int) BarRead {
	pct := float64(filled) * 100 / float64(full)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return BarRead{
		Percent:     pct,
		FilledWidth: filled,
		FullWidth:   full,
		Found:       true,
	}
}

func trimBarEdges(img image.Image, r Rect, hpBar bool) Rect {
	// Collect edge positions from all rows, then take the median.
	// This is robust to rows where the game background has no bar
	// pixels (e.g. a bright blue sky at the bar's middle row) —
	// the other rows still contribute correct edges and the median
	// filters out the deviant row.
	var lefts, rights []int
	for row := 0; row < r.H; row++ {
		y := r.Y + row
		l, rEdge := -1, -1
		for x := r.X; x < r.X+r.W; x++ {
			rp, gp, bp := pixelAt(img, x, y)
			if barEdgePixel(rp, gp, bp, hpBar) {
				l = x
				break
			}
		}
		for x := r.X + r.W - 1; x >= r.X; x-- {
			rp, gp, bp := pixelAt(img, x, y)
			if barEdgePixel(rp, gp, bp, hpBar) {
				rEdge = x
				break
			}
		}
		if l >= 0 && rEdge >= 0 && l < rEdge {
			lefts = append(lefts, l)
			rights = append(rights, rEdge)
		}
	}
	if len(lefts) == 0 {
		return r // no row found edges — return unchanged
	}
	sort.Ints(lefts)
	sort.Ints(rights)
	r.X = lefts[len(lefts)/2]
	r.W = rights[len(rights)/2] - r.X + 1
	if r.W < 1 {
		r.W = 1
	}
	return r
}

func barEdgePixel(r, g, b uint8, hpBar bool) bool {
	if hpBar {
		return IsHPPixel(r, g, b) || isHPTrack(r, g, b) || isBarBackground(r, g, b)
	}
	// IsSPPixel calls isSPFill internally — use isSPFill directly.
	return isSPFill(r, g, b) || isHPTrack(r, g, b) || isBarBackground(r, g, b)
}

func normalizeBarRead(img image.Image, r Rect, hpBar bool, read BarRead) BarRead {
	if !read.Found || r.W < 2 {
		return read
	}
	// Bar is full when FilledWidth nearly equals the bar width AND there's
	// no empty track at the right edge. Uses the already-computed fill
	// width instead of calling bestFillWidth again via BarLooksFull.
	if read.FilledWidth < r.W-2 || barRightHasEmptyTrack(img, r, hpBar) {
		return read
	}
	if read.FullWidth < 1 {
		read.FullWidth = r.W
	}
	read.FilledWidth = read.FullWidth
	read.Percent = 100
	read.Found = true
	return read
}

// BarLooksFull reports whether the bar in rect appears to be at 100% fill.
// Exported (uppercase) so the runner package can re-export it for tests.
func BarLooksFull(img image.Image, r Rect, hpBar bool) bool {
	if r.W < 2 {
		return false
	}
	if barRightHasEmptyTrack(img, r, hpBar) {
		return false
	}
	return bestFillWidth(img, r, hpBar) >= r.W-2
}

func bestFillWidth(img image.Image, r Rect, hpBar bool) int {
	if !hpBar {
		// For SP bars, use middle-row-primary with fallback, matching ReadSPFill.
		// The middle row is most reliable; only widen the search when it shows 0.
		midRow := r.Y + r.H/2
		mid := readBarFillSingleRow(img, r.X, midRow, r.W, isSPFill).FilledWidth
		if mid > 0 {
			return mid
		}
		best := 0
		for row := 0; row < r.H; row++ {
			br := readBarFillSingleRow(img, r.X, r.Y+row, r.W, isSPFill)
			if br.FilledWidth > best {
				best = br.FilledWidth
			}
		}
		return best
	}
	// For HP bars, use median across rows (matching ReadHPFill) so a single
	// noisy row with game-background green pixels can't falsely inflate the
	// fill width and make BarLooksFull think the bar is full.
	var vals []int
	for row := 0; row < r.H; row++ {
		br := readBarFillSingleRow(img, r.X, r.Y+row, r.W, isHPFillRead)
		vals = append(vals, br.FilledWidth)
	}
	sort.Ints(vals)
	return vals[r.H/2]
}

func barConfirmedNotFull(img image.Image, r Rect, hpBar bool, read BarRead) bool {
	if !read.Found || !barReadConsistent(img, r, hpBar, read) {
		return false
	}
	// BarLooksFull is already checked by the caller (UpdatePair) before
	// reaching here — no need to re-check. Only check the Percent guard
	// and the empty-track test.
	if read.Percent >= 99 {
		return false
	}
	return barRightHasEmptyTrack(img, r, hpBar)
}

func barReadConsistent(img image.Image, r Rect, hpBar bool, read BarRead) bool {
	if !read.Found || r.W < 2 {
		return false
	}
	// Sanity-check the reading. No second-guessing via bestFillWidth:
	// bestFillWidth operates on the original mapped rect (which may
	// differ from the trimmed rect ReadHPFill used), and it picks up
	// game-background pixels that happen to match the fill colour,
	// producing inflated values that would wrongfully fail the check.
	// The bar position was already validated by RefreshBarPair; the
	// fill reading comes from a direct left-to-right scan across all
	// rows. Trust it within reasonable bounds.
	if read.Percent < 0 || read.Percent > 100 {
		return false
	}
	if read.FilledWidth < 0 || read.FilledWidth > read.FullWidth {
		return false
	}
	return true
}

func barRightHasEmptyTrack(img image.Image, r Rect, hpBar bool) bool {
	if r.W < 4 || r.H < 1 {
		return false
	}
	checkCols := r.W / 5
	if checkCols < 3 {
		checkCols = 3
	}
	if checkCols > 8 {
		checkCols = 8
	}
	for row := 0; row < r.H; row++ {
		y := r.Y + row
		empty := 0
		for col := r.W - checkCols; col < r.W; col++ {
			rp, gp, bp := pixelAt(img, r.X+col, y)
			if isBarEmptyPixel(rp, gp, bp, hpBar) {
				empty++
			}
		}
		if empty >= 2 {
			return true
		}
	}
	return false
}

func isBarEmptyPixel(r, g, b uint8, hpBar bool) bool {
	if hpBar {
		if isHPFillRead(r, g, b) {
			return false
		}
	} else if isSPFill(r, g, b) {
		return false
	}
	return isHPTrack(r, g, b) || isBarBackground(r, g, b)
}

