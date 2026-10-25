// Package frametap streams what the daemon draws — the front screen, the ring and the ambilight,
// exactly as handed to the hardware — to a browser, and takes the daemon's commands back. On a
// unit it shows what we write (the screen framebuffer reads back as zeros, so this is the only
// way to see it); in the host simulator (cmd/ledsim) it is the display. The embedded viewer puts
// several taps side by side and sends every command to all of them, so the simulator and a unit
// can be compared frame by frame. docs/led-preview.md.
//
// Off unless frametap_addr is set. No authentication, like the MQTT broker it mirrors: LAN only.
package frametap

import (
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

//go:embed viewer.html
var viewer []byte

// Info describes the surfaces and the commands of one daemon, for the viewer.
type Info struct {
	Device      string   `json:"device"`
	Name        string   `json:"name"`
	Board       string   `json:"board"`
	Model       string   `json:"model"`
	Version     string   `json:"version"`
	Screen      [2]int   `json:"screen"` // w, h; 0, 0 = no pixel screen
	Ring        int      `json:"ring"`
	Ambilight   int      `json:"ambilight"`
	Expressions []string `json:"expressions"`
	RingEffects []string `json:"ring_effects"`
	Patterns    []string `json:"patterns"` // the screen patterns a "clip" display command can name
	Commands    []string `json:"commands"`
}

// Frame is one message of the stream: the latest state of every surface, hex RGB (RGBW for the
// ambilight) per pixel, row-major for the screen. T is the daemon's clock in ms; Seq counts
// frames sent, so the viewer sees drops.
type Frame struct {
	T         int64  `json:"t"`
	Seq       uint64 `json:"seq"`
	Screen    string `json:"screen,omitempty"`
	Ring      string `json:"ring,omitempty"`
	Ambilight string `json:"ambilight,omitempty"`
}

// Tap collects the surfaces' latest frames and fans them out. A nil Tap ignores everything, so
// the render loop calls it unconditionally.
type Tap struct {
	info Info
	cmd  func(name string, payload []byte) bool
	log  *slog.Logger

	mu     sync.Mutex
	cur    Frame
	dirty  bool
	seq    uint64
	subs   map[chan []byte]struct{}
	notify chan struct{}
}

// New makes a tap; cmd runs one command and reports whether the name exists.
func New(info Info, cmd func(name string, payload []byte) bool, log *slog.Logger) *Tap {
	return &Tap{info: info, cmd: cmd, log: log, subs: map[chan []byte]struct{}{}, notify: make(chan struct{}, 1)}
}

func hexRGB(px []hw.RGB) string {
	b := make([]byte, 0, len(px)*3)
	for _, c := range px {
		b = append(b, c.R, c.G, c.B)
	}
	return hex.EncodeToString(b)
}

func (t *Tap) set(f func(*Frame)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if len(t.subs) == 0 { // nobody watching: skip the encoding
		t.mu.Unlock()
		return
	}
	old := t.cur
	f(&t.cur)
	if t.cur != old {
		t.dirty = true
		select {
		case t.notify <- struct{}{}:
		default:
		}
	}
	t.mu.Unlock()
}

// Screen records the pixels just written to the front screen.
func (t *Tap) Screen(px []hw.RGB) { t.set(func(f *Frame) { f.Screen = hexRGB(px) }) }

// Ring records the ring frame just written.
func (t *Tap) Ring(px []hw.RGB) { t.set(func(f *Frame) { f.Ring = hexRGB(px) }) }

// Ambilight records the ambilight frame just written.
func (t *Tap) Ambilight(px []hw.RGBW) {
	t.set(func(f *Frame) {
		b := make([]byte, 0, len(px)*4)
		for _, c := range px {
			b = append(b, c.R, c.G, c.B, c.W)
		}
		f.Ambilight = hex.EncodeToString(b)
	})
}

// flush sends the current frame to every subscriber at most once per tick (the screen's 20 ms):
// a slow client loses frames (Seq shows it), the render loop never waits.
func (t *Tap) flush(ctx context.Context) {
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.notify:
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		t.mu.Lock()
		if !t.dirty {
			t.mu.Unlock()
			continue
		}
		t.dirty = false
		t.seq++
		f := t.cur
		f.T, f.Seq = time.Now().UnixMilli(), t.seq
		msg, _ := json.Marshal(f)
		for c := range t.subs {
			select {
			case c <- msg:
			default:
			}
		}
		t.mu.Unlock()
	}
}

// Serve listens on addr until ctx ends.
func (t *Tap) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: t.Handler()}
	go t.flush(ctx)
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	t.log.Info("frame tap listening", "addr", ln.Addr().String())
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Handler serves the viewer (/), the surfaces (/info), the stream (/frames, server-sent events)
// and the commands (POST /cmd/<name>, the body is the MQTT payload). Every reply allows any
// origin: the viewer served by one tap reads the others.
func (t *Tap) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(viewer)
	})
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(t.info)
	})
	mux.HandleFunc("/frames", t.frames)
	mux.HandleFunc("/cmd/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/cmd/")
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !t.cmd(name, body) {
			http.Error(w, fmt.Sprintf("unknown command %q", name), http.StatusNotFound)
			return
		}
		t.log.Debug("frame tap command", "name", name, "payload", string(body))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		mux.ServeHTTP(w, r)
	})
}

func (t *Tap) frames(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	c := make(chan []byte, 8)
	t.mu.Lock()
	t.subs[c] = struct{}{}
	t.dirty = true // a new viewer gets the current picture at once
	t.mu.Unlock()
	select {
	case t.notify <- struct{}{}:
	default:
	}
	defer func() {
		t.mu.Lock()
		delete(t.subs, c)
		t.mu.Unlock()
	}()
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-c:
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
