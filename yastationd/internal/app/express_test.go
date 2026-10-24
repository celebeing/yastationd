package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func newExpressApp(t *testing.T, faces bool) *App {
	t.Helper()
	st := defaultState(128, 128, 128, false)
	st.Faces = faces
	a := &App{
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(6) }),
		matrixStack: display.NewStack(func(time.Time) display.MatrixFrame { return display.MatrixFrame{} }),
		matrix:      hw.NewMatrix(t.TempDir(), "clock{seg}{digit}", []string{"DH"}, "", ""),
		store:       newStateStore(t.TempDir()+"/state.json", st),
		clips:       &display.ClipLibrary{Dirs: []string{t.TempDir()}},
	}
	return a
}

func TestExpressSurfaces(t *testing.T) {
	a := newExpressApp(t, true)
	if !a.express("voice", "listening", 0) {
		t.Fatal("listening showed nothing")
	}
	if !a.ringStack.Has("voice") || !a.matrixStack.Has("voice") {
		t.Fatalf("ring %v / front %v", a.ringStack.Has("voice"), a.matrixStack.Has("voice"))
	}
	if f := a.matrixStack.Frame(time.Now()); !f.Graded {
		t.Fatalf("front frame is not a clip frame: %+v", f)
	}
	// error has no front part: the listening clip must not stay on the front display.
	a.express("voice", "error", time.Second)
	if a.matrixStack.Has("voice") || !a.ringStack.Has("voice") {
		t.Fatal("error left the front clip or dropped the ring")
	}
	a.unexpress("voice")
	if a.ringStack.Has("voice") || a.matrixStack.Has("voice") {
		t.Fatal("unexpress left layers")
	}
	// A clip name that is no expression plays front-only.
	if !a.express("ha", "wink", 0) || a.ringStack.Has("ha") || !a.matrixStack.Has("ha") {
		t.Fatal("bare clip name")
	}
}

func TestExpressFacesOff(t *testing.T) {
	a := newExpressApp(t, false)
	a.express("voice", "speaking", 0)
	if a.matrixStack.Has("voice") || !a.ringStack.Has("voice") {
		t.Fatal("faces off: front must stay clear, ring must still show the stage")
	}
}

func TestExpressOneShotRingEnds(t *testing.T) {
	a := newExpressApp(t, true)
	a.express("boot", "happy", 0) // one-shot clip + ring effect
	clip, _ := a.clips.Load("happy")
	now := time.Now()
	if !a.ringStack.Has("boot") {
		t.Fatal("no ring part")
	}
	a.ringStack.Frame(now.Add(clip.Duration() + time.Second)) // past the clip: the ring layer expires
	if a.ringStack.Has("boot") {
		t.Fatal("ring effect outlived the one-shot clip")
	}
}

func TestExpressWinkClearsRingAndHolds(t *testing.T) {
	a := newExpressApp(t, true)
	a.express("voice", "speaking", 0)
	a.express("voice", "wink", 5*time.Second)
	if a.ringStack.Has("voice") || !a.matrixStack.Has("voice") {
		t.Fatal("wink must take over the slot: front only, no ring left")
	}
	clip, _ := a.clips.Load("wink")
	a.matrixStack.Frame(time.Now())
	a.matrixStack.Frame(time.Now().Add(clip.Duration() + 500*time.Millisecond))
	if !a.matrixStack.Has("voice") {
		t.Fatal("short clip with a TTL did not hold its last frame")
	}
}
