package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(6) }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
		matrix:      hw.NewMatrix(t.TempDir(), "clock{seg}{digit}", []string{"DH"}, "", ""),
		store:       newStateStore(t.TempDir()+"/state.json", st),
		front:       display.Seg7Front{Clips: &display.ClipLibrary{Dirs: []string{t.TempDir()}}},
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
	if f := a.matrixStack.Frame(time.Now()).Seg; !f.Graded {
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
	clip, _ := a.front.Anim("happy")
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
	clip, _ := a.front.Anim("wink")
	a.matrixStack.Frame(time.Now())
	a.matrixStack.Frame(time.Now().Add(clip.Duration() + 500*time.Millisecond))
	if !a.matrixStack.Has("voice") {
		t.Fatal("short clip with a TTL did not hold its last frame")
	}
}

// On a pixel screen an expression plays the screen's own pattern (native pixels), never a seg7
// clip turned into pixels; an expression the screen has no pattern for leaves the front alone.
func TestExpressPixelFront(t *testing.T) {
	dir := t.TempDir()
	frame := strings.TrimSpace(strings.Repeat("ffffff ", 21*12)) + " 20\n"
	if err := os.MkdirAll(filepath.Join(dir, "oknix"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oknix", "06-answer-loop.led"), []byte("loop\n"+frame), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newExpressApp(t, true)
	a.matrix = nil
	a.front = display.PixelFront{W: 21, H: 12, Patterns: &display.PixelLibrary{Dirs: []string{dir}, W: 21, H: 12}}
	if !a.express("voice", "speaking", 0) || !a.matrixStack.Has("voice") {
		t.Fatal("speaking showed no front layer")
	}
	if f := a.matrixStack.Frame(time.Now()); len(f.Px) != 252 {
		t.Fatalf("front frame is not native pixels: %d px", len(f.Px))
	}
	a.express("voice", "sad", time.Second) // no pixel pattern: the slot leaves the front
	if a.matrixStack.Has("voice") {
		t.Fatal("sad left a front layer on a screen without its pattern")
	}
	for _, n := range a.expressionOptions() {
		if n == "sad" || n == "wink" {
			t.Fatalf("%s offered without a pattern or a ring part", n)
		}
	}
}
