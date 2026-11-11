package app

import (
	"strings"
	"time"
)

// Layers per surface (docs/asset-packs.md, "Layers per surface"): a moment's show is the top
// layer's (the first pack in the theme stack that defines the moment, else the built-in theme),
// but only for the surfaces it names — the front display, the ring, the sound. A surface it
// leaves out comes from the next layer down that names it, down to the built-in theme. So a
// picture-only pack keeps the call's ring tone and ring, and a sound pack keeps the pictures.
// A layer that wants a surface empty says so: "sound": "none", "ring": "none", "anim": "none"
// in a step, or the references "sound:none", "ring:none", "anim:none".

// surfaceNone is the name that empties a surface on purpose.
const surfaceNone = "none"

type surfaces struct{ front, ring, sound bool }

// stepSurfaces: what a step names (an empty "none" counts: it names the surface as empty).
func stepSurfaces(st scenarioStep) surfaces {
	return surfaces{
		front: st.anim() != "" || st.Text != "" || st.Else != "" || (st.Express != "" && st.Express != surfaceNone),
		ring:  st.Ring != "" || st.Outer != "" || expressions[st.Express].ring != "",
		sound: st.Sound != "" || st.Phrase != "",
	}
}

// showSurfaces: what a show names.
func (a *App) showSurfaces(s show) surfaces {
	if steps, ok := a.showSteps(s); ok {
		var u surfaces
		for _, st := range steps {
			v := stepSurfaces(st)
			u.front, u.ring, u.sound = u.front || v.front, u.ring || v.ring, u.sound || v.sound
		}
		return u
	}
	// a bare reference: an expression (front, and its ring part) or a front animation
	e := expressions[strings.Split(s.Ref, ",")[0]]
	return surfaces{front: true, ring: e.ring != ""}
}

// momentLayers: the moment's definitions, top first: each pack of the stack that defines it,
// then the built-in theme.
func (a *App) momentLayers(name string) [][]show {
	var out [][]show
	packs := a.installedPacks()
	kind := a.packDisplay()
	for _, id := range a.themeStack() {
		p := packs[id]
		if p == nil {
			continue
		}
		for _, k := range []string{kind, "any"} {
			if c, ok := p.Moments[k][name]; ok {
				out = append(out, c)
				break
			}
		}
	}
	return append(out, a.momentDefault(name))
}

// firstAvailable is a layer's first show this speaker can play.
func (a *App) firstAvailable(layer []show) (show, bool) {
	for _, s := range layer {
		if a.available(s) {
			return s, true
		}
	}
	return show{}, false
}

// layered is what a moment adds under the top show for the surfaces the top one leaves out: a
// ring effect kept under the whole show, and steps that play only sound (both "" / nil when the
// top names them, or nothing below has them). top is the index of the top show's layer.
func (a *App) layered(layers [][]show, top int, s0 show) (ring string, sound []scenarioStep) {
	need := a.showSurfaces(s0)
	for _, layer := range layers[top+1:] {
		if need.ring && need.sound {
			break
		}
		s, ok := a.firstAvailable(layer)
		if !ok {
			continue
		}
		have := a.showSurfaces(s)
		if !need.ring && have.ring {
			need.ring = true
			ring = a.showRing(s)
		}
		if !need.sound && have.sound {
			need.sound = true
			sound = a.soundOnly(s)
		}
	}
	if ring == surfaceNone {
		ring = ""
	}
	return ring, sound
}

// showRing: the ring effect a show starts with (its first step's, or a bare expression's).
func (a *App) showRing(s show) string {
	steps, ok := a.showSteps(s)
	if !ok {
		return expressions[strings.Split(s.Ref, ",")[0]].ring
	}
	for _, st := range steps {
		if st.Ring != "" {
			return st.Ring
		}
		if r := expressions[st.Express].ring; r != "" {
			return r
		}
	}
	return ""
}

// soundOnly is a show's steps with only their sounds, each lasting as long as before (a step
// that showed something but played nothing becomes a pause).
func (a *App) soundOnly(s show) []scenarioStep {
	steps, _ := a.showSteps(s)
	out := make([]scenarioStep, 0, len(steps))
	silent := true
	for i, st := range steps {
		n := scenarioStep{Sound: st.Sound, Phrase: st.Phrase, Ms: st.Ms, Hold: st.Hold}
		if n.Sound == surfaceNone {
			n.Sound = ""
		}
		if n.Phrase == surfaceNone {
			n.Phrase = ""
		}
		if n.Sound != "" || n.Phrase != "" {
			silent = false
		}
		if n.Ms == 0 && !(st.Hold && i == len(steps)-1) {
			n.Ms = int(a.stepLength(st) / time.Millisecond)
		}
		out = append(out, n)
	}
	if silent {
		return nil
	}
	return out
}

// soundSlot is the slot a moment's layered sound runs in, beside its own slot.
func soundSlot(slot string) string { return slot + "#sound" }

// bareSteps is a bare reference as one step (so a layered ring or sound can run beside it): held
// when the moment is (ttl 0), else lasting the ttl.
func bareSteps(ref string, ttl time.Duration) []scenarioStep {
	if ttl == 0 {
		return []scenarioStep{{Express: ref, Hold: true}}
	}
	return []scenarioStep{{Express: ref, Ms: int(ttl / time.Millisecond)}}
}

// showMoment shows a moment in slot from its layers: the top available show, with what the layers
// below give for the surfaces it leaves out. stepRing is its steps' ring priority, keptPrio the
// priority of a ring kept under the whole show. Reports whether it showed anything and when its
// last sound ends.
func (a *App) showMoment(slot, name string, ttl time.Duration, prio, stepRing, keptPrio int) (bool, time.Duration) {
	// The top layer is the first that defines the moment: when nothing of it plays here (or it is
	// an empty list, the moment turned off), nothing shows — the surfaces below come only under
	// a show of it.
	layers := a.momentLayers(name)
	if s, ok := a.firstAvailable(layers[0]); ok {
		ring, sound := a.layered(layers, 0, s)
		steps, isSteps := a.showSteps(s)
		if !isSteps {
			ref := oneOf(s.Ref, a.refAvailable)
			if ring == "" && sound == nil {
				return a.expressAt(slot, ref, ttl, prio), 0
			}
			steps = bareSteps(ref, ttl)
		}
		a.runSteps(slot, steps, ttl, prio, stepRing, ring, keptPrio)
		if sound != nil {
			a.runSteps(soundSlot(slot), sound, ttl, prio, stepRing, "", 0)
		}
		return true, max(a.soundEnd(steps), a.soundEnd(sound))
	}
	return false, 0
}
