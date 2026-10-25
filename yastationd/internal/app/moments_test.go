package app

import (
	"reflect"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/display"
)

func TestMomentCandidates(t *testing.T) {
	a := newExpressApp(t, true)
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, []string{"wink"}) {
		t.Errorf("seg7 dialog_end = %v", c)
	}
	if c := a.momentCandidates("listening"); !reflect.DeepEqual(c, []string{"listening"}) {
		t.Errorf("unmapped moment = %v, want the expression of the same name", c)
	}
	a.front = display.PixelFront{W: 21, H: 12}
	if c := a.momentCandidates("dialog_end"); c[0] != "blink" {
		t.Errorf("pixels dialog_end = %v", c)
	}
	a.prof = board.Profile{Moments: map[string][]string{"dialog_end": {"happy"}, "startup": nil}}
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, []string{"happy"}) {
		t.Errorf("profile override = %v", c)
	}
	if a.moment("x", "startup", 0) {
		t.Error("a moment mapped to nothing showed something")
	}
}

func TestMomentShows(t *testing.T) {
	a := newExpressApp(t, true)
	// seg7: dialog_end -> our embedded wink clip on the front
	if !a.moment("voice", "dialog_end", 0) || !a.matrixStack.Has("voice") {
		t.Fatal("dialog_end showed nothing on seg7")
	}
	// a candidate this speaker lacks is skipped for the next one
	a.prof = board.Profile{Moments: map[string][]string{"dialog_end": {"no-such-anim", "happy"}}}
	a.unexpress("voice")
	if !a.moment("voice", "dialog_end", 0) || !a.ringStack.Has("voice") {
		t.Fatal("fallback candidate not shown")
	}
	if !a.momentAvailable("scenario:startup") || a.momentAvailable("scenario:nope") {
		t.Error("scenario availability")
	}
}
