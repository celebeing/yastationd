package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
)

type fakeSource struct {
	n        string
	on       bool
	canPause bool
	paused   int
	level    float32
}

func (f *fakeSource) name() string      { return f.n }
func (f *fakeSource) playing() bool     { return f.on }
func (f *fakeSource) setDuck(l float32) { f.level = l }
func (f *fakeSource) pause() bool {
	f.paused++
	if f.canPause {
		f.on = false
	}
	return f.canPause
}

// newest: a phone starting over Music Assistant mutes it (it can't be paused) until the phone
// stops; Music Assistant starting over a phone pauses the phone, once.
func TestMusicOverlapNewest(t *testing.T) {
	a := newExpressApp(t)
	c := config.Default()
	a.conf.Store(&c)
	ma := &fakeSource{n: "music_assistant"}
	bt := &fakeSource{n: "bluetooth", canPause: true}
	a.addMedia(ma)
	a.addMedia(bt)
	now := time.Now()
	tick := func(level float32) { now = now.Add(duckTick); a.mediaTick(now, level) }

	ma.on = true
	tick(1)
	if ma.level != 1 || a.playingSource() != "music_assistant" {
		t.Fatalf("Music Assistant alone: level %v, playing %q", ma.level, a.playingSource())
	}
	bt.on, bt.canPause = true, false // a phone that ignores the pause: still playing
	tick(1)
	if ma.level != 0 || bt.level != 1 || ma.paused != 1 {
		t.Fatalf("phone over MA: ma %v (paused %d), bt %v", ma.level, ma.paused, bt.level)
	}
	tick(1)
	if ma.paused != 1 {
		t.Fatal("asked to pause twice")
	}
	bt.on = false
	tick(1)
	if ma.level != 1 {
		t.Fatalf("phone stopped: ma %v", ma.level)
	}

	// The assistant's duck reaches every source.
	bt.on, bt.canPause = true, true
	tick(0.05)
	if bt.level != 0.05 || ma.level != 0 {
		t.Fatalf("ducked: bt %v, ma %v", bt.level, ma.level)
	}

	c.MusicOverlap = "mix"
	a.conf.Store(&c)
	tick(1)
	if ma.level != 1 || bt.level != 1 {
		t.Fatalf("mix: ma %v, bt %v", ma.level, bt.level)
	}
}
