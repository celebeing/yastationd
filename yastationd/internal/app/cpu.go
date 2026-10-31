package app

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The processor's load, 0-100 % over all cores: how much of the time it was not idle. Load average
// says little on a speaker: a kernel thread that never wakes counts as a full process on orion
// (docs/boards/orion.md), and the audio threads' short bursts add the rest, so a busy-looking 4.5
// comes with 40 % idle. The percentage is from /proc/stat's counters between two reads.

const cpuMinSpan = time.Second // shorter spans are mostly noise; the last figure stands

type cpuMeter struct {
	mu          sync.Mutex
	busy, total uint64
	at          time.Time
	pct         float64
	have        bool
}

// cpuTimes reads the first line of /proc/stat: the time all cores spent busy and in total, in
// ticks. iowait counts as idle: nothing was running.
func cpuTimes(stat string) (busy, total uint64, ok bool) {
	line, _, _ := strings.Cut(stat, "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, x := range f[1:] {
		if i >= 8 { // guest time is already in user
			break
		}
		n, err := strconv.ParseUint(x, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i != 3 && i != 4 { // idle, iowait
			busy += n
		}
	}
	return busy, total, true
}

// sample takes the counters; it returns the percentage since the sample before, when there is one.
func (m *cpuMeter) sample(stat string, now time.Time) (float64, bool) {
	busy, total, ok := cpuTimes(stat)
	if !ok {
		return 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.at.IsZero() && now.Sub(m.at) < cpuMinSpan {
		return m.pct, m.have
	}
	if !m.at.IsZero() && total > m.total {
		m.pct = min(100, 100*float64(busy-m.busy)/float64(total-m.total))
		m.have = true
	}
	m.busy, m.total, m.at = busy, total, now
	return m.pct, m.have
}

// cpuPercent is the load now. The first call has nothing to compare with, so it measures a moment.
func (a *App) cpuPercent(path string) (float64, bool) {
	read := func() string {
		b, _ := os.ReadFile(path)
		return string(b)
	}
	if pct, ok := a.cpu.sample(read(), time.Now()); ok {
		return pct, true
	}
	time.Sleep(cpuMinSpan + 50*time.Millisecond)
	return a.cpu.sample(read(), time.Now())
}
