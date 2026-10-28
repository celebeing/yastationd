package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	}
}

func (a *App) apiWifi(w http.ResponseWriter, r *http.Request) {
	if a.setup == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Wi-Fi setup is off on this speaker"})
		return
	}
	nets, cur, err := a.setup.SavedNetworks()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if nets == nil {
		nets = []wifisetup.NetView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets, "current": cur, "setup_mode": a.setup.Active()})
}

func (a *App) apiWifiAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || a.setup == nil {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST, with Wi-Fi setup on"})
		return
	}
	var in struct {
		SSID     string `json:"ssid"`
		PSK      string `json:"psk"`
		Hidden   bool   `json:"hidden"`
		Priority int    `json:"priority"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.SSID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a network name"})
		return
	}
	err := a.setup.AddNetwork(in.SSID, in.PSK, in.Hidden, in.Priority)
	switch {
	case errors.Is(err, wifisetup.ErrNetworkExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
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
	c := momentCatalogView{Display: a.frontKind(), Scenarios: a.scenarioNames(), RingEffects: ringEffects}
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
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"moments": a.momentViews(), "catalog": a.momentCatalogView()})
		return
	}
	if !knownMoment(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such moment"})
		return
	}
	var shows []show
	switch r.Method {
	case http.MethodPut:
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&shows); err != nil || shows == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "want a list of candidates"})
			return
		}
	case http.MethodDelete:
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "PUT or DELETE"})
		return
	}
	if err := a.setMoment(name, shows); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/moment/")
	if !knownMoment(name) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such moment"})
		return
	}
	shows := a.momentCandidates(name)
	var draft []show
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&draft); err == nil && draft != nil {
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
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "LED preview is off (led_preview = true)"})
			return
		}
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if !a.web.tokens.Valid(tok) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login required"})
			return
		}
		http.StripPrefix("/tap", a.tap.Handler()).ServeHTTP(w, r)
	})
}
