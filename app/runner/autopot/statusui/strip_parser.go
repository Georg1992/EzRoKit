// Package statusui reads Ragnarok Online HP/SP values from an
// already-cropped status strip image using pure template
// matching. No Tesseract, no neural networks, no ML.
//
// Pipeline (executed on every Reader.Read call):
//
//	strip Image → binarize (dark OR red) →
//	  tight crop + 8-connected component split (floodFillCC
//	        joins diagonally-connected pixels the 4-way rule
//	        would split — e.g. '7' crossbar + diagonal stem) →
//	    per-component resize + nearest-neighbor template match →
//	      assembled text → regex parse → value validation → Result.
//
// Color tolerance is built into binarization: dark text and red
// text (low HP/SP state) become the same foreground mask, so
// font-color changes do not affect the read.
//
// The reader uses a per-frame glyph hint to short-circuit template
// scanning: when the component count matches the previous frame,
// each glyph is tested against the prior frame's glyph first at a
// high confidence threshold (0.90). If it matches, the full O(n)
// template scan is skipped. This makes stable HP/SP reads O(1)
// per glyph; only changed values pay the full scan cost.
//
// The reader is constructed once via NewReader(templatesDir),
// which loads every PNG from templates/glyphs/ and pre-binarizes
// each. Read() then reuses the cached templates for every strip.
// Filename → rune mapping is fixed (see filenameRune); extra or
// non-matching files in templatesDir are silently ignored so the
// directory can grow without code changes.
//
// MinGlyphScore defaults to 0.70; raise it for stricter
// confidence, lower it for noisier captures. The match score is
// the fraction of pixels that agree after the candidate has
// been resized to the template's bounding-box dimensions via
// nearest-neighbour.
//
// Reader is safe for concurrent Read callers as long as no other
// goroutine mutates its public fields during a Read in flight.
package statusui

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Result is the structured outcome of a single Reader.Read call.
//
// Reason is empty on success; otherwise it carries one of:
//
//   - "no_components"            — strip has no measurable foreground.
//   - "low_glyph_score"          — best match below MinGlyphScore.
//   - "parse_failed"             — assembled text didn't match the
//     HP/SP regex.
//   - "value_validation_failed"  — regex matched but hp/hpMax or
//     sp/spMax are mutually inconsistent.
//
// Confidence is the mean of per-glyph match scores (0..1); Text
// is the assembled template-string used to attempt the regex.
type Result struct {
	OK         bool
	HP         int
	HPMax      int
	SP         int
	SPMax      int
	Text       string
	Confidence float64
	Reason     string
}

// Reader is a reusable HP/SP template matcher.
//
// Construct with NewReader(templatesDir); call Read(strip) for
// each subsequent strip. A single Reader is intended for the
// lifetime of the program — templates are loaded once and
// cached.
//
//   - TemplatesDir   — directory containing templates/glyphs/*.png.
//   - MinGlyphScore  — minimum per-glyph match score (default 0.70).
//   - Debug          — if true, drop diagnostic PNGs into DebugDir.
//   - DebugDir       — output directory used when Debug=true.
//
// Public fields are read at the start of every Read call; values
// passed via NewReader are written before any Read can begin, so
// concurrent Read callers see a consistent state.
type Reader struct {
	TemplatesDir  string
	MinGlyphScore float64
	Debug         bool
	DebugDir      string

	mu         sync.RWMutex
	templates  []templateEntry // sorted by rune for deterministic match logging
	prevGlyphs []rune          // glyph sequence from last successful Read; nil until first success
}

// templateEntry is a single loaded template: source filename,
// decoded rune, tight bounding-box size, and pre-binarized mask
// cropped to that bbox.
type templateEntry struct {
	rune   rune
	name   string
	bounds image.Rectangle
	mask   [][]bool // [y][x], true = foreground
}

// matchRec records one component's match outcome. rect is in
// strip-local coordinates so debug crops line up directly on
// the source image without further translation.
type matchRec struct {
	idx   int
	ch    rune
	score float64
	rect  image.Rectangle // strip-local
}

