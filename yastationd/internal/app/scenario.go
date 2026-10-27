package app

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Scenarios: named compositions in time over the surfaces (docs/display-expressions.md), played as
// a sequence (sequence.go) under the "scenario" slot, so a newer scenario or a stop clears them
// everywhere at once.

const scenarioSlot = "scenario"

type scenario struct {
	Steps []scenarioStep `json:"steps"`
}

//go:embed scenarios/*.json
var ownScenarios embed.FS

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

// runScenario plays s in the scenario slot, replacing a scenario still running (sequence.go).
func (a *App) runScenario(s scenario) {
	a.runSteps(scenarioSlot, s.Steps, 0, display.PrioExpression, "", 0)
}

func (a *App) stopScenario() {
	a.unexpress(scenarioSlot)
}
