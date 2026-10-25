package intercom

import (
	"math"
	"sync"
	"time"
)

// Guard is the cheap safety around the echo canceller (docs/intercom.md, "Echo and howling"):
// both directions fade in at the start of a call (the AEC has not converged yet), the uplink is
// raised to a call level and gated in pauses, it is turned down while the far end talks and our
// room doesn't (soft half-duplex: what the mic picks up then is mostly the far end's own voice
// back), and both directions are limited.
type Guard struct {
	// UpGainDB is the uplink's fixed gain (0 = upGainDefault).
	UpGainDB float64
	// Processed: the uplink is the VQE's call mode (its own NR, AGC and echo suppression), so
	// it gets no gain, gate or half-duplex here — only the fade-in and the limiter.
	Processed bool

	mu     sync.Mutex
	farAt  time.Time // the far end was last heard in our playout
	farRMS float64   // its recent level (linear, full scale 1)

	up, down gainState
	gate     gate
}

// The uplink level. The VQE stream is made for speech recognition: on a bergamot (2026-10-25,
// a 200 s recording with the speaker's own annotations) speech from mumbling at 1 m to
// shouting across the room all came out within ~7 dB — median -54..-61 dBFS RMS per frame,
// loud frames -42..-48 — over a -74 floor. The VQE already compresses, so an AGC only adds
// harm (it raised pauses and street noise); a fixed gain brings speech to a call level, and a
// gate keyed to the tracked floor takes the raised floor and small noises out of the pauses.
const (
	upGainDefault = 30   // dB: speech -58 -> -28 dBFS, loud frames -45 -> -15
	gateOverFloor = 2.5  // the gate opens 8 dB over the floor...
	gateDepth     = 0.13 // ...and closed takes 18 dB more off
	gateHold      = 15   // frames (300 ms) open after the last loud one: word tails stay
	gateRelease   = 0.7  // per frame while closing (~120 ms to the full depth)
)

type gate struct {
	floor float64 // tracked noise floor (linear RMS)
	hold  int
	gain  float64

	frames, open int
}

// level returns the gate's gain for a frame of RMS r and whether it is open (speech).
func (g *gate) level(r float64) (float64, bool) {
	if g.gain == 0 {
		g.floor, g.gain = r, gateDepth
	}
	// Minimum tracking: quiet frames pull the floor down fast, loud ones push it up very slowly.
	if r < g.floor {
		g.floor += 0.3 * (r - g.floor)
	} else {
		g.floor *= 1.002 // ~+0.9 dB/s
	}
	g.floor = math.Max(g.floor, 1e-5)
	g.frames++
	if r > g.floor*gateOverFloor {
		g.hold = gateHold
	} else if g.hold > 0 {
		g.hold--
	}
	open := g.hold > 0
	if open {
		g.open++
		g.gain = 1
	} else {
		g.gain = math.Max(gateDepth, g.gain*gateRelease)
	}
	return g.gain, open
}

type gainState struct {
	n    int     // samples since the start (fade-in)
	duck float64 // current half-duplex gain
	lim  float64 // current limiter gain
}

const (
	fadeSamples   = Rate / 2 // 0.5 s fade-in
	farSpeechRMS  = 0.01     // -40 dBFS: the far end is talking
	farHold       = 300 * time.Millisecond
	halfDuplexLow = 0.18 // -15 dB on the uplink while only the far end talks
	limitPeak     = 0.9  // full scale fraction the limiter holds peaks to
)

func rms(f []int16) float64 {
	if len(f) == 0 {
		return 0
	}
	var s float64
	for _, v := range f {
		x := float64(v) / 32768
		s += x * x
	}
	return math.Sqrt(s / float64(len(f)))
}

// Far notes a frame about to be played (the far end's voice).
func (g *Guard) Far(f []int16, now time.Time) {
	r := rms(f)
	g.mu.Lock()
	g.farRMS += 0.3 * (r - g.farRMS)
	if r >= farSpeechRMS {
		g.farAt = now
	}
	g.mu.Unlock()
}

// FarSpokeWithin reports whether the far end was heard in the last d (the wake-word guard: an
// «Алиса» from the other room leaking through the AEC is not ours).
func (g *Guard) FarSpokeWithin(now time.Time, d time.Duration) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.farAt.IsZero() && now.Sub(g.farAt) < d
}

// Up processes an uplink frame in place.
func (g *Guard) Up(f []int16, now time.Time) {
	g.mu.Lock()
	farActive := !g.farAt.IsZero() && now.Sub(g.farAt) < farHold
	far := g.farRMS
	g.mu.Unlock()
	if g.Processed {
		g.gate.frames++
		g.up.apply(f, 1)
		return
	}
	gainDB := g.UpGainDB
	if gainDB == 0 {
		gainDB = upGainDefault
	}
	want, speech := g.gate.level(rms(f))
	want *= math.Pow(10, gainDB/20)
	// While the far end talks, what doesn't open our gate is most likely its own voice coming
	// back through the echo canceller.
	if farActive && far >= farSpeechRMS && !speech {
		want *= halfDuplexLow
	}
	g.up.apply(f, want)
}

// Down processes a downlink frame in place; level scales it further (1 = as is, e.g. lower
// while an Assist session runs during the call).
func (g *Guard) Down(f []int16, level float64) { g.down.apply(f, level) }

// apply ramps from the previous gain to want over the frame, fades in, limits and clips.
func (st *gainState) apply(f []int16, want float64) {
	if st.n == 0 {
		st.duck, st.lim = want, 1
	}
	peak := 0.0
	for _, v := range f {
		if a := math.Abs(float64(v)) / 32768; a > peak {
			peak = a
		}
	}
	lim := st.lim
	if p := peak * want; p > limitPeak {
		lim = math.Min(lim, limitPeak/p) // instant attack
	}
	from, to := st.duck*st.lim, want*lim
	n := len(f)
	for i, v := range f {
		k := from + (to-from)*float64(i+1)/float64(n)
		if st.n+i < fadeSamples {
			k *= float64(st.n+i) / fadeSamples
		}
		x := float64(v) * k
		switch {
		case x > 32767:
			x = 32767
		case x < -32768:
			x = -32768
		}
		f[i] = int16(x)
	}
	st.n += n
	st.duck = want
	st.lim = math.Min(1, lim*1.05) // release over ~0.3 s
}

// UplinkStats is what the uplink gate has seen, for the log.
type UplinkStats struct {
	FloorDB    float64 // dBFS
	OpenFrames int     // frames the gate was open (speech)
	Frames     int
}

func db(x float64) float64 {
	if x <= 0 {
		return -120
	}
	return math.Round(20*math.Log10(x)*10) / 10
}

// Uplink reports the gate's estimates. Call it from the uplink goroutine (no locking).
func (g *Guard) Uplink() UplinkStats {
	return UplinkStats{FloorDB: db(g.gate.floor), OpenFrames: g.gate.open, Frames: g.gate.frames}
}
