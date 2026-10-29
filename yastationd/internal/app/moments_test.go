package app

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/display"
)

func TestMomentCandidates(t *testing.T) {
	a := newExpressApp(t)
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, refs([]string{"wink"})) {
		t.Errorf("seg7 dialog_end = %v", c)
	}
	if c := a.momentCandidates("listening"); !reflect.DeepEqual(c, refs([]string{"listening"})) {
		t.Errorf("unmapped moment = %v, want the expression of the same name", c)
	}
	a.front = display.PixelFront{W: 21, H: 12}
	if c := a.momentCandidates("dialog_end"); c[0].Ref != "blink" {
		t.Errorf("pixels dialog_end = %v", c)
	}
	a.prof = board.Profile{Moments: map[string][]string{"dialog_end": {"happy"}, "startup": nil}}
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, refs([]string{"happy"})) {
		t.Errorf("profile override = %v", c)
	}
	if a.moment("x", "startup", 0) {
		t.Error("a moment mapped to nothing showed something")
	}
}

func TestMomentShows(t *testing.T) {
	a := newExpressApp(t)
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
	if !a.refAvailable("scenario:startup") || a.refAvailable("scenario:nope") {
		t.Error("scenario availability")
	}
}

func TestShowJSON(t *testing.T) {
	in := `["blink", {"steps": [{"express": "eyes", "ms": 500}, {"anim": "wink", "hold": true}]}]`
	var s []show
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0].Ref != "blink" || len(s[1].Steps) != 2 || !s[1].Steps[1].Hold {
		t.Fatalf("parsed %+v", s)
	}
	out, _ := json.Marshal(s)
	var back []show
	json.Unmarshal(out, &back)
	if !reflect.DeepEqual(s, back) {
		t.Errorf("round trip: %s", out)
	}
	if json.Unmarshal([]byte(`[{"steps": []}]`), &s) == nil {
		t.Error("an empty sequence was accepted")
	}
}

func TestOwnerMoments(t *testing.T) {
	a := newExpressApp(t)
	a.cfg().SysRoot = t.TempDir()
	a.prof.Platform.CFWRoot = "/cfw"
	os.MkdirAll(a.cfg().SysRoot+"/cfw", 0o755)
	seq := []show{{Steps: []scenarioStep{{Express: "happy", Ms: 30}, {Anim: "wink", Hold: true}}}}
	if err := a.setMoment("dialog_end", seq); err != nil {
		t.Fatal(err)
	}
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, seq) {
		t.Fatalf("owner's candidates = %+v", c)
	}
	// it is on flash and loads back
	a.packs.Store(nil)
	if a.loadPacks(); !reflect.DeepEqual(a.momentCandidates("dialog_end"), seq) {
		t.Fatalf("reload: %+v", a.momentCandidates("dialog_end"))
	}
	// the sequence plays: happy (ring + front), then the held wink, which stays
	if !a.moment("voice", "dialog_end", 0) || !a.ringStack.Has("voice") {
		t.Fatal("first step not shown")
	}
	time.Sleep(150 * time.Millisecond)
	if !a.matrixStack.Has("voice") || a.ringStack.Has("voice") {
		t.Fatalf("held step: front %v ring %v", a.matrixStack.Has("voice"), a.ringStack.Has("voice"))
	}
	// anything else in the slot stops the sequence
	a.express("voice", "error", 0)
	a.unexpress("voice")
	time.Sleep(50 * time.Millisecond)
	if a.matrixStack.Has("voice") || a.ringStack.Has("voice") {
		t.Fatal("the slot did not move on")
	}
	// back to the defaults
	a.setMoment("dialog_end", nil)
	if c := a.momentCandidates("dialog_end"); !reflect.DeepEqual(c, refs([]string{"wink"})) {
		t.Fatalf("reset = %+v", c)
	}
}

