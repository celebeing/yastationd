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

// The ladder: down -> reconfigure at 2 min -> controller reset at 4 min (again every 10) ->
// reboot at the limit; any good check starts it over.
func TestNetWatchLadder(t *testing.T) {
	w := netWatch{limit: 30 * time.Minute, reconfigureAfter: 2 * time.Minute, resetAfter: 4 * time.Minute, resetEvery: 10 * time.Minute}
	t0 := time.Unix(1000, 0)
	up := time.Hour // uptime well past the limit
	var got []string
	names := map[netAction]string{netDown: "down", netReconfigure: "reconfigure", netWifiResetAction: "reset", netReboot: "reboot"}
	for s := 0; s <= 31*60; s += 30 {
		if a := w.step(t0.Add(time.Duration(s)*time.Second), false, up); a != netNothing {
			got = append(got, names[a]+"@"+time.Duration(s*int(time.Second)).String())
			if a == netReboot {
				break
			}
		}
	}
	want := []string{"down@0s", "reconfigure@2m0s", "reset@4m0s", "reset@14m0s", "reset@24m0s", "reboot@30m0s"}
	if len(got) != len(want) {
		t.Fatalf("ladder %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ladder %v, want %v", got, want)
		}
	}
	// Back up: the next outage starts at the bottom again.
	t1 := t0.Add(time.Hour)
	w.step(t1, true, up)
	if a := w.step(t1.Add(30*time.Second), false, up); a != netDown {
		t.Fatalf("new outage: %v", a)
	}
	if a := w.step(t1.Add(150*time.Second), false, up); a != netReconfigure {
		t.Fatalf("new outage, 2 min: %v", a)
	}
}

// Without a reset command the ladder skips that rung; a young speaker is not rebooted.
func TestNetWatchNoResetYoung(t *testing.T) {
	w := netWatch{limit: 10 * time.Minute, reconfigureAfter: 2 * time.Minute}
	t0 := time.Unix(1000, 0)
	for s := 0; s <= 11*60; s += 30 {
		a := w.step(t0.Add(time.Duration(s)*time.Second), false, 5*time.Minute)
		if a == netWifiResetAction || a == netReboot {
			t.Fatalf("at %ds: %v", s, a)
		}
	}
	if a := w.step(t0.Add(12*time.Minute), false, 12*time.Minute); a != netReboot {
		t.Fatalf("old enough: %v", a)
	}
}
