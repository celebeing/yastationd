package app

import (
	"testing"
	"time"
)

func TestCPUMeter(t *testing.T) {
	var m cpuMeter
	t0 := time.Unix(1000, 0)
	if _, ok := m.sample("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 1 2 3\n", t0); ok {
		t.Fatal("a first sample has no percentage")
	}
	// 100 more ticks of work and 100 of idle + iowait: half busy.
	got, ok := m.sample("cpu  150 0 150 880 20 0 0 0 0 0\n", t0.Add(2*time.Second))
	if !ok || got != 50 {
		t.Fatalf("got %v, %v", got, ok)
	}
	// Too soon: the last figure stands.
	if got, _ := m.sample("cpu  999 0 150 880 20 0 0 0 0 0\n", t0.Add(2500*time.Millisecond)); got != 50 {
		t.Fatalf("soon: %v", got)
	}
	if _, ok := m.sample("garbage", t0.Add(9*time.Second)); ok {
		t.Fatal("garbage read as a percentage")
	}
}
