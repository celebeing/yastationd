package hw

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
)

// Mic owns the mic-array capture: one arecord on the interleaved capture device (bergamot and
// pickle open the exclusive raw device; orion the stock 96 kHz "mroute" plug, at its native rate),
// picking one channel and decimating Rate -> 16 kHz mono S16 for the consumers (Wyoming
// streaming now, the wake-word spotter later). arecord runs only while at least one consumer
// is subscribed.
type Mic struct {
	Device   string // capture device, e.g. "hw:0,1" or orion's stock "mroute"
	Channels int    // interleaved channels the device delivers
	Rate     int    // capture rate; must be a multiple of MicOutRate
	Channel  int    // which channel to use
	Log      *slog.Logger

	mu   sync.Mutex
	subs map[int]func([]byte) // 16 kHz mono of Channel
	raws map[int]func([]byte) // raw interleaved device frames
	next int
	cmd  *exec.Cmd
}

const (
	MicOutRate    = 16000
	micChunkFrame = 1024 // output samples per delivered chunk (64 ms)
)

// micReadFrames: device frames per read — 20 ms of audio, whatever the capture rate.
func micReadFrames(rate int) int { return rate / 50 }

// Subscribe registers fn for 16 kHz mono S16LE chunks (called from the capture goroutine; must
// not block) and starts capture if needed. The returned func unsubscribes.
func (m *Mic) Subscribe(fn func([]byte)) (unsubscribe func()) { return m.add(&m.subs, fn) }

// SubscribeRaw registers fn for the raw interleaved device frames (Channels x S16LE at Rate), as
// arecord delivers them — the wake-word host's input. Same rules as Subscribe.
func (m *Mic) SubscribeRaw(fn func([]byte)) (unsubscribe func()) { return m.add(&m.raws, fn) }

func (m *Mic) add(set *map[int]func([]byte), fn func([]byte)) func() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if *set == nil {
		*set = map[int]func([]byte){}
	}
	id := m.next
	m.next++
	(*set)[id] = fn
	if m.cmd == nil {
		m.start()
	}
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(*set, id)
		if len(m.subs)+len(m.raws) == 0 && m.cmd != nil {
			m.cmd.Process.Signal(syscall.SIGTERM) // let arecord close the PCM cleanly
			m.cmd = nil
		}
	}
}

// start launches arecord; m.mu held.
func (m *Mic) start() {
	// The default hw:0,1 buffer (~43 ms) overruns constantly on this SoC (853 in 12 min under
	// load); 1 s buffer / 20 ms periods gave none.
	cmd := exec.Command("arecord", "-q", "-D", m.Device, "-c", strconv.Itoa(m.Channels),
		"-r", strconv.Itoa(m.Rate), "-f", "S16_LE", "-t", "raw",
		"--buffer-time=1000000", "--period-time=20000")
	out, err := cmd.StdoutPipe()
	if err != nil {
		m.Log.Warn("mic: pipe", "err", err)
		return
	}
	if err := cmd.Start(); err != nil {
		m.Log.Warn("mic: start arecord", "err", err)
		return
	}
	m.cmd = cmd
	m.Log.Debug("mic: capture started", "device", m.Device, "channel", m.Channel)
	go m.read(cmd, out)
}

