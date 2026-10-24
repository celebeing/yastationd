package hw

import (
	"testing"
	"time"
)

func TestCircularAxisClockwiseAcrossWrap(t *testing.T) {
	axis := NewCircularAxis(AbsInfo{Minimum: 0, Maximum: 1000}, 20, 750*time.Millisecond)
	now := time.Unix(1, 0)
	values := []int32{722, 800, 806, 900, 0, 7, 100}
	steps := 0
	for _, value := range values {
		steps += axis.Move(value, now)
		now = now.Add(100 * time.Millisecond)
	}
	if steps != 7 {
		t.Fatalf("clockwise steps = %d, want 7", steps)
	}
}

func TestCircularAxisCounterclockwiseAcrossWrap(t *testing.T) {
	axis := NewCircularAxis(AbsInfo{Minimum: 0, Maximum: 1000}, 20, 750*time.Millisecond)
	now := time.Unix(1, 0)
	values := []int32{500, 492, 400, 372, 300, 287, 200, 191, 100, 82, 0, 992, 900}
	steps := 0
	for _, value := range values {
		steps += axis.Move(value, now)
		now = now.Add(100 * time.Millisecond)
	}
	if steps != -12 {
		t.Fatalf("counterclockwise steps = %d, want -12", steps)
	}
}

func TestCircularAxisNewGestureResetsBaseline(t *testing.T) {
	const gap = 750 * time.Millisecond
	axis := NewCircularAxis(AbsInfo{Minimum: 0, Maximum: 1000}, 20, gap)
	now := time.Unix(1, 0)
	if got := axis.Move(100, now); got != 0 {
		t.Fatalf("first sample = %d, want 0", got)
	}
	if got := axis.Move(900, now.Add(gap+time.Millisecond)); got != 0 {
		t.Fatalf("new gesture sample = %d, want 0", got)
	}
}
