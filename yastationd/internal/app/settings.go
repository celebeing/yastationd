package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
)

// Settings (docs/settings.md): the config file stays the one place for settings. Every change —
// the settings page, the config editor, the sync between speakers — rewrites it and is applied
// at once when every changed key is a runtime one (config.Keys), else by a restart on trial.

// applyConfigText makes data the config: written to the file (the old one kept as .bak), then
// applied live, or by a trial restart when a changed key needs one (restart reports that; the
// caller's reply goes out before the daemon quits). Changes in comments only are just written.
func (a *App) applyConfigText(data []byte, why string) (restart bool, changed []string, err error) {
	a.confMu.Lock()
	defer a.confMu.Unlock()
	cur := a.cfg()
	if cur.File == "" {
		return false, nil, errors.New("no config file")
	}
	old, err := os.ReadFile(cur.File)
	if err != nil {
		return false, nil, err
	}
	next, err := checkConfigEdit(old, data)
	if err != nil {
		return false, nil, err
	}
	next.File = cur.File
	if string(old) == string(data) {
		return false, nil, nil
	}
	changed = config.Changed(cur, &next)
	if config.NeedsRestart(changed) {
		if err := a.saveConfigTrial(old, data); err != nil {
			return false, nil, err
		}
		a.log.Info("config: saved, restarting on trial", "why", why, "keys", changed)
		a.shareSettings(&next, changed)
		go func() {
			time.Sleep(500 * time.Millisecond) // the reply goes out first
			if a.quit != nil {
				a.quit() // exits 0; the supervisor starts us again with the new config
			}
		}()
		return true, changed, nil
	}
	if err := writeConfigFile(cur.File, old, data); err != nil {
		return false, nil, err
	}
	a.conf.Store(&next)
	if len(changed) > 0 {
		a.log.Info("config: applied live", "why", why, "keys", changed)
	}
	a.shareSettings(&next, changed)
	return false, changed, nil
}

// checkConfigEdit parses the new text of the config file. A bad line is refused unless it was
// already in the old text (the daemon started with it skipped): an old line a newer build refuses
// must not block every other change.
func checkConfigEdit(old, data []byte) (config.Config, error) {
	next, errs := config.ParseLenient(strings.NewReader(string(data)))
	_, oldErrs := config.ParseLenient(strings.NewReader(string(old)))
	had := map[string]bool{}
	for _, e := range oldErrs {
		had[e.Text] = true
	}
	for _, e := range errs {
		if !had[e.Text] {
			return next, e
		}
	}
	return next, nil
}

