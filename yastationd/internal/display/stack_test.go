package display

import (
	"testing"
	"time"
)

func TestStackPriorityAndBase(t *testing.T) {
	s := NewStack(func(time.Time) string { return "base" })
	now := time.Now()
	if got := s.Frame(now); got != "base" {
		t.Fatalf("base: got %q", got)
	}
	s.Push("scenario", PrioScenario, 0, func(time.Time, string) (string, bool) { return "scenario", false })
	s.Push("error", PrioError, 0, func(time.Time, string) (string, bool) { return "error", false })
	if got := s.Frame(now); got != "error" {
		t.Fatalf("highest priority should win: got %q", got)
	}
	s.Remove("error")
	if got := s.Frame(now); got != "scenario" {
		t.Fatalf("after pop: got %q", got)
	}
}

func TestStackTTLExpiry(t *testing.T) {
	s := NewStack(func(time.Time) string { return "base" })
	s.Push("x", PrioLocal, 50*time.Millisecond, func(time.Time, string) (string, bool) { return "x", false })
	now := time.Now()
	if got := s.Frame(now); got != "x" {
		t.Fatalf("before TTL: got %q", got)
	}
	if got := s.Frame(now.Add(100 * time.Millisecond)); got != "base" {
		t.Fatalf("after TTL: got %q", got)
	}
	if s.Has("x") {
		t.Fatal("expired layer should be gone")
	}
}

func TestStackLayerDrawsOnBelow(t *testing.T) {
	s := NewStack(func(time.Time) string { return "base" })
	s.Push("deco", PrioDegraded, 0, func(_ time.Time, below string) (string, bool) { return below + "+dot", false })
	if got := s.Frame(time.Now()); got != "base+dot" {
		t.Fatalf("layer should see below: got %q", got)
	}
}

func TestStackDonePops(t *testing.T) {
	s := NewStack(func(time.Time) string { return "base" })
	calls := 0
	s.Push("once", PrioScenario, 0, func(time.Time, string) (string, bool) {
		calls++
		return "x", true // done immediately
	})
	s.Frame(time.Now())
	if s.Has("once") {
		t.Fatal("done layer should be removed")
	}
	s.Frame(time.Now())
	if calls != 1 {
		t.Fatalf("done layer rendered again: calls=%d", calls)
	}
}
