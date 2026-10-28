package app

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

// Wake capture (wake_capture, docs/wake-word-boundary.md): the vqe_0 audio around each WAKE, with
// the WAKE's position in it, saved under <cfw>/wakes to measure where the wake word ends in real
// speech. A test option: it records the room.

const (
	captureBefore = 2 * time.Second
	captureAfter  = 1500 * time.Millisecond
	captureWait   = 5 * time.Second // the audio after the WAKE arrives in real time
	captureKeep   = 200             // newest captures kept (~110 kB each)
	captureDir    = "wakes"
	// captureSubhitSpan: a near miss (SUBHIT) this long before the WAKE is recorded with it.
	captureSubhitSpan = 3 * time.Second
)

// wakeCapture is the JSON saved next to each WAV.
type wakeCapture struct {
	Time        string `json:"time"`
	Device      string `json:"device"`
	Board       string `json:"board"`
	Model       string `json:"model"`
	Phrase      string `json:"phrase"`
	StreamMs    int64  `json:"stream_ms"`           // the WAKE line's position in the host's stream
	WakeSample  int64  `json:"wake_sample"`         // ... in vqe_0 (-1: an older spotter-host)
	StartSample int64  `json:"start_sample"`        // the WAV's first sample in vqe_0
	WakeAtMs    int64  `json:"wake_at_ms"`          // the WAKE point in the WAV (-1: unknown)
	SubhitMs    int64  `json:"subhit_ms,omitempty"` // a near miss just before, in the host's stream
	Rate        int    `json:"rate"`
}

func (a *App) captureWake(ev wake.Event) {
	if a.spot == nil || !a.spot.Running() {
		return
	}
	want := int((captureBefore+captureAfter).Seconds()*wake.VQERate) * 2
	var mu sync.Mutex
	buf := make([]byte, 0, want)
	full := make(chan struct{})
	// fn runs under the spotter's lock: it only collects, the goroutine below unsubscribes.
	unsub, from := a.spot.SubscribeVQEAt(ev, captureBefore, func(b []byte) {
		mu.Lock()
		defer mu.Unlock()
		if len(buf) >= want {
			return
		}
		if n := want - len(buf); len(b) > n {
			b = b[:n]
		}
		buf = append(buf, b...)
		if len(buf) >= want {
			close(full)
		}
	})
	now := time.Now()
	meta := wakeCapture{
		Time: now.Format(time.RFC3339Nano), Device: a.cfg().DeviceID, Board: a.prof.Name,
		Model: a.prof.Wake.Model, Phrase: ev.Phrase, StreamMs: ev.At.Milliseconds(),
		WakeSample: ev.VQE, StartSample: from, WakeAtMs: -1, Rate: wake.VQERate,
	}
	if from >= 0 {
		meta.WakeAtMs = (ev.VQE - from) * 1000 / wake.VQERate
	}
	if sh := a.lastSubhit.Load(); sh != nil && ev.At-sh.At >= 0 && ev.At-sh.At < captureSubhitSpan {
		meta.SubhitMs = sh.At.Milliseconds()
	}
	go func() {
		select {
		case <-full:
		case <-time.After(captureWait):
		}
		unsub()
		mu.Lock()
		pcm := buf
		mu.Unlock()
		dir := a.cfwFile(captureDir)
		if err := saveCapture(dir, now.Format("20060102-150405.000"), pcm, meta); err != nil {
			a.log.Warn("wake capture: not saved", "err", err)
			return
		}
		a.log.Info("wake capture: saved", "at_ms", meta.WakeAtMs, "bytes", len(pcm))
	}()
}

// saveCapture writes <name>.wav and <name>.json to dir and keeps the newest captureKeep pairs.
func saveCapture(dir, name string, pcm []byte, meta wakeCapture) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	j, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(j, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, name+".wav"), wavFile(pcm, meta.Rate), 0o600); err != nil {
		return err
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	sort.Strings(names) // the names are timestamps
	for len(names) > captureKeep {
		os.Remove(names[0])
		os.Remove(strings.TrimSuffix(names[0], ".wav") + ".json")
		names = names[1:]
	}
	return nil
}

// wavHeaderLen is the canonical PCM WAV header (RIFF + fmt + data chunk headers).
const wavHeaderLen = 44

// wavFile wraps 16-bit mono PCM in a WAV header.
func wavFile(pcm []byte, rate int) []byte {
	h := make([]byte, wavHeaderLen, wavHeaderLen+len(pcm))
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1) // PCM
	binary.LittleEndian.PutUint16(h[22:], 1) // mono
	binary.LittleEndian.PutUint32(h[24:], uint32(rate))
	binary.LittleEndian.PutUint32(h[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}