// filenameRune maps templates/glyphs/ file basenames to the rune
// the reader emits when the template wins the per-glyph match.
// Anything not in this map is silently ignored at load time, so
// it's safe to drop notes or auxiliary PNGs into the templates
// directory.
//
//   - "0".."9" → digit rune
//   - "dot"    → '.'
//   - "slash"  → '/'
//   - "pipe"   → '|'
//   - "H","P","S" → letter runes (HP label = H+P glyphs, etc.)
func filenameRune(base string) (rune, bool) {
	switch base {
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return rune(base[0]), true
	case "dot":
		return '.', true
	case "slash":
		return '/', true
	case "pipe":
		return '|', true
	case "H":
		return 'H', true
	case "P":
		return 'P', true
	case "S":
		return 'S', true
	}
	return 0, false
}

// NewReader loads every PNG from templatesDir, binarizes each,
// stores the mask + tight bounding-box size for each, and
// returns a configured *Reader with MinGlyphScore defaulted to
// 0.70 if zero.
//
// Returns an error if:
//   - templatesDir cannot be globbed,
//   - no PNGs are present,
//   - no PNGs have a recognized filename,
//   - any recognized PNG fails to decode.
func NewReader(templatesDir string) (*Reader, error) {
	pattern := filepath.Join(templatesDir, "*.png")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("statusui: glob %q: %w", pattern, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("statusui: no PNGs in %q", templatesDir)
	}

	var entries []templateEntry
	for _, fp := range files {
		base := strings.TrimSuffix(filepath.Base(fp), filepath.Ext(fp))
		r, ok := filenameRune(base)
		if !ok {
			continue
		}
		img, err := readPNGFile(fp)
		if err != nil {
			return nil, fmt.Errorf("statusui: read template %q: %w", fp, err)
		}
		mask := binarize(img)
		bounds := maskBounds(mask)
		mask = maskCrop(mask, bounds)
		entries = append(entries, templateEntry{
			rune:   r,
			name:   base + ".png",
			bounds: bounds,
			mask:   mask,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("statusui: no recognizable templates in %q (need 0-9, dot, slash, pipe, H, P, S)", templatesDir)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rune < entries[j].rune })

	return &Reader{
		TemplatesDir: templatesDir,
		// MinGlyphScore stays 0 here — Read substitutes 0.70 at
		// call time so the user-set value is preserved (don't
		// double-default in NewReader AND Read).
		templates: entries,
	}, nil
}

// NewReaderFromFS loads glyph templates from an [io/fs.FS].
// glyphsDir is the directory path inside fsys (e.g. "glyphs").
// Otherwise identical to NewReader.
func NewReaderFromFS(fsys fs.FS, glyphsDir string) (*Reader, error) {
	pattern := glyphsDir + "/*.png"
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, fmt.Errorf("statusui: glob %q: %w", pattern, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("statusui: no PNGs in %q", glyphsDir)
	}

	var entries []templateEntry
	for _, fp := range files {
		base := strings.TrimSuffix(filepath.Base(fp), filepath.Ext(fp))
		r, ok := filenameRune(base)
		if !ok {
			continue
		}
		f, err := fsys.Open(fp)
		if err != nil {
			return nil, fmt.Errorf("statusui: open template %q: %w", fp, err)
		}
		img, decErr := png.Decode(f)
		f.Close()
		if decErr != nil {
			return nil, fmt.Errorf("statusui: decode template %q: %w", fp, decErr)
		}
		mask := binarize(img)
		bounds := maskBounds(mask)
		mask = maskCrop(mask, bounds)
		entries = append(entries, templateEntry{
			rune:   r,
			name:   filepath.Base(fp),
			bounds: bounds,
			mask:   mask,
		})
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("statusui: no recognizable templates in %q (need 0-9, dot, slash, pipe, H, P, S)", glyphsDir)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rune < entries[j].rune })
	return &Reader{templates: entries}, nil
}

