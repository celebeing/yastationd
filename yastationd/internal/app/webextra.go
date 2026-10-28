package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
)

// The app's extra tabs (docs/web-ui.md): Wi-Fi (the saved networks; add only, from a running
// speaker), moments (each with what it shows here, and a button to show it), and the LED preview
// (the frame tap under /tap/, behind the login).

// features tells the app which tabs this speaker has.
func (a *App) features() map[string]bool {
	return map[string]bool{
		"wifi":        a.setup != nil,
		"led_preview": a.tap != nil && a.cfg().LEDPreview,
		"moments":     true,
		"videos":      a.screen != nil, // the Assets page's videos (assets.go)
	}
}

func (a *App) apiWifi(w http.ResponseWriter, r *http.Request) {
	if a.setup == nil {
		writeErr(w, http.StatusNotFound, "Wi-Fi setup is off on this speaker")
		return
	}
	nets, cur, err := a.setup.SavedNetworks()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if nets == nil {
		nets = []wifisetup.NetView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets, "current": cur, "setup_mode": a.setup.Active()})
}

func (a *App) apiWifiAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || a.setup == nil {
		writeErr(w, http.StatusMethodNotAllowed, "POST, with Wi-Fi setup on")
		return
	}
	var in struct {
		SSID     string `json:"ssid"`
		PSK      string `json:"psk"`
		Hidden   bool   `json:"hidden"`
		Priority int    `json:"priority"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil || in.SSID == "" {
		writeErr(w, http.StatusBadRequest, "a network name")
		return
	}
	err := a.setup.AddNetwork(in.SSID, in.PSK, in.Hidden, in.Priority)
	switch {
	case errors.Is(err, wifisetup.ErrNetworkExists):
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

// momentView is a moment for the web app's editor: its default candidates, the owner's (null =
// the defaults apply), and which candidate shows here (-1: none).
type momentView struct {
	momentInfo
	Default []show `json:"default"`
	Custom  []show `json:"custom"`
	Shows   int    `json:"shows"`
}

// momentCatalogView is what a moment can be made of on this speaker.
type momentCatalogView struct {
	Display     string     `json:"display"` // the front display's kind: seg7, pixels, ""
	Expressions []string   `json:"expressions"`
	Scenarios   []string   `json:"scenarios"`
	Anims       []animInfo `json:"anims"`
	RingEffects []string   `json:"ring_effects"`
	// ColourEffects are the ring shapes a step can colour (color, period).
	ColourEffects []string `json:"colour_effects"`
	Sounds        []string `json:"sounds"` // the sound names this speaker can play (sounds.go)
}

type animInfo struct {
	Name string `json:"name"`
	Ms   int    `json:"ms"`
	Loop bool   `json:"loop,omitempty"`
}

func (a *App) momentViews() []momentView {
	custom := a.userMomentMap()
	var out []momentView
	for _, m := range momentCatalog {
		v := momentView{momentInfo: m, Default: a.momentDefault(m.Name), Custom: custom[m.Name], Shows: -1}
		for i, c := range a.momentCandidates(m.Name) {
			if a.available(c) {
				v.Shows = i
				break
			}
		}
		out = append(out, v)
	}
	return out
}

func (a *App) momentCatalogView() momentCatalogView {
	c := momentCatalogView{Display: a.frontKind(), Scenarios: a.scenarioNames(), Sounds: a.soundNames()}
	for e := range colourEffects {
		c.ColourEffects = append(c.ColourEffects, e)
	}
	sort.Strings(c.ColourEffects)
	for _, e := range slices.Concat(ringEffects, statusEffects, c.ColourEffects) {
		if e != "none" && !slices.Contains(c.RingEffects, e) {
			c.RingEffects = append(c.RingEffects, e)
		}
	}
	for n := range expressions {
		if a.refAvailable(n) {
			c.Expressions = append(c.Expressions, n)
		}
	}
	sort.Strings(c.Expressions)
	if a.front != nil {
		for _, n := range a.front.Anims() {
			if an, ok := a.front.Anim(n); ok {
				c.Anims = append(c.Anims, animInfo{Name: n, Ms: int(an.Duration() / time.Millisecond), Loop: an.Looping()})
			}
		}
	}
	return c
}

// apiMoments: GET the moments and the catalogue; PUT /api/moments/<name> [candidates] sets the
// owner's choice, DELETE /api/moments/<name> goes back to the defaults.
func (a *App) apiMoments(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/moments"), "/")
	if name == "" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"moments": a.momentViews(), "catalog": a.momentCatalogView()})
		return
	}
	if !knownMoment(name) {
		writeErr(w, http.StatusNotFound, "no such moment")
		return
	}
	var shows []show
	switch r.Method {
	case http.MethodPut:
		if err := json.NewDecoder(io.LimitReader(r.Body, listBody)).Decode(&shows); err != nil || shows == nil {
			writeErr(w, http.StatusBadRequest, "want a list of candidates")
			return
		}
	case http.MethodDelete:
	default:
		writeErr(w, http.StatusMethodNotAllowed, "PUT or DELETE")
		return
	}
	if err := a.setMoment(name, shows); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.log.Info("web: moment set", "moment", name, "custom", shows != nil)
	writeJSON(w, http.StatusOK, map[string]any{"moments": a.momentViews()})
}

func knownMoment(name string) bool {
	for _, m := range momentCatalog {
		if m.Name == name {
			return true
		}
	}
	return false
}

// momentPreview is how long a previewed moment may stay (a looping one would never end).
const momentPreview = 6 * time.Second

// apiMoment plays a moment: POST /api/moment/<name>, as it is set, or with a body of candidates,
// a draft that is not saved.
func (a *App) apiMoment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST")
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/moment/")
	if !knownMoment(name) {
		writeErr(w, http.StatusNotFound, "no such moment")
		return
	}
	shows := a.momentCandidates(name)
	var draft []show
	if err := json.NewDecoder(io.LimitReader(r.Body, listBody)).Decode(&draft); err == nil && draft != nil {
		shows = draft
	}
	shown := a.play("ov_moment", name, shows, momentPreview, display.PrioExpression)
	writeJSON(w, http.StatusOK, map[string]any{"shown": shown})
}

// tapHandler is the frame tap behind the login; the token may come as ?token= (an EventSource
// cannot send headers).
func (a *App) tapHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.tap == nil || !a.cfg().LEDPreview {
			writeErr(w, http.StatusNotFound, "LED preview is off (led_preview = true)")
			return
		}
		tok := bearerToken(r)
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if !a.web.tokens.Valid(tok) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		http.StripPrefix("/tap", a.tap.Handler()).ServeHTTP(w, r)
	})
}
