package statusui

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testdataPath resolves a path relative to the autopot testdata
// directory, which lives one package up. Using runtime.Caller(0) keeps
// the test working regardless of the process working directory.
func testdataPath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", name)
}

// templatePath resolves the StatusPanel.png template that's checked in
// alongside this test file (kept next to the source rather than in
// testdata/ so the //go:embed in non-test builds finds it).
func templatePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "assets", "StatusPanel.png")
}

func loadPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func TestFindStatusPanel_TopLeftOnDriftFixtures(t *testing.T) {
	template := loadPNG(t, templatePath(t))
	t.Logf("template: %dx%d", template.Bounds().Dx(), template.Bounds().Dy())

	cases := []string{
		"status_bar_drift1.png",
		"status_bar_drift2.png",
		"status_bar_drift3.png",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			img := loadPNG(t, testdataPath(t, name))
			t.Logf("image:    %dx%d", img.Bounds().Dx(), img.Bounds().Dy())

			rect, score, ok := FindStatusPanel(img, template, FindStatusPanelOptions{})
			if !ok {
				t.Fatalf("FindStatusPanel returned ok=false (score=%.4f)", score)
			}
			t.Logf("found at %v with score=%.4f", rect, score)

			// Sanity: the panel should be found somewhere on the
			// screen with a low score (high confidence). The exact
			// position varies between fixtures — drift1 has it in
			// the top-left, drift2 lower on the left, drift3 on the
			// right — so we don't assert a specific quadrant, just
			// that the rect is in-bounds and the score is well
			// below the default 0.15 threshold.
			ib := img.Bounds()
			if !rect.In(ib) {
				t.Errorf("panel rect %v is outside image bounds %v", rect, ib)
			}
			if rect.Dx() != template.Bounds().Dx() || rect.Dy() != template.Bounds().Dy() {
				t.Errorf("panel rect %v has dimensions %dx%d, expected %dx%d (template size)",
					rect, rect.Dx(), rect.Dy(), template.Bounds().Dx(), template.Bounds().Dy())
			}
			if score > 0.15 {
				t.Errorf("panel score %.4f is too high (expected < 0.15 for a real match)", score)
			}
		})
	}
}

func TestFindStatusPanel_NotFoundWhenTemplateDoesNotMatch(t *testing.T) {
	// A solid black image cannot match a 218x58 template that has
	// content (the template is mostly white-on-dark, so the SAD
	// against a fully-black image is very high). The function
	// should return ok=false.
	//
	// We deliberately don't use a flat-WHITE image: the template
	// itself is mostly white, so a white image would have a low
	// SAD (~0.13) and incorrectly pass the default 0.15 threshold.
	// Black is the right contrast to exercise the "no match" path.
	img := image.NewRGBA(image.Rect(0, 0, 500, 500))
	// image.NewRGBA returns a solid black image (all zeros) by default.
	template := loadPNG(t, templatePath(t))
	rect, score, ok := FindStatusPanel(img, template, FindStatusPanelOptions{})
	if ok {
		t.Fatalf("expected ok=false on a flat-black image, got rect=%v score=%.4f", rect, score)
	}
	if !rect.Empty() {
		t.Errorf("expected empty rect on miss, got %v", rect)
	}
	// Score must be above the default threshold — otherwise a future
	// regression that lowers the default MaxScore could silently flip
	// this test to a false-positive pass.
	if score <= 0.15 {
		t.Errorf("expected score > 0.15 on a flat-black image (high SAD), got %.4f", score)
	}
	t.Logf("correctly returned ok=false with score=%.4f", score)
}

func TestFindStatusPanel_EmptyImageDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("FindStatusPanel panicked on empty image: %v", r)
		}
	}()
	template := loadPNG(t, templatePath(t))
	img := image.NewRGBA(image.Rect(0, 0, 0, 0)) // zero-size
	_, _, _ = FindStatusPanel(img, template, FindStatusPanelOptions{})
	// We don't care about ok — we just care that no panic happens.
}

