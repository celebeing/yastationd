package app

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

func TestMinuteStats(t *testing.T) {
	// 540 quiet blocks at -70 dB and 60 loud ones at -30 dB: the floor is the quiet level, the
	// Leq is dominated by the loud tenth, the activity is that tenth.
	var lv []float64
	for i := 0; i < 600; i++ {
		v := -70.0
		if i%10 == 0 {
			v = -30
		}
		lv = append(lv, v)
	}
	st := minuteStats(lv, 600)
	if !st.ok || st.floor != -70 || math.Abs(st.activity-10) > 1e-9 {
		t.Fatalf("stats %+v", st)
	}
	if want := 10 * math.Log10(0.1*1e-3+0.9*1e-7); math.Abs(st.level-want) > 1e-6 {
		t.Fatalf("level %v, want %v", st.level, want)
	}
	if st := minuteStats(lv[:100], 600); st.ok {
		t.Fatal("a minute mostly skipped has no value")
	}
}

func TestRoomSoundFeed(t *testing.T) {
	// 2 channels at 16 kHz, channel 1 carries a -20 dBFS-ish 1 kHz tone, channel 0 silence.
	skip := false
	r := newRoomSound(16000, 2, 1, func() bool { return skip })
	var raw []byte
	amp := 32768 * math.Pow(10, -20.0/20) * math.Sqrt2 // RMS at -20 dBFS
	for i := 0; i < 16000; i++ {
		raw = binary.LittleEndian.AppendUint16(raw, 0)
		raw = binary.LittleEndian.AppendUint16(raw, uint16(int16(amp*math.Sin(2*math.Pi*1000*float64(i)/16000))))
	}
	r.feed(raw[:len(raw)/2])
	skip = true
	r.feed(raw[len(raw)/2:])
	if r.total != 50 || len(r.levels) != 25 {
		t.Fatalf("blocks %d kept %d", r.total, len(r.levels))
	}
	if v := r.levels[24]; math.Abs(v+20) > 0.5 {
		t.Fatalf("level %v, want about -20", v)
	}
	st := r.take()
	if !st.ok || r.total != 0 || r.levels != nil {
		t.Fatalf("take %+v", st)
	}
}

func TestChangeGates(t *testing.T) {
	now := time.Now()
	var g changeGate
	if !g.due("-60", now) || g.due("-60", now.Add(time.Minute)) || !g.due("-59", now.Add(2*time.Minute)) ||
		!g.due("-59", now.Add(2*time.Minute+roomRefresh)) {
		t.Fatal("changeGate")
	}
	var l luxGate
	if !l.due(100, now) || l.due(105, now.Add(time.Minute)) || !l.due(115, now.Add(2*time.Minute)) {
		t.Fatal("luxGate relative")
	}
	var d luxGate
	if !d.due(0.005, now) || d.due(0.01, now.Add(time.Minute)) || !d.due(0.01, now.Add(roomRefresh+time.Minute)) {
		t.Fatal("luxGate dark noise")
	}
}

func TestRoomSoundStride(t *testing.T) {
	// Orion's capture: 13 channels at 96 kHz; white-ish noise on channel 0 keeps its level when
	// only every sixth sample is read.
	r := newRoomSound(96000, 13, 0, func() bool { return false })
	var raw []byte
	seed := uint32(1)
	var e float64
	for i := 0; i < 96000/10; i++ {
		seed = seed*1664525 + 1013904223
		v := int16(int32(seed>>16) - 32768)
		v /= 10
		e += float64(v) * float64(v)
		for c := 0; c < 13; c++ {
			if c == 0 {
				raw = binary.LittleEndian.AppendUint16(raw, uint16(v))
			} else {
				raw = binary.LittleEndian.AppendUint16(raw, 0)
			}
		}
	}
	r.feed(raw)
	if r.total != 5 {
		t.Fatalf("blocks %d", r.total)
	}
	if want := dbfs(e / 9600); math.Abs(r.levels[2]-want) > 1 {
		t.Fatalf("level %v, want about %v", r.levels[2], want)
	}
}
