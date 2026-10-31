package app

import (
	"encoding/binary"
	"math"
	"testing"
)

// The sweep is the loudest thing this feature can put on a speaker, so pin its bounds.
func TestAcousticSweepBounds(t *testing.T) {
	wave := acousticSweep(-40)
	lead := acousticSweepRate / 4
	if lead > 0 && binary.LittleEndian.Uint16(wave[0:]) != 0 {
		t.Fatal("lead-in not silent")
	}
	peak := 0.0
	for i := 0; i < len(wave)/2; i++ {
		peak = math.Max(peak, math.Abs(float64(int16(binary.LittleEndian.Uint16(wave[2*i:])))))
	}
	want := 32767 * math.Pow(10, -40.0/20)
	if peak > want+1 {
		t.Fatalf("sweep peak %v exceeds -40 dBFS (%v)", peak, want)
	}
	// A quieter level must be strictly quieter.
	quiet := acousticSweep(-50)
	qpeak := 0.0
	for i := 0; i < len(quiet)/2; i++ {
		qpeak = math.Max(qpeak, math.Abs(float64(int16(binary.LittleEndian.Uint16(quiet[2*i:])))))
	}
	if qpeak >= peak {
		t.Fatalf("-50 dBFS peak %v not quieter than -40 dBFS peak %v", qpeak, peak)
	}
}

func TestAcousticLevelGate(t *testing.T) {
	for _, ok := range []int{-50, -45, -40} {
		if !acousticLevel(ok) {
			t.Fatalf("level %d rejected", ok)
		}
	}
	for _, bad := range []int{0, -39, -41, -60, 10} {
		if acousticLevel(bad) {
			t.Fatalf("level %d accepted", bad)
		}
	}
}

// A capture with a present reference and unclipped mics passes; clipping rejects; the
// saturated PDM open transient in the first second must not count as clipping (the live
// unit rejected every capture until the clip check skipped it).
func TestInspectAcousticCapture(t *testing.T) {
	reference := int16(32767 * math.Pow(10, -40.0/20))
	frames := acousticSeconds * acousticCaptureRate
	frameBytes := 2 * acousticChannels
	data := make([]byte, frames*frameBytes)
	for i := 0; i < frames; i++ {
		for c := 0; c < acousticChannels; c++ {
			binary.LittleEndian.PutUint16(data[i*frameBytes+2*c:], uint16(int16(i%100)))
		}
		binary.LittleEndian.PutUint16(data[i*frameBytes+2*acousticReference:], uint16(reference))
		if i < acousticTransientSeconds*acousticCaptureRate {
			binary.LittleEndian.PutUint16(data[i*frameBytes+0:], 0x7ffe) // the open transient, pinned mic 0
		}
	}
	if v := inspectAcousticCapture(data, -40, acousticTransientSeconds); v.MicClipped || !v.Accepted {
		t.Fatalf("capture with open transient rejected: %+v", v)
	}
	var clipped int16 = -32760
	bad := append([]byte(nil), data...)
	clipAt := (acousticTransientSeconds*acousticCaptureRate + 100) * frameBytes
	binary.LittleEndian.PutUint16(bad[clipAt+0:], uint16(clipped)) // mid-capture clip on mic 0, past the transient
	if v := inspectAcousticCapture(bad, -40, acousticTransientSeconds); !v.MicClipped || v.Accepted {
		t.Fatalf("clipped capture accepted: %+v", v)
	}
	if v := inspectAcousticCapture(make([]byte, frames*frameBytes), -40, acousticTransientSeconds); v.Accepted {
		t.Fatalf("silent reference accepted: %+v", v)
	}
}
