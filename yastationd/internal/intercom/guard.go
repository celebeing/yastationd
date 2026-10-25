package intercom

import (
	"math"
	"sync"
	"time"
)

// Guard is the cheap safety around the echo canceller (docs/intercom.md, "Echo and howling"):
// both directions fade in at the start of a call (the AEC has not converged yet), the uplink is
// turned down while the far end talks and our room doesn't (soft half-duplex: what the mic
// picks up then is mostly the far end's own voice back), and both directions are limited.
// The levels are first guesses until measured on a speaker.
type Guard struct {
	mu     sync.Mutex
	farAt  time.Time // the far end was last heard in our playout
	farRMS float64   // its recent level (linear, full scale 1)

	up, down gainState
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
	want := 1.0
	if farActive && rms(f) < far {
		want = halfDuplexLow
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
