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
