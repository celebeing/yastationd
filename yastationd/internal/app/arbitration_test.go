package app

import (
	"encoding/binary"
	"math"
	"testing"
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
