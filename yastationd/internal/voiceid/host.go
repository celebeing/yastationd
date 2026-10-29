package voiceid

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host runs `spotter-host voice <dir>` (a process of its own, so a crash in TFLite never touches
// the wake word) and turns speech into voiceprints, one utterance at a time. It starts on first
// use and again after the child dies.
type Host struct {
	Bin string   // spotter-host
	Dir string   // bio_lingware
	Env []string // LD_LIBRARY_PATH=…
	Log *slog.Logger

	mu    sync.Mutex
	proc  *exec.Cmd
	reqs  chan hostReq
	tag   string // the model's compatibility tag (READY)
	gen   int    // utterances begun; only the latest one feeds and asks
	dead  chan struct{}
	start time.Time
}

type hostReq struct {
	pcm   []byte
	reset bool
	print chan Print // nil unless asking for the voiceprint
}

// Print is a voiceprint: Emb is nil when there was too little speech (Secs is what was heard).
type Print struct {
	Secs float64
	Emb  []float32
	Err  error
}

// hostQueue bounds the audio waiting for the child (~10 s of 32 ms chunks); past it audio is
// dropped rather than stalling the caller.
const hostQueue = 300

// Tag is the voiceprint model's compatibility tag ("" before the first start).
func (h *Host) Tag() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tag
}

// ensure starts the child if it isn't running. Called with h.mu held.
func (h *Host) ensure() error {
	if h.proc != nil {
		select {
		case <-h.dead:
		default:
			return nil
		}
	}
	cmd := exec.Command(h.Bin, "voice", h.Dir)
	cmd.Env = append(os.Environ(), h.Env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = logWriter{h.Log}
	if err := cmd.Start(); err != nil {
		return err
	}
	out := bufio.NewReaderSize(stdout, 16<<10)
	ready := make(chan string, 1)
	go func() {
		l, _ := out.ReadString('\n')
		ready <- strings.TrimSpace(l)
	}()
	select {
	case l := <-ready:
		if !strings.HasPrefix(l, "READY ") {
			cmd.Process.Kill()
			cmd.Wait()
			return fmt.Errorf("voice host did not start: %q", l)
		}
		h.tag = strings.TrimPrefix(l, "READY ")
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		cmd.Wait()
		return errors.New("voice host: no READY in 10 s")
	}
	h.proc, h.reqs, h.dead, h.start = cmd, make(chan hostReq, hostQueue), make(chan struct{}), time.Now()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	go h.serve(cmd, stdin, out, h.reqs, h.dead, exited)
	h.Log.Info("voice id: voiceprint host started", "model", h.tag)
	return nil
}

func (h *Host) serve(cmd *exec.Cmd, in io.WriteCloser, out *bufio.Reader, reqs chan hostReq, dead chan struct{}, exited chan error) {
	var err error
	defer func() {
		in.Close()
		cmd.Process.Kill()
		if e := <-exited; err == nil {
			err = e
		}
		close(dead)
		h.Log.Warn("voice id: voiceprint host exited", "err", err)
		for { // fail whoever still waits
			select {
			case r := <-reqs:
				if r.print != nil {
					r.print <- Print{Err: errors.New("voice host exited")}
				}
			default:
				return
			}
		}
	}()
	w := bufio.NewWriterSize(in, 64<<10)
	for {
		var r hostReq
		select {
		case r = <-reqs:
		case err = <-exited:
			exited <- err // for the deferred wait
			return
		}
		switch {
		case r.reset:
			_, err = w.WriteString("r\n")
		case r.print != nil:
			if _, err = w.WriteString("v p\n"); err == nil {
				err = w.Flush()
			}
			if err == nil {
				p := readPrint(out)
				r.print <- p
				err = p.Err
			} else {
				r.print <- Print{Err: err}
			}
		default:
			if _, err = fmt.Fprintf(w, "a %d\n", len(r.pcm)); err == nil {
				_, err = w.Write(r.pcm)
			}
		}
		if err == nil && len(reqs) == 0 {
			err = w.Flush()
		}
		if err != nil {
			return
		}
	}
}

func readPrint(out *bufio.Reader) Print {
	for {
		l, err := out.ReadString('\n')
		if err != nil {
			return Print{Err: err}
		}
		f := strings.Fields(l)
		if len(f) >= 1 && f[0] == "ERR" {
			return Print{Err: errors.New(strings.TrimSpace(l))}
		}
		if len(f) < 4 || f[0] != "VOICE" {
			continue
		}
		secs, _ := strconv.ParseFloat(f[2], 64)
		if f[3] == "short" {
			return Print{Secs: secs}
		}
		emb := make([]float32, 0, len(f)-3)
		for _, s := range f[3:] {
			v, err := strconv.ParseFloat(s, 32)
			if err != nil {
				return Print{Err: fmt.Errorf("bad voiceprint value %q", s)}
			}
			emb = append(emb, float32(v))
		}
		return Print{Secs: secs, Emb: emb}
	}
}

// Utterance is one person's speech being turned into a voiceprint.
type Utterance struct {
	h    *Host
	gen  int
	reqs chan hostReq
}

// Begin starts a new utterance; any earlier one is abandoned (its Feed and Print do nothing).
func (h *Host) Begin() (*Utterance, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.ensure(); err != nil {
		return nil, err
	}
	h.gen++
	u := &Utterance{h: h, gen: h.gen, reqs: h.reqs}
	u.send(hostReq{reset: true})
	return u, nil
}

func (u *Utterance) current() bool {
	u.h.mu.Lock()
	defer u.h.mu.Unlock()
	return u.gen == u.h.gen
}

func (u *Utterance) send(r hostReq) bool {
	select {
	case u.reqs <- r:
		return true
	default:
		return false
	}
}

// Feed adds 16 kHz mono S16LE speech. It never blocks: audio the child can't take is dropped.
func (u *Utterance) Feed(pcm []byte) {
	if len(pcm) == 0 || !u.current() {
		return
	}
	u.send(hostReq{pcm: append([]byte(nil), pcm...)})
}

// Print returns the voiceprint of everything fed so far (the last 7.2 s of it).
func (u *Utterance) Print(timeout time.Duration) Print {
	if !u.current() {
		return Print{Err: errors.New("utterance superseded")}
	}
	c := make(chan Print, 1)
	select {
	case u.reqs <- hostReq{print: c}:
	case <-time.After(timeout):
		return Print{Err: errors.New("voice host busy")}
	}
	select {
	case p := <-c:
		return p
	case <-time.After(timeout):
		return Print{Err: errors.New("voice host timed out")}
	}
}

// End abandons the utterance.
func (u *Utterance) End() {
	u.h.mu.Lock()
	if u.gen == u.h.gen {
		u.h.gen++
	}
	u.h.mu.Unlock()
}

// Stop ends the child; a later Begin starts it again.
func (h *Host) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.proc != nil {
		h.proc.Process.Kill()
	}
}

type logWriter struct{ l *slog.Logger }

func (w logWriter) Write(p []byte) (int, error) {
	for _, l := range strings.Split(strings.TrimSpace(string(p)), "\n") {
		if l != "" {
			w.l.Warn("voice host", "msg", l)
		}
	}
	return len(p), nil
}
