package app

import (
	"strings"
	"time"
)

// Moments: what happens in the CFW's life that the speaker reacts to (docs/display-expressions.md,
// "Moments"). Code that reacts to an event calls moment(), never express() with a picture: which
// picture fits is a per-platform choice. A moment resolves through the board profile's
// moment.<name> (and <cfw>/board.conf), then the defaults of the front display's kind below, then
// an expression of the same name; a candidate counts only if this speaker has it.

// momentDefaults per front display kind ("seg7"; "pixels" for every pixel screen).
var momentDefaults = map[string]map[string][]string{
	"seg7": {
		"startup":          {"scenario:startup"},
		"dialog_end":       {"wink"},
		"arbitration_lost": {"glance", "sad"},
		"media_pause":      {"pause"},
		"media_resume":     {"resume"},
	},
	"pixels": {
		"startup":          {"scenario:startup-pixel", "scenario:startup"},
		"dialog_end":       {"blink", "wink"},
		"arbitration_lost": {"scenario_emotion/Deactivation_Output", "sad"},
		"media_pause":      {"pause"},
		"media_resume":     {"resume"},
	},
}

// momentCandidates lists what a moment may show on this speaker, in order.
func (a *App) momentCandidates(name string) []string {
	if c, ok := a.prof.Moments[name]; ok {
		return c
	}
	if a.front != nil {
		kind := a.front.Kind()
		if strings.HasPrefix(kind, "pixels") {
			kind = "pixels"
		}
		if c, ok := momentDefaults[kind][name]; ok {
			return c
		}
	}
	return []string{name}
}

// momentAvailable: the candidate exists here — a scenario that loads, an expression with a part on
// one of this speaker's surfaces, or an animation of its front display.
func (a *App) momentAvailable(c string) bool {
	if s, ok := strings.CutPrefix(c, "scenario:"); ok {
		_, err := a.loadScenario(s)
		return err == nil
	}
	if e, ok := expressions[c]; ok && e.ring != "" && a.ring != nil {
		return true
	}
	if a.frontClips() {
		_, ok := a.front.Anim(c)
		return ok
	}
	return false
}

// moment shows the first available candidate of the moment in slot for ttl (a scenario runs in its
// own slot and timing) and reports whether anything was shown.
func (a *App) moment(slot, name string, ttl time.Duration) bool {
	for _, c := range a.momentCandidates(name) {
		if !a.momentAvailable(c) {
			continue
		}
		if s, ok := strings.CutPrefix(c, "scenario:"); ok {
			sc, _ := a.loadScenario(s)
			a.runScenario(sc)
			return true
		}
		return a.express(slot, c, ttl)
	}
	return false
}
