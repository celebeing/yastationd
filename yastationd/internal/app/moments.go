package app

import (
	"bytes"
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

// momentInfo is one moment the app announces, for the web app's editor.
type momentInfo struct {
	Name string `json:"name"`
	When string `json:"when"`
}

// momentCatalog: the moments the app announces, in the order the web app lists them.
var momentCatalog = []momentInfo{
	{"startup", "once per device boot"},
	{"listening", "the wake word or the voice key: the speaker listens"},
	{"thinking", "the request went to the assistant: waiting for the answer"},
	{"speaking", "the answer plays"},
	{"waiting", "a follow-up: the speaker waits for the next phrase"},
	{"dialog_end", "an answer played and no follow-up comes"},
	{"arbitration_lost", "another speaker heard the wake word louder and answers"},
	{"media_pause", "music paused"},
	{"media_resume", "music resumed"},
}

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
// ("scenario:startup") or an animation of the front display ("anim:media/Like", or a bare name)
// — or a sequence of steps of its own. In JSON a reference is a string, a sequence
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
	return a.cfg.Path(a.prof.Platform.CFWRoot + "/" + momentFile)
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
	if k := a.front.Kind(); strings.HasPrefix(k, "pixels") {
		return "pixels"
	} else {
		return k
	}
}

// momentDefault lists what a moment shows without the owner's choice, in order.
func (a *App) momentDefault(name string) []show {
	if c, ok := a.prof.Moments[name]; ok {
		return refs(c)
	}
	if c, ok := momentDefaults[a.frontKind()][name]; ok {
		return refs(c)
	}
	return refs([]string{name})
}

// momentCandidates lists what a moment may show on this speaker, in order.
func (a *App) momentCandidates(name string) []show {
	if c, ok := a.userMomentMap()[name]; ok {
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
	c = strings.TrimPrefix(c, "anim:")
	if e, ok := expressions[c]; ok && e.ring != "" && a.ring != nil {
		return true
	}
	if a.frontClips() {
		_, ok := a.front.Anim(c)
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

// play shows the first available of shows in slot. A bare reference is express(); an "anim:" one,
// a scenario or a sequence runs as steps in slot. A moment named after an expression with a ring
// part (the voice stages) keeps that ring under a show of the owner's that has none.
func (a *App) play(slot, name string, shows []show, ttl time.Duration, prio int) bool {
	for _, s := range shows {
		if !a.available(s) {
			continue
		}
		steps := s.Steps
		switch {
		case steps != nil:
		case strings.HasPrefix(s.Ref, "scenario:"):
			sc, _ := a.loadScenario(strings.TrimPrefix(s.Ref, "scenario:"))
			steps = sc.Steps
		case strings.HasPrefix(s.Ref, "anim:"):
			steps = []scenarioStep{{Anim: strings.TrimPrefix(s.Ref, "anim:")}}
		default:
			return a.expressAt(slot, s.Ref, ttl, prio)
		}
		e := expressions[name]
		if e.ring == "" || stepsHaveRing(steps) {
			e.ring = ""
		}
		a.runSteps(slot, steps, ttl, prio, e.ring, e.ringPrio)
		return true
	}
	return false
}
