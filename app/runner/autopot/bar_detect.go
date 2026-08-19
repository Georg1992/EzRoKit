package autopot

import (
	"errors"
	"image"
	"sort"
)

const (
	// 55px half-width covers ±50px horizontal camera drift while keeping height tight.
	mapROIHalfW = 55
	mapROIHalfH = 30
	// Player bars sit below the screen center; bias ROI downward without enlarging it.
	mapROICenterYOffset = 15

	barRowHeight   = 3
	expectedBarGap = 4
	maxBarPairGap  = 12
	minRunWidth    = 1
	runGapMerge    = 2

	minBarWidth = 20
	maxBarWidth = 120

	minPairOverlap = 4
	minPairRunW    = 2
	barExtentGap   = 2

	// BarPositionMaxDrift is max rect movement allowed between timed pot confirmations.
	BarPositionMaxDrift = 8

	// Bar alignment tolerance
	barXAlignmentTolerance = 6

	// Bar pair scoring weights
	centerDistXWeight = 3
	centerDistYWeight = 4
	gapPenaltyWeight  = 4
	barRunDistYWeight = 2
	barRunWidthWeight = 4

	// Bar run scoring
	barRunDistXWeight = 3

	// Bar run finding tolerance for anchoring
	barExtensionSearchLimit = 12
)

// ErrBarsNotFound is returned when player HP/SP bars cannot be detected.
var ErrBarsNotFound = errors.New("player bars not found")

// ColorRun represents a contiguous horizontal sequence of same-colored pixels.
type ColorRun struct {
	X1    int
	X2    int
	Y     int
	Width int
	Color string // "hp" or "sp"
}

// MappedBars holds the detected HP and SP bar rectangles and metadata.
type MappedBars struct {
	Block    Rect
	HP       Rect
	SP       Rect
	Valid    bool
	MapScore int
}

// RefreshBarPair locates the player HP/SP colored-run pair near screen center.
// This is a periodic pair refresh, not a full-screen rectangle search.
func RefreshBarPair(img image.Image) (MappedBars, error) {
	b := img.Bounds()
	roi := driftROI(b)
	roiCX := roi.X + roi.W/2
	roiCY := roi.Y + roi.H/2

	hpRuns := consolidateRuns(scanColorRuns(img, roi, IsHPPixel, "hp"))
	spRuns := consolidateRuns(scanColorRuns(img, roi, IsSPPixel, "sp"))

	hpRun, spRun, score, ok := findPlayerBarPair(hpRuns, spRuns, roi, roiCX, roiCY)
	if !ok {
		return MappedBars{}, ErrBarsNotFound
	}

	hpRect, spRect := deriveBarRects(img, hpRun, spRun)
	block := unionRect(hpRect, spRect)

	return MappedBars{
		Block:    block,
		HP:       hpRect,
		SP:       spRect,
		Valid:    true,
		MapScore: score,
	}, nil
}

// RefreshConsistentBarPair locates the player HP/SP pair and reports whether
// the detection succeeded. The algorithm is deterministic for a given image.
func RefreshConsistentBarPair(img image.Image) (MappedBars, bool) {
	mapped, err := RefreshBarPair(img)
	if err != nil {
		return MappedBars{}, false
	}
	return mapped, true
}

func rectDrifted(a, b Rect, max int) bool {
	if a.W < 1 || b.W < 1 {
		return true
	}
	return absInt(a.X-b.X) > max ||
		absInt(a.Y-b.Y) > max ||
		absInt(a.W-b.W) > max
}

func driftROI(bounds image.Rectangle) Rect {
	cx := bounds.Min.X + bounds.Dx()/2
	cy := bounds.Min.Y + bounds.Dy()/2 + mapROICenterYOffset
	return clampROI(bounds, Rect{
		X: cx - mapROIHalfW,
		Y: cy - mapROIHalfH,
		W: mapROIHalfW * 2,
		H: mapROIHalfH * 2,
	})
}

