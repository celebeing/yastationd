package display

import (
	"math"
	"strconv"
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

// volumeGlide is how fast the bar follows a change: the time constant of its approach. A 5 %
// step of the ring takes about three of them.
const volumeGlide = 50 * time.Millisecond

// volumeFade is how long the number cross-fades into a new value: short, a fast turn of the ring
// changes it every few tens of ms, and a longer fade keeps the changing strokes half-lit.
const volumeFade = 70 * time.Millisecond

// volumeTrack is the intensity of the bar's unlit part, so its length reads at a glance.
const volumeTrack = 0.12

// pixelVolume: the exact percentage in the clock's 4x9 digits (right-aligned in a fixed place),
// cross-fading from the old value,
// over a full-width bar whose last pixel is partly lit, so even 1 % moves it. The firmware's own
// volume patterns don't fit: volume.led is a sweep through the tens only (a 5 % step lands
// mid-scroll) and volume_edge is drawn for the remote's 0..10 scale.
type pixelVolume struct {
	w, h       int
	gamma      float64
	brightness func() int

	mu       sync.Mutex
	pct      int
	prev     int
	changed  time.Time
	bar      float64 // shown bar level, 0..100
	last     time.Time
	edge     int
	edgeTime time.Time
}

func (v *pixelVolume) Set(pct, edge int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	pct = min(max(pct, 0), 100)
	now := time.Now()
	if v.last.IsZero() {
		v.prev, v.bar = pct, float64(pct)
	} else if pct != v.pct {
		v.prev, v.changed = v.pct, now
	}
	v.pct = pct
	if edge != 0 {
		v.edge, v.edgeTime = edge, now
	}
}

// volumeFace draws n over the bar at level (0..100).
func (v *pixelVolume) face(n int, level float64) canvas {
	c := newCanvas(v.w, v.h)
	// Right-aligned in a fixed three-digit place: the units stay put, 95 -> 100 shifts nothing,
	// and only the strokes that change fade.
	s := strconv.Itoa(n)
	right := (v.w+textWidth(bigDigits, "100"))/2 - 1
	c.textAt(bigDigits, s, right+1-textWidth(bigDigits, s), 0, 1)
	fill := level / 100 * float64(v.w)
	for x := 0; x < v.w; x++ {
		lit := min(max(fill-float64(x), 0), 1)
		c.v[(v.h-1)*v.w+x] = volumeTrack + (1-volumeTrack)*lit
	}
	return c
}

func (v *pixelVolume) Render(now time.Time, _ FrontFrame) (FrontFrame, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.last.IsZero() {
		k := 1 - math.Exp(-float64(now.Sub(v.last))/float64(volumeGlide))
		v.bar += (float64(v.pct) - v.bar) * min(max(k, 0), 1)
		if math.Abs(float64(v.pct)-v.bar) < 0.2 {
			v.bar = float64(v.pct)
		}
	}
	v.last = now
	c := v.face(v.pct, v.bar)
	if t := now.Sub(v.changed); t < volumeFade {
		c = blend(v.face(v.prev, v.bar), c, float64(t)/float64(volumeFade))
	}
	// Pushed past an end: the bar's end pixel flashes once.
	if t := now.Sub(v.edgeTime); v.edge != 0 && t < 300*time.Millisecond {
		x := 0
		if v.edge > 0 {
			x = v.w - 1
		}
		c.v[(v.h-1)*v.w+x] = 1 - float64(t)/float64(300*time.Millisecond)*(1-volumeTrack)
	}
	return FrontFrame{Px: c.pixels(v.gamma, v.brightness())}, false
}

// Volume on a pixel screen: the number over a bar (pixelVolume).
func (f PixelFront) Volume(brightness func() int) VolumeScene {
	if f.W < textWidth(bigDigits, "100") || f.H < 11 {
		return &numberVolume{front: f, brightness: brightness}
	}
	return &pixelVolume{w: f.W, h: f.H, gamma: f.Gamma, brightness: brightness}
}
