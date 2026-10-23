// Package sendspin runs sendspin-host (the Music Assistant / Sendspin player, a native child process
// linking the device's libasound) and relays its events and our commands. Music plays through the
// same dmix PCM as the daemon's own output; the daemon only steers it (ducking, linked volume).
package sendspin

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is one host line: Kind READY, CONNECTED, STREAM, VOLUME with its fields.
type Event struct {
	Kind   string
	Fields []string
}

// Int returns field i as an int (def when absent or malformed).
func (e Event) Int(i, def int) int {
	if i >= len(e.Fields) {
		return def
	}
	n, err := strconv.Atoi(e.Fields[i])
	if err != nil {
		return def
	}
	return n
}

// Host supervises sendspin-host. Send is safe for concurrent use.
type Host struct {
	Bin     string
	Args    func() []string // built per start, so a restart picks up the current volume
	Log     *slog.Logger
	OnEvent func(Event)
	// OnStart is called after each (re)start, before any event: resend state (duck level).
	OnStart func()

	mu    sync.Mutex
	stdin io.WriteCloser
}

// Run supervises the child until stop closes, restarting it with backoff.
func (h *Host) Run(stop <-chan struct{}) {
	backoff := time.Second
	for {
		start := time.Now()
		err := h.runOnce(stop)
		select {
		case <-stop:
			return
		default:
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		h.Log.Warn("music: sendspin-host exited, restarting", "err", err, "in", backoff)
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

func (h *Host) runOnce(stop <-chan struct{}) error {
	cmd := exec.Command(h.Bin, h.Args()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	h.mu.Lock()
	h.stdin = stdin
	h.mu.Unlock()
	if h.OnStart != nil {
		h.OnStart()
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-stop:
			cmd.Process.Kill()
		case <-done:
		}
	}()
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) > 0 && h.OnEvent != nil {
			h.OnEvent(Event{Kind: f[0], Fields: f[1:]})
		}
	}
	close(done)
	h.mu.Lock()
	h.stdin = nil
	h.mu.Unlock()
	stdin.Close() // EOF also tells a still-running child to exit
	return cmd.Wait()
}

// Send writes one command line to the host; dropped while it is down.
func (h *Host) Send(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stdin != nil {
		fmt.Fprintf(h.stdin, format+"\n", args...)
	}
}
