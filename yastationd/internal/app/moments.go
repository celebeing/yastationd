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
// picture fits is a per-platform choice. A moment resolves through the owner's <cfw>/moments.json
// (set from the web app), the board profile's moment.<name> (and <cfw>/board.conf), the defaults
// of the front display's kind below, then an expression of the same name; a candidate counts only
// if this speaker has it.

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

// momentFile is <cfw>/moments.json: the owner's moments, name → candidates (an empty list: the
// moment shows nothing here).
const momentFile = "moments.json"

func (a *App) momentPath() string {
	return a.cfwFile(momentFile)
}

// loadMoments reads the owner's moments (none when the file is missing).
func (a *App) loadMoments() error {
	m := map[string][]show{}
	b, err := os.ReadFile(a.momentPath())
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("%s: %w", momentFile, err)
		}
	}
	a.userMoments.Store(&m)
	return nil
}

// setMoment stores the owner's candidates for name (nil: back to the defaults), live and on flash.
func (a *App) setMoment(name string, shows []show) error {
	cur := a.userMomentMap()
	m := make(map[string][]show, len(cur)+1)
	for k, v := range cur {
		m[k] = v
	}
	if shows == nil {
		delete(m, name)
	} else {
		m[name] = shows
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := a.momentPath()
	if old, err := os.ReadFile(path); err == nil {
		os.WriteFile(path+".bak", old, 0o644)
	}
	if err := os.WriteFile(path+".tmp", append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	a.userMoments.Store(&m)
	a.shareChanged(a.momentKind(), name)
	return nil
}

func (a *App) userMomentMap() map[string][]show {
	if m := a.userMoments.Load(); m != nil {
		return *m
	}
	return nil
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
		return a.soundPath(s) != ""
	}
	if s, ok := strings.CutPrefix(c, "phrase:"); ok {
		return a.soundPath(s) != ""
	}
	if s, ok := strings.CutPrefix(c, "ring:"); ok {
		return a.ring != nil && effect(s) != nil
	}
	c = strings.TrimPrefix(c, "anim:")
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
			return a.expressAt(slot, s.Ref, ttl, prio)
		}
		e := expressions[name]
		if e.ring == "" || stepsHaveRing(steps) {
			e.ring = ""
		}
		a.runSteps(slot, steps, ttl, prio, display.PrioScenario, e.ring, e.ringPrio)
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
