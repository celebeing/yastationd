package app

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Moments: what happens in the CFW's life that the speaker reacts to (docs/display-expressions.md,
// "Moments"). Code that reacts to an event calls moment(), never express() with a picture: which
// picture fits is a per-platform choice. A moment resolves through the theme stack's packs, top
// first (the owner's editable packs among them, packs.go), then level 0: the board profile's
// moment.<name> (and <cfw>/board.conf), the defaults of the front display's kind below, the
// default theme, an expression of the same name; a candidate counts only if this speaker has it.

// momentDefaults per front display kind ("seg7"; "pixels" for every pixel screen). Moments not
// listed (the voice stages) show the expression of their name.
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

// show is one candidate of a moment: a reference — an expression ("wink"), a scenario
// ("scenario:startup"), an animation of the front display ("anim:media/Like", or a bare name), a
// sound ("sound:volume_sound"), a sound said as speech ("phrase:no_power"), a ring effect
// ("ring:degraded") — or a sequence of steps of its own. In JSON a reference is a string, a sequence
// {"steps": [...]}.
type show struct {
	Ref   string
	Steps []scenarioStep
}

func (s show) MarshalJSON() ([]byte, error) {
	if s.Steps != nil {
		return json.Marshal(struct {
			Steps []scenarioStep `json:"steps"`
		}{s.Steps})
	}
	return json.Marshal(s.Ref)
}