func (m *Mic) read(cmd *exec.Cmd, out io.Reader) {
	defer cmd.Wait()
	r := bufio.NewReaderSize(out, 64<<10)
	fsz := 2 * m.Channels
	block := make([]byte, micReadFrames(m.Rate)*fsz)
	dec, decErr := NewDecimator(m.Rate)
	if decErr != nil {
		m.Log.Warn("mic: decimator", "err", decErr)
		return
	}
	hpf := NewHighpass(150, float64(m.Rate)) // the PDM channels open with a big DC/LF transient
	chunk := make([]byte, 0, 2*micChunkFrame)
	for {
		if _, err := io.ReadFull(r, block); err != nil {
			m.mu.Lock()
			if m.cmd == cmd {
				m.cmd = nil // died on its own; the next Subscribe restarts it
				m.Log.Warn("mic: capture ended", "err", err)
			}
			m.mu.Unlock()
			return
		}
		m.mu.Lock()
		for _, fn := range m.raws {
			fn(append([]byte(nil), block...))
		}
		mono := len(m.subs) > 0
		m.mu.Unlock()
		if !mono {
			continue
		}
		for off := 0; off < len(block); off += fsz {
			s := hpf.Push(int16(binary.LittleEndian.Uint16(block[off+2*m.Channel:])))
			if v, ok := dec.Push(s); ok {
				chunk = binary.LittleEndian.AppendUint16(chunk, uint16(v))
				if len(chunk) == cap(chunk) {
					m.mu.Lock()
					for _, fn := range m.subs {
						fn(append([]byte(nil), chunk...))
					}
					m.mu.Unlock()
					chunk = chunk[:0]
				}
			}
		}
	}
}

// Decimator low-pass filters and keeps every Mth sample (inRate -> 16 kHz): a windowed-sinc
// FIR with its cutoff at 7 kHz, below the new 8 kHz Nyquist. bergamot and pickle capture at
// 48 kHz (3x), orion at 96 kHz (6x).
type Decimator struct {
	taps  []float64
	hist  []float64
	pos   int
	phase int
	ratio int
}

// NewDecimator builds one for inRate, which must be an integral multiple of MicOutRate
// (96 kHz -> 6, 48 kHz -> 3).
func NewDecimator(inRate int) (*Decimator, error) {
	ratio := inRate / MicOutRate
	if ratio < 1 || inRate != MicOutRate*ratio {
		return nil, fmt.Errorf("rate %d is not an integral multiple of %d", inRate, MicOutRate)
	}
	// Odd length that scales with the ratio, so the transition band stays the same: 63 taps at
	// 48 kHz, 127 at 96 kHz.
	n := 21*ratio | 1
	fc := 7000.0 / float64(inRate) // normalised cutoff
	taps := make([]float64, n)
	sum := 0.0
	for i := range taps {
		x := float64(i - n/2)
		sinc := 2 * fc
		if x != 0 {
			sinc = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1)) // Hamming
		taps[i] = sinc * w
		sum += taps[i]
	}
	for i := range taps {
		taps[i] /= sum // unity DC gain
	}
	return &Decimator{taps: taps, hist: make([]float64, n), ratio: ratio}, nil
}

// Push feeds one input sample; every ratio-th call returns one output sample.
func (d *Decimator) Push(s int16) (int16, bool) {
	d.hist[d.pos] = float64(s)
	d.pos = (d.pos + 1) % len(d.hist)
	d.phase++
	if d.phase < d.ratio {
		return 0, false
	}
	d.phase = 0
	acc := 0.0
	for i, t := range d.taps {
		acc += t * d.hist[(d.pos+i)%len(d.hist)]
	}
	return int16(math.Max(-32768, math.Min(32767, math.Round(acc)))), true
}

// Highpass is a 2nd-order Butterworth high-pass biquad.
type Highpass struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func NewHighpass(fc, fs float64) *Highpass {
	w := 2 * math.Pi * fc / fs
	alpha := math.Sin(w) / math.Sqrt2 // Q = 1/sqrt(2)
	cw := math.Cos(w)
	a0 := 1 + alpha
	return &Highpass{
		b0: (1 + cw) / 2 / a0, b1: -(1 + cw) / a0, b2: (1 + cw) / 2 / a0,
		a1: -2 * cw / a0, a2: (1 - alpha) / a0,
	}
}

func (h *Highpass) Push(s int16) int16 {
	x := float64(s)
	y := h.b0*x + h.b1*h.x1 + h.b2*h.x2 - h.a1*h.y1 - h.a2*h.y2
	h.x2, h.x1, h.y2, h.y1 = h.x1, x, h.y1, y
	return int16(math.Max(-32768, math.Min(32767, math.Round(y))))
}
