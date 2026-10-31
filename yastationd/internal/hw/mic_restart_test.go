package hw

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// A capture that ends on its own while someone is subscribed is reopened.
func TestMicRestartsEndedCapture(t *testing.T) {
	dir := t.TempDir()
	// A fake arecord: one 20 ms block of 16 kHz mono, then exit.
	script := "#!/bin/sh\nhead -c 640 /dev/zero\necho 'overrun!!!' >&2\n"
	if err := os.WriteFile(filepath.Join(dir, "arecord"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m := &Mic{Device: "x", Channels: 1, Rate: 16000, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var blocks atomic.Int32
	unsub := m.SubscribeRaw(func([]byte) { blocks.Add(1) })
	deadline := time.Now().Add(micRestart*2 + 2*time.Second)
	for blocks.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	unsub()
	if blocks.Load() < 2 {
		t.Fatalf("blocks %d: the capture was not reopened", blocks.Load())
	}
}

func TestMicStopWaitsForCaptureClose(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arecord"), []byte("#!/bin/sh\nexec cat /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m := &Mic{Device: "x", Channels: 1, Rate: 16000, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var blocks atomic.Int32
	m.SubscribeRaw(func([]byte) { blocks.Add(1) })
	deadline := time.Now().Add(time.Second)
	for blocks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if blocks.Load() == 0 {
		t.Fatal("capture did not start")
	}
	m.Stop()
	m.mu.Lock()
	running := m.cmd != nil
	m.mu.Unlock()
	if running {
		t.Fatal("capture remained registered after Stop")
	}
}

// A capture that ignores SIGTERM makes Suspend fail; the mic must not stay paused, or the wake
// word and voice would be off until the daemon restarts.
func TestMicSuspendFailureResumes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "arecord"), []byte("#!/bin/sh\ntrap '' TERM\nexec cat /dev/zero\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	m := &Mic{Device: "x", Channels: 1, Rate: 16000, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var blocks atomic.Int32
	defer m.SubscribeRaw(func([]byte) { blocks.Add(1) })()
	for deadline := time.Now().Add(time.Second); blocks.Load() == 0 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := m.Suspend(); err == nil {
		t.Fatal("Suspend succeeded with a capture that ignores SIGTERM")
	}
	m.mu.Lock()
	paused, running := m.paused, m.cmd != nil
	m.mu.Unlock()
	if paused || !running {
		t.Fatalf("after a failed Suspend: paused=%v running=%v", paused, running)
	}
}
