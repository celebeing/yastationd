package frametap

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func TestTap(t *testing.T) {
	var got string
	tap := New(Info{Device: "t", Screen: [2]int{2, 1}, Ring: 1, Commands: []string{"express"}},
		func(name string, payload []byte) bool {
			if name != "express" {
				return false
			}
			got = string(payload)
			return true
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tap.flush(ctx)
	srv := httptest.NewServer(tap.Handler())
	defer srv.Close()

	// commands
	r, err := http.Post(srv.URL+"/cmd/express", "text/plain", strings.NewReader("wink"))
	if err != nil || r.StatusCode != 200 || got != "wink" {
		t.Fatalf("cmd: %v %v %q", err, r.StatusCode, got)
	}
	if r, _ := http.Post(srv.URL+"/cmd/nope", "text/plain", nil); r.StatusCode != 404 {
		t.Errorf("unknown cmd: %d", r.StatusCode)
	}
	if r.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("no CORS header")
	}

	// nobody watching: frames are not even encoded
	tap.Screen([]hw.RGB{{R: 1}, {B: 2}})
	if tap.cur.Screen != "" {
		t.Error("encoded without subscribers")
	}

	// the stream
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/frames", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for i := 0; i < 50 && !tap.watched(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	tap.Screen([]hw.RGB{{R: 1}, {B: 2}})
	tap.Ring([]hw.RGB{{G: 255}})
	sc := bufio.NewScanner(resp.Body)
	deadline := time.After(2 * time.Second)
	lines := make(chan string)
	go func() {
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	for {
		select {
		case <-deadline:
			t.Fatal("no frame with both surfaces")
		case l := <-lines:
			if !strings.HasPrefix(l, "data: ") {
				continue
			}
			var f Frame
			if err := json.Unmarshal([]byte(l[6:]), &f); err != nil {
				t.Fatal(err)
			}
			if f.Screen == "010000000002" && f.Ring == "00ff00" {
				return
			}
		}
	}
}

func (t *Tap) watched() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs) > 0
}

func TestNilTap(t *testing.T) {
	var tap *Tap
	tap.Screen([]hw.RGB{{}})
	tap.Ring(nil)
	tap.Ambilight([]hw.RGBW{{}})
}
