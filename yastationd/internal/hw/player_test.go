package hw

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

func TestPlayerStreamArgumentsAndLifecycle(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("ARGS", args)
	fakeAplay(t, `printf '%s\n' "$@" > "$ARGS"; cat >/dev/null`)
	var states []bool
	p := &Player{Device: "default", OnState: func(playing bool) { states = append(states, playing) }}
	w, done, err := p.Stream(16_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not finish")
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatal(err)
	}
	if want := "-q\n-D\ndefault\n-t\nraw\n-f\nS16_LE\n-r\n16000\n-c\n1\n"; string(got) != want {
		t.Fatalf("aplay arguments = %q, want %q", got, want)
	}
	if want := []bool{true, false}; !reflect.DeepEqual(states, want) {
		t.Fatalf("playback states = %v, want %v", states, want)
	}
}

func TestPlayerStreamSupersededDoesNotStopReplacement(t *testing.T) {
	fakeAplay(t, "cat >/dev/null")
	var states []bool
	p := &Player{Device: "default", OnState: func(playing bool) { states = append(states, playing) }}
	first, firstDone, err := p.Stream(16_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	second, secondDone, err := p.Stream(16_000, 1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not stop")
	}
	if want := []bool{true, true}; !reflect.DeepEqual(states, want) {
		t.Fatalf("states after replacement = %v, want %v", states, want)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement stream did not finish")
	}
	if want := []bool{true, true, false}; !reflect.DeepEqual(states, want) {
		t.Fatalf("final playback states = %v, want %v", states, want)
	}
}

func TestBlipChannelsReplaceOnlyTheirOwnPlayback(t *testing.T) {
	fakeAplay(t, "cat >/dev/null")
	p := &Player{Device: "default"}
	if err := p.BlipOn("keys", "key.wav"); err != nil {
		t.Fatal(err)
	}
	if err := p.BlipOn("alerts", "alert.wav"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	keys := p.blips["keys"]
	alerts := p.blips["alerts"]
	p.mu.Unlock()
	if keys == nil || alerts == nil || len(p.blips) != 2 {
		t.Fatal("independent blip channels were not tracked")
	}

	if err := p.BlipOn("keys", "next-key.wav"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	keys = p.blips["keys"]
	alerts = p.blips["alerts"]
	p.mu.Unlock()
	if keys == nil || alerts == nil {
		t.Fatal("same-channel replacement removed another channel")
	}

	p.StopBlipOn("keys")
	p.mu.Lock()
	_, keysRemain := p.blips["keys"]
	_, alertRemains := p.blips["alerts"]
	p.mu.Unlock()
	if keysRemain || !alertRemains {
		t.Fatal("stopping keys blip affected channel ownership")
	}
	p.StopBlipOn("alerts")
}
