package app

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

func TestSplitLevel(t *testing.T) {
	for _, c := range []struct {
		level     float64
		bl, drive int
	}{{255, 255, 255}, {128, 128, 255}, {2.5, 3, 213}, {2, 2, 255}, {1, 1, 255}, {0.3, 1, 255}, {300, 255, 255}} {
		if bl, d := splitLevel(c.level); bl != c.bl || d != c.drive {
			t.Errorf("splitLevel(%v) = %d, %d; want %d, %d", c.level, bl, d, c.bl, c.drive)
		}
	}
	// The product follows the level smoothly, and the frame never drops below 2/3 of full scale
	// above a level of 1.
	for l := 1.0; l <= 255; l += 0.37 {
		bl, d := splitLevel(l)
		if got := float64(bl) * float64(d) / 255; math.Abs(got-l)/l > 0.01 || (l >= 2 && d < 170) {
			t.Fatalf("level %v: backlight %d x drive %d = %v", l, bl, d, got)
		}
	}
}

func TestStepBrightnessGlidesInLogSpace(t *testing.T) {
	a := &App{store: newStateStore("", defaultState(128, 128, 4, false))}
	t0 := time.Now()
	a.stepBrightness(t0)
	if l := a.shownLevel(); l != 4 {
		t.Fatalf("first step jumps to the target, got %v", l)
	}
	a.store.update(func(s *persistState) { s.ClockBright = 5 })
	a.stepBrightness(t0.Add(brightGlide))
	// A small change: one time constant, 1-1/e of the way in log space.
	if l := a.shownLevel(); math.Abs(l-4*math.Pow(5.0/4, 1-1/math.E)) > 0.01 {
		t.Fatalf("after one glide constant: %v", l)
	}
	a.store.update(func(s *persistState) { s.ClockBright = 64 })
	t1 := t0.Add(brightGlide)
	from := a.shownLevel()
	for i := 1; i <= 20; i++ { // 1 s in 50 ms render ticks
		a.stepBrightness(t1.Add(time.Duration(i) * 50 * time.Millisecond))
	}
	// A big change runs at the capped pace.
	if got, want := math.Log(a.shownLevel()/from), brightMaxRate; math.Abs(got-want) > 0.01 {
		t.Fatalf("1 s of a big change: ln ratio %v, want %v", got, want)
	}
	a.stepBrightness(t1.Add(30 * time.Second))
	if l := a.shownLevel(); l != 64 {
		t.Fatalf("settles on the target, got %v", l)
	}
}

func TestMedianDropsSpikes(t *testing.T) {
	if m := median([]float64{1, 1, 9, 1, 1}); m != 1 {
		t.Fatal(m)
	}
	if m := median([]float64{1, 3}); m != 2 {
		t.Fatal(m)
	}
}

func TestBrightnessCurveMinimum(t *testing.T) {
	var p board.Profile
	p.Matrix.MinBrightness = 2
	cfg := config.Default()
	if c := brightnessCurve(cfg, p); c[0].Level != 2 || config.DefaultBrightnessCurve[0].Level != 4 {
		t.Fatalf("the board's minimum is the default curve's darkest point (without touching the default): %v", c)
	}
	a := &App{prof: p, store: newStateStore("", defaultState(128, 128, 1, false))}
	if got := a.brightnessTarget(); got != 2 {
		t.Fatalf("manual level floors at the minimum: %v", got)
	}
}

// Auto brightness spans the dimmest (the config's, else the board's) to the clock brightness:
// the curve's darkest level maps to the first, 255 to the second.
func TestAutoBrightnessRange(t *testing.T) {
	a := &App{luxPath: "x", store: newStateStore("", defaultState(128, 128, 200, true))}
	a.prof.Matrix.MinBrightness = 4
	at := func(curveLevel float64) float64 {
		a.autoBright.Store(math.Float64bits(curveLevel))
		return a.brightnessTarget()
	}
	if got := at(4); got != 4 {
		t.Fatalf("darkest: %v", got)
	}
	if got := at(255); got != 200 {
		t.Fatalf("brightest = the clock brightness: %v", got)
	}
	if got := at(129.5); math.Abs(got-102) > 0.01 {
		t.Fatalf("halfway: %v", got)
	}
	c := config.Default()
	c.AutoBrightnessMin = 20
	a.conf.Store(&c)
	if got := at(4); got != 20 {
		t.Fatalf("the config's dimmest: %v", got)
	}
	// A clock brightness below the dimmest: the dimmest.
	a.store.update(func(s *persistState) { s.ClockBright = 10 })
	if got := at(255); got != 20 {
		t.Fatalf("max below min: %v", got)
	}
	// Setting the clock brightness keeps auto on now.
	a.cmdClockBrightness([]byte("150"))
	if s := a.store.get(); !s.ClockAuto || s.ClockBright != 150 {
		t.Fatalf("after setting it: %+v", s)
	}
}

// A state file from before: with auto on its clock level was unused, so it becomes 255 (the auto
// maximum it had); with auto off it stays.
func TestClockMaxMigration(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		file string
		want int
	}{{`{"clock_auto":true,"clock_bright":1}`, 255}, {`{"clock_auto":false,"clock_bright":40}`, 40},
		{`{"clock_auto":true,"clock_bright":90,"clock_bright_is_max":true}`, 90}} {
		p := dir + "/state"
		os.WriteFile(p, []byte(c.file), 0o644)
		if got := newStateStore(p, defaultState(128, 128, 255, true)).get(); got.ClockBright != c.want || !got.ClockMax {
			t.Errorf("%s: %d, want %d", c.file, got.ClockBright, c.want)
		}
	}
}