// PlayerBarSearchROI returns the screen-space region to search for the
// player HP/SP colour-run pair. The region is centred at screen centre
// plus mapROICenterYOffset and sized to cover typical horizontal camera
// drift (mapROIHalfW×2 × mapROIHalfH×2).
//
// This is the public counterpart to driftROI: driftROI derives a
// clamped ROI from an already-captured image bounds, while
// PlayerBarSearchROI computes the same ROI directly from screen
// dimensions so the caller can issue a capture.
func PlayerBarSearchROI(sw, sh int) Rect {
	cx := sw / 2
	cy := sh/2 + mapROICenterYOffset
	return Rect{
		X: cx - mapROIHalfW,
		Y: cy - mapROIHalfH,
		W: mapROIHalfW * 2,
		H: mapROIHalfH * 2,
	}
}

// scanColorRuns finds horizontal runs of matching pixels in the ROI.
// A "run" is a contiguous horizontal sequence of pixels matching the provided color test.
// Returns all runs >= minRunWidth, with small gaps (<=runGapMerge pixels) automatically merged.
func scanColorRuns(img image.Image, roi Rect, isPixel func(r, g, b uint8) bool, colorKind string) []ColorRun {
	if rgba, ok := img.(*image.RGBA); ok {
		var runs []ColorRun
		for y := roi.Y; y < roi.Y+roi.H; y++ {
			runs = append(runs, extractRowRunsRGBA(rgba, y, roi.X, roi.X+roi.W, isPixel, colorKind)...)
		}
		return runs
	}
	var runs []ColorRun
	for y := roi.Y; y < roi.Y+roi.H; y++ {
		runs = append(runs, extractRowRuns(img, y, roi.X, roi.X+roi.W, isPixel, colorKind)...)
	}
	return runs
}

func extractRowRunsRGBA(img *image.RGBA, y, x0, x1 int, isPixel func(r, g, b uint8) bool, colorKind string) []ColorRun {
	if x0 >= x1 {
		return nil
	}
	var runs []ColorRun
	runStart := -1
	runEnd := -1
	gap := 0
	rowOff := img.PixOffset(x0, y)
	pix := img.Pix

	flush := func() {
		if runStart < 0 {
			return
		}
		w := runEnd - runStart + 1
		if w >= minRunWidth {
			runs = append(runs, ColorRun{
				X1: runStart, X2: runEnd, Y: y, Width: w, Color: colorKind,
			})
		}
		runStart = -1
		runEnd = -1
		gap = 0
	}

	off := rowOff
	for x := x0; x < x1; x++ {
		if isPixel(pix[off], pix[off+1], pix[off+2]) {
			if runStart < 0 {
				runStart = x
			}
			runEnd = x
			gap = 0
			off += 4
			continue
		}
		if runStart >= 0 {
			gap++
			if gap > runGapMerge {
				flush()
			}
		}
		off += 4
	}
	flush()
	return runs
}

func extractRowRuns(img image.Image, y, x0, x1 int, isPixel func(r, g, b uint8) bool, colorKind string) []ColorRun {
	var runs []ColorRun
	runStart := -1
	runEnd := -1
	gap := 0

	flush := func() {
		if runStart < 0 {
			return
		}
		w := runEnd - runStart + 1
		if w >= minRunWidth {
			runs = append(runs, ColorRun{
				X1: runStart, X2: runEnd, Y: y, Width: w, Color: colorKind,
			})
		}
		runStart = -1
		runEnd = -1
		gap = 0
	}

	for x := x0; x < x1; x++ {
		rp, gp, bp := pixelAt(img, x, y)
		if isPixel(rp, gp, bp) {
			if runStart < 0 {
				runStart = x
			}
			runEnd = x
			gap = 0
			continue
		}
		if runStart >= 0 {
			gap++
			if gap > runGapMerge {
				flush()
			}
		}
	}
	flush()
	return runs
}

func consolidateRuns(runs []ColorRun) []ColorRun {
	if len(runs) == 0 {
		return nil
	}
	bestByY := map[int]ColorRun{}
	for _, r := range runs {
		prev, ok := bestByY[r.Y]
		if !ok || r.Width > prev.Width {
			bestByY[r.Y] = r
		}
	}
	out := make([]ColorRun, 0, len(bestByY))
	for _, r := range bestByY {
		if r.Width >= minPairRunW {
			out = append(out, r)
		}
	}
	// Sort by Y then X1 for deterministic output. Without this sort,
	// Go's non-deterministic map iteration would produce different results
	// when there are tied scores in findPlayerBarPair, causing spurious
	// pair misalignment on the same input.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Y != out[j].Y {
			return out[i].Y < out[j].Y
		}
		return out[i].X1 < out[j].X1
	})
	return out
}

