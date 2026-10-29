package app

import (
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Expressions: semantic states shown on every surface the board has, each surface on its own
// stack (docs/display-expressions.md). Callers name the state and a slot; all of the slot's
// layers are replaced or removed together.

// expression says what one state shows on the ring and how its front part plays. Which animation
// the front part is, is the front display's business (display.Front.Anim resolves the name): a
// seg7 display plays a clock clip, a pixel screen its own screen pattern, and a display without
// one shows nothing.
type expression struct {
	ring     string // ring effect (effects.go); "" = leaves the ring alone
	ringPrio int    // its priority on the ring
	loop     bool   // the front animation repeats until the slot is cleared
}

var expressions = map[string]expression{
	"listening": {ring: "listening", ringPrio: display.PrioVoice, loop: true},
	"thinking":  {ring: "thinking", ringPrio: display.PrioVoice, loop: true},
	"speaking":  {ring: "speaking", ringPrio: display.PrioVoice, loop: true},
	"waiting":   {ring: "waiting", ringPrio: display.PrioVoice, loop: true},
	"busy":      {ring: "busy", ringPrio: display.PrioVoice},
	"error":     {ring: "error", ringPrio: display.PrioVoice},
	"mute":      {ring: "mute", ringPrio: display.PrioVoice},
	"boot":      {ring: "boot", ringPrio: display.PrioScenario},
	"ringing":   {loop: true},
	"pause":     {},
	"next":      {},
	"previous":  {},
	"eyes":      {loop: true},
	"blink":     {},
	"happy":     {ring: "success", ringPrio: display.PrioScenario},
	"sad":       {},
	"angry":     {},
	"wink":      {},
	"sleep":     {loop: true},
}

// oneShotRing is how long a one-shot expression's ring part lasts when it has no clip to end with.
const oneShotRing = 2 * time.Second

// ringHolds: voice-pipeline states, whose ring part lasts until the pipeline moves on (the
// callers clear the slot), whatever the front shows.
func ringHolds(name string) bool {
	switch name {
	case "listening", "thinking", "waiting", "speaking", "error", "mute":
		return true
	}
	return false
}

// expressionNames is the HA-facing list (stable order).
var expressionNames = []string{"listening", "thinking", "speaking", "happy", "sad", "angry", "wink", "eyes",
	"blink", "sleep", "ringing", "pause", "next", "previous", "error"}

// frontClips: there is a front display to play animations on.
func (a *App) frontClips() bool { return a.front != nil }

// frontAnim resolves name to the front display's own animation (display.Front.Anim), or
// "@<pack>/<name>" to an installed pack's.
func (a *App) frontAnim(name string) (display.Anim, bool) {
	if a.front == nil {
		return nil, false
	}
	if strings.HasPrefix(name, "@") {
		return a.packAnim(name)
	}
	return a.front.Anim(name)
}

// express shows state name in slot on every surface it has a part for; ttl 0 = until replaced,
// removed or (for a one-shot clip) played out. A name that is no expression but a clip plays
// that clip once. It reports whether anything was shown.
func (a *App) express(slot, name string, ttl time.Duration) bool {
	return a.expressAt(slot, name, ttl, display.PrioExpression)
}

// expressAt is express with the front part at prio. It stops a sequence running in slot.
func (a *App) expressAt(slot, name string, ttl time.Duration, prio int) bool {
	a.cancelSteps(slot)
	return a.expressIn(slot, name, ttl, prio, false)
}

// expressIn shows the expression in slot (a sequence's step); keepRing leaves the slot's ring
// alone when the expression has no ring part.
func (a *App) expressIn(slot, name string, ttl time.Duration, prio int, keepRing bool) bool {
	e := expressions[name] // no expression: an animation name, front only
	shown := false
	var clip display.Anim
	haveClip := false
	if a.frontClips() {
		clip, haveClip = a.frontAnim(name)
	}
	// Replace in place (Push over the same slot), never remove-then-push: a render between the two
	// would show the layer below for a frame. A state without a front part (speaking -> error)
	// removes the previous state's clip.
	if haveClip {
		// A short clip asked for ttl keeps its last frame.
		a.pushFront(slot, prio, ttl, clip.Play(e.loop, !e.loop && ttl > 0, a.brightness))
		shown = true
	} else {
		a.matrixStack.Remove(slot)
	}
	if e.ring == "" && !keepRing { // the slot moves on as a whole: no ring part left from before
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
			a.pushRing(slot, e.ringPrio, rttl, r)
			shown = true
		}
	}
	return shown
}

// unexpress clears slot on every surface (and stops a sequence running there).
func (a *App) unexpress(slot string) {
	a.cancelSteps(slot)
	a.ringStack.Remove(slot)
	a.matrixStack.Remove(slot)
}

// newFront picks the front display's scene catalogue by its kind. Animations are searched in the
// theme stack's packs by their exact name (refreshPackAnims), then by the semantic map in the
// CFW's clips directory, then the firmware's (then, on seg7, among our embedded clips).
func (a *App) newFront() display.Front {
	dirs := []string{a.cfg().Path(a.cfg().ClipsDir)}
	if d := a.prof.Clips.Dir; d != "" {
		dirs = append(dirs, a.cfg().Path(d))
	}
	switch {
	case a.screen != nil:
		w, h := a.screen.Size()
		return display.PixelFront{W: w, H: h, Gamma: a.prof.Matrix.Gamma, TextLines: a.cfg().TextLines,
			Patterns:     &display.PixelLibrary{Dirs: dirs, W: w, H: h, Gamma: a.prof.Matrix.Gamma},
			PackPatterns: &display.PixelLibrary{W: w, H: h, Gamma: a.prof.Matrix.Gamma}}
	case a.matrix != nil:
		return display.Seg7Front{Clips: &display.ClipLibrary{Dirs: dirs}, Packs: &display.ClipLibrary{NoBuiltin: true}}
	}
	return nil
}
