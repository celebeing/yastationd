// Package wake runs the vendor wake-word/command spotter (spotter-host serve, which dlopens
// libyandex_evil.so) as a child process, so the blob never enters the daemon. The daemon feeds it
// raw mic frames on stdin and gets event lines on stdout plus the VQE-processed 16 kHz mono
// stream (vqe_0) on fd 3.
package wake

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one spotter line: WAKE/CMD (confirmed) or SUBHIT (near miss, Kind "SUBHIT:WAKE").
type Event struct {
	Kind   string
	At     time.Duration // position in the host's input stream
	Phrase string
}

const (
	feedQueue  = 256           // raw 20 ms blocks (~5 s) buffered towards the child
	prerollMax = 2 * 16000 * 2 // 2 s of vqe_0 kept for pre-roll
	VQERate    = 16000
)

// Spotter supervises spotter-host serve. Feed and the VQE fan-out are safe for concurrent use.
type Spotter struct {
	Bin     string
	Args    []string // e.g. serve -a <wake model> -k <command model>
	Env     []string // LD_LIBRARY_PATH=/system/vendor/quasar/libs
	Log     *slog.Logger
	OnEvent func(Event)

	feed chan []byte

	mu      sync.Mutex
	vqeSubs map[int]func([]byte)
	next    int
	ring    []byte // last prerollMax bytes of vqe_0
	running bool
	drops   int
}

// Run supervises the child until stop closes, restarting it with backoff.
func (s *Spotter) Run(stop <-chan struct{}) {
	s.feed = make(chan []byte, feedQueue)
	backoff := time.Second
	for {
		start := time.Now()
		err := s.runOnce(stop)
		select {
		case <-stop:
			return
		default:
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		s.Log.Warn("wake: spotter exited, restarting", "err", err, "in", backoff)
		select {
		case <-stop:
			return
		case <-time.After(backoff):
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (s *Spotter) runOnce(stop <-chan struct{}) error {
	cmd := exec.Command(s.Bin, s.Args...)
	cmd.Env = append(os.Environ(), s.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	vr, vw, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.ExtraFiles = []*os.File{vw} // fd 3
	if err := cmd.Start(); err != nil {
		vr.Close()
		vw.Close()
		return err
	}
	vw.Close() // the child holds its end
	s.setRunning(true)
	defer s.setRunning(false)

	done := make(chan struct{})
	go s.readVQE(vr)
	go func() { // stdin writer
		defer stdin.Close()
		for {
			select {
			case b := <-s.feed:
				if _, err := stdin.Write(b); err != nil {
					return
				}
			case <-done:
				return
			case <-stop:
				cmd.Process.Kill()
				return
			}
		}
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		s.line(sc.Text())
	}
	close(done)
	return cmd.Wait()
}

func (s *Spotter) setRunning(v bool) {
	s.mu.Lock()
	s.running = v
	s.ring = s.ring[:0]
	s.mu.Unlock()
}

// Running reports whether the child is up (its vqe_0 stream is then available).
func (s *Spotter) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Spotter) line(l string) {
	f := strings.Fields(l)
	if len(f) == 0 {
		return
	}
	switch f[0] {
	case "READY":
		s.Log.Info("wake: spotter ready")
	case "WAKE", "CMD":
		if ev, ok := parseEvent(f[0], f[1:]); ok && s.OnEvent != nil {
			s.OnEvent(ev)
		}
	case "SUBHIT":
		if len(f) >= 2 {
			if ev, ok := parseEvent("SUBHIT:"+f[1], f[2:]); ok && s.OnEvent != nil {
				s.OnEvent(ev)
			}
		}
	case "LEVEL":
		s.Log.Debug("wake: level", "line", l)
	default:
		s.Log.Debug("wake: spotter says", "line", l)
	}
}

// parseEvent reads "<stream_ms> <phrase...>".
func parseEvent(kind string, f []string) (Event, bool) {
	if len(f) < 1 {
		return Event{}, false
	}
	ms, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return Event{}, false
	}
	return Event{Kind: kind, At: time.Duration(ms) * time.Millisecond, Phrase: strings.Join(f[1:], " ")}, true
}

// Feed queues raw mic frames for the child; drops them (counted) if it lags, so the mic never
// stalls on it. Call from the mic's capture goroutine.
func (s *Spotter) Feed(raw []byte) {
	if s.feed == nil {
		return
	}
	select {
	case s.feed <- raw:
	default:
		s.mu.Lock()
		s.drops++
		n := s.drops
		s.mu.Unlock()
		if n%250 == 1 {
			s.Log.Warn("wake: spotter input lagging, dropped blocks", "total", n)
		}
	}
}

func (s *Spotter) readVQE(r io.ReadCloser) {
	defer r.Close()
	buf := make([]byte, 640) // 20 ms: the intercom streams it live
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			b := append([]byte(nil), buf[:n]...)
			s.mu.Lock()
			s.ring = append(s.ring, b...)
			if over := len(s.ring) - prerollMax; over > 0 {
				s.ring = append(s.ring[:0], s.ring[over:]...)
			}
			for _, fn := range s.vqeSubs {
				fn(b)
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// SubscribeVQE delivers vqe_0 (16 kHz mono S16LE) from now on, after first handing fn the last
// preroll of buffered audio. fn must not block or call back into the Spotter.
func (s *Spotter) SubscribeVQE(preroll time.Duration, fn func([]byte)) (unsubscribe func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := int(preroll.Seconds()*VQERate) * 2; n > 0 && len(s.ring) > 0 {
		if n > len(s.ring) {
			n = len(s.ring)
		}
		fn(append([]byte(nil), s.ring[len(s.ring)-n:]...))
	}
	if s.vqeSubs == nil {
		s.vqeSubs = map[int]func([]byte){}
	}
	id := s.next
	s.next++
	s.vqeSubs[id] = fn
	return func() {
		s.mu.Lock()
		delete(s.vqeSubs, id)
		s.mu.Unlock()
	}
}
