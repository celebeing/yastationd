package hw

import (
	"encoding/binary"
	"math"
	"sort"
	"sync"
)

// LevelMeter collects per-100 ms RMS and the peak of 16 kHz mono S16 audio, to judge mic gain:
// the loud windows approximate speech level, the quiet ones the noise floor.
type LevelMeter struct {
	mu    sync.Mutex
	win   []float64 // RMS per window
	acc   float64
	n     int
	peak  int
	total int
}

const levelWindow = MicOutRate / 10

// Add measures a block of 16 kHz S16LE mono audio.
func (l *LevelMeter) Add(pcm []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := 0; i+1 < len(pcm); i += 2 {
		v := int(int16(binary.LittleEndian.Uint16(pcm[i:])))
		if v < 0 {
			v = -v
		}
		if v > l.peak {
			l.peak = v
		}
		l.acc += float64(v * v)
		l.n++
		l.total++
		if l.n == levelWindow {
			l.win = append(l.win, math.Sqrt(l.acc/float64(l.n)))
			l.acc, l.n = 0, 0
		}
	}
}

// LevelStats summarises what a LevelMeter measured (see Stats).
type LevelStats struct {
	Seconds                       float64
	PeakDBFS, LoudDBFS, FloorDBFS float64
}

// Stats: peak, the 90th-percentile window RMS (speech) and the 10th (floor), in dBFS (1 dp).
func (l *LevelMeter) Stats() LevelStats {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := LevelStats{Seconds: round1(float64(l.total) / MicOutRate), PeakDBFS: dbfs(float64(l.peak))}
	if len(l.win) > 0 {
		w := append([]float64(nil), l.win...)
		sort.Float64s(w)
		st.LoudDBFS = dbfs(w[len(w)*9/10])
		st.FloorDBFS = dbfs(w[len(w)/10])
	}
	return st
}

func dbfs(v float64) float64 {
	if v < 1 {
		v = 1
	}
	return round1(20 * math.Log10(v/32768))
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
