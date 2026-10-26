package app

import (
	"testing"
	"time"
)

func TestNetWatchLimitBacksOff(t *testing.T) {
	base := 10 * time.Minute
	for n, want := range []time.Duration{10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute,
		160 * time.Minute, 4 * time.Hour, 4 * time.Hour} {
		if got := netWatchLimit(base, n); got != want {
			t.Errorf("after %d reboots: %v, want %v", n, got, want)
		}
	}
	if got := netWatchLimit(6*time.Hour, 3); got != 6*time.Hour {
		t.Errorf("a base above the cap stays: %v", got)
	}
}
