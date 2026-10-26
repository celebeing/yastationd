package app

import (
	"encoding/json"
	"strings"
	"time"
)

// HA side of the expressions (docs/display-expressions.md): the Expression select, the
// expression and scenario command topics, the Display faces switch and the capabilities.

const haSlot = "ha"

// expressionOptions: the select's options — off plus every expression this speaker can show.
func (a *App) expressionOptions() []string {
	opts := []string{animOff}
	for _, n := range expressionNames {
		_, front := a.frontAnim(n)
		if expressions[n].ring != "" || front {
			opts = append(opts, n)
		}
	}
	return opts
}

// cmdExpressionSelect plays the chosen expression for AnimSecs, then reports "off" again.
func (a *App) cmdExpressionSelect(payload []byte) {
	name := strings.TrimSpace(string(payload))
	topic := a.topics.State("expression")
	if name == animOff {
		a.unexpress(haSlot)
		a.publish(topic, animOff)
		return
	}
	d := time.Duration(a.store.get().AnimSecs) * time.Second
	if !a.express(haSlot, name, d) {
		a.log.Info("expression not available here", "name", name)
		a.publish(topic, animOff)
		return
	}
	a.publish(topic, name)
	a.animGen.Add(1)
	gen := a.animGen.Load()
	time.AfterFunc(d, func() {
		if a.animGen.Load() == gen {
			a.publish(topic, animOff)
		}
	})
}

// express/set: a bare name, or {"name": …, "ttl_ms": …}; "off" (or an empty name) clears.
func (a *App) cmdExpress(payload []byte) {
	var c struct {
		Name  string `json:"name"`
		TTLms int    `json:"ttl_ms"`
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		c.Name = strings.TrimSpace(string(payload))
	}
	if c.Name == "" || c.Name == animOff {
		a.unexpress(haSlot)
		return
	}
	ttl := time.Duration(c.TTLms) * time.Millisecond
	if ttl <= 0 {
		ttl = time.Duration(a.store.get().AnimSecs) * time.Second
	}
	if !a.express(haSlot, c.Name, ttl) {
		a.log.Info("expression not available here", "name", c.Name)
	}
}

// scenario/set: a bare name, {"name": …}, {"steps": [...]} (inline) or {"stop": true}.
func (a *App) cmdScenario(payload []byte) {
	var c struct {
		Name string `json:"name"`
		Stop bool   `json:"stop"`
		scenario
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		c.Name = strings.TrimSpace(string(payload))
	}
	switch {
	case c.Stop || c.Name == animOff:
		a.stopScenario()
	case len(c.Steps) > 0:
		a.runScenario(c.scenario)
	case c.Name != "":
		s, err := a.loadScenario(c.Name)
		if err != nil {
			a.log.Warn("scenario", "err", err)
			return
		}
		a.runScenario(s)
	}
}

func (a *App) cmdFaces(payload []byte) {
	on := strings.EqualFold(strings.TrimSpace(string(payload)), "ON")
	a.store.update(func(s *persistState) { s.Faces = on })
	if !on {
		for _, slot := range []string{"voice", haSlot, scenarioSlot, "boot"} {
			a.matrixStack.Remove(slot)
		}
	}
	a.publish(a.topics.State("display_faces"), onOff(on))
}

// displayCaps tells HA scripts and an LLM agent what this speaker can show.
func (a *App) displayCaps() string {
	front := "none"
	if a.front != nil {
		front = a.front.Kind()
	}
	m := map[string]any{
		"front":       front,
		"ring_pixels": a.ring.Pixels(),
		"ambilight":   a.ambilight != nil,
		"expressions": a.expressionOptions()[1:],
		"scenarios":   a.scenarioNames(),
	}
	if a.frontClipsPossible() {
		m["clips"] = a.front.Anims()
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// frontClipsPossible: the board has a front display to play animations on (whatever the faces
// switch says).
func (a *App) frontClipsPossible() bool { return a.front != nil }