// findPlayerBarPair finds the best HP/SP bar pair from detected color runs.
// First tries to find pairs that satisfy all geometric constraints (gap, overlap, alignment).
// If no valid pair is found, anchors from the nearest single run.
// Returns empty runs if no bars are detected at all.
func findPlayerBarPair(hpRuns, spRuns []ColorRun, roi Rect, cx, cy int) (hp, sp ColorRun, score int, ok bool) {
	bestScore := -1
	var bestHP, bestSP ColorRun
	hasPair := false

	for _, spRun := range spRuns {
		for _, hpRun := range hpRuns {
			if hpRun.Color != "hp" || spRun.Color != "sp" {
				continue
			}
			gap := spRun.Y - hpRun.Y
			if gap < 1 || gap > maxBarPairGap {
				continue
			}
			if runOverlap(hpRun, spRun) < minPairOverlap {
				continue
			}
			if absInt(hpRun.X1-spRun.X1) > barXAlignmentTolerance {
				continue
			}
			if !pairCenterInROI(hpRun, spRun, roi) {
				continue
			}
			pairScore := scoreBarPair(hpRun, spRun, cx, cy)
			if pairScore > bestScore {
				bestScore = pairScore
				bestHP = hpRun
				bestSP = spRun
				hasPair = true
			}
		}
	}

	if hasPair {
		// Verify vertical bar structure: each detected run must have at
		// least one adjacent row with an overlapping run (real bars have
		// barRowHeight=3 rows of fill; single-row artifacts are desktop noise).
		if !barHasHeight(hpRuns, bestHP) || !barHasHeight(spRuns, bestSP) {
			return ColorRun{}, ColorRun{}, 0, false
		}
		return bestHP, bestSP, bestScore, true
	}

	if len(spRuns) > 0 {
		spRun := nearestBarRun(spRuns, roi, cx, cy)
		hpRun := anchorHPFromSP(spRun)
		// When HP runs exist from direct scanning, the anchored HP should
		// be geometrically consistent with at least one of them. If not,
		// the source SP run is likely a false positive (e.g. stray blue
		// pixels from the game world that match IsSPPixel).
		if len(hpRuns) > 0 && !anchoredRunConsistent(hpRun, hpRuns) {
			return ColorRun{}, ColorRun{}, 0, false
		}
		// Require the source SP run to have vertical bar structure.
		if !barHasHeight(spRuns, spRun) {
			return ColorRun{}, ColorRun{}, 0, false
		}
		score := scoreBarPair(hpRun, spRun, cx, cy)
		return hpRun, spRun, score, true
	}
	if len(hpRuns) > 0 {
		hpRun := nearestBarRun(hpRuns, roi, cx, cy)
		spRun := anchorSPFromHP(hpRun)
		// Symmetric check: if SP runs exist, anchored SP position should
		// be near at least one genuine scanned SP run.
		if len(spRuns) > 0 && !anchoredRunConsistent(spRun, spRuns) {
			return ColorRun{}, ColorRun{}, 0, false
		}
		// Require the source HP run to have vertical bar structure.
		if !barHasHeight(hpRuns, hpRun) {
			return ColorRun{}, ColorRun{}, 0, false
		}
		score := scoreBarPair(hpRun, spRun, cx, cy)
		return hpRun, spRun, score, true
	}
	return ColorRun{}, ColorRun{}, 0, false
}

func pairCenterInROI(hp, sp ColorRun, roi Rect) bool {
	midX := (hp.X1 + hp.X2 + sp.X1 + sp.X2) / 4
	midY := (hp.Y + sp.Y) / 2
	return midX >= roi.X && midX < roi.X+roi.W &&
		midY >= roi.Y && midY < roi.Y+roi.H
}