// writeConfigFile replaces the config file with data, the old text kept as .bak.
func writeConfigFile(path string, old, data []byte) error {
	if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// adoptConfig records that the file now holds data, for writers that apply their own key live
// (the login, the light calibration): the next diff must not see those keys as changes.
func (a *App) adoptConfig(data []byte) {
	next, _ := config.ParseLenient(strings.NewReader(string(data)))
	next.File = a.cfg().File
	a.conf.Store(&next)
}

// settingView is one config key as the settings page shows it.
type settingView struct {
	Name     string   `json:"name"`
	Group    string   `json:"group"`
	Label    string   `json:"label"`
	Doc      string   `json:"doc"`
	Kind     string   `json:"kind"`
	Min      float64  `json:"min,omitempty"`
	Max      float64  `json:"max,omitempty"`
	Unit     string   `json:"unit,omitempty"`
	Options  []string `json:"options,omitempty"`
	Runtime  bool     `json:"runtime"`
	Sync     bool     `json:"sync"`
	Advanced bool     `json:"advanced,omitempty"`
	Value    []string `json:"value"`           // secrets: [secretMask] when set
	Default  []string `json:"default"`         //
	Board    []string `json:"board,omitempty"` // the board profile's value, where it stands in for the default
	Set      bool     `json:"set"`             // the file sets it (else the default rules)
	Changed  bool     `json:"changed"`         // set to something other than the default (or the board's)
}

// boardValues: what this board's profile has for the keys where it, not the config's default,
// is the value in force when the key is unset — the hardware overrides, the mic calibration, the
// brightness curve (its first level is the board's dimmest).
func (a *App) boardValues() map[string][]string {
	c := a.cfg()
	p, err := board.Load(c.SysRoot, a.prof.Name, c.BoardFile)
	if err != nil {
		return nil
	}
	one := func(s string) []string { return []string{s} }
	num := func(n int) []string { return one(strconv.Itoa(n)) }
	def := config.Default()
	return map[string][]string{
		"aplay_device":          one(p.Audio.Playback),
		"amixer_card":           one(p.Audio.Card),
		"unmute":                append([]string{}, p.Audio.Unmute...),
		"mic_pdm_gain":          num(p.Audio.MicGainValue),
		"mic_device":            one(p.Mic.Device),
		"mic_channels":          num(p.Mic.Channels),
		"mic_rate":              num(p.Mic.Rate),
		"mic_channel":           num(p.Mic.Voice),
		"wake_model":            one(p.Wake.Model),
		"command_model":         one(p.Wake.CommandModel),
		"spotter_libs":          one(p.Wake.Libs),
		"mic_spl_offset":        one(strconv.FormatFloat(p.Mic.SPLOffset, 'f', -1, 64)),
		"auto_brightness_curve": one(config.CurveString(brightnessCurve(def, p))),
		"auto_brightness_min":   num(max(p.Matrix.MinBrightness, 1)),
	}
}

// boardHas: this board has the hardware a key needs ("" = any board).
func (a *App) boardHas(needs string) bool {
	switch needs {
	case "ambilight":
		return a.prof.Ambilight.Kind != "" && a.prof.Ambilight.Kind != "none"
	case "pixels":
		return a.prof.Matrix.Kind == "dotmatrix-fb"
	}
	return true
}

// zeroIsBoard: keys whose 0 means "the board's value".
var zeroIsBoard = map[string]bool{"mic_spl_offset": true, "auto_brightness_min": true}

func sameLines(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

// secretMask stands for a secret's value both ways: shown for a set secret, and sent back
// unchanged it leaves the secret as it is.
const secretMask = "••••••••"

func (a *App) settingViews() []settingView {
	c := a.cfg()
	boardVals := a.boardValues()
	out := []settingView{}
	for _, k := range config.Keys {
		if k.Hidden || !a.boardHas(k.Needs) {
			continue
		}
		v := settingView{Name: k.Name, Group: k.Group, Label: k.Label, Doc: k.Doc, Kind: k.Kind,
			Min: k.Min, Max: k.Max, Unit: k.Unit, Options: k.Options, Runtime: k.Runtime, Sync: k.Sync,
			Advanced: k.Advanced, Value: k.Get(c), Default: k.Default(), Set: c.Has(k.Name)}
		if v.Value == nil {
			v.Value = []string{}
		}
		if v.Default == nil {
			v.Default = []string{}
		}
		v.Board = boardVals[k.Name]
		ref := v.Default
		if v.Board != nil {
			ref = v.Board
		}
		v.Changed = v.Set && !sameLines(v.Value, ref) && !(zeroIsBoard[k.Name] && sameLines(v.Value, []string{"0"}))
		if k.Kind == "secret" {
			v.Default = []string{}
			if len(v.Value) == 1 && v.Value[0] != "" {
				v.Value = []string{secretMask}
			} else {
				v.Value = []string{}
			}
		}
		out = append(out, v)
	}
	return out
}

// apiSettings: GET the keys with their values and descriptions; PUT {"changes": {key: [lines]}}
// sets keys (an empty list removes the key, back to its default) and replies what happened.
func (a *App) apiSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_, terr := os.Stat(a.trialMarker())
		skipped := a.cfg().Skipped
		if skipped == nil {
			skipped = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"groups": config.Groups, "keys": a.settingViews(), "trial": terr == nil, "skipped": skipped})
	case http.MethodPut:
		var in struct {
			Changes map[string][]string `json:"changes"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in) != nil || len(in.Changes) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "want {\"changes\": {key: [values]}}"})
			return
		}
		changes := map[string][]string{}
		for name, vals := range in.Changes {
			k := config.Lookup(name)
			if k == nil || k.Hidden {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": name + ": not a setting"})
				return
			}
			if k.Kind == "secret" && len(vals) == 1 && vals[0] == secretMask {
				continue // unchanged
			}
			if !k.List() && len(vals) > 1 {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": name + ": one value"})
				return
			}
			changes[name] = vals
		}
		if len(changes) == 0 {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": false, "changed": []string{}})
			return
		}
		old, err := os.ReadFile(a.cfg().File)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		restart, changed, err := a.applyConfigText(config.EditNote(old, changes, "set from the web app"), "settings")
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		if changed == nil {
			changed = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "restarting": restart, "changed": changed})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or PUT"})
	}
}
