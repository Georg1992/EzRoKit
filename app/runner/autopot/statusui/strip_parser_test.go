package statusui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ----------------------------------------------------------------------------
// Hand-rolled 7×9 monospace bitmap font for the 16 runes the reader needs.
// Each glyph is a [9]byte, low 7 bits per row, bit=1 is foreground.
// Used by TestReader_IntegrationSynthetic to render BOTH the templates
// (saved via NewReader into a t.TempDir) AND the synthetic strip image,
// so the round-trip tests a complete recognisable pipeline.
// ----------------------------------------------------------------------------

var font9 = map[rune][9]byte{
	'0': {0b0111110, 0b1100011, 0b1100111, 0b1101011, 0b1101011, 0b1100111, 0b1100011, 0b0111110, 0b0000000},
	'1': {0b0001100, 0b0011100, 0b0111100, 0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0111111, 0b0000000},
	'2': {0b0111110, 0b1100011, 0b0000011, 0b0000110, 0b0001100, 0b0011000, 0b0110000, 0b1111111, 0b0000000},
	'3': {0b0111110, 0b1100011, 0b0000011, 0b0001110, 0b0000110, 0b0000011, 0b1100011, 0b0111110, 0b0000000},
	'4': {0b0000110, 0b0001110, 0b0011110, 0b0110110, 0b1100110, 0b1111111, 0b0000110, 0b0000110, 0b0000000},
	'5': {0b1111111, 0b1100000, 0b1111110, 0b0000011, 0b0000011, 0b0000011, 0b1100011, 0b0111110, 0b0000000},
	'6': {0b0011110, 0b0111000, 0b1100000, 0b1111110, 0b1100011, 0b1100011, 0b1100011, 0b0111110, 0b0000000},
	'7': {0b1111111, 0b0000011, 0b0000110, 0b0001100, 0b0011000, 0b0110000, 0b0110000, 0b0110000, 0b0000000},
	'8': {0b0111110, 0b1100011, 0b1100011, 0b0111110, 0b1100011, 0b1100011, 0b1100011, 0b0111110, 0b0000000},
	'9': {0b0111110, 0b1100011, 0b1100011, 0b0111111, 0b0000011, 0b0000011, 0b0000110, 0b0111100, 0b0000000},
	// '.' rendered as a 2×2 dot at rows 6..7 cols 2..3; area = 4
	// px so it survives the findComponents <area filter built to
	// drop isolated 1×2 pixel pairs inside digits (e.g. the waist
	// bump of '0' that would otherwise resize-down to a 2×1 dot
	// template and tie at score 1.0, producing spurious '.'  matches).
	'.': {0b0000000, 0b0000000, 0b0000000, 0b0000000, 0b0000000, 0b0000000, 0b0011000, 0b0011000, 0b0000000},
	'/': {0b0000011, 0b0000110, 0b0001100, 0b0011000, 0b0110000, 0b1100000, 0b1000000, 0b0000000, 0b0000000},
	'|': {0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0001100, 0b0000000},
	'H': {0b1100011, 0b1100011, 0b1100011, 0b1111111, 0b1100011, 0b1100011, 0b1100011, 0b1100011, 0b0000000},
	'P': {0b1111110, 0b1100011, 0b1100011, 0b1111110, 0b1100000, 0b1100000, 0b1100000, 0b1100000, 0b0000000},
	'S': {0b0111111, 0b1100000, 0b1100000, 0b0111110, 0b0000011, 0b0000011, 0b0000011, 0b1111110, 0b0000000},
}

