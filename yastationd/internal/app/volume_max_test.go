package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

func TestVolumeMaxScalesAfterTheCurve(t *testing.T) {
	prof, err := board.Builtin("orion")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{prof: prof}
	dc := config.Default()
	a.conf.Store(&dc)
	if a.rawFromPercent(100) != 255 || a.rawFromPercent(50) != 163 {
		t.Fatalf("no cap: 100 %% = %d, 50 %% = %d", a.rawFromPercent(100), a.rawFromPercent(50))
	}
	a.cfg().VolumeMax = 80
	top := prof.Audio.RawFromPercent(80) // the curve at 80 %
	if got := a.rawFromPercent(100); got != top {
		t.Errorf("100 %% = %d, want the curve's %d at volume_max", got, top)
	}
	if got, want := a.rawFromPercent(50), (163*top+127)/255; got != want {
		t.Errorf("50 %% = %d, want the curve's 163 scaled to %d", got, want)
	}
	for pct := 0; pct <= 100; pct += 5 {
		if got := a.percentFromRaw(a.rawFromPercent(pct)); got != pct {
			t.Errorf("round trip %d %% -> %d %%", pct, got)
		}
	}
}

func TestRingPercentSteps(t *testing.T) {
	prof, err := board.Builtin("orion")
	if err != nil {
		t.Fatal(err)
	}
	a := &App{prof: prof}
	dc := config.Default()
	a.conf.Store(&dc)
	// every percent from 5 up maps to its own raw value and reads back as itself
	for pct := 5; pct <= 100; pct++ {
		if got := a.percentFromRaw(a.rawFromPercent(pct)); got != pct {
			t.Errorf("%d %% reads back as %d %%", pct, got)
		}
	}
}

func TestRingStepAcceleration(t *testing.T) {
	if ringStepPercent(time.Second) != 1 || ringStepPercent(250*time.Millisecond) != 2 ||
		ringStepPercent(150*time.Millisecond) != 3 || ringStepPercent(50*time.Millisecond) != 5 {
		t.Error("ring step sizes")
	}
}
