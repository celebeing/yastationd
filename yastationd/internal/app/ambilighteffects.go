package app

import (
	"math"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Music effects for the ambilight (docs/ambilight-stream.md, "Music effects"): the usual
// audio-reactive strip effects (LedFx, WLED's sound-reactive set), our own code. Each takes the
// spectrum frames sendspin-host sends (~25/s, bands 0..1, low to high) and draws the strip at the
// render rate. Pixel 0 is the top of orion's strip: "bottom" below is the last pixel.

type musicFx interface {
	feed(bands []float64, now time.Time, pixels int)
	frame(now time.Time, pixels int) []hw.RGBW
}

// musicFxNames is the effect select's options; "none" leaves the strip to its light state.
var musicFxNames = []string{"none", "rain", "vu", "spectrum", "energy", "scroll", "pulse"}

func newMusicFx(name string) musicFx {
	switch name {
	case "rain":
		return newRainFx()
	case "vu":
		return &vuFx{}
	case "spectrum":
		return &spectrumFx{}
	case "energy":
		return &energyFx{}
	case "scroll":
		return &scrollFx{}
	case "pulse":
		return &pulseFx{}
	}
	return nil
}

// rangeEnergy is the mean of bands over [lo, hi) given as fractions x 1000 of the band count.
func rangeEnergy(bands []float64, lo, hi int) float64 {
	a, b := lo*len(bands)/1000, max(hi*len(bands)/1000, lo*len(bands)/1000+1)
	b = min(b, len(bands))
	if a >= b {
		return 0
	}
	e := 0.0
	for _, v := range bands[a:b] {
		e += v
	}
	return e / float64(b-a)
}

// lmh is the lows, mids and highs of a spectrum frame.
func lmh(bands []float64) (l, m, h float64) {
	return rangeEnergy(bands, 0, 300), rangeEnergy(bands, 300, 650), rangeEnergy(bands, 650, 1000)
}

// follow moves v towards target: at once when it rises, at fall per second when it drops.
func follow(v, target, fall, dt float64) float64 {
	if target >= v {
		return target
	}
	return math.Max(target, v-fall*dt)
}

func hue(h, v float64) hw.RGBW {
	f := func(n float64) uint8 {
		k := math.Mod(n+h*6, 6)
		return uint8(255 * v * (1 - math.Max(0, math.Min(math.Min(k, 4-k), 1))))
	}
	return hw.RGBW{R: f(5), G: f(3), B: f(1)}
}

func scaleRGBW(c hw.RGBW, k float64) hw.RGBW {
	k = math.Max(0, math.Min(1, k))
	return hw.RGBW{R: uint8(float64(c.R) * k), G: uint8(float64(c.G) * k), B: uint8(float64(c.B) * k), W: uint8(float64(c.W) * k)}
}

// clock is the common "time since the last call" bookkeeping.
type clock struct{ t time.Time }

func (c *clock) dt(now time.Time) float64 {
	d := 0.04
	if !c.t.IsZero() {
		d = math.Min(now.Sub(c.t).Seconds(), 0.5)
	}
	c.t = now
	return d
}

// vu: a level meter rising from the bottom, green to red, with a peak dot that falls (WLED's
// gravimeter, LedFx's "power" bar).
type vuFx struct {
	mu          sync.Mutex
	level, peak float64
	peakAt      time.Time
	ck          clock
}

func (f *vuFx) feed(bands []float64, now time.Time, _ int) {
	l, m, h := lmh(bands)
	e := math.Min(1, 0.5*l+0.35*m+0.15*h*1.2)
	f.mu.Lock()
	f.level = e
	if e >= f.peak {
		f.peak, f.peakAt = e, now
	}
	f.mu.Unlock()
}

func (f *vuFx) frame(now time.Time, n int) []hw.RGBW {
	f.mu.Lock()
	dt := f.ck.dt(now)
	if now.Sub(f.peakAt) > 300*time.Millisecond { // the peak hangs, then falls
		f.peak = math.Max(f.level, f.peak-0.8*dt)
	}
	level, peak := f.level, f.peak
	f.mu.Unlock()
	out := make([]hw.RGBW, n)
	lit := level * float64(n)
	for i := 0; i < n; i++ {
		h := float64(i) / float64(n) // 0 at the bottom
		p := n - 1 - i
		if v := math.Min(1, lit-float64(i)); v > 0 {
			out[p] = hue(0.33*(1-h), v) // green at the bottom, red at the top
		}
	}
	if pk := int(math.Floor(peak*float64(n) - 0.5)); peak > 0.05 && pk >= 0 && pk < n {
		out[n-1-pk] = hw.RGBW{R: 120, G: 120, B: 120, W: 200}
	}
	return out
}

// spectrum: one band per pixel, lows at the bottom, the colour by frequency (LedFx's
// "wavelength", WLED's GEQ on a strip).
type spectrumFx struct {
	mu    sync.Mutex
	shown []float64
	bands []float64
	ck    clock
}

func (f *spectrumFx) feed(bands []float64, _ time.Time, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.bands) != n {
		f.bands, f.shown = make([]float64, n), make([]float64, n)
	}
	for p := 0; p < n; p++ { // n pixels from the bands, bottom = lowest
		a, b := p*len(bands)/n, max((p+1)*len(bands)/n, p*len(bands)/n+1)
		v := 0.0
		for _, x := range bands[a:min(b, len(bands))] {
			v = math.Max(v, x)
		}
		f.bands[p] = v
	}
}

