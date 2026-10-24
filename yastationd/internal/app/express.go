package app

import (
	"os"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Expressions: semantic states shown on every surface the board has, each surface on its own
// stack (docs/display-expressions.md). Callers name the state and a slot; all of the slot's
// layers are replaced or removed together.

// expression says what one state shows per surface.
type expression struct {
	ring     string   // ring effect (effects.go); "" = leaves the ring alone
	ringPrio int      // its priority on the ring
	clips    []string // front-display clip candidates (stock names across firmwares, then ours)
	loop     bool     // the clip repeats until the slot is cleared
}

var expressions = map[string]expression{
	"listening": {ring: "listening", ringPrio: display.PrioVoice, clips: []string{"alice_listening", "listening"}, loop: true},
	"thinking":  {ring: "thinking", ringPrio: display.PrioVoice, clips: []string{"alice_thinking", "thinking"}, loop: true},
	"speaking":  {ring: "speaking", ringPrio: display.PrioVoice, clips: []string{"alice_speaking", "speaking"}, loop: true},
	"error":     {ring: "error", ringPrio: display.PrioVoice},
	"mute":      {ring: "mute", ringPrio: display.PrioVoice},
	"boot":      {ring: "boot", ringPrio: display.PrioScenario, clips: []string{"boot"}},
	"ringing":   {clips: []string{"ringing"}, loop: true},
	"pause":     {clips: []string{"player_pause", "pause"}},
	"next":      {clips: []string{"player_next_track", "next_track"}},
	"previous":  {clips: []string{"player_previous_track", "previous_track"}},
	"eyes":      {clips: []string{"eyes", "configuring"}, loop: true},
	"blink":     {clips: []string{"blink"}},
	"happy":     {ring: "success", ringPrio: display.PrioScenario, clips: []string{"happy"}},
	"sad":       {clips: []string{"sad"}},
	"wink":      {clips: []string{"wink"}},
	"sleep":     {clips: []string{"sleep"}, loop: true},
}

// oneShotRing is how long a one-shot expression's ring part lasts when it has no clip to end with.
const oneShotRing = 2 * time.Second

// ringHolds: voice-pipeline states, whose ring part lasts until the pipeline moves on (the
// callers clear the slot), whatever the front shows.
func ringHolds(name string) bool {
	switch name {
	case "listening", "thinking", "speaking", "error", "mute":
		return true
	}
	return false
}

// expressionNames is the HA-facing list (stable order).
var expressionNames = []string{"listening", "thinking", "speaking", "happy", "sad", "wink", "eyes",
	"blink", "sleep", "ringing", "pause", "next", "previous", "error"}

// frontClips: the front display takes clip layers. Only 7-segment displays for now: pixel
// screens get their own scenes once explored on a device, and show none of these meanwhile.
func (a *App) frontClips() bool {
	return a.matrix != nil && a.screen == nil && a.store.get().Faces
}

// express shows state name in slot on every surface it has a part for; ttl 0 = until replaced,
// removed or (for a one-shot clip) played out. A name that is no expression but a clip plays
// that clip once. It reports whether anything was shown.
func (a *App) express(slot, name string, ttl time.Duration) bool {
	e, ok := expressions[name]
	if !ok {
		e = expression{clips: []string{name}}
	}
	shown := false
	var clip display.Clip
	haveClip := false
	if len(e.clips) > 0 && a.frontClips() {
		clip, haveClip = a.clips.Load(e.clips...)
	}
	// Replace in place (Push over the same slot), never remove-then-push: a render between the two
	// would show the layer below for a frame. A state without a front part (speaking -> error)
	// removes the previous state's clip.
	if haveClip {
		r := display.PlayClip(clip, e.loop, a.brightness)
		if !e.loop && ttl > 0 { // a short clip asked for ttl keeps its last frame
			r = display.HoldClip(clip, a.brightness)
		}
		a.matrixStack.Push(slot, display.PrioExpression, ttl, r)
		shown = true
	} else {
		a.matrixStack.Remove(slot)
	}
	if e.ring == "" { // the slot moves on as a whole: no ring part left from its previous state
		a.ringStack.Remove(slot)
	}
	if e.ring != "" {
		if r := effect(e.ring); r != nil {
			// A one-shot expression without a TTL ends with its clip; ring effects never end on
			// their own, so give the ring the clip's length (or a short default without one).
			rttl := ttl
			if rttl == 0 && !e.loop && !ringHolds(name) {
				rttl = oneShotRing
				if haveClip {
					rttl = clip.Duration()
				}
			}
			a.ringStack.Push(slot, e.ringPrio, rttl, r)
			shown = true
		}
	}
	return shown
}

// unexpress clears slot on every surface.
func (a *App) unexpress(slot string) {
	a.ringStack.Remove(slot)
	a.matrixStack.Remove(slot)
}

// bootMarker plays the startup greeting once per device boot, not on every daemon restart. The
// stock "boot" clip is not used: it is made to go with a boot jingle and runs ~10 s.
const bootMarker = "/tmp/yastationd.boot-shown"

func (a *App) expressBoot() {
	marker := a.cfg.Path(bootMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	os.WriteFile(marker, nil, 0o644)
	if s, err := a.loadScenario("startup"); err == nil {
		a.runScenario(s)
	}
}

// newClipLibrary searches the CFW's converted clips, then the firmware's own, then ours.
func (a *App) newClipLibrary() *display.ClipLibrary {
	dirs := []string{a.cfg.Path(a.cfg.ClipsDir)}
	if d := a.prof.Clips.Dir; d != "" {
		dirs = append(dirs, a.cfg.Path(d))
	}
	return &display.ClipLibrary{Dirs: dirs}
}