// Read runs the full pipeline on strip and returns either a
// successful Result or one with Reason != "" describing the
// earliest failure encountered.
//
// Debug mode (Debug=true && DebugDir!="") writes diagnostic
// PNGs and a recognized.txt regardless of success or failure —
// useful when iterating on MinGlyphScore or template coverage.
func (r *Reader) Read(strip image.Image) Result {
	r.mu.RLock()
	templates := r.templates
	minScore := r.MinGlyphScore
	debug := r.Debug
	debugDir := r.DebugDir
	prevGlyphs := r.prevGlyphs
	r.mu.RUnlock()

	if minScore == 0 {
		minScore = 0.70
	}

	mask := binarize(strip)
	bounds := maskBounds(mask)
	if bounds.Empty() {
		res := Result{OK: false, Reason: "no_components"}
		emitDebug(debug, debugDir, strip, mask, bounds, nil, res)
		return res
	}
	cropped := maskCrop(mask, bounds)
	cropW := len(cropped[0])
	cropH := len(cropped)
	comps := findComponents(cropped, image.Rect(0, 0, cropW, cropH))
	if len(comps) == 0 {
		res := Result{OK: false, Reason: "no_components"}
		emitDebug(debug, debugDir, strip, mask, bounds, nil, res)
		return res
	}

	// Match each component, in left-to-right strip order.
	// When the component count matches the previous frame, pass the prior
	// glyph at each position as a hint so matchGlyphHinted can short-circuit
	// the full template scan for unchanged digits.
	matches := make([]matchRec, 0, len(comps))
	var text strings.Builder
	var scores []float64
	usePrev := len(prevGlyphs) == len(comps)
	for i, cLocal := range comps {
		glyph := maskCrop(cropped, cLocal)
		var hint rune
		if usePrev {
			hint = prevGlyphs[i]
		}
		ch, score := matchGlyphHinted(glyph, templates, hint)
		rect := image.Rect(
			bounds.Min.X+cLocal.Min.X,
			bounds.Min.Y+cLocal.Min.Y,
			bounds.Min.X+cLocal.Max.X,
			bounds.Min.Y+cLocal.Max.Y,
		)
		matches = append(matches, matchRec{idx: i, ch: ch, score: score, rect: rect})
		if score < minScore {
			tail := text.String() + string(ch) + "<reject>"
			res := Result{
				OK:         false,
				Reason:     "low_glyph_score",
				Text:       tail,
				Confidence: mean(scores),
			}
			emitDebug(debug, debugDir, strip, mask, bounds, matches, res)
			return res
		}
		text.WriteRune(ch)
		scores = append(scores, score)
	}

	res := Result{
		Text:       text.String(),
		Confidence: mean(scores),
	}

	hp, hpMax, sp, spMax, err := parseText(res.Text)
	if err != nil {
		res.Reason = "parse_failed"
		res.OK = false
		emitDebug(debug, debugDir, strip, mask, bounds, matches, res)
		return res
	}
	res.HP = hp
	res.HPMax = hpMax
	res.SP = sp
	res.SPMax = spMax

	if !validateValues(hp, hpMax, sp, spMax) {
		res.Reason = "value_validation_failed"
		res.OK = false
		emitDebug(debug, debugDir, strip, mask, bounds, matches, res)
		return res
	}

	res.OK = true
	// Store the matched glyph sequence so the next call can use it as a hint.
	glyphs := make([]rune, len(matches))
	for j, m := range matches {
		glyphs[j] = m.ch
	}
	r.mu.Lock()
	r.prevGlyphs = glyphs
	r.mu.Unlock()
	emitDebug(debug, debugDir, strip, mask, bounds, matches, res)
	return res
}

// mean returns the mean of a float slice, or 0 for an empty slice.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range xs {
		sum += v
	}
	return sum / float64(len(xs))
}

