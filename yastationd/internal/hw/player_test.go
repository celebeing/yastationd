package hw

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeAplay puts an aplay on PATH that runs script (sh) and returns.
func fakeAplay(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "aplay"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("aplay did not exit")
		return nil
	}
}

func TestPlayerStartOutcomes(t *testing.T) {
	fakeAplay(t, "exit 0")
	p := &Player{Device: "default"}
	done, _, err := p.Start("a.wav")
	if err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, done); err != nil {
		t.Errorf("clean end: %v", err)
	}

	fakeAplay(t, "echo 'no such device' >&2; exit 1")
	done, _, _ = p.Start("a.wav")
	if err := waitDone(t, done); err == nil || errors.Is(err, ErrCut) {
		t.Errorf("failure: %v", err)
	}

	fakeAplay(t, "exec sleep 10")
	done, _, _ = p.Start("a.wav")
	p.Stop()
	if err := waitDone(t, done); !errors.Is(err, ErrCut) {
		t.Errorf("Stop: %v", err)
	}
}

// A later Play cuts the earlier one, and the earlier one's stop can no longer touch the new sound.
func TestPlayerStartSuperseded(t *testing.T) {
	fakeAplay(t, "exec sleep 10")
	p := &Player{Device: "default"}
	first, stopFirst, err := p.Start("a.wav")
	if err != nil {
		t.Fatal(err)
	}
	second, _, _ := p.Start("b.wav")
	if err := waitDone(t, first); !errors.Is(err, ErrCut) {
		t.Errorf("superseded: %v", err)
	}
	stopFirst()
	select {
	case err := <-second:
		t.Fatalf("the old stop cut the new playback: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	p.Stop()
	waitDone(t, second)
}