func runOverlap(a, b ColorRun) int {
	lo := a.X1
	if b.X1 > lo {
		lo = b.X1
	}
	hi := a.X2
	if b.X2 < hi {
		hi = b.X2
	}
	if hi < lo {
		return 0
	}
	return hi - lo + 1
}

// scoreBarPair computes a quality score for an HP/SP pair.
// Favors pairs centered near (cx,cy), with correct bar gap, proper alignment, and good width.
// Higher scores indicate better quality matches.
// Penalties: distance from center, gap deviation, horizontal misalignment.
// Bonus: bar width (especially wider SP bars).
func scoreBarPair(hp, sp ColorRun, cx, cy int) int {
	midX := (hp.X1 + hp.X2 + sp.X1 + sp.X2) / 4
	midY := (hp.Y + sp.Y) / 2
	centerDist := absInt(midX-cx)*centerDistXWeight + absInt(midY-cy)*centerDistYWeight

	gapPenalty := absInt((sp.Y-hp.Y)-expectedBarGap) * gapPenaltyWeight
	leftPenalty := absInt(hp.X1-sp.X1) * centerDistXWeight

	qualityBonus := (hp.Width + sp.Width) * barRunWidthWeight
	if sp.Width >= 40 {
		qualityBonus += sp.Width * 2 // Extra bonus for wider SP bar
	}

	return 1000 - centerDist - gapPenalty - leftPenalty + qualityBonus
}

func nearestBarRun(runs []ColorRun, roi Rect, cx, cy int) ColorRun {
	best := runs[0]
	bestScore := -1
	for _, r := range runs {
		if !runCenterInROI(r, roi) {
			continue
		}
		s := scoreBarRun(r, cx, cy)
		if s > bestScore {
			bestScore = s
			best = r
		}
	}
	if bestScore >= 0 {
		return best
	}
	return nearestRunToCenter(runs, cx, cy)
}

func runCenterInROI(r ColorRun, roi Rect) bool {
	mx := (r.X1 + r.X2) / 2
	return mx >= roi.X && mx < roi.X+roi.W &&
		r.Y >= roi.Y && r.Y < roi.Y+roi.H
}

func scoreBarRun(r ColorRun, cx, cy int) int {
	mx := (r.X1 + r.X2) / 2
	return r.Width*barRunWidthWeight - absInt(mx-cx)*barRunDistXWeight - absInt(r.Y-cy)*barRunDistYWeight
}

// anchoredRunConsistent checks whether the anchored (synthetic) run's Y
// position is within maxBarPairGap of at least one genuine scanned run.
// This prevents false positive SP/HP runs from producing a pair at an
// incorrect position via the single-run anchoring fallback.
func anchoredRunConsistent(anchored ColorRun, scannedRuns []ColorRun) bool {
	for _, r := range scannedRuns {
		if absInt(r.Y-anchored.Y) <= maxBarPairGap {
			return true
		}
	}
	return false
}

// barHasHeight checks that the given run has at least one adjacent row
// with an overlapping run within barRowHeight distance. A real bar has
// barRowHeight=3 rows of fill (e.g. Y, Y+1, Y+2), so its runs should have
// vertical neighbors. Single-row artifacts on the desktop lack this structure.
func barHasHeight(runs []ColorRun, target ColorRun) bool {
	for _, r := range runs {
		if r.Y == target.Y {
			continue
		}
		if absInt(r.Y-target.Y) < barRowHeight && runOverlap(r, target) >= minRunWidth {
			return true
		}
	}
	return false
}

func nearestRunToCenter(runs []ColorRun, cx, cy int) ColorRun {
	best := runs[0]
	bestDist := runCenterDist(best, cx, cy)
	for _, r := range runs[1:] {
		d := runCenterDist(r, cx, cy)
		if d < bestDist {
			bestDist = d
			best = r
		}
	}
	return best
}

// runCenterDist returns the Manhattan distance from run center to point (cx, cy)
func runCenterDist(r ColorRun, cx, cy int) int {
	mx := (r.X1 + r.X2) / 2
	return absInt(mx-cx) + absInt(r.Y-cy)
}