func TestVoiceStageKeepsRing(t *testing.T) {
	a := newExpressApp(t)
	a.cfg().SysRoot = t.TempDir()
	os.MkdirAll(a.cfg().SysRoot+"/cfw", 0o755)
	a.prof.Platform.CFWRoot = "/cfw"
	// the owner's listening is front-only: the voice ring stays under it
	a.setMoment("listening", []show{{Steps: []scenarioStep{{Anim: "wink", Hold: true}}}})
	if !a.moment("voice", "listening", time.Minute) || !a.ringStack.Has("voice") || !a.matrixStack.Has("voice") {
		t.Fatalf("ring %v front %v", a.ringStack.Has("voice"), a.matrixStack.Has("voice"))
	}
	// a show that ends clears its slot; a ttl ends a held one
	a.setMoment("listening", []show{{Steps: []scenarioStep{{Anim: "wink", Hold: true}}}})
	a.momentAt("voice", "listening", 80*time.Millisecond, display.PrioExpression)
	time.Sleep(200 * time.Millisecond)
	if a.ringStack.Has("voice") || a.matrixStack.Has("voice") {
		t.Fatal("the ttl did not end the show")
	}
}

// newMomentApp is newExpressApp with a CFW root for moments.json.
func newMomentApp(t *testing.T) *App {
	a := newExpressApp(t)
	a.cfg().SysRoot = t.TempDir()
	a.prof.Platform.CFWRoot = "/cfw"
	os.MkdirAll(a.cfg().SysRoot+"/cfw", 0o755)
	return a
}

// layers: slot has a layer on the ring, on the front.
func layers(a *App, slot string) (ring, front bool) {
	return a.ringStack.Has(slot), a.matrixStack.Has(slot)
}

