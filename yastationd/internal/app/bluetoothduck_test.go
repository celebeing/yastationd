package app

import (
	"testing"
	"time"
)

func TestSoftvolRaw(t *testing.T) {
	for _, c := range []struct {
		level float32
		want  int
	}{{1, 255}, {0, 0}, {0.5, 225}, {0.05, 125}} { // -6.02 dB -> 225 (0.2 dB a step), -26 dB -> 125
		if got := softvolRaw(c.level, 255); got != c.want {
			t.Errorf("level %v: raw %d, want %d", c.level, got, c.want)
		}
	}
}

func TestWakeDucksAtOnce(t *testing.T) {
	a := &App{}
	a.duckNow()
	a.media.mu.Lock()
	until := a.media.wakeUntil
	a.media.mu.Unlock()
	if until.Before(time.Now().Add(time.Second)) {
		t.Fatalf("wake duck ends too soon: %v", until)
	}
}