func (s *show) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		*s = show{}
		return json.Unmarshal(b, &s.Ref)
	}
	var v struct {
		Steps []scenarioStep `json:"steps"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if len(v.Steps) == 0 {
		return fmt.Errorf("a sequence needs steps")
	}
	*s = show{Steps: v.Steps}
	return nil
}

func refs(names []string) []show {
	out := make([]show, len(names))
	for i, n := range names {
		out[i] = show{Ref: n}
	}
	return out
}

// momentFile is <cfw>/moments.json: where the owner's moments lived before they became the
// editable pack "custom" (v0.22). Found at start, it is folded into that pack and renamed .old.
const momentFile = "moments.json"

func (a *App) momentPath() string {
	return a.cfwFile(momentFile)
}

// foldOldMoments moves an old <cfw>/moments.json into the pack "custom" (its entries go in where
// the pack has none for the moment) and renames the file .old. A broken file is only renamed.
func (a *App) foldOldMoments() {
	b, err := os.ReadFile(a.momentPath())
	if err != nil {
		return
	}
	var m map[string][]show
	if err := json.Unmarshal(b, &m); err != nil {
		a.log.Warn("moments: the old moments.json is broken, left aside", "err", err)
	}
	for name, shows := range m {
		if _, ok := a.packMoment(customPack, name); !ok {
			if err := a.setPackMoment(customPack, name, shows); err != nil {
				a.log.Warn("moments: folding the old moments.json", "err", err)
				return
			}
		}
	}
	os.Rename(a.momentPath(), a.momentPath()+".old")
	a.log.Info("moments: the old moments.json is now the pack custom", "moments", len(m))
}

// setMoment stores candidates for name in the pack the Moments page edits (editTarget; nil: the
// pack's entry goes and the layers below show).
func (a *App) setMoment(name string, shows []show) error {
	return a.setPackMoment(a.editTarget(), name, shows)
}

// frontKind is the front display's kind for the moment defaults ("" without one).
func (a *App) frontKind() string {
	if a.front == nil {
		return ""
	}
	if k := a.front.Kind(); !strings.HasPrefix(k, "pixels") {
		return k
	}
	return "pixels"
}

// momentDefault lists what a moment shows without the owner's choice, in order.
func (a *App) momentDefault(name string) []show {
	if c, ok := a.prof.Moments[name]; ok {
		return refs(c)
	}
	if c, ok := momentDefaults[a.frontKind()][name]; ok {
		return refs(c)
	}
	if c, ok := defaultTheme[name]; ok {
		return c
	}
	return refs([]string{name})
}

// momentCandidates lists what a moment may show on this speaker, in order.
func (a *App) momentCandidates(name string) []show {
	if c, _, ok := a.themeMoment(name); ok {
		return c
	}
	return a.momentDefault(name)
}

// refAvailable: the reference exists here — a scenario that loads, an expression with a part on
// one of this speaker's surfaces, or an animation of its front display.
func (a *App) refAvailable(c string) bool {
	if s, ok := strings.CutPrefix(c, "scenario:"); ok {
		_, err := a.loadScenario(s)
		return err == nil
	}
	if s, ok := strings.CutPrefix(c, "sound:"); ok {
		return anyOf(s, a.soundOK)
	}
	if s, ok := strings.CutPrefix(c, "phrase:"); ok {
		return anyOf(s, a.soundOK)
	}
	if s, ok := strings.CutPrefix(c, "ring:"); ok {
		return a.ring != nil && effect(s) != nil
	}
	c = strings.TrimPrefix(c, "anim:")
	if strings.Contains(c, ",") { // a random choice: one of them
		return anyOf(c, a.refAvailable)
	}
	if e, ok := expressions[c]; ok && e.ring != "" && a.ring != nil {
		return true
	}
	if a.frontClips() {
		_, ok := a.frontAnim(c)
		return ok
	}
	return false
}

// available: the candidate can show something here (a sequence: one of its steps can).
func (a *App) available(s show) bool {
	if s.Steps == nil {
		return a.refAvailable(s.Ref)
	}
	for _, st := range s.Steps {
		if a.stepAvailable(st) {
			return true
		}
	}
	return false
}

// moment shows the first available candidate of the moment in slot for ttl and reports whether
// anything was shown.
func (a *App) moment(slot, name string, ttl time.Duration) bool {
	return a.momentAt(slot, name, ttl, display.PrioExpression)
}

// momentAt is moment with the front part at prio.
func (a *App) momentAt(slot, name string, ttl time.Duration, prio int) bool {
	if a.renewMoment(slot, name, ttl) {
		return true // the voice stage again (HA's next event of it): its show goes on
	}
	return a.play(slot, name, a.momentCandidates(name), ttl, prio)
}

// showSteps is what a show runs as steps; false for a bare expression, which express() shows.
func (a *App) showSteps(s show) ([]scenarioStep, bool) {
	switch {
	case s.Steps != nil:
		return s.Steps, true
	case strings.HasPrefix(s.Ref, "scenario:"):
		sc, _ := a.loadScenario(strings.TrimPrefix(s.Ref, "scenario:"))
		return sc.Steps, true
	case strings.HasPrefix(s.Ref, "anim:"):
		return []scenarioStep{{Anim: strings.TrimPrefix(s.Ref, "anim:")}}, true
	case strings.HasPrefix(s.Ref, "sound:"):
		return []scenarioStep{{Sound: strings.TrimPrefix(s.Ref, "sound:")}}, true
	case strings.HasPrefix(s.Ref, "phrase:"):
		return []scenarioStep{{Phrase: strings.TrimPrefix(s.Ref, "phrase:")}}, true
	case strings.HasPrefix(s.Ref, "ring:"):
		return []scenarioStep{{Ring: strings.TrimPrefix(s.Ref, "ring:")}}, true
	}
	return nil, false
}

// play shows the first available of shows in slot. A bare reference is express(); the others
// run as steps in slot. A moment named after an expression with a ring part (the voice stages)
// keeps that ring under a show of the owner's that has none.
func (a *App) play(slot, name string, shows []show, ttl time.Duration, prio int) bool {
	for _, s := range shows {
		if !a.available(s) {
			continue
		}
		steps, ok := a.showSteps(s)
		if !ok {
			return a.expressAt(slot, oneOf(s.Ref, a.refAvailable), ttl, prio)
		}
		e := expressions[name]
		if e.ring == "" || stepsHaveRing(steps) {
			e.ring = ""
		}
		a.runSteps(slot, steps, ttl, prio, display.PrioScenario, e.ring, e.ringPrio)
		a.markMoment(slot, name)
		return true
	}
	return false
}

// hook fires a moment of the catalogue in its slot, at its priorities (defaulttheme.go).
func (a *App) hook(name string) bool {
	shown, _ := a.fire(name)
	return shown
}

// fire is hook that also reports when the show's last sound ends (0: it plays none), for a caller
// whose own audio waits for it (the announce chime before the announcement).
func (a *App) fire(name string) (shown bool, soundEnd time.Duration) {
	m := momentByName[name]
	slot := cmp.Or(m.slot, "moment:"+name)
	prio := cmp.Or(m.prio, display.PrioExpression)
	// a step's ring: under the voice stages in an ordinary moment (as a scenario's), at the
	// moment's own priority in a status one (booting, a call, the alarm)
	ringPrio := cmp.Or(m.ringPrio, m.prio, display.PrioScenario)
	if m.yield != nil {
		a.setYield(slot, func() bool { return m.yield(a) })
	} else {
		a.setYield(slot, nil)
	}
	for _, s := range a.momentCandidates(name) {
		if !a.available(s) {
			continue
		}
		steps, ok := a.showSteps(s)
		if !ok {
			return a.expressAt(slot, s.Ref, m.ttl, prio), 0
		}
		a.runSteps(slot, steps, m.ttl, prio, ringPrio, "", 0)
		return true, a.soundEnd(steps)
	}
	if m.slot != "" {
		a.unexpress(slot) // nothing here: what the shared slot showed ends anyway (a voice stage)
	}
	return false, 0
}

// endHook ends a held moment of the catalogue: its layers, its sequence and its sound.
func (a *App) endHook(name string) {
	a.endShow(cmp.Or(momentByName[name].slot, "moment:"+name))
}
