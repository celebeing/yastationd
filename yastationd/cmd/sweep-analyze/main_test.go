package main

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeCaptureFindsReferenceAndDelay(t *testing.T) {
	cfg := config{sampleRate: 1000, channels: 3, referenceChannel: 2, micChannels: []int{0}, sweepSeconds: .1, maxDelaySeconds: .03, bands: nil, motionThresholdDB: 1.5}
	const start, sweepFrames, delay, total = 20, 100, 7, 160
	frames := make([][]int16, total)
	for i := range frames {
		frames[i] = make([]int16, cfg.channels)
	}
	for i := range sweepFrames {
		value := int16((i*97)%401 - 200)
		if value == 0 {
			value = 1
		}
		frames[start+i][2] = value
		if start+i+delay < total {
			frames[start+i+delay][0] = value / 2
		}
	}
	path := filepath.Join(t.TempDir(), "synthetic.raw")
	writeFrames(t, path, frames)
	capture, err := analyzeCapture(cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	if capture.Reference.StartFrame != start {
		t.Fatalf("reference starts at %d, want %d", capture.Reference.StartFrame, start)
	}
	if capture.Reference.PeakLSB == 0 {
		t.Fatal("reference peak was zero")
	}
	if got := capture.Mics[0].CorrelationDelayMilliseconds; math.Abs(got-float64(delay)) > .001 {
		t.Fatalf("correlation delay %.3f ms, want %d ms", got, delay)
	}
}
func TestClassifyMotionRejectsHighBandOutlier(t *testing.T) {
	makeCapture := func(value float64) captureReport {
		return captureReport{Quality: quality{Accepted: true}, Mics: []micReport{{OneThirdOctaveDB: []bandDB{{CenterHz: 500, DB: 0}, {CenterHz: 1000, DB: value}, {CenterHz: 4000, DB: value}}}}}
	}
	r := report{Captures: []captureReport{makeCapture(0), makeCapture(.2), makeCapture(4)}}
	classifyMotion(&r, config{motionThresholdDB: 1.5})
	if !r.Captures[0].Quality.Accepted || !r.Captures[1].Quality.Accepted {
		t.Fatal("stationary captures were rejected")
	}
	if r.Captures[2].Quality.Accepted {
		t.Fatal("high-band outlier was accepted")
	}
}

func writeFrames(t *testing.T, path string, frames [][]int16) {
	t.Helper()
	data := make([]byte, len(frames)*len(frames[0])*2)
	for i, frame := range frames {
		for channel, sample := range frame {
			binary.LittleEndian.PutUint16(data[(i*len(frame)+channel)*2:], uint16(sample))
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