// renderGlyph returns a white-background, black-foreground RGBA
// image with the given glyph filled into its bounding box.
// Unknown glyphs render as a 7×9 white rectangle, which produces
// a 0.0 match score against every template (so callers can verify
// the matching rejects empty/unknown glyphs loudly).
func renderGlyph(r rune) image.Image {
	const w, h = 7, 9
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	g, ok := font9[r]
	if !ok {
		return img // all white, all-zero foreground
	}
	for y := 0; y < h; y++ {
		row := g[y]
		for x := 0; x < w; x++ {
			if (row>>(6-x))&1 == 1 {
				img.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
			} else {
				img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
	return img
}

// synthStrip renders text using renderGlyph for each rune,
// separated by 1 px of whitespace, on a single RGBA image.
// Same font as templates → matching after binarization is
// guaranteed for valid input.
func synthStrip(text string) image.Image {
	const glyphW, glyphH, gap = 7, 9, 1
	totalW := len([]rune(text))*(glyphW+gap) - gap
	img := image.NewRGBA(image.Rect(0, 0, totalW, glyphH))
	// Whole-strip white background is the binarize-empty case
	// (interior whitespace is between glyphs).
	xCursor := 0
	for _, r := range text {
		gi := renderGlyph(r)
		for y := 0; y < glyphH; y++ {
			for x := 0; x < glyphW; x++ {
				img.Set(xCursor+x, y, gi.At(x, y))
			}
		}
		xCursor += glyphW + gap
	}
	return img
}

// writeGlyphTemplates saves one PNG per rune into dir using
// the same renderGlyph used by synthStrip. Filenames use the
// filenameRune conventions (0..9, dot, slash, pipe, H, P, S).
func writeGlyphTemplates(t *testing.T, dir string) {
	t.Helper()
	nameFor := func(r rune) string {
		switch r {
		case '.':
			return "dot"
		case '/':
			return "slash"
		case '|':
			return "pipe"
		}
		if r >= '0' && r <= '9' {
			return string(r)
		}
		return string(r)
	}
	for r := range font9 {
		fp := filepath.Join(dir, nameFor(r)+".png")
		f, err := os.Create(fp)
		if err != nil {
			t.Fatalf("create %s: %v", fp, err)
		}
		if err := png.Encode(f, renderGlyph(r)); err != nil {
			t.Fatalf("encode %s: %v", fp, err)
		}
		f.Close()
	}
}

// ----------------------------------------------------------------------------
// Unit tests
// ----------------------------------------------------------------------------

func TestBinarize_DarkTextForegroundOnWhiteBackground(t *testing.T) {
	// 4x2 image: black on the left half, white on the right half.
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	img.Set(1, 0, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	img.Set(2, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	img.Set(3, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	mask := binarize(img)
	if w := maskWidth(mask); w != 4 {
		t.Fatalf("mask width = %d, want 4", w)
	}
	if h := maskHeight(mask); h != 2 {
		t.Fatalf("mask height = %d, want 2", h)
	}
	want := [][]bool{
		{true, true, false, false},
		{false, false, false, false},
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			if mask[y][x] != want[y][x] {
				t.Errorf("mask[%d][%d] = %v, want %v", y, x, mask[y][x], want[y][x])
			}
		}
	}
}

func TestBinarize_RedTextForegroundLikeDarkText(t *testing.T) {
	// Same shape as the dark test but text is bright red.
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{R: 200, G: 30, B: 30, A: 255})
	img.Set(1, 0, color.RGBA{R: 180, G: 20, B: 20, A: 255})
	img.Set(2, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	img.Set(3, 0, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	mask := binarize(img)
	if !mask[0][0] || !mask[0][1] {
		t.Errorf("red foreground pixel not detected: mask[0][0..1] = %v", []bool{mask[0][0], mask[0][1]})
	}
	if mask[0][2] || mask[0][3] {
		t.Errorf("white background wrongly marked foreground: mask[0][2..3] = %v", []bool{mask[0][2], mask[0][3]})
	}
}

func TestBinarize_AllWhiteReturnsEmptyMask(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	mask := binarize(img)
	if len(mask) != 3 || maskWidth(mask) != 3 {
		t.Fatalf("mask dim = %dx%d, want 3x3", maskHeight(mask), maskWidth(mask))
	}
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			if mask[y][x] {
				t.Errorf("mask[%d][%d] = true; want false (all-white background)", y, x)
			}
		}
	}
}

func TestFindComponents_TwoBlocksSplitByEmptyColumn(t *testing.T) {
	// Two 4×1 blocks of foreground, 2 fully-empty rows between
	// them, plus 1 empty row above and 1 below so each block
	// has area >= 4 (the findComponents micro-noise filter
	// drops CCs with area < 4). Connected-components groups
	// each block separately since the empty rows disconnect.
	mask := [][]bool{
		{false, false, false, false},
		{true, true, true, true},
		{false, false, false, false},
		{false, false, false, false},
		{true, true, true, true},
		{false, false, false, false},
	}
	comps := findComponents(mask, image.Rect(0, 0, 4, 6))
	if len(comps) != 2 {
		t.Fatalf("got %d components, want 2: %v", len(comps), comps)
	}
	if comps[0] != (image.Rect(0, 1, 4, 2)) {
		t.Errorf("comps[0] = %v, want Rect(0,1,4,2)", comps[0])
	}
	if comps[1] != (image.Rect(0, 4, 4, 5)) {
		t.Errorf("comps[1] = %v, want Rect(0,4,4,5)", comps[1])
	}
}

func TestFindComponents_TightVerticalBBox(t *testing.T) {
	// 6×6 mask with foreground only in rows 1..4 of cols 2..3
	// (8 px, well above the area-4 filter cutoff). Tight Y
	// must be 1..5 even though mask rows 0 and 5 are empty.
	mask := [][]bool{
		{false, false, false, false, false, false},
		{false, false, true, true, false, false},
		{false, false, true, true, false, false},
		{false, false, true, false, false, false},
		{false, false, true, true, false, false},
		{false, false, false, false, false, false},
	}
	comps := findComponents(mask, image.Rect(0, 0, 6, 6))
	if len(comps) != 1 {
		t.Fatalf("got %d, want 1", len(comps))
	}
	if comps[0] != (image.Rect(2, 1, 4, 5)) {
		t.Errorf("bbox = %v, want Rect(2,1,4,5) (tight y-range)", comps[0])
	}
}

func TestFindComponents_MicroNoiseFiltered(t *testing.T) {
	// A 1×2 isolated pixel pair (at (3,0) and (0,3)) plus a
	// 5×4 main block at cols 4..7; only the latter survives
	// the area<minGlyphArea(4) filter.
	mask := [][]bool{
		{false, false, false, false, true, true, true, true},
		{false, false, false, false, true, true, true, true},
		{false, false, false, false, true, true, true, true},
		{true, false, false, false, true, true, true, true},
		{false, false, false, false, true, true, true, true},
	}
	comps := findComponents(mask, image.Rect(0, 0, 8, 5))
	if len(comps) != 1 {
		t.Fatalf("got %d components, want 1 (after noise-filter): %v", len(comps), comps)
	}
	// Main block: rows 0..4 cols 4..7 (5×4=20 px, well above
	// the 4-px area cutoff). Isolated 1×2 noise at (3,0)+(0,3)
	// area = 2 px, filtered out by minGlyphArea.
	if comps[0] != (image.Rect(4, 0, 8, 5)) {
		t.Errorf("comps[0] = %v, want Rect(4,0,8,5)", comps[0])
	}
}

func TestMaskEqualFraction_MatchingScoresHigherThanDifferent(t *testing.T) {
	a := [][]bool{
		{true, false},
		{false, true},
	}
	b := [][]bool{
		{true, false},
		{false, true},
	}
	if got := maskEqualFraction(a, b); got != 1.0 {
		t.Errorf("identical masks score = %v, want 1.0", got)
	}
	c := [][]bool{
		{false, true},
		{true, false},
	}
	if got := maskEqualFraction(a, c); got != 0.0 {
		t.Errorf("opposite masks score = %v, want 0.0", got)
	}
}

func TestResizeMaskNearest_PreservesCornerPixels(t *testing.T) {
	// src is intentionally sparse: only src[0][0] is true.
	// After a 2×2 → 5×5 nearest-neighbor resize, the integer
	// step `y*gH/tH` (resp. `x*gW/tW`) is:
	//
	//   y=0..2 → sy=0          x=0..2 → sx=0     (top-left
	//                       quadrant maps to src[0][0] = true)
	//   y=3..4 → sy=1          x=3..4 → sx=1     (bottom-right
	//                       quadrant maps to src[1][1] = false)
	//
	// Cross-quadrants (e.g. y=0..2, x=3..4) map to src[0][1]=false.
	src := [][]bool{
		{true, false},
		{false, false},
	}
	dst := resizeMaskNearest(src, 2, 2, 5, 5)
	if maskWidth(dst) != 5 || maskHeight(dst) != 5 {
		t.Fatalf("dst dim = %dx%d, want 5x5", maskHeight(dst), maskWidth(dst))
	}
	// Anchor: dst[0][0] must be true (src[0][0]).
	if !dst[0][0] {
		t.Error("dst[0][0] should be true (maps to src[0][0])")
	}
	// Spot-check the top-left quadrant fills with true.
	for _, p := range [][2]int{{0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 2}} {
		if !dst[p[1]][p[0]] {
			t.Errorf("dst[%d][%d] should be true (top-left quadrant of nearest-neighbour sample from src[0][0])", p[1], p[0])
		}
	}
	// Bottom-right quadrant maps to src[1][1]=false.
	for _, p := range [][2]int{{3, 3}, {3, 4}, {4, 3}, {4, 4}} {
		if dst[p[1]][p[0]] {
			t.Errorf("dst[%d][%d] should be false (maps to src[1][1] = false)", p[1], p[0])
		}
	}
	// Cross-edges map to src[1][0]=false or src[0][1]=false.
	if dst[4][0] {
		t.Error("dst[4][0] should be false (maps to src[1][0])")
	}
	if dst[0][4] {
		t.Error("dst[0][4] should be false (maps to src[0][1])")
	}
}

func TestParseText_Valid(t *testing.T) {
	cases := []struct {
		text  string
		hp    int
		hpMax int
		sp    int
		spMax int
	}{
		{"HP.1045/1290|SP.66/201", 1045, 1290, 66, 201},
		{"HP.987/4294|SP.948/948", 987, 4294, 948, 948},
		{"HP.76/4294SP.948/948", 76, 4294, 948, 948}, // missing pipe tolerated
		{"HP.1290/1290SP.201/201", 1290, 1290, 201, 201},
	}
	for _, c := range cases {
		hp, hpMax, sp, spMax, err := parseText(c.text)
		if err != nil {
			t.Errorf("parseText(%q) err: %v", c.text, err)
			continue
		}
		if hp != c.hp || hpMax != c.hpMax || sp != c.sp || spMax != c.spMax {
			t.Errorf("parseText(%q) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
				c.text, hp, hpMax, sp, spMax, c.hp, c.hpMax, c.sp, c.spMax)
		}
	}
}

func TestParseText_Invalid(t *testing.T) {
	for _, text := range []string{
		"HP.1045/1290XX.66/201", // wrong label
		"HP.1045/1290|SP.66/201noise", // trailing OCR noise
		"noiseHP.1045/1290|SP.66/201", // leading OCR noise
		"HP.66/201",                   // sp missing
		"hello world",                 // not numbers at all
		"",                            // empty
	} {
		if _, _, _, _, err := parseText(text); err == nil {
			t.Errorf("parseText(%q): want error, got nil", text)
		}
	}
}

func TestValidateValues_TableDriven(t *testing.T) {
	cases := []struct {
		hp, hpMax, sp, spMax int
		want                 bool
		why                  string
	}{
		{1045, 1290, 66, 201, true, "spec example, healthy read"},
		{0, 1290, 0, 201, true, "all-zero current is legal (dead character)"},
		{1290, 1290, 201, 201, true, "full HP and SP"},
		{-1, 1290, 0, 201, false, "negative hp"},
		{1045, 0, 0, 201, false, "zero hpMax"},
		{2000, 1290, 0, 201, false, "hp above hpMax"},
		{0, 1290, 300, 201, false, "sp above spMax"},
		{0, 1290, -5, 201, false, "negative sp"},
		{1, 1290, 1, -1, false, "negative spMax"},
	}
	for _, c := range cases {
		got := validateValues(c.hp, c.hpMax, c.sp, c.spMax)
		if got != c.want {
			t.Errorf("validateValues(%d,%d,%d,%d) = %v, want %v (%s)",
				c.hp, c.hpMax, c.sp, c.spMax, got, c.want, c.why)
		}
	}
}

func TestFilenameRune_KnownAndUnknown(t *testing.T) {
	type pair struct {
		base string
		want rune
	}
	cases := []pair{
		{"0", '0'}, {"9", '9'}, {"dot", '.'},
		{"slash", '/'}, {"pipe", '|'},
		{"H", 'H'}, {"P", 'P'}, {"S", 'S'},
	}
	for _, c := range cases {
		got, ok := filenameRune(c.base)
		if !ok || got != c.want {
			t.Errorf("filenameRune(%q) = (%q,%v), want (%q,true)", c.base, got, ok, c.want)
		}
	}
	for _, junk := range []string{"", "Q", "tilde", "NOTE", "Period"} {
		if _, ok := filenameRune(junk); ok {
			t.Errorf("filenameRune(%q): want !ok, got ok", junk)
		}
	}
}

func TestNewReader_IgnoresUnknownFilenames(t *testing.T) {
	dir := t.TempDir()
	writeGlyphTemplates(t, dir)
	// Drop in a junk file alongside legitimate templates.
	if err := os.WriteFile(filepath.Join(dir, "README.txt"),
		[]byte("note"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if got := len(r.templates); got != len(font9) {
		t.Fatalf("loaded %d templates, want %d (only the 16 recognised names)", got, len(font9))
	}
}

func TestNewReader_EmptyDirErrors(t *testing.T) {
	if _, err := NewReader(t.TempDir()); err == nil {
		t.Fatalf("NewReader on empty dir: want error, got nil")
	}
}

// ----------------------------------------------------------------------------
// End-to-end integration: synthetic strip + synthetic templates.
// ----------------------------------------------------------------------------

func TestReader_IntegrationSynthetic(t *testing.T) {
	dir := t.TempDir()
	writeGlyphTemplates(t, dir)
	r, err := NewReader(dir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	cases := []struct {
		text      string
		hp, hpMax int
		sp, spMax int
		minConf   float64 // minimum acceptable Result.Confidence
	}{
		{"HP.1045/1290|SP.66/201", 1045, 1290, 66, 201, 0.95},
		{"HP.987/4294|SP.948/948", 987, 4294, 948, 948, 0.95},
		{"HP.76/4294|SP.948/948", 76, 4294, 948, 948, 0.95},
		{"HP.674/1290|SP.18/201", 674, 1290, 18, 201, 0.95},
		{"HP.1290/1290|SP.201/201", 1290, 1290, 201, 201, 0.95},
	}
	for _, c := range cases {
		t.Run(strings.ReplaceAll(c.text, "|", "_pipe_"), func(t *testing.T) {
			strip := synthStrip(c.text)
			res := r.Read(strip)
			if !res.OK {
				t.Fatalf("Read returned !OK: reason=%q text=%q", res.Reason, res.Text)
			}
			if res.HP != c.hp || res.HPMax != c.hpMax || res.SP != c.sp || res.SPMax != c.spMax {
				t.Errorf("Read(%q) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
					c.text, res.HP, res.HPMax, res.SP, res.SPMax,
					c.hp, c.hpMax, c.sp, c.spMax)
			}
			if res.Confidence < c.minConf {
				t.Errorf("Read(%q) conf=%.4f, want >= %.4f", c.text, res.Confidence, c.minConf)
			}
			if res.Text != c.text && !regexp.MustCompile(`^HP`).
				MatchString(res.Text) {
				t.Errorf("Read text=%q, expected at least an HP-prefixed text", res.Text)
			}
		})
	}
}

func TestReader_DebugArtifactsWritten(t *testing.T) {
	dir := t.TempDir()
	tplDir := filepath.Join(dir, "tpl")
	if err := os.MkdirAll(tplDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGlyphTemplates(t, tplDir)
	r, err := NewReader(tplDir)
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	debDir := filepath.Join(dir, "debug")
	r.Debug = true
	r.DebugDir = debDir
	strip := synthStrip("HP.66/201|SP.18/201")
	res := r.Read(strip)
	if !res.OK {
		t.Fatalf("Read ok=false: %q", res.Reason)
	}
	for _, name := range []string{"mask.png", "components.png", "recognized.txt"} {
		if _, err := os.Stat(filepath.Join(debDir, name)); err != nil {
			t.Errorf("debug artifact %q missing: %v", name, err)
		}
	}
}
