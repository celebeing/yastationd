package frametap

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestInfoAndPreflight(t *testing.T) {
	calls := 0
	info := Info{Device: "mini", Screen: [2]int{21, 12}, Ring: 6, Commands: []string{"express"}}
	tap := New(info, func(string, []byte) bool {
		calls++
		return true
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	res := httptest.NewRecorder()
	tap.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("/info response = %d %q", res.Code, res.Header().Get("Content-Type"))
	}
	var got Info
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, info) {
		t.Fatalf("/info = %+v, want %+v", got, info)
	}

	req = httptest.NewRequest(http.MethodOptions, "/cmd/express", nil)
	res = httptest.NewRecorder()
	tap.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Header().Get("Access-Control-Allow-Methods") != "GET, POST, OPTIONS" {
		t.Fatalf("preflight response = %d %q", res.Code, res.Header().Get("Access-Control-Allow-Methods"))
	}
	if calls != 0 {
		t.Fatalf("preflight invoked command %d times", calls)
	}
}

func TestFlushDeliversCurrentFramesWithoutBlockingSlowSubscribers(t *testing.T) {
	tap := New(Info{}, func(string, []byte) bool { return false }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	slow := make(chan []byte, 1)
	slow <- []byte("old")
	fast := make(chan []byte, 2)
	tap.mu.Lock()
	tap.subs[slow] = struct{}{}
	tap.subs[fast] = struct{}{}
	tap.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tap.flush(ctx)
	tap.Screen([]hw.RGB{{R: 1}})
	first := waitFrame(t, fast)
	tap.Ring([]hw.RGB{{G: 2}})
	second := waitFrame(t, fast)
	if first.Screen != "010000" || second.Ring != "000200" || second.Seq <= first.Seq {
		t.Fatalf("frames = %+v then %+v", first, second)
	}
	if got := <-slow; string(got) != "old" {
		t.Fatalf("slow subscriber was overwritten: %q", got)
	}
}

func waitFrame(t *testing.T, ch <-chan []byte) Frame {
	t.Helper()
	select {
	case msg := <-ch:
		var f Frame
		if err := json.Unmarshal(msg, &f); err != nil {
			t.Fatal(err)
		}
		return f
	case <-time.After(time.Second):
		t.Fatal("frame was not delivered")
		return Frame{}
	}
}
