package app

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Rain, a music effect for the ambilight (docs/ambilight-stream.md, "Music effects"; the idea is
// LedFx's effect of that name, the code our own): an attack in the lows, the mids or the highs
// drops a drop of that range's colour at a random place on the strip; it spreads and fades. The
// highs fall in the white channel, which an RGBW strip has and LedFx's strips do not.

type rainRange struct {
	lo, hi int     // band range, as fractions of the band count x 1000
	color  hw.RGBW // at full strength
}

var rainRanges = []rainRange{
	{0, 300, hw.RGBW{R: 40, G: 60, B: 255}}, // lows: deep blue
	{300, 650, hw.RGBW{G: 190, B: 255}},     // mids: cyan
	{650, 1000, hw.RGBW{B: 60, W: 255}},     // highs: white, a touch of blue
}

const (
	rainLife     = 900 * time.Millisecond // a drop is gone after this
	rainFade     = 0.30                   // its light halves every ~0.2 s (seconds per e-fold)
	rainSpread   = 5.0                    // pixels per second its edge moves out
	rainOnset    = 0.10                   // energy above its running average that is an attack
	rainFloor    = 0.20                   // and the least energy that may be one
	rainAvgTau   = 0.6                    // seconds: the running average's time constant
	rainRefract  = 140 * time.Millisecond // per range: no new drop sooner
	rainMaxDrops = 24
)

type rainDrop struct {
	pos      float64
	strength float64
	color    hw.RGBW
	at       time.Time
}

type rainFx struct {
	mu    sync.Mutex
	avg   []float64
	last  []time.Time // the range's last drop
	ck    clock
	drops []rainDrop
	rnd   *rand.Rand
}

func newRainFx() *rainFx {
	return &rainFx{avg: make([]float64, len(rainRanges)), last: make([]time.Time, len(rainRanges)),
		rnd: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// feed takes a spectrum frame (bands 0..1, low to high) and drops drops on attacks.
func (r *rainFx) feed(bands []float64, now time.Time, pixels int) {
	if len(bands) == 0 || pixels == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	k := 1 - math.Exp(-r.ck.dt(now)/rainAvgTau)
	for i, rg := range rainRanges {
		e := rangeEnergy(bands, rg.lo, rg.hi)
		rise := e - r.avg[i]
		r.avg[i] += k * (e - r.avg[i])
		if rise < rainOnset || e < rainFloor || now.Sub(r.last[i]) < rainRefract {
			continue
		}
		r.last[i] = now
		if len(r.drops) >= rainMaxDrops {
			r.drops = r.drops[1:]
		}
		r.drops = append(r.drops, rainDrop{
			pos:      r.rnd.Float64() * float64(pixels-1),
			strength: math.Min(1, 0.4+3*rise),
			color:    rg.color,
			at:       now,
		})
	}
}

// frame draws the drops at now on pixels positions.
func (r *rainFx) frame(now time.Time, pixels int) []hw.RGBW {
	r.mu.Lock()
	defer r.mu.Unlock()
	acc := make([][4]float64, pixels)
	keep := r.drops[:0]
	for _, d := range r.drops {
		age := now.Sub(d.at)
		if age > rainLife {
			continue
		}
		keep = append(keep, d)
		s := age.Seconds()
		light := d.strength * math.Exp(-s/rainFade)
		width := 0.6 + rainSpread*s
		for p := range acc {
			x := (float64(p) - d.pos) / width
			v := light * math.Exp(-x*x)
			acc[p][0] += v * float64(d.color.R)
			acc[p][1] += v * float64(d.color.G)
			acc[p][2] += v * float64(d.color.B)
			acc[p][3] += v * float64(d.color.W)
		}
	}
	r.drops = keep
	out := make([]hw.RGBW, pixels)
	c := func(v float64) uint8 { return uint8(math.Min(255, v+0.5)) }
	for p, v := range acc {
		out[p] = hw.RGBW{R: c(v[0]), G: c(v[1]), B: c(v[2]), W: c(v[3])}
	}
	return out
}