func anchorHPFromSP(sp ColorRun) ColorRun {
	y := sp.Y - expectedBarGap
	if y < 0 {
		y = 0
	}
	return ColorRun{X1: sp.X1, X2: sp.X2, Y: y, Width: sp.Width, Color: "hp"}
}

func anchorSPFromHP(hp ColorRun) ColorRun {
	return ColorRun{X1: hp.X1, X2: hp.X2, Y: hp.Y + expectedBarGap, Width: hp.Width, Color: "sp"}
}

func deriveBarRects(img image.Image, hpRun, spRun ColorRun) (hp, sp Rect) {
	roi := driftROI(img.Bounds())

	left := hpRun.X1
	if spRun.X1 < left {
		left = spRun.X1
	}
	if left <= roi.X+2 {
		leftHP := extendHPBarLeft(img, hpRun.Y, left)
		leftSP := extendSPBarLeft(img, spRun.Y, left)
		minLeft := left - barExtensionSearchLimit
		if leftHP < minLeft {
			leftHP = minLeft
		}
		if leftSP < minLeft {
			leftSP = minLeft
		}
		if leftHP < left {
			left = leftHP
		}
		if leftSP < left {
			left = leftSP
		}
	}

	rightHP := extendHPBarRight(img, hpRun.Y, hpRun.X2)
	rightSP := extendSPBarRight(img, spRun.Y, spRun.X2)
	right := rightHP
	if rightSP > right {
		right = rightSP
	}
	coloredRight := hpRun.X2
	if spRun.X2 > coloredRight {
		coloredRight = spRun.X2
	}
	if hpRun.Width >= 50 && spRun.Width >= 45 {
		right = coloredRight
	} else {
		maxRight := coloredRight + 30
		if right > maxRight {
			right = maxRight
		}
	}

	w := right - left + 1
	if w < minBarWidth {
		w = hpRun.Width
		if spRun.Width > w {
			w = spRun.Width
		}
	}
	if w > maxBarWidth {
		w = maxBarWidth
	}

	hpY := hpRun.Y - 1
	if hpY < 0 {
		hpY = hpRun.Y
	}
	spY := hpRun.Y + expectedBarGap - 1

	hp = Rect{X: left, Y: hpY, W: w, H: barRowHeight}
	sp = Rect{X: left, Y: spY, W: w, H: barRowHeight}
	return hp, sp
}

func extendHPBarRight(img image.Image, y, fromX int) int {
	right := fromX
	b := img.Bounds()
	gap := 0
	for x := fromX + 1; x < b.Max.X; x++ {
		rp, gp, bp := pixelAt(img, x, y)
		if IsHPPixel(rp, gp, bp) || isHPTrack(rp, gp, bp) {
			right = x
			gap = 0
			continue
		}
		gap++
		if gap >= barExtentGap {
			break
		}
	}
	return right
}

func extendSPBarRight(img image.Image, y, fromX int) int {
	right := fromX
	b := img.Bounds()
	gap := 0
	for x := fromX + 1; x < b.Max.X; x++ {
		rp, gp, bp := pixelAt(img, x, y)
		if IsSPPixel(rp, gp, bp) {
			right = x
			gap = 0
			continue
		}
		gap++
		if gap >= barExtentGap {
			break
		}
	}
	return right
}

func extendHPBarLeft(img image.Image, y, fromX int) int {
	left := fromX
	b := img.Bounds()
	gap := 0
	for x := fromX - 1; x >= b.Min.X; x-- {
		rp, gp, bp := pixelAt(img, x, y)
		if IsHPPixel(rp, gp, bp) || isHPTrack(rp, gp, bp) {
			left = x
			gap = 0
			continue
		}
		gap++
		if gap >= barExtentGap {
			break
		}
	}
	return left
}

func extendSPBarLeft(img image.Image, y, fromX int) int {
	left := fromX
	b := img.Bounds()
	gap := 0
	for x := fromX - 1; x >= b.Min.X; x-- {
		rp, gp, bp := pixelAt(img, x, y)
		if IsSPPixel(rp, gp, bp) {
			left = x
			gap = 0
			continue
		}
		gap++
		if gap >= barExtentGap {
			break
		}
	}
	return left
}
