package app

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestBeats(t *testing.T) {
	a := arbClaim{ID: "mini3", Level: 62}
	b := arbClaim{ID: "station3", Level: 58}
	if !beats(a, b) || beats(b, a) {
		t.Fatal("louder wins")
	}
	c := arbClaim{ID: "alpha", Level: 61.7}
	if !beats(c, a) || beats(a, c) {
		t.Fatal("a tie within 0.5 dB goes to the lower id")
	}
}

func TestPeakIsTheWakeWordsLevel(t *testing.T) {
	r := newRoomSound(16000, 1, 0, func() bool { return false })
	block := func(amp float64) []byte {
		var b []byte
		for i := 0; i < 320; i++ { // 20 ms at 16 kHz
			b = binary.LittleEndian.AppendUint16(b, uint16(int16(amp*math.Sin(2*math.Pi*1000*float64(i)/16000))))
		}
		return b
	}
	for i := 0; i < 40; i++ { // a quiet room, then a loud word
		r.feed(block(30))
	}
	for i := 0; i < 20; i++ {
		r.feed(block(3000))
	}
	r.floor = -60
	level, over, ok := r.peak(arbWakeSpan, arbLoudest)
	if !ok || level < -25 || level > -15 || math.Abs(over-(level+60)) > 1e-9 {
		t.Fatalf("peak %v over %v ok %v", level, over, ok)
	}
}

// Two people saying «Алиса» in two rooms 1.3 s apart are two rounds: the earlier claim does not
// silence the later speaker (2026-11-10: two rooms calling each other at once). One phrase
// reaches the speakers' spotters within ~150 ms, either way round.
func TestArbitrationRound(t *testing.T) {
	at := time.Now()
	for _, tc := range []struct {
		off  time.Duration // the claim's arrival against our wake word
		want bool
	}{{-1300 * time.Millisecond, false}, {-500 * time.Millisecond, true}, {-100 * time.Millisecond, true},
		{100 * time.Millisecond, true}, {1400 * time.Millisecond, true}, {1600 * time.Millisecond, false}} {
		if got := inRound(at.Add(tc.off), at); got != tc.want {
			t.Errorf("a claim %v from ours: in the round %v, want %v", tc.off, got, tc.want)
		}
	}
}
