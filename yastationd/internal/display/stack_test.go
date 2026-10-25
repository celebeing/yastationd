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

func TestStackTopAndFader(t *testing.T) {
	s := NewStack(func(time.Time) int { return 0 })
	now := time.Unix(100, 0)
	s.Frame(now)
	f := Fader{Up: 100 * time.Millisecond, Down: 400 * time.Millisecond}
	if k := f.Level(now, s.Top()); k != 1 {
		t.Fatalf("first frame faded: %v", k)
	}
	s.Push("bubble", PrioLocal, 0, func(time.Time, int) (int, bool) { return 1, false })
	s.Frame(now)
	if top := s.Top(); top.Name != "bubble" || top.Prio != PrioLocal {
		t.Fatalf("top = %+v", top)
	}
	if k := f.Level(now, s.Top()); k != 0 { // a higher layer starts its fast fade from 0
		t.Fatalf("up fade start = %v", k)
	}
	if k := f.Level(now.Add(100*time.Millisecond), s.Top()); k != 1 {
		t.Fatalf("up fade not done after Up: %v", k)
	}
	s.Remove("bubble")
	s.Frame(now.Add(time.Second))
	f.Level(now.Add(time.Second), s.Top())
	if k := f.Level(now.Add(time.Second+200*time.Millisecond), s.Top()); k < 0.4 || k > 0.6 {
		t.Fatalf("down fade halfway = %v, want ~0.5", k)
	}
}

func TestStackKeep(t *testing.T) {
	s := NewStack(func(time.Time) int { return 0 })
	if s.Keep("x", time.Second) {
		t.Fatal("kept a missing layer")
	}
	s.Push("x", 10, 50*time.Millisecond, func(time.Time, int) (int, bool) { return 1, false })
	s.Frame(time.Now())
	top := s.Top()
	if !s.Keep("x", time.Hour) {
		t.Fatal("Keep of a live layer failed")
	}
	time.Sleep(80 * time.Millisecond)
	if s.Frame(time.Now()) != 1 || s.Top() != top {
		t.Errorf("kept layer expired or changed its Top: %+v vs %+v", s.Top(), top)
	}
}
