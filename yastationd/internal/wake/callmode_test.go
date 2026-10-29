package wake

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeHost(t *testing.T, ready string) string {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"trap 'echo \"MODE 1 ok\"; head -c 6400 /dev/zero >&4' USR1\n" +
		"trap 'echo \"MODE 0 ok\"' USR2\n" +
		"echo \"" + ready + "\"\n" +
		"while :; do sleep 0.05; done\n"
	p := filepath.Join(dir, "spotter-host")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func runFake(t *testing.T, ready string) *Spotter {
	s := &Spotter{Bin: fakeHost(t, ready), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	stop := make(chan struct{})
	go s.Run(stop)
	t.Cleanup(func() { close(stop) })
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		s.mu.Lock()
		ok := s.running && s.proc != nil
		s.mu.Unlock()
		if ok {
			if !strings.Contains(ready, "call-mode") {
				return s
			}
			for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
				s.mu.Lock()
				canMode := s.canMode
				s.mu.Unlock()
				if canMode {
					return s
				}
			}
			t.Fatal("fake spotter-host did not report call-mode support")
		}
	}
	t.Fatal("fake spotter-host did not start")
	return nil
}

func TestCallModeSwitchAndStream(t *testing.T) {
	s := runFake(t, "READY call-mode")
	got := make(chan int, 100)
	unsub := s.SubscribeCall(func(b []byte) { got <- len(b) })
	defer unsub()
	if !s.CallMode(true) {
		t.Fatal("call mode not sent")
	}
	waitMode := func(want bool) {
		t.Helper()
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if s.InCallMode() == want {
				return
			}
		}
		t.Fatalf("InCallMode never became %v", want)
	}
	waitMode(true)
	select {
	case n := <-got:
		if n != 640 {
			t.Fatalf("call stream block of %d bytes", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no call stream on fd 4")
	}
	s.CallMode(false)
	waitMode(false)
}

// An older spotter-host has no SIGUSR1 handler: the signal would kill it, so none is sent.
func TestCallModeNotSentToOldHost(t *testing.T) {
	s := runFake(t, "READY")
	if s.CallMode(true) || s.InCallMode() {
		t.Fatal("call mode claimed on a host that can't switch")
	}
	time.Sleep(200 * time.Millisecond)
	if !s.Running() {
		t.Fatal("the old host was killed")
	}
}
