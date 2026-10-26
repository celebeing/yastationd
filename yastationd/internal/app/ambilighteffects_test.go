package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func lit(f []hw.RGBW) int {
	n := 0
	for _, c := range f {
		if c.R|c.G|c.B|c.W != 0 {
			n++
		}
	}
	return n
}

// Every effect draws something on loud music after quiet, and goes dark after a while of silence.
func TestMusicEffects(t *testing.T) {
	quiet, loud := make([]float64, 21), make([]float64, 21)
	for i := range loud {
		loud[i] = 0.9
	}
	for _, name := range musicFxNames[1:] {
		fx := newMusicFx(name)
		now := time.Unix(1000, 0)
		for i := 0; i < 25; i++ { // a second of quiet
			fx.feed(quiet, now, 12)
			fx.frame(now, 12)
			now = now.Add(40 * time.Millisecond)
		}
		fx.feed(loud, now, 12) // an attack
		got := 0
		for i := 0; i < 5; i++ {
			now = now.Add(40 * time.Millisecond)
			got = max(got, lit(fx.frame(now, 12)))
			fx.feed(loud, now, 12)
		}
		if got == 0 {
			t.Errorf("%s: dark on loud music", name)
		}
		for i := 0; i < 75; i++ { // three seconds of silence
			now = now.Add(40 * time.Millisecond)
			fx.feed(quiet, now, 12)
			fx.frame(now, 12)
		}
		if n := lit(fx.frame(now, 12)); n != 0 {
			t.Errorf("%s: %d pixels lit after silence", name, n)
		}
	}
	if newMusicFx("none") != nil || newMusicFx("nope") != nil {
		t.Fatal("none/unknown made an effect")
	}
}

func TestFoldWhite(t *testing.T) {
	f := []hw.RGBW{{R: 10, B: 250, W: 20}}
	foldWhite(f)
	if f[0] != (hw.RGBW{R: 30, G: 20, B: 255}) {
		t.Fatalf("folded %v", f[0])
	}
}
