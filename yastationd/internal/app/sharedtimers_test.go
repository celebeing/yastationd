package app

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/voiceid"
	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

func timerMsg(t *testing.T, m sharedTimer) []byte {
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func alarmOn(a *App) bool {
	a.timers.mu.Lock()
	defer a.timers.mu.Unlock()
	return a.timers.alarm != nil
}

// A speaker in the person's places follows another speaker's timer: it counts it down, rings at
// its deadline, and a stop from another speaker silences it. One not in the places ignores it.
func TestSharedTimerMirror(t *testing.T) {
	r := newRegressApp(t)
	me := r.cfg().DeviceID
	other := sharedTimer{Owner: "kitchen", ID: "t1", Name: "Дима", Places: []string{"bedroom"}, State: timerRunning,
		Deadline: time.Now().Add(time.Hour).UnixMilli(), By: "kitchen"}
	r.onSharedTimer("", timerMsg(t, other))
	r.timers.mu.Lock()
	n := len(r.timers.m)
	r.timers.mu.Unlock()
	if n != 0 {
		t.Fatal("followed a timer that is not for this speaker")
	}

	m := sharedTimer{Owner: "kitchen", ID: "t2", Name: "Дима", Places: []string{me}, State: timerRunning,
		Deadline: time.Now().Add(300 * time.Millisecond).UnixMilli(), By: "kitchen"}
	r.onSharedTimer("", timerMsg(t, m))
	if !r.matrixStack.Has("timer") {
		t.Fatal("no countdown for the followed timer")
	}
	deadline := time.Now().Add(3 * time.Second)
	for !alarmOn(r.App) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !alarmOn(r.App) {
		t.Fatal("the followed timer did not ring at its deadline")
	}
	m.State, m.By = timerDone, "study"
	r.onSharedTimer("", timerMsg(t, m))
	if alarmOn(r.App) {
		t.Fatal("a stop on another speaker left this one ringing")
	}

	// paused, then cancelled by the owner: no ring
	m = sharedTimer{Owner: "kitchen", ID: "t3", Places: []string{me}, State: timerPaused, Left: 60000, By: "kitchen"}
	r.onSharedTimer("", timerMsg(t, m))
	m.State = timerDone
	r.onSharedTimer("", timerMsg(t, m))
	r.timers.mu.Lock()
	_, left := r.timers.m[mirrorKey("kitchen", "t3")]
	r.timers.mu.Unlock()
	if left {
		t.Fatal("a cancelled timer is still shown")
	}
}

// The owner takes the request's person from the latest known verdict, and their places from the
// household (this speaker left out).
func TestSharedTimerRequester(t *testing.T) {
	r := newRegressApp(t)
	r.vid = &voiceID{}
	r.vid.people = []voiceid.Person{{ID: "g-1", Name: "Дима", Places: []string{"study", r.cfg().DeviceID}}}
	if _, ok := r.requester(0); ok {
		t.Fatal("a requester before any verdict")
	}
	r.noteVerdict(voiceid.Verdict{State: voiceid.Known, ID: "g-1", Name: "Дима"})
	v, ok := r.requester(0)
	if !ok || v.ID != "g-1" {
		t.Fatalf("requester: %+v %v", v, ok)
	}
	if p := r.personPlaces("g-1"); len(p) != 1 || p[0] != "study" {
		t.Fatalf("places: %v", p)
	}
	r.noteVerdict(voiceid.Verdict{State: voiceid.Guest})
	if _, ok := r.requester(0); ok {
		t.Fatal("a guest was taken for the requester")
	}
}

// A near-miss «стоп» over the beep stops the ringing; a near miss of another word does not.
func TestNearStopStopsAlarm(t *testing.T) {
	r := newRegressApp(t)
	r.startAlarm()
	if !alarmOn(r.App) {
		t.Fatal("no alarm")
	}
	r.onWakeEvent(wake.Event{Kind: "SUBHIT:CMD", Phrase: "громче"})
	if !alarmOn(r.App) {
		t.Fatal("a near «громче» stopped the alarm")
	}
	r.onWakeEvent(wake.Event{Kind: "SUBHIT:CMD", Phrase: "стоп"})
	if alarmOn(r.App) {
		t.Fatal("a near «стоп» left the alarm ringing")
	}
}
