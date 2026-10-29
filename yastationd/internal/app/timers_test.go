package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

func timerUpdate(id string, seconds int, active bool) wyoming.Event {
	return wyoming.Event{Type: wyoming.TypeTimerUpdated, Data: map[string]any{
		"id": id, "total_seconds": float64(seconds), "is_active": active,
	}}
}

func TestTimerDisplayPrefersSoonestActiveAndClearsCancelled(t *testing.T) {
	a := newExpressApp(t)
	a.onTimerEvent(timerUpdate("paused", 10, false))
	if !a.matrixStack.Has("timer") {
		t.Fatal("paused timer did not produce a display layer")
	}
	a.onTimerEvent(timerUpdate("later", 60, true))
	a.onTimerEvent(timerUpdate("sooner", 30, true))

	a.timers.mu.Lock()
	deadline := a.timers.m["sooner"].deadline
	a.timers.mu.Unlock()
	now := time.Now()
	want, _ := a.front.Countdown(deadline, a.brightness)(now, display.FrontFrame{})
	if got := a.matrixStack.Frame(now); got.Seg != want.Seg {
		t.Fatalf("countdown = %+v, want sooner timer %+v", got.Seg, want.Seg)
	}

	a.onTimerEvent(wyoming.Event{Type: wyoming.TypeTimerCancelled, Data: map[string]any{"id": "sooner"}})
	a.onTimerEvent(wyoming.Event{Type: wyoming.TypeTimerCancelled, Data: map[string]any{"id": "later"}})
	if !a.matrixStack.Has("timer") {
		t.Fatal("paused timer disappeared while still present")
	}
	a.onTimerEvent(wyoming.Event{Type: wyoming.TypeTimerCancelled, Data: map[string]any{"id": "paused"}})
	if a.matrixStack.Has("timer") {
		t.Fatal("timer layer remained after all timers were cancelled")
	}
}

func TestStopAlarmOnlyConsumesAnActiveAlarm(t *testing.T) {
	a := newExpressApp(t)
	if a.stopAlarm() {
		t.Fatal("idle alarm reported stopped")
	}
	stop := make(chan struct{})
	a.timers.alarm = stop
	if !a.stopAlarm() {
		t.Fatal("active alarm was not stopped")
	}
	select {
	case <-stop:
	default:
		t.Fatal("alarm stop channel was not closed")
	}
	if a.stopAlarm() {
		t.Fatal("stopped alarm reported active twice")
	}
}
