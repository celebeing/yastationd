package display

import (
	"math"
	"sync"
	"time"
)

// VolumeScene is the front display's volume indication: the app sets the level on every change
// and pushes Render as a short-lived layer. It keeps state between changes, so a display can
// move from the old level to the new one instead of redrawing.
type VolumeScene interface {
	// Set shows pct (0..100); edge is -1 or +1 when a key or the ring pushed past the bottom or
	// the top (the level did not change), else 0.
	Set(pct, edge int)
	Render(now time.Time, below FrontFrame) (FrontFrame, bool)
}

// numberVolume: the percentage as a number (every display can).
type numberVolume struct {
	front      Front
	brightness func() int
	mu         sync.Mutex
	pct        int
}

func (v *numberVolume) Set(pct, _ int) { v.mu.Lock(); v.pct = pct; v.mu.Unlock() }

func (v *numberVolume) Render(time.Time, FrontFrame) (FrontFrame, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.front.Number(v.pct, v.brightness()), false
}

// Volume on seg7: the number.
func (f Seg7Front) Volume(brightness func() int) VolumeScene {
	return &numberVolume{front: f, brightness: brightness}
}

// volumeGlide is how fast the shown level follows a change: the time constant of the approach.
// A 5 % step of the ring takes about three of them.
const volumeGlide = 50 * time.Millisecond

// patternVolume scrubs the firmware's volume.led — a 0→100 sweep of the level's digits, about
// five frames per percent — to the frame of the level instead of playing it, gliding the frame
// index towards a new level so a step reads as the digits rolling. Pushing past either end plays
// the firmware's volume_edge pattern (a shake of "0" or "10") once.
type patternVolume struct {
	sweep      PixelClip
	edges      [2]PixelClip // bottom, top; empty = none
	brightness func() int

	mu        sync.Mutex
	target    float64 // frame index of the level
	shown     float64 // frame index on the screen
	last      time.Time
	edge      int // -1 / +1 while an edge pattern plays
	edgeStart time.Time
}

func (v *patternVolume) Set(pct, edge int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	n := len(v.sweep.Frames)
	v.target = float64(min(max(pct, 0), 100)) * float64(n-1) / 100
	if v.last.IsZero() {
		v.shown = v.target // first time: no glide in from 0
	}
	if edge != 0 && len(v.edges[(edge+1)/2].Frames) > 0 {
		v.edge, v.edgeStart = edge, time.Now()
	} else if edge == 0 {
		v.edge = 0
	}
}

func (v *patternVolume) Render(now time.Time, _ FrontFrame) (FrontFrame, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	b := v.brightness()
	if v.edge != 0 {
		c := v.edges[(v.edge+1)/2]
		if i, ok := frameAt(c, now.Sub(v.edgeStart)); ok {
			return c.frame(i, b), false
		}
		v.edge = 0
	}
	if !v.last.IsZero() {
		k := 1 - math.Exp(-float64(now.Sub(v.last))/float64(volumeGlide))
		v.shown += (v.target - v.shown) * min(max(k, 0), 1)
		if math.Abs(v.target-v.shown) < 0.5 {
			v.shown = v.target
		}
	}
	v.last = now
	return v.sweep.frame(int(math.Round(v.shown)), b), false
}

// frameAt is the frame of a one-shot clip at elapsed d (false once it has ended).
func frameAt(c PixelClip, d time.Duration) (int, bool) {
	for i, f := range c.Frames {
		if d < f.Dur {
			return i, true
		}
		d -= f.Dur
	}
	return 0, false
}

// Volume on a pixel screen: the firmware's volume patterns, else the number.
func (f PixelFront) Volume(brightness func() int) VolumeScene {
	if f.Patterns != nil {
		if sweep, ok := f.Patterns.Load("volume"); ok && len(sweep.Frames) > 1 {
			v := &patternVolume{sweep: sweep, brightness: brightness}
			v.edges[0], _ = f.Patterns.Load("rcu_volume/volume_edge/0")
			v.edges[1], _ = f.Patterns.Load("rcu_volume/volume_edge/10")
			return v
		}
	}
	return &numberVolume{front: f, brightness: brightness}
}
