package statusui

import (
	"embed"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io/fs"
)

//go:embed glyphs/*.png
var embeddedGlyphs embed.FS

var (
	// ErrPanelNotFound means template matching did not find a status panel.
	ErrPanelNotFound = errors.New("statusui: panel not found")
	// ErrStripNotFound means status strip extraction produced no crop.
	ErrStripNotFound = errors.New("statusui: status strip not found")
)

// ParsedStatus is the normalized HP/SP payload produced by strip parsing.
type ParsedStatus struct {
	HP    int
	HPMax int
	SP    int
	SPMax int
}

// StripParseResult contains both parsed numeric values and parser diagnostics.
type StripParseResult struct {
	ParsedStatus
	Text       string
	Confidence float64
}

// ScreenRecognition is the full output of the canonical statusui pipeline.
//
// Pipeline stages:
//  1. FindStatusPanel (layout locate)
//  2. VerifyPanel (layout verification)
//  3. ExtractStatusLineStrip (text-strip extraction)
//  4. ParseStrip (HP/SP parser)
//
// OverlayImage is a copy of the input screenshot with panel+strip boxes and
// the parsed HP/SP values rendered in the corner.
type ScreenRecognition struct {
	PanelRect    image.Rectangle
	PanelScore   float64
	PanelImage   image.Image
	StripRect    image.Rectangle
	StripImage   image.Image
	ParseResult  StripParseResult
	OverlayImage image.Image
}

// Pipeline is the single, canonical status recognition implementation.
type Pipeline struct {
	template image.Image
	locator  StatusLineLocator
	reader   *Reader
}

// NewPipeline builds the canonical status pipeline.
func NewPipeline(templatesDir string, minGlyphScore float64) (*Pipeline, error) {
	tpl := DefaultStatusPanelTemplate()
	if tpl == nil {
		return nil, errors.New("statusui: embedded StatusPanel template unavailable")
	}
	r, err := NewReader(templatesDir)
	if err != nil {
		return nil, err
	}
	r.MinGlyphScore = minGlyphScore
	if r.MinGlyphScore == 0 {
		r.MinGlyphScore = 0.70
	}
	return &Pipeline{
		template: tpl,
		locator:  DefaultStatusLineLocator(),
		reader:   r,
	}, nil
}

// NewDefaultPipeline builds a Pipeline from embedded glyph templates.
// No external file paths are required; suitable for production use.
func NewDefaultPipeline() (*Pipeline, error) {
	return NewPipelineFromFS(embeddedGlyphs, "glyphs", 0.70)
}

// NewPipelineFromFS builds a Pipeline loading glyph templates from fsys/glyphsDir.
func NewPipelineFromFS(fsys fs.FS, glyphsDir string, minGlyphScore float64) (*Pipeline, error) {
	tpl := DefaultStatusPanelTemplate()
	if tpl == nil {
		return nil, errors.New("statusui: embedded StatusPanel template unavailable")
	}
	r, err := NewReaderFromFS(fsys, glyphsDir)
	if err != nil {
		return nil, err
	}
	r.MinGlyphScore = minGlyphScore
	if r.MinGlyphScore == 0 {
		r.MinGlyphScore = 0.70
	}
	return &Pipeline{
		template: tpl,
		locator:  DefaultStatusLineLocator(),
		reader:   r,
	}, nil
}

// ParseStrip parses HP/SP values from a pre-cropped status strip.
func (p *Pipeline) ParseStrip(strip image.Image) (StripParseResult, error) {
	if p == nil || p.reader == nil {
		return StripParseResult{}, errors.New("statusui: pipeline is not initialized")
	}
	res := p.reader.Read(strip)
	if !res.OK {
		return StripParseResult{
			Text:       res.Text,
			Confidence: res.Confidence,
		}, fmt.Errorf("statusui: parse strip failed: %s", res.Reason)
	}
	return StripParseResult{
		ParsedStatus: ParsedStatus{
			HP:    res.HP,
			HPMax: res.HPMax,
			SP:    res.SP,
			SPMax: res.SPMax,
		},
		Text:       res.Text,
		Confidence: res.Confidence,
	}, nil
}

