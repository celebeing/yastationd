package intercom

import "sync"

// Jitter is the receive buffer between the network and the playout. Frames are slotted by their
// index; Pop is called at the pace of the local DAC (the playout's blocking write), one frame per
// call, and always returns audio: the frame, a concealment of a missing one, or silence before
// the stream starts.
//
// Drift: the far end's capture clock and our DAC clock differ by up to a few hundred ppm. The
// averaged depth tells which way: above the target band one sample per frame is dropped, below
// it one is repeated, so the playout runs slightly fast or slow until the depth is back. No
// resampler, and the correction is inaudible at speech level.
type Jitter struct {
	mu sync.Mutex

	base, target, max int // frames: configured target, current (adapted) target, hard maximum

	frames  map[uint64][]int16
	next    uint64 // index Pop plays next
	highest uint64
	started bool // got a first packet
	playing bool // prebuffered, Pop plays

	avg    float64 // averaged depth before a pop, in samples
	last   []int16 // last real frame, for concealment
	miss   int     // consecutive missing frames
	steady int     // pops since the last underrun (for the target's decay)

	st JitterStats
}

// JitterStats are counters for the log.
type JitterStats struct {
	Frames, Missing, Late, Overflow, Dropped, Inserted, Rebuffers int
	TargetMs                                                      int
}

const (
	jitterMaxFrames = 10       // 200 ms: above this the oldest audio is dropped
	driftBand       = 200      // samples (12.5 ms) around the target before correcting drift
	avgK            = 0.02     // depth averaging per pop (~1 s at 50 pops/s)
	decayPops       = 10 * 50  // 10 s without an underrun brings the target one frame down
	rebufferMisses  = 500 / 20 // 500 ms with nothing: wait for a fresh prebuffer
)

// NewJitter makes a buffer with a target depth in milliseconds (at least one frame).
func NewJitter(targetMs int) *Jitter {
	t := (targetMs + 19) / 20
	if t < 1 {
		t = 1
	}
	if t > jitterMaxFrames-2 {
		t = jitterMaxFrames - 2
	}
	return &Jitter{base: t, target: t, max: jitterMaxFrames, frames: map[uint64][]int16{}}
}

// Push stores frame idx (an extended index from the packet's sequence number).
func (j *Jitter) Push(idx uint64, s []int16) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch {
	case !j.started:
		j.started, j.next, j.highest = true, idx, idx
	case !j.playing && idx < j.next:
		j.next = idx // reordered ahead of the start
	case idx < j.next:
		// It came after its slot was concealed: that is network jitter beyond the target, so
		// buffer one frame deeper (decays again after a quiet while). Plain loss doesn't grow it.
		j.st.Late++
		if j.target < j.max-2 {
			j.target++
			j.steady = 0
		}
		return
	}
	if _, dup := j.frames[idx]; dup {
		return
	}
	j.frames[idx] = s
	if idx > j.highest {
		j.highest = idx
	}
	if depth := int(j.highest + 1 - j.next); depth > j.max {
		// Far behind (a stall that ended in a burst, or a DAC slower than drift can fix): skip to
		// the target depth instead of carrying the delay for the rest of the call.
		nn := j.highest + 1 - uint64(j.target)
		for k := j.next; k < nn; k++ {
			if _, ok := j.frames[k]; ok {
				delete(j.frames, k)
				j.st.Overflow++
			}
		}
		j.next = nn
	}
	if !j.playing && int(j.highest+1-j.next) >= j.target {
		j.playing = true
		j.avg = float64(j.target * FrameSamples)
	}
}

// Pop returns the next frame to play.
func (j *Jitter) Pop() []int16 {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.playing {
		return make([]int16, FrameSamples)
	}
	depth := float64(int(j.highest+1-j.next) * FrameSamples)
	j.avg += avgK * (depth - j.avg)

	f, ok := j.frames[j.next]
	delete(j.frames, j.next)
	j.next++
	if !ok {
		j.st.Missing++
		j.miss++
		if j.miss >= rebufferMisses && j.next > j.highest {
			// The far end went quiet on the network: start over with a fresh prebuffer.
			j.playing, j.started = false, false
			j.st.Rebuffers++
			for k := range j.frames {
				delete(j.frames, k)
			}
		}
		return j.conceal()
	}
	j.st.Frames++
	j.miss = 0
	j.last = f
	if j.steady++; j.steady >= decayPops && j.target > j.base {
		j.target--
		j.steady = 0
	}
	tgt := float64(j.target * FrameSamples)
	switch {
	case j.avg > tgt+driftBand:
		f = dropSample(f)
		j.st.Dropped++
	case j.avg < tgt-driftBand:
		f = insertSample(f)
		j.st.Inserted++
	}
	return f
}

// conceal stands in for a missing frame: the last one at half, then a quarter level, then
// silence — a short repeat hides a single lost packet, a longer gap fades out instead of buzzing.
func (j *Jitter) conceal() []int16 {
	out := make([]int16, FrameSamples)
	if j.last == nil || j.miss > 2 {
		return out
	}
	shift := uint(j.miss) // 1: half, 2: quarter
	for i := range out {
		if i < len(j.last) {
			out[i] = j.last[i] >> shift
		}
	}
	return out
}

// Stats reports the counters so far.
func (j *Jitter) Stats() JitterStats {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.st
	s.TargetMs = j.target * 20
	return s
}

// dropSample removes one sample from the middle, blending its neighbours.
func dropSample(f []int16) []int16 {
	if len(f) < 3 {
		return f
	}
	m := len(f) / 2
	out := make([]int16, 0, len(f)-1)
	out = append(out, f[:m-1]...)
	out = append(out, int16((int(f[m-1])+int(f[m]))/2))
	return append(out, f[m+1:]...)
}

// insertSample repeats the middle as the mean of two neighbours.
func insertSample(f []int16) []int16 {
	if len(f) < 2 {
		return f
	}
	m := len(f) / 2
	out := make([]int16, 0, len(f)+1)
	out = append(out, f[:m]...)
	out = append(out, int16((int(f[m-1])+int(f[m]))/2))
	return append(out, f[m:]...)
}
