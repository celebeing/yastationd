package app

import (
	"math"
	"os"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func TestWantsFollowUp(t *testing.T) {
	cases := []struct {
		mode, reply string
		want        bool
	}{
		{"question", "Какой свет выключить?", true},
		{"question", "Включить «радугу»?» ", true},
		{"question", "Выключила свет.", false},
		{"always", "Выключила свет.", true},
		{"always", "  ", false},
		{"off", "Что дальше?", false},
	}
	for _, c := range cases {
		if got := wantsFollowUp(c.mode, c.reply); got != c.want {
			t.Errorf("wantsFollowUp(%q, %q) = %v", c.mode, c.reply, got)
		}
	}
}

func TestStateDebouncedFlush(t *testing.T) {
	path := t.TempDir() + "/state.json"
	st := newStateStore(path, defaultState(128, 128, 128, false))
	for v := 0; v < 10; v++ {
		st.update(func(s *persistState) { s.Volume = v })
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("written before the debounce delay")
	}
	st.flush()
	if got := newStateStore(path, defaultState(128, 128, 128, false)).get().Volume; got != 9 {
		t.Fatalf("persisted volume = %d, want 9", got)
	}
}

func TestPowerProblem(t *testing.T) {
	for _, c := range []struct {
		pd  hw.PD
		bad bool
	}{
		{hw.PD{MilliVolts: 12000, Health: "Good"}, false},
		{hw.PD{MilliVolts: 0, Health: "Dead"}, true}, // no contract: bare 5 V
		{hw.PD{MilliVolts: 5000, Health: "Good"}, true},
		{hw.PD{MilliVolts: 12000, Health: "Overvoltage"}, true},
	} {
		if got := powerProblem(c.pd, 9000); got != c.bad {
			t.Errorf("powerProblem(%+v) = %v", c.pd, got)
		}
	}
}

func TestCurveLevel(t *testing.T) {
	c := []config.CurvePoint{{Lux: 0.04, Level: 4}, {Lux: 0.55, Level: 128}, {Lux: 10, Level: 255}}
	for _, k := range []struct {
		lux  float64
		want float64
	}{{0, 4}, {0.03, 4}, {0.04, 4}, {0.55, 128}, {80, 255}} {
		if got := curveLevel(c, k.lux); math.Abs(got-k.want) > 1e-9 {
			t.Errorf("curveLevel(%v) = %v, want %v", k.lux, got, k.want)
		}
	}
	// Between two points the level is geometric in log(lux): halfway is the geometric mean.
	if mid := curveLevel(c, math.Sqrt(0.04*0.55)); math.Abs(mid-math.Sqrt(4*128)) > 1e-6 {
		t.Errorf("curveLevel(mid) = %v, want %v", mid, math.Sqrt(4*128))
	}
	if moveBrightness(128, 130, false) || !moveBrightness(128, 150, false) || !moveBrightness(4, 5, false) ||
		moveBrightness(4, 4.5, false) || !moveBrightness(4, 4.5, true) || moveBrightness(4, 4.1, true) {
		t.Error("hysteresis")
	}
}
