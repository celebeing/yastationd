package wake

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
			time.Sleep(100 * time.Millisecond) // the READY line
			return s
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
		t.Fatal("call mode not confirmed")
	}
	if !s.CallMode(false) {
		t.Fatal("ASR mode not confirmed")
	}
	select {
	case n := <-got:
		if n != 640 {
			t.Fatalf("call stream block of %d bytes", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no call stream on fd 4")
	}
}

// An older spotter-host has no SIGUSR1 handler: the signal would kill it, so none is sent.
func TestCallModeNotSentToOldHost(t *testing.T) {
	s := runFake(t, "READY")
	if s.CallMode(true) {
		t.Fatal("call mode claimed on a host that can't switch")
	}
	time.Sleep(200 * time.Millisecond)
	if !s.Running() {
		t.Fatal("the old host was killed")
	}
}
