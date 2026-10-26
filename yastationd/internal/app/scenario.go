package app

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Scenarios: named compositions in time over the surfaces (docs/display-expressions.md). Steps run
// in sequence; the fields of one step run in parallel, each on its own surface, all under the
// "scenario" slot, so a newer scenario or a stop clears them everywhere at once.

const scenarioSlot = "scenario"

// scenarioStep: an expression and/or explicit surface parts, shown for ms.
type scenarioStep struct {
	Express string `json:"express,omitempty"` // an expression (ring + front)
	Ring    string `json:"ring,omitempty"`    // a ring effect (effects.go)
	Matrix  string `json:"matrix,omitempty"`  // a front-display clip
	Text    string `json:"text,omitempty"`    // front-display text (scrolls when long)
	Ms      int    `json:"ms"`
}

type scenario struct {
	Steps []scenarioStep `json:"steps"`
}

//go:embed scenarios/*.json
var ownScenarios embed.FS

type scenarioRunner struct {
	mu   sync.Mutex
	stop chan struct{}
}

// loadScenario finds name in <cfw>/scenarios, then among ours.
func (a *App) loadScenario(name string) (scenario, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || name[0] == '.' {
		return scenario{}, fmt.Errorf("bad scenario name %q", name)
	}
	b, err := os.ReadFile(filepath.Join(a.cfg.Path(a.cfg.ScenariosDir), name+".json"))
	if err != nil {
		if b, err = ownScenarios.ReadFile("scenarios/" + name + ".json"); err != nil {
			return scenario{}, fmt.Errorf("no scenario %q", name)
		}
	}
	var s scenario
	if err := json.Unmarshal(b, &s); err != nil {
		return scenario{}, fmt.Errorf("scenario %s: %w", name, err)
	}
	return s, nil
}

// scenarioNames lists ours and the CFW's, ours first.
func (a *App) scenarioNames() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		n := strings.TrimSuffix(filepath.Base(p), ".json")
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	m, _ := ownScenarios.ReadDir("scenarios")
	for _, e := range m {
		add(e.Name())
	}
	f, _ := filepath.Glob(filepath.Join(a.cfg.Path(a.cfg.ScenariosDir), "*.json"))
	for _, p := range f {
		add(p)
	}
	return out
}

// runScenario plays s, replacing a scenario still running. Steps longer than maxScenarioStep are
// cut: a scenario is a show, not a mode.
const maxScenarioStep = 5 * time.Minute

func (a *App) runScenario(s scenario) {
	a.scen.mu.Lock()
	if a.scen.stop != nil {
		close(a.scen.stop)
	}
	stop := make(chan struct{})
	a.scen.stop = stop
	a.scen.mu.Unlock()
	go func() {
		defer func() {
			a.scen.mu.Lock()
			if a.scen.stop == stop {
				a.scen.stop = nil
				a.unexpress(scenarioSlot)
			}
			a.scen.mu.Unlock()
		}()
		for _, st := range s.Steps {
			d := min(time.Duration(st.Ms)*time.Millisecond, maxScenarioStep)
			a.showStep(st, d) // replaces the previous step's layers in place: no blank between steps
			select {
			case <-stop:
				return
			case <-time.After(d):
			}
		}
	}()
}

// showStep puts one step on the surfaces, replacing the previous step's layers in place; a
// surface the step does not name is cleared (unless its expression covers it).
func (a *App) showStep(st scenarioStep, d time.Duration) {
	ring, front := false, false
	if st.Express != "" {
		a.express(scenarioSlot, st.Express, d) // replaces or clears both surfaces itself
		ring, front = true, true
	}
	if st.Ring != "" {
		if r := effect(st.Ring); r != nil {
			a.ringStack.Push(scenarioSlot, display.PrioScenario, d, r)
			ring = true
		}
	}
	if st.Matrix != "" && a.frontClips() {
		if c, ok := a.front.Anim(st.Matrix); ok {
			a.matrixStack.Push(scenarioSlot, display.PrioExpression, d, c.Play(c.Looping(), !c.Looping(), a.brightness))
			front = true
		}
	}
	if st.Text != "" && a.front != nil {
		a.matrixStack.Push(scenarioSlot, display.PrioExpression, d, a.front.Marquee(st.Text, a.marqueeStep(), a.brightness))
		front = true
	}
	if !ring {
		a.ringStack.Remove(scenarioSlot)
	}
	if !front {
		a.matrixStack.Remove(scenarioSlot)
	}
}

func (a *App) stopScenario() {
	a.scen.mu.Lock()
	if a.scen.stop != nil {
		close(a.scen.stop)
		a.scen.stop = nil
	}
	a.scen.mu.Unlock()
	a.unexpress(scenarioSlot)
}