// RecognizeScreen runs the canonical end-to-end pipeline on a screenshot.
func (p *Pipeline) RecognizeScreen(screen image.Image) (ScreenRecognition, error) {
	if p == nil {
		return ScreenRecognition{}, errors.New("statusui: pipeline is nil")
	}
	if screen == nil {
		return ScreenRecognition{}, errors.New("statusui: nil screenshot")
	}

	panelRect, panelScore, ok := FindStatusPanel(screen, p.template, FindStatusPanelOptions{})
	if !ok {
		return ScreenRecognition{PanelScore: panelScore}, ErrPanelNotFound
	}

	out := ScreenRecognition{
		PanelRect:  panelRect,
		PanelScore: panelScore,
		PanelImage: ExtractROI(screen, panelRect),
	}
	if out.PanelImage == nil {
		return out, ErrPanelNotFound
	}
	if err := VerifyPanel(out.PanelImage); err != nil {
		return out, err
	}

	stripRect := p.locator.LocateStatusTextLine(panelRect)
	strip := ExtractROI(screen, stripRect)
	if strip == nil || strip.Bounds().Dx() != p.locator.Width || strip.Bounds().Dy() != p.locator.Height {
		// Never parse a clipped strip: a partial HP/SP line can still
		// contain a regex-looking substring and produce a plausible but
		// incorrect value. Treat it as an acquisition failure instead.
		return out, ErrStripNotFound
	}
	out.StripRect = stripRect
	out.StripImage = strip
	parsed, err := p.ParseStrip(strip)
	out.ParseResult = parsed
	out.OverlayImage = OverlayStatusValues(screen, panelRect, stripRect, parsed.ParsedStatus)
	return out, err
}

// OverlayStatusValues returns a copy of screen with panel/strip outlines and
// parsed HP/SP values drawn into a compact banner.
func OverlayStatusValues(screen image.Image, panelRect, stripRect image.Rectangle, status ParsedStatus) image.Image {
	if screen == nil {
		return nil
	}
	dst := image.NewRGBA(screen.Bounds())
	draw.Draw(dst, dst.Bounds(), screen, screen.Bounds().Min, draw.Src)

	drawRectOutline(dst, panelRect, color.RGBA{R: 0, G: 200, B: 0, A: 255})
	drawRectOutline(dst, stripRect, color.RGBA{R: 220, G: 30, B: 30, A: 255})

	label := fmt.Sprintf("HP=%d/%d SP=%d/%d", status.HP, status.HPMax, status.SP, status.SPMax)
	drawTinyLabel(dst, label, 4, 4)
	return dst
}

var tinyFont5x7 = map[rune][7]string{
	'0': {" ### ", "#   #", "#  ##", "# # #", "##  #", "#   #", " ### "},
	'1': {"  #  ", " ##  ", "  #  ", "  #  ", "  #  ", "  #  ", " ### "},
	'2': {" ### ", "#   #", "    #", "   # ", "  #  ", " #   ", "#####"},
	'3': {" ### ", "#   #", "    #", "  ## ", "    #", "#   #", " ### "},
	'4': {"   # ", "  ## ", " # # ", "#  # ", "#####", "   # ", "   # "},
	'5': {"#####", "#    ", "#### ", "    #", "    #", "#   #", " ### "},
	'6': {" ### ", "#   #", "#    ", "#### ", "#   #", "#   #", " ### "},
	'7': {"#####", "    #", "   # ", "  #  ", " #   ", " #   ", " #   "},
	'8': {" ### ", "#   #", "#   #", " ### ", "#   #", "#   #", " ### "},
	'9': {" ### ", "#   #", "#   #", " ####", "    #", "#   #", " ### "},
	'H': {"#   #", "#   #", "#   #", "#####", "#   #", "#   #", "#   #"},
	'P': {"#### ", "#   #", "#   #", "#### ", "#    ", "#    ", "#    "},
	'S': {" ####", "#    ", "#    ", " ### ", "    #", "    #", "#### "},
	'=': {"     ", "#####", "     ", "#####", "     ", "     ", "     "},
	'/': {"    #", "   # ", "   # ", "  #  ", " #   ", " #   ", "#    "},
	' ': {"     ", "     ", "     ", "     ", "     ", "     ", "     "},
}

func drawTinyLabel(dst *image.RGBA, label string, x, y int) {
	if dst == nil {
		return
	}
	charW := 5
	charH := 7
	gap := 1
	pad := 2
	bgW := pad*2 + len(label)*(charW+gap)
	bgH := pad*2 + charH
	bg := image.Rect(x, y, x+bgW, y+bgH)
	draw.Draw(dst, bg, &image.Uniform{C: color.RGBA{R: 0, G: 0, B: 0, A: 220}}, image.Point{}, draw.Src)

	cx := x + pad
	for _, r := range label {
		glyph, ok := tinyFont5x7[r]
		if !ok {
			glyph = tinyFont5x7[' ']
		}
		for gy := 0; gy < charH; gy++ {
			row := glyph[gy]
			for gx := 0; gx < charW; gx++ {
				if gx < len(row) && row[gx] != ' ' {
					px := cx + gx
					py := y + pad + gy
					if image.Pt(px, py).In(dst.Bounds()) {
						dst.Set(px, py, color.RGBA{R: 255, G: 255, B: 255, A: 255})
					}
				}
			}
		}
		cx += charW + gap
	}
}