// TestFindStatusPanel_TopLeftMissContinuesToFullImageScan forces the
// second-pass continuation by giving FindStatusPanel a top-left region
// too small to contain the 218×58 panel. It then verifies that the
// continuation scan across the full image still finds the panel at a
// sensible location + score. This stands in for any case where the
// real panel is not in the default 400×200 top-left corner — the
// search must continue, not silently miss it.
func TestPrecomputeImageGrayscale_HonorsNonZeroBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 12, 22))
	img.SetRGBA(10, 20, color.RGBA{R: 20, G: 40, B: 80, A: 255})
	img.SetRGBA(11, 21, color.RGBA{R: 200, G: 100, B: 30, A: 255})

	gray, ok := precomputeImageGrayscale(img, img.Bounds())
	if !ok {
		t.Fatal("expected RGBA fast path")
	}
	if got, want := gray[0][0], luma8(img.At(10, 20)); got != want {
		t.Errorf("gray[0][0] = %d, want %d", got, want)
	}
	if got, want := gray[1][1], luma8(img.At(11, 21)); got != want {
		t.Errorf("gray[1][1] = %d, want %d", got, want)
	}
}

func TestSadOnGrayscalePartial_HonorsNonZeroBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 12, 22))
	img.SetRGBA(10, 20, color.RGBA{R: 20, G: 40, B: 80, A: 255})
	img.SetRGBA(11, 20, color.RGBA{R: 200, G: 100, B: 30, A: 255})
	img.SetRGBA(10, 21, color.RGBA{R: 50, G: 60, B: 70, A: 255})
	img.SetRGBA(11, 21, color.RGBA{R: 90, G: 100, B: 110, A: 255})

	imgGray, ok := precomputeImageGrayscale(img, img.Bounds())
	if !ok {
		t.Fatal("expected RGBA fast path")
	}
	tplGray := imgGray
	got := sadOnGrayscalePartial(imgGray, tplGray, img.Bounds(), 10, 20, 0, 0, 2, 2, 1e9)
	if got != 0 {
		t.Fatalf("SAD for identical non-zero-bounded image = %v, want 0", got)
	}
}

func TestFindStatusPanel_TopLeftMissContinuesToFullImageScan(t *testing.T) {
	template := loadPNG(t, templatePath(t))
	img := loadPNG(t, testdataPath(t, "status_bar_drift1.png"))

	// 50×50 doesn't fit a 218×58 panel at all — top-left scan yields no
	// valid positions and the search continues across the full image.
	rect, score, ok := FindStatusPanel(img, template, FindStatusPanelOptions{
		TopLeftRegion: image.Rect(0, 0, 50, 50),
	})
	if !ok {
		t.Fatalf("full-image continuation failed: score=%.4f", score)
	}
	t.Logf("full-image continuation found panel at %v with score=%.4f", rect, score)

	// Sanity: panel should still be in the top-left quadrant, and the
	// reported Y should be around 94 (where the panel is in this
	// fixture) — NOT the (0,0) default that a continuation scan
	// didn't traverse would return. We assert the Y range rather
	// than equality because the full-image scan may land within a
	// few pixels of the exact panel location.
	if rect.Empty() {
		t.Fatalf("continuation returned empty rect — bestX/Y not initialized across scan")
	}
	if rect.Min.Y < 50 {
		t.Fatalf("continuation returned Y=%d, expected around 94 — the top-left 50×50 region was excluded so the panel must be below it", rect.Min.Y)
	}
	if rect.Min.Y > 150 {
		t.Errorf("continuation returned Y=%d, too far below the known panel position (~94) for drift1", rect.Min.Y)
	}
	ib := img.Bounds()
	if rect.Min.X > ib.Dx()/3 {
		t.Errorf("panel at %v is outside top-left third of %v", rect, ib)
	}
}
