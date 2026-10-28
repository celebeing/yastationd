package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveCaptureKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < captureKeep+3; i++ {
		if err := saveCapture(dir, fmt.Sprintf("20261028-1200%03d", i), make([]byte, 64), wakeCapture{Rate: 16000}); err != nil {
			t.Fatal(err)
		}
	}
	wavs, _ := filepath.Glob(filepath.Join(dir, "*.wav"))
	jsons, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(wavs) != captureKeep || len(jsons) != captureKeep {
		t.Fatalf("%d wavs, %d jsons", len(wavs), len(jsons))
	}
	if _, err := os.Stat(filepath.Join(dir, "20261028-1200000.wav")); !os.IsNotExist(err) {
		t.Fatal("the oldest is still there")
	}
	b, _ := os.ReadFile(wavs[0])
	if len(b) != 44+64 || string(b[:4]) != "RIFF" || string(b[36:40]) != "data" {
		t.Fatalf("wav header: %q", b[:44])
	}
}

func TestPrerollLead(t *testing.T) {
	a, _ := newWebApp(t)
	if a.prerollLead() != defaultPrerollMs*time.Millisecond {
		t.Fatalf("no board value, no config: %v", a.prerollLead())
	}
	a.prof.Wake.PrerollMs = 150
	if a.prerollLead() != 150*time.Millisecond {
		t.Fatalf("the board's: %v", a.prerollLead())
	}
	a.cfg().PrerollMs = 220
	if a.prerollLead() != 220*time.Millisecond {
		t.Fatalf("the config's wins: %v", a.prerollLead())
	}
}