func TestSequenceEndsAndReplacesInPlace(t *testing.T) {
	a := newMomentApp(t)
	steps := []scenarioStep{{Express: "happy", Ms: 40}, {Anim: "wink", Ms: 40}, {Text: "hi", Ms: 40}}
	a.runSteps("x", steps, 0, display.PrioExpression, display.PrioScenario, "", 0)
	// the front is never empty while the sequence runs (steps replace in place)
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, f := layers(a, "x"); !f {
			t.Fatal("blank front between steps")
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(60 * time.Millisecond)
	if r, f := layers(a, "x"); r || f {
		t.Fatalf("a sequence without hold did not end: ring %v front %v", r, f)
	}
}

func TestSequenceStoppedBySlot(t *testing.T) {
	a := newMomentApp(t)
	// step 2 would start at 50 ms; an express in the slot at 10 ms must stop the sequence
	a.runSteps("voice", []scenarioStep{{Anim: "wink", Ms: 50}, {Express: "happy", Hold: true}}, 0, display.PrioExpression, display.PrioScenario, "", 0)
	time.Sleep(10 * time.Millisecond)
	a.express("voice", "listening", 0)
	time.Sleep(100 * time.Millisecond)
	if !a.ringStack.Has("voice") {
		t.Fatal("the express was undone by the stopped sequence")
	}
	if a.seqs.stop["voice"] != nil {
		t.Fatal("the sequence still runs")
	}
	// a new sequence in the slot replaces the old one
	a.runSteps("voice", []scenarioStep{{Anim: "wink", Ms: 30}}, 0, display.PrioExpression, display.PrioScenario, "", 0)
	a.runSteps("voice", []scenarioStep{{Anim: "wink", Hold: true}}, 0, display.PrioExpression, display.PrioScenario, "", 0)
	time.Sleep(80 * time.Millisecond)
	if _, f := layers(a, "voice"); !f {
		t.Fatal("the first sequence's end cleared the second's slot")
	}
}

func TestSequenceTTLCutsShort(t *testing.T) {
	a := newMomentApp(t)
	a.runSteps("x", []scenarioStep{{Anim: "wink", Ms: 500}, {Express: "happy", Ms: 500}}, 50*time.Millisecond, display.PrioExpression, display.PrioScenario, "", 0)
	time.Sleep(120 * time.Millisecond)
	if r, f := layers(a, "x"); r || f {
		t.Fatal("the ttl did not end the sequence")
	}
}

func TestStepLength(t *testing.T) {
	a := newMomentApp(t)
	if d := a.stepLength(scenarioStep{Anim: "wink", Ms: 700}); d != 700*time.Millisecond {
		t.Errorf("ms: %v", d)
	}
	c, _ := a.front.Anim("wink")
	if d := a.stepLength(scenarioStep{Anim: "wink"}); d != c.Duration() || d == 0 {
		t.Errorf("auto: %v, the clip is %v", d, c.Duration())
	}
	if d := a.stepLength(scenarioStep{Text: "x"}); d != stepDefault {
		t.Errorf("default: %v", d)
	}
	if d := a.stepLength(scenarioStep{Anim: "wink", Ms: 1 << 30}); d != maxSequenceMs*time.Millisecond {
		t.Errorf("cap: %v", d)
	}
}

func TestOwnerMomentFallbacks(t *testing.T) {
	a := newMomentApp(t)
	// unavailable candidates are skipped, in order
	a.setMoment("dialog_end", []show{{Ref: "anim:nope"}, {Steps: []scenarioStep{{Anim: "nope"}}}, {Ref: "happy"}})
	if !a.moment("voice", "dialog_end", 0) || !a.ringStack.Has("voice") {
		t.Fatal("the available fallback did not play")
	}
	// none available: nothing, and false
	a.unexpress("voice")
	a.setMoment("dialog_end", []show{{Ref: "nope"}, {Ref: "scenario:nope"}})
	if a.moment("voice", "dialog_end", 0) {
		t.Fatal("showed something with no candidate available")
	}
	// an empty list: the owner turned it off
	a.setMoment("dialog_end", []show{})
	if a.moment("voice", "dialog_end", 0) {
		t.Fatal("an empty list showed something")
	}
	if c := a.momentCandidates("dialog_end"); c == nil || len(c) != 0 {
		t.Fatalf("empty list became %v", c)
	}
	// the owner's choice beats the board profile
	a.prof.Moments = map[string][]string{"dialog_end": {"wink"}}
	if len(a.momentCandidates("dialog_end")) != 0 {
		t.Fatal("the profile beat the owner")
	}
}

// An old moments.json (before the owner's packs) folds into the pack custom; a broken one is
// put aside and the speaker starts with the defaults.
func TestOldMomentsFile(t *testing.T) {
	a := newMomentApp(t)
	os.WriteFile(a.momentPath(), []byte(`{"dialog_end": ["happy"]}`), 0o644)
	a.foldOldMoments()
	if c := a.momentCandidates("dialog_end"); len(c) != 1 || c[0].Ref != "happy" {
		t.Fatalf("folded: %v", c)
	}
	if _, err := os.Stat(a.momentPath()); err == nil {
		t.Error("the old file stayed")
	}
	b := newMomentApp(t)
	os.WriteFile(b.momentPath(), []byte(`{"dialog_end": [{"steps": []}]}`), 0o644)
	b.foldOldMoments()
	if c := b.momentCandidates("dialog_end"); !reflect.DeepEqual(c, refs([]string{"wink"})) {
		t.Fatalf("defaults lost: %v", c)
	}
}

func TestVoiceStagesHandOver(t *testing.T) {
	a := newMomentApp(t)
	// the owner's listening: a held front-only sequence over the voice ring
	a.setMoment("listening", []show{{Steps: []scenarioStep{{Anim: "wink", Ms: 20}, {Anim: "happy", Hold: true}}}})
	a.moment("voice", "listening", time.Minute)
	time.Sleep(60 * time.Millisecond)
	if r, f := layers(a, "voice"); !r || !f {
		t.Fatalf("listening: ring %v front %v", r, f)
	}
	// thinking (the default expression) takes the slot: the sequence stops, its ring goes
	a.moment("voice", "thinking", time.Minute)
	if a.seqs.stop["voice"] != nil {
		t.Fatal("listening's sequence still runs under thinking")
	}
	if r, _ := layers(a, "voice"); !r {
		t.Fatal("thinking has no ring")
	}
	// the end of the dialog clears everything
	a.unexpress("voice")
	time.Sleep(30 * time.Millisecond)
	if r, f := layers(a, "voice"); r || f {
		t.Fatal("left over after unexpress")
	}
}

func TestScenarioStillWorks(t *testing.T) {
	a := newMomentApp(t)
	a.runScenario(scenario{Steps: []scenarioStep{{Express: "happy", Ms: 30}, {Matrix: "wink", Ms: 30}}})
	if !a.ringStack.Has(scenarioSlot) {
		t.Fatal("the first step (happy) has no ring")
	}
	time.Sleep(45 * time.Millisecond)
	if r, f := layers(a, scenarioSlot); r || !f {
		t.Fatalf("the matrix step (old name): ring %v front %v", r, f)
	}
	a.stopScenario()
	if r, f := layers(a, scenarioSlot); r || f {
		t.Fatal("stopScenario left layers")
	}
}