// maskCrop returns a sub-mask (as a fresh [][]bool) covering
// the rectangle r clipped against mask bounds. The result keeps
// its own coordinate system: row 0 of the result corresponds to
// r.Min.Y of the source.
func maskCrop(mask [][]bool, r image.Rectangle) [][]bool {
	mw := maskWidth(mask)
	mh := maskHeight(mask)
	clip := r.Intersect(image.Rect(0, 0, mw, mh))
	if clip.Empty() {
		return nil
	}
	outH := clip.Dy()
	outW := clip.Dx()
	out := make([][]bool, outH)
	for y := 0; y < outH; y++ {
		out[y] = make([]bool, outW)
		for x := 0; x < outW; x++ {
			out[y][x] = mask[clip.Min.Y+y][clip.Min.X+x]
		}
	}
	return out
}

// maskWidth / maskHeight return the width / height of a
// binarized [][]bool, treating it as image.Rectangle-aligned.
func maskWidth(mask [][]bool) int {
	if len(mask) == 0 {
		return 0
	}
	return len(mask[0])
}
func maskHeight(mask [][]bool) int { return len(mask) }

// binarize converts an image.Image into a foreground mask.
// A pixel is foreground iff:
//
//	gray = (299*R + 587*G + 114*B)/1000 < 180      (dark text)
//	  OR
//	R > 120 && G < 130 && B < 130                 (red text —
//	                                                low HP/SP marker)
//
// white background and pale intermediate values stay as
// background. Pure colour tolerance is intentional: red and
// black HP/SP labels must read identically.
func binarize(img image.Image) [][]bool {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil
	}
	out := make([][]bool, h)
	for y := 0; y < h; y++ {
		out[y] = make([]bool, w)
		for x := 0; x < w; x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			r, g, bl, a := c.RGBA()
			// Fully-transparent pixels carry no colour signal
			// and must be treated as background regardless of
			// their RGB channels (an alpha=0 RGBA pixel still
			// reports r=g=b=0, which would otherwise be flagged
			// as dark text). image.RGBA() returns 16-bit
			// pre-multiplied; a transparent pixel has a=0 and
			// thus r=g=b=0. We default a >= some threshold to
			// mean "this pixel has colour information".
			if a < 0x4000 {
				out[y][x] = false
				continue
			}
			r8 := uint8(r >> 8)
			g8 := uint8(g >> 8)
			b8 := uint8(bl >> 8)
			gray := uint8((uint32(r8)*299 + uint32(g8)*587 + uint32(b8)*114) / 1000)
			dark := gray < 180
			red := r8 > 120 && g8 < 130 && b8 < 130
			out[y][x] = dark || red
		}
	}
	return out
}

