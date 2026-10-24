package display

import (
	"math"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// RingFrame is one picture of the LED ring, one colour per pixel. Its length is the board's
// pixel count: the stack's base layer always yields a frame of that length, and every effect
// sizes its output from the `below` frame it is handed — so effects work on any ring. Effects
// never modify `below` in place (frames are slices; a shared one would leak between layers).
type RingFrame = []hw.RGB

// Blank is an all-off frame of n pixels (the base when the ring light is off).
func Blank(n int) RingFrame { return make(RingFrame, n) }

// Solid is one colour on every one of n pixels (already brightness-scaled).
func Solid(n int, c hw.RGB) RingFrame {
	f := make(RingFrame, n)
	for i := range f {
		f[i] = c
	}
	return f
}

// Fill lights the first k of n pixels in c — a level bar.
func Fill(n, k int, c hw.RGB) RingFrame {
	f := make(RingFrame, n)
	for i := 0; i < k && i < n; i++ {
		f[i] = c
	}
	return f
}

var (
	Cyan   = hw.RGB{R: 0, G: 180, B: 255}
	Blue   = hw.RGB{R: 0, G: 40, B: 255}
	Amber  = hw.RGB{R: 255, G: 140, B: 0}
	Yellow = hw.RGB{R: 255, G: 210, B: 0}
	Red    = hw.RGB{R: 255, G: 0, B: 0}
	Green  = hw.RGB{R: 0, G: 220, B: 0}
	White  = hw.RGB{R: 255, G: 255, B: 255}
)

func scaleAll(f RingFrame, k float64) RingFrame {
	for i := range f {
		f[i] = hw.RGB{R: uint8(float64(f[i].R) * k), G: uint8(float64(f[i].G) * k), B: uint8(float64(f[i].B) * k)}
	}
	return f
}

// --- effect layers (self-contained; they ignore what is below and draw the whole ring) ---

// Pulse breathes colour c at the given period: the "listening" effect in cyan.
func Pulse(c hw.RGB, period time.Duration) RenderFunc[RingFrame] {
	start := time.Now()
	return func(now time.Time, below RingFrame) (RingFrame, bool) {
		ph := math.Sin(2 * math.Pi * now.Sub(start).Seconds() / period.Seconds())
		return scaleAll(Solid(len(below), c), 0.25+0.75*(ph*0.5+0.5)), false
	}
}

// Comet is one bright pixel with a fading tail going round the ring once per period: "thinking".
// The tail length scales with the ring (about a third of it), so it reads the same on 6 or 60 px.
func Comet(c hw.RGB, period time.Duration) RenderFunc[RingFrame] {
	start := time.Now()
	return func(now time.Time, below RingFrame) (RingFrame, bool) {
		n := len(below)
		f := make(RingFrame, n)
		if n == 0 {
			return f, false
		}
		decay := math.Pow(0.45, 6/float64(n)) // 0.45 per pixel on the 6-px ring
		head := now.Sub(start).Seconds() / period.Seconds() * float64(n)
		for i := 0; i < n; i++ {
			k := math.Pow(decay, math.Mod(head-float64(i)+float64(n), float64(n)))
			f[i] = hw.RGB{R: uint8(float64(c.R) * k), G: uint8(float64(c.G) * k), B: uint8(float64(c.B) * k)}
		}
		return f, false
	}
}

// Blink alternates colour c and off at the given period; used for the error effect (red).
func Blink(c hw.RGB, period time.Duration) RenderFunc[RingFrame] {
	start := time.Now()
	return func(now time.Time, below RingFrame) (RingFrame, bool) {
		if int(now.Sub(start)/period)%2 == 0 {
			return Solid(len(below), c), false
		}
		return Blank(len(below)), false
	}
}

// Flash shows colour c for d, then pops itself: the success flash.
func Flash(c hw.RGB, d time.Duration) RenderFunc[RingFrame] {
	start := time.Now()
	return func(now time.Time, below RingFrame) (RingFrame, bool) {
		return Solid(len(below), c), now.Sub(start) > d
	}
}

var rainbowHues = []hw.RGB{{R: 255}, {R: 255, G: 160}, {G: 255}, {G: 255, B: 255}, {B: 255}, {R: 255, B: 255}}

// Rainbow spreads the hue wheel over the ring and rotates it once per period; k scales it.
func Rainbow(period time.Duration, k int) RenderFunc[RingFrame] {
	start := time.Now()
	return func(now time.Time, below RingFrame) (RingFrame, bool) {
		n := len(below)
		f := make(RingFrame, n)
		if n == 0 {
			return f, false
		}
		step := period / time.Duration(n)
		if step <= 0 {
			step = time.Millisecond
		}
		shift := int(now.Sub(start)/step) % n
		for i := range f {
			f[i] = rainbowHues[((i+shift)%n)*len(rainbowHues)/n].Scale(k)
		}
		return f, false
	}
}
