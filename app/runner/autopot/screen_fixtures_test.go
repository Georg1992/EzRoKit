package autopot

import (
	"image"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

type screenBarCase struct {
	file  string
	hpUI  string
	spUI  string
	hpPct float64
	spPct float64
}

func knownBarCases() map[string]screenBarCase {
	return map[string]screenBarCase{
		"aa.png":       {file: "aa.png", hpUI: "751/1290", spUI: "102/201", hpPct: 751.0 / 1290.0 * 100, spPct: 102.0 / 201.0 * 100},
		"gg.png":       {file: "gg.png", hpUI: "411/1254", spUI: "117/195", hpPct: 411.0 / 1254.0 * 100, spPct: 117.0 / 195.0 * 100},
		"ii.png":       {file: "ii.png", hpUI: "1254/1254", spUI: "195/195", hpPct: 100, spPct: 100},
		"jj.png":       {file: "jj.png", hpUI: "120/1290", spUI: "6/201", hpPct: 120.0 / 1290.0 * 100, spPct: 6.0 / 201.0 * 100},
		"pp.png":       {file: "pp.png", hpUI: "1045/1290", spUI: "66/201", hpPct: 1045.0 / 1290.0 * 100, spPct: 66.0 / 201.0 * 100},
		"tt.png":       {file: "tt.png", hpUI: "674/1290", spUI: "18/201", hpPct: 674.0 / 1290.0 * 100, spPct: 18.0 / 201.0 * 100},
		"drift1.png":   {file: "drift1.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"drift1.2.png": {file: "drift1.2.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"drift2.png":   {file: "drift2.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"drift3.png":   {file: "drift3.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"drift4.png":   {file: "drift4.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"drift5.png":   {file: "drift5.png", hpUI: "639/1290", spUI: "33/201", hpPct: 639.0 / 1290.0 * 100, spPct: 33.0 / 201.0 * 100},
		"drift6.png":   {file: "drift6.png", hpUI: "651/1290", spUI: "57/201", hpPct: 651.0 / 1290.0 * 100, spPct: 57.0 / 201.0 * 100},
		"Drift7.png":   {file: "Drift7.png", hpUI: "663/1290", spUI: "93/201", hpPct: 663.0 / 1290.0 * 100, spPct: 93.0 / 201.0 * 100},
		"Drift8.png":   {file: "Drift8.png", hpUI: "1290/1290", spUI: "201/201", hpPct: 100, spPct: 100},
		"zoomed1.png":  {file: "zoomed1.png", hpUI: "675/1290", spUI: "117/201", hpPct: 675.0 / 1290.0 * 100, spPct: 117.0 / 201.0 * 100},
		"nogrftest1.png": {file: "nogrftest1.png", hpUI: "pixel 100%", spUI: "pixel 69%", hpPct: 100, spPct: 69},
		"nogrftest2.png": {file: "nogrftest2.png", hpUI: "pixel 50%", spUI: "pixel 82.8%", hpPct: 50, spPct: 82.8},
	}
}

func knownFixtureFiles(t *testing.T) []string {
	t.Helper()
	known := knownBarCases()
	files := make([]string, 0, len(known))
	for name := range known {
		files = append(files, name)
	}
	sort.Strings(files)
	return files
}

func TestRefreshBarPairFixtures(t *testing.T) {
	known := knownBarCases()
	for _, file := range knownFixtureFiles(t) {
		t.Run(file, func(t *testing.T) {
			img := loadFixture(t, file)
			mapped, err := RefreshBarPair(img)
			if err != nil {
				t.Fatalf("RefreshBarPair: %v", err)
			}
			hp := ReadHPFill(img, mapped.HP)
			sp := ReadSPFill(img, mapped.SP)
			if !hp.Found || !sp.Found {
				t.Fatalf("bars not found hp=%v sp=%v", hp.Found, sp.Found)
			}

			assertBarPairGeometry(t, mapped)

			tc := known[file]
			if !barDeltaOK(hp.Percent, tc.hpPct, mapped.HP.W) {
				t.Fatalf("HP got %.1f%% want %.1f%% (%s)", hp.Percent, tc.hpPct, tc.hpUI)
			}
			if !barDeltaOK(sp.Percent, tc.spPct, mapped.SP.W) {
				t.Fatalf("SP got %.1f%% want %.1f%% (%s)", sp.Percent, tc.spPct, tc.spUI)
			}
		})
	}
}

func TestStabilizerMatchesGameValues(t *testing.T) {
	for _, file := range knownFixtureFiles(t) {
		t.Run(file, func(t *testing.T) {
			hpStab, spStab := newTestStabilizers(1)
			tc := knownBarCases()[file]
			img := loadFixture(t, file)
			mapped, err := RefreshBarPair(img)
			if err != nil {
				t.Fatal(err)
			}

			thp := hpStab.UpdatePair(img, true, mapped, true)
			if !thp.Found || thp.Status == BarStatusUnknown {
				t.Fatal("HP stabilizer read failed")
			}
			if thp.Status != BarStatusFull && !barDeltaOK(thp.Percent, tc.hpPct, mapped.HP.W) {
				t.Fatalf("HP stable %.1f%% want %.1f%% (%s)", thp.Percent, tc.hpPct, tc.hpUI)
			}
			if thp.Status == BarStatusFull && tc.hpPct < 99.9 {
				t.Fatalf("HP falsely full %.1f%% (game %.1f%%)", thp.Percent, tc.hpPct)
			}

			tsp := spStab.UpdatePair(img, false, mapped, true)
			if !tsp.Found || tsp.Status == BarStatusUnknown {
				t.Fatal("SP stabilizer read failed")
			}
			if tsp.Status != BarStatusFull && !barDeltaOK(tsp.Percent, tc.spPct, mapped.SP.W) {
				t.Fatalf("SP stable %.1f%% want %.1f%% (%s)", tsp.Percent, tc.spPct, tc.spUI)
			}
			if tsp.Status == BarStatusFull && tc.spPct < 99.9 {
				t.Fatalf("SP falsely full %.1f%% (game %.1f%%)", tsp.Percent, tc.spPct)
			}
		})
	}
}

func assertBarPairGeometry(t *testing.T, mapped MappedBars) {
	t.Helper()
	if !mapped.Valid {
		t.Fatal("mapped bars not valid")
	}
	if mapped.HP.Y >= mapped.SP.Y {
		t.Fatalf("HP must be above SP: HP=%+v SP=%+v", mapped.HP, mapped.SP)
	}
	if mapped.HP.X != mapped.SP.X {
		t.Fatalf("HP/SP X mismatch: HP=%+v SP=%+v", mapped.HP, mapped.SP)
	}
	if mapped.HP.W != mapped.SP.W {
		t.Fatalf("HP/SP W mismatch: HP=%+v SP=%+v", mapped.HP, mapped.SP)
	}
	if mapped.HP.W < 1 || mapped.SP.W < 1 {
		t.Fatalf("invalid bar width: HP=%+v SP=%+v", mapped.HP, mapped.SP)
	}
}

func barDeltaOK(got, want float64, barW int) bool {
	if want < 10 {
		pxDelta := absInt(int(got/100*float64(barW)+0.5) - int(want/100*float64(barW)+0.5))
		if pxDelta <= 3 {
			return true
		}
		return math.Abs(got-want) <= 5
	}
	return math.Abs(got-want) <= 4
}

func loadFixture(t *testing.T, name string) image.Image {
	t.Helper()
	path := filepath.Join(testdataDir(t), name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("fixture missing %s: %v", name, err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return img
}

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}

// newTestStabilizers creates a pair of BarStabilizer instances for testing.
func newTestStabilizers(threshold int) (*BarStabilizer, *BarStabilizer) {
	hpStab := NewBarStabilizer(true, threshold)
	spStab := NewBarStabilizer(false, threshold)
	return hpStab, spStab
}