// maskBounds returns the tight bounding box enclosing every
// foreground pixel in mask, or the zero Rectangle if the mask
// has no foreground. Width / height are clamped to the mask's
// own dimensions.
func maskBounds(mask [][]bool) image.Rectangle {
	w := maskWidth(mask)
	h := maskHeight(mask)
	if w == 0 || h == 0 {
		return image.Rectangle{}
	}
	minX, minY := w, h
	maxX, maxY := -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mask[y][x] {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// minGlyphArea is the minimum bbox area (width × height) a
// connected component must have to qualify as a glyph candidate.
// Tuning knob: raise it to drop more questionable fragments
// (incidental antialias speckle around text edges); lower it if a
// real game's glyphs render thinner than 2×2 px. The spec says
// ignore "совсем micro-noise" while keeping a small dot — a 2×2
// dot (4 px) is the smallest legitimate glyph, hence 4.
const minGlyphArea = 4

// findComponents splits a binary mask into a left-to-right list
// of glyph bounding boxes.
//
// Algorithm: 4-connected-components flood fill. Each connected
// blob is one glyph, with tight bounding box. We deliberately
// DEVIATE from the spec's literal "empty-column break" rule
// because real fonts have interior whitespace (e.g. the gap
// inside an 'H' or 'P' loop is wider than the 1-px inter-glyph
// gap), so column-only splitting fragments letters like 'H'
// into a left-stroke + a right-stroke, each of which then
// mis-matches a digit template. The flood-fill returns each
// glyph as one piece because the middle bar of H or the upper
// loop of P connects the strokes within a single CC.
//
// Micro-noise filter: any CC with bbox area < minGlyphArea is
// dropped per spec ("ignore совсем micro-noise"). This catches
// incidental pixel pairs such as the waist bump inside a digit
// '0' that would otherwise resize-down to a 2×1 dot template
// and tie at score 1.0, producing spurious '.' matches. The
// canonical 2×2 dot (4 px area) survives. Real fonts are dense
// enough that this only removes speckle.
//
// Returned components are sorted left-to-right by Min.X so
// text reconstruction is in reading order.
func findComponents(mask [][]bool, _ image.Rectangle) []image.Rectangle {
	w := maskWidth(mask)
	h := maskHeight(mask)
	if w == 0 || h == 0 {
		return nil
	}
	visited := make([][]bool, h)
	for y := 0; y < h; y++ {
		visited[y] = make([]bool, w)
	}
	var comps []image.Rectangle
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if !mask[y][x] || visited[y][x] {
				continue
			}
			bbox := floodFillCC(mask, visited, x, y)
			if bbox.Dx()*bbox.Dy() < minGlyphArea {
				continue
			}
			comps = append(comps, bbox)
		}
	}
	sort.Slice(comps, func(i, j int) bool { return comps[i].Min.X < comps[j].Min.X })
	return comps
}