func (f *spectrumFx) frame(now time.Time, n int) []hw.RGBW {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]hw.RGBW, n)
	if len(f.bands) != n {
		return out
	}
	dt := f.ck.dt(now)
	for p := 0; p < n; p++ {
		f.shown[p] = follow(f.shown[p], f.bands[p], 2.5, dt)
		out[n-1-p] = hue(0.75*float64(p)/float64(n-1), f.shown[p]*f.shown[p])
	}
	return out
}

// energy: a fill from the middle outwards by the overall energy, its colour the mix of lows
// (red), mids (green) and highs (blue) — LedFx's "energy".
type energyFx struct {
	mu      sync.Mutex
	l, m, h float64
	ck      clock
	sl, sm  float64
	sh      float64
}

func (f *energyFx) feed(bands []float64, _ time.Time, _ int) {
	l, m, h := lmh(bands)
	f.mu.Lock()
	f.l, f.m, f.h = l, m, h
	f.mu.Unlock()
}

func (f *energyFx) frame(now time.Time, n int) []hw.RGBW {
	f.mu.Lock()
	dt := f.ck.dt(now)
	f.sl, f.sm, f.sh = follow(f.sl, f.l, 2, dt), follow(f.sm, f.m, 2, dt), follow(f.sh, f.h, 2, dt)
	l, m, h := f.sl, f.sm, f.sh
	f.mu.Unlock()
	out := make([]hw.RGBW, n)
	sum := l + m + h
	if sum <= 0 {
		return out
	}
	c := hw.RGBW{R: uint8(255 * l / sum), G: uint8(255 * m / sum), B: uint8(255 * h / sum)}
	reach := math.Min(1, sum/1.5) * float64(n) / 2 // half-length lit from the middle
	mid := float64(n-1) / 2
	for p := 0; p < n; p++ {
		d := math.Abs(float64(p) - mid)
		if v := math.Min(1, reach-d+0.5); v > 0 {
			out[p] = scaleRGBW(c, v)
		}
	}
	return out
}

// scroll: the sound's colour now (lows red, mids green, highs blue) enters at the top and runs
// down the strip — LedFx's "scroll".
type scrollFx struct {
	mu   sync.Mutex
	cur  hw.RGBW
	line []hw.RGBW
	acc  float64
	ck   clock
}

const scrollSpeed = 20.0 // pixels per second

func (f *scrollFx) feed(bands []float64, _ time.Time, _ int) {
	l, m, h := lmh(bands)
	sq := func(v float64) uint8 { return uint8(255 * math.Min(1, v*v*1.6)) }
	f.mu.Lock()
	f.cur = hw.RGBW{R: sq(l), G: sq(m), B: sq(h)}
	f.mu.Unlock()
}

func (f *scrollFx) frame(now time.Time, n int) []hw.RGBW {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.line) != n {
		f.line = make([]hw.RGBW, n)
	}
	f.acc += f.ck.dt(now) * scrollSpeed
	for ; f.acc >= 1; f.acc-- {
		copy(f.line[1:], f.line[:n-1])
		f.line[0] = f.cur
	}
	return append([]hw.RGBW(nil), f.line...)
}

// pulse: the whole strip flashes on a beat in the lows and fades (a "beat" strobe, softened).
type pulseFx struct {
	mu   sync.Mutex
	avg  float64
	ck   clock
	fck  clock
	glow float64
	last time.Time
}

func (f *pulseFx) feed(bands []float64, now time.Time, _ int) {
	l, _, _ := lmh(bands)
	f.mu.Lock()
	defer f.mu.Unlock()
	dt := f.ck.dt(now)
	rise := l - f.avg
	f.avg += (1 - math.Exp(-dt/0.5)) * (l - f.avg)
	if rise > 0.08 && l > 0.25 && now.Sub(f.last) > 180*time.Millisecond {
		f.last = now
		f.glow = math.Min(1, 0.5+4*rise)
	}
}

func (f *pulseFx) frame(now time.Time, n int) []hw.RGBW {
	f.mu.Lock()
	dt := f.fck.dt(now)
	f.glow *= math.Exp(-dt / 0.18)
	g := f.glow
	f.mu.Unlock()
	c := scaleRGBW(hw.RGBW{R: 255, G: 70, B: 10, W: 40}, g)
	out := make([]hw.RGBW, n)
	for i := range out {
		out[i] = c
	}
	return out
}