// floodFillCC performs an iterative 4-connected flood fill from
// the seed (sx, sy) and returns the tight bounding box of every
// foreground pixel reachable. visited mutates to mark consumed
// pixels (the caller seeds it as all-false). The internal stack
// is bounded by the component size, so worst-case memory is the
// whole mask — fine for status-strip-sized inputs (< 100k px).
func floodFillCC(mask, visited [][]bool, sx, sy int) image.Rectangle {
	w := maskWidth(mask)
	h := maskHeight(mask)
	type pt struct{ x, y int }
	stack := []pt{{sx, sy}}
	minX, minY, maxX, maxY := sx, sy, sx, sy
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.x < 0 || p.y < 0 || p.x >= w || p.y >= h {
			continue
		}
		if visited[p.y][p.x] || !mask[p.y][p.x] {
			continue
		}
		visited[p.y][p.x] = true
		if p.x < minX {
			minX = p.x
		}
		if p.x > maxX {
			maxX = p.x
		}
		if p.y < minY {
			minY = p.y
		}
		if p.y > maxY {
			maxY = p.y
		}
		// 8-connected flood fill — N4 cardinals + N4 diagonals.
		// Diagonals matter for glyphs whose strokes meet at a
		// single pixel — e.g. '7' crossbar meets its diagonal
		// stem at one corner, or '/' is two diagonals. 4-conn
		// splits these into separate CCs (we observed the
		// 5×3 fragment being the crossbar of a digit whose
		// diagonal stem disconnects from it). 8-conn keeps
		// them joined.
		stack = append(stack,
			pt{p.x + 1, p.y},
			pt{p.x - 1, p.y},
			pt{p.x, p.y + 1},
			pt{p.x, p.y - 1},
			pt{p.x + 1, p.y + 1},
			pt{p.x - 1, p.y + 1},
			pt{p.x + 1, p.y - 1},
			pt{p.x - 1, p.y - 1},
		)
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// matchGlyph finds the closest template for a single glyph
// mask. Returns the best rune and its equal-pixel score.
//
// Both candidate and template must be at least 1×1; if either
// is empty, the match is degenerate and returns (0, 0).
//
// Resize is nearest-neighbor; ties are broken in favour of the
// template whose rune sorts earlier, so match-log is stable
// across runs.
//
// bestScore starts at 0 (NOT -1) so all-zero-score templates
// (e.g. an empty resize) do NOT win the first iteration; only
// templates with score > 0 are considered, which means empty
// or all-mismatched candidates correctly return (0, 0) below.
func matchGlyph(glyph [][]bool, templates []templateEntry) (rune, float64) {
	gH := maskHeight(glyph)
	gW := maskWidth(glyph)
	if gH == 0 || gW == 0 {
		return 0, 0
	}
	var bestRune rune
	var bestScore float64
	for _, t := range templates {
		tH := t.bounds.Dy()
		tW := t.bounds.Dx()
		if tH == 0 || tW == 0 {
			continue
		}
		// Skip templates whose dims are wildly off the
		// candidate. Without this filter, the tiny dot template
		// (2×1 pixel) gets resized against 7×8 digits losing
		// all detail and ties at score 1.0, leaving '.'
		// (lowest ASCII) as tiebreak winner for almost every
		// component. The 0.5–2.0 envelope keeps matches at
		// comparable scale (digit↔digit, slash↔slash,
		// pipe↔pipe, H↔H etc.).
		if gW*2 < tW || tW*2 < gW || gH*2 < tH || tH*2 < gH {
			continue
		}
		resized := resizeMaskNearest(glyph, gW, gH, tW, tH)
		score := maskEqualFraction(resized, t.mask)
		if score > bestScore || (score == bestScore && bestRune != 0 && t.rune < bestRune) {
			bestScore = score
			bestRune = t.rune
		}
	}
	return bestRune, bestScore
}

// hintMinScore is the early-exit threshold for matchGlyphHinted.
// It is set above the default MinGlyphScore (0.70) so that a hint
// is only accepted when the match is unambiguous. A changed digit
// (different shape) scores well below 0.90 against the old template
// and falls through to the full matchGlyph search.
const hintMinScore = 0.90

// matchGlyphHinted is matchGlyph with a warm-path hint. If hint != 0
// and the hint template scores ≥ hintMinScore, it returns immediately
// without scanning the rest of the template list. This makes
// repeated parsing of the same strip value O(1) per glyph instead
// of O(numTemplates), which is the common case when HP/SP are stable.
//
// Correctness: a wrong hint (the value changed) scores well below
// hintMinScore because glyph shapes for different digits/letters
// differ significantly at the scale used. The full search then runs
// and finds the correct glyph.
func matchGlyphHinted(glyph [][]bool, templates []templateEntry, hint rune) (rune, float64) {
	if hint != 0 {
		gH := maskHeight(glyph)
		gW := maskWidth(glyph)
		if gH > 0 && gW > 0 {
			for _, t := range templates {
				if t.rune != hint {
					continue
				}
				tH := t.bounds.Dy()
				tW := t.bounds.Dx()
				if tH == 0 || tW == 0 {
					break
				}
				if gW*2 < tW || tW*2 < gW || gH*2 < tH || tH*2 < gH {
					break // size ratio fails — hint not applicable
				}
				resized := resizeMaskNearest(glyph, gW, gH, tW, tH)
				if score := maskEqualFraction(resized, t.mask); score >= hintMinScore {
					return t.rune, score
				}
				break // hint scored below threshold — fall through
			}
		}
	}
	return matchGlyph(glyph, templates)
}

// resizeMaskNearest nearest-neighbor resizes src (gW×gH)
// into dst (tW×tH) using integer step ratio. Out-of-range
// samples at the edges clamp to the last source row/column.
func resizeMaskNearest(src [][]bool, gW, gH, tW, tH int) [][]bool {
	if tW == 0 || tH == 0 {
		return nil
	}
	dst := make([][]bool, tH)
	for y := 0; y < tH; y++ {
		sy := y * gH / tH
		if sy >= gH {
			sy = gH - 1
		}
		dst[y] = make([]bool, tW)
		for x := 0; x < tW; x++ {
			sx := x * gW / tW
			if sx >= gW {
				sx = gW - 1
			}
			dst[y][x] = src[sy][sx]
		}
	}
	return dst
}

// maskEqualFraction counts equal pixels between two masks of
// matching dimensions, returning a 0..1 score. Two empty masks
// score 1.0 (vacuous match) so the caller doesn't artificially
// penalise empty regions.
func maskEqualFraction(a, b [][]bool) float64 {
	aH := maskHeight(a)
	bH := maskHeight(b)
	if aH == 0 || bH == 0 {
		if aH == 0 && bH == 0 {
			return 1
		}
		return 0
	}
	aW := maskWidth(a)
	bW := maskWidth(b)
	if aW != bW {
		return 0 // caller is responsible for resize; protect against drift
	}
	var equal, total int
	for y := 0; y < aH; y++ {
		for x := 0; x < aW; x++ {
			if a[y][x] == b[y][x] {
				equal++
			}
			total++
		}
	}
	if total == 0 {
		return 1
	}
	return float64(equal) / float64(total)
}

// hpSpRegex matches the complete "HP.<d>/<d>[|]SP.<d>/<d>" string with
// optional dots after HP/SP and an optional pipe separator. Anchoring the
// expression prevents unrelated OCR noise before or after a valid-looking
// substring from being accepted.
//
// Group indices: 1=hp 2=hpMax 3=sp 4=spMax.
var hpSpRegex = regexp.MustCompile(`^HP\.?(\d+)/(\d+)\|?SP\.?(\d+)/(\d+)$`)

// parseText applies the HP/SP regex to a candidate text. If
// the regex misses, returns an error; otherwise returns
// (hp, hpMax, sp, spMax) parsed as base-10 ints.
func parseText(text string) (hp, hpMax, sp, spMax int, err error) {
	match := hpSpRegex.FindStringSubmatch(text)
	if match == nil {
		err = errors.New("statusui: regex did not match")
		return
	}
	hp, err = strconv.Atoi(match[1])
	if err != nil {
		return
	}
	hpMax, err = strconv.Atoi(match[2])
	if err != nil {
		return
	}
	sp, err = strconv.Atoi(match[3])
	if err != nil {
		return
	}
	spMax, err = strconv.Atoi(match[4])
	return
}

// validateValues enforces:
//   - hp >= 0,     hpMax > 0 (zero or negative max is nonsense)
//   - sp >= 0,     spMax > 0
//   - hp <= hpMax (current above max is absurd)
//   - sp <= spMax (current above max is absurd)
//
// Inputs that pass validation are sound values to feed into the
// autopot decision layer.
func validateValues(hp, hpMax, sp, spMax int) bool {
	if hp < 0 || hpMax <= 0 {
		return false
	}
	if sp < 0 || spMax <= 0 {
		return false
	}
	if hp > hpMax {
		return false
	}
	if sp > spMax {
		return false
	}
	return true
}

// readPNGFile decodes a PNG from disk, propagating decode
// errors so NewReader surfaces a clean message.
func readPNGFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	return img, nil
}

// emitDebug writes mask.png (binarized foreground mask), components.png
// (post-CC red bboxes on the strip), one glyph_NN_<char>_S.SS.png per
// candidate, and recognized.txt into debDir. debDir is created if
// missing. Write failures are swallowed silently — debug writes must
// never cause Read to return a fake success. Returns nothing.
func emitDebug(
	enabled bool, debDir string,
	strip image.Image, mask [][]bool, bounds image.Rectangle,
	matches []matchRec, res Result,
) {
	if !enabled || debDir == "" {
		return
	}
	if err := os.MkdirAll(debDir, 0o755); err != nil {
		return
	}
	writeMaskPNG(debDir, mask)
	writeComponentsPNG(debDir, strip, matches, mask)
	writeGlyphCrops(debDir, strip, matches)
	writeRecognizedText(debDir, res, bounds, matches)
}

func writeMaskPNG(debDir string, mask [][]bool) {
	mw := maskWidth(mask)
	mh := maskHeight(mask)
	if mw == 0 || mh == 0 {
		return
	}
	gray := image.NewGray(image.Rect(0, 0, mw, mh))
	for y := 0; y < mh; y++ {
		for x := 0; x < mw; x++ {
			if mask[y][x] {
				gray.SetGray(x, y, color.Gray{Y: 0})
			} else {
				gray.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	_ = os.WriteFile(filepath.Join(debDir, "mask.png"), encodePNG(gray), 0o644)
}

func writeComponentsPNG(debDir string, strip image.Image, matches []matchRec, mask [][]bool) {
	mw := maskWidth(mask)
	mh := maskHeight(mask)
	if strip == nil || mw == 0 || mh == 0 {
		return
	}
	annotated := cloneImage(strip)
	red := color.RGBA{R: 255, A: 255}
	for _, m := range matches {
		drawBox(annotated, m.rect, red)
	}
	_ = os.WriteFile(filepath.Join(debDir, "components.png"), encodePNG(annotated), 0o644)
}

func writeGlyphCrops(debDir string, strip image.Image, matches []matchRec) {
	if strip == nil {
		return
	}
	for _, m := range matches {
		if m.rect.Empty() {
			continue
		}
		crop := cropImage(strip, m.rect)
		if crop == nil {
			continue
		}
		name := fmt.Sprintf("glyph_%02d_%c_%.2f.png", m.idx, m.ch, m.score)
		_ = os.WriteFile(filepath.Join(debDir, name), encodePNG(crop), 0o644)
	}
}

func writeRecognizedText(debDir string, res Result, bounds image.Rectangle, matches []matchRec) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "ok=%v\n", res.OK)
	if res.Reason != "" {
		fmt.Fprintf(&sb, "reason=%s\n", res.Reason)
	}
	fmt.Fprintf(&sb, "text=%q\n", res.Text)
	fmt.Fprintf(&sb, "confidence=%.4f\n", res.Confidence)
	if res.OK {
		fmt.Fprintf(&sb, "hp=%d hpMax=%d sp=%d spMax=%d\n", res.HP, res.HPMax, res.SP, res.SPMax)
	}
	fmt.Fprintf(&sb, "bounds=%v\n", bounds)
	fmt.Fprintf(&sb, "matches:\n")
	for _, m := range matches {
		fmt.Fprintf(&sb, "  idx=%d char=%q score=%.4f rect=%v\n", m.idx, m.ch, m.score, m.rect)
	}
	_ = os.WriteFile(filepath.Join(debDir, "recognized.txt"), []byte(sb.String()), 0o644)
}

// cloneImage returns a fresh RGBA copy of src sized to
// src.Bounds() and with all pixels preserved. Used as the
// surface for debug bbox drawing.
func cloneImage(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// cropImage returns a fresh RGBA image cropped to rect (in
// src.Bounds() coordinates). Returns nil if the rect is empty
// or wholly outside src.
func cropImage(src image.Image, rect image.Rectangle) image.Image {
	if src == nil || rect.Empty() {
		return nil
	}
	clip := rect.Intersect(src.Bounds())
	if clip.Empty() {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, clip.Dx(), clip.Dy()))
	draw.Draw(dst, dst.Bounds(), src, clip.Min, draw.Src)
	return dst
}

// drawBox strokes the perimeter of rect on img with the given
// colour. One-pixel-wide; image.Set writes a single pixel on
// *image.RGBA. Coordinates are img-relative.
func drawBox(img *image.RGBA, rect image.Rectangle, c color.RGBA) {
	if img == nil || rect.Empty() {
		return
	}
	for x := rect.Min.X; x < rect.Max.X; x++ {
		img.Set(x, rect.Min.Y, c)
		img.Set(x, rect.Max.Y-1, c)
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		img.Set(rect.Min.X, y, c)
		img.Set(rect.Max.X-1, y, c)
	}
}

// encodePNG encodes img to PNG bytes using an in-memory
// bytes.Buffer. Returns nil if encoding fails; callers ignore
// the result for debug emissions (best-effort).
func encodePNG(img image.Image) []byte {
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
