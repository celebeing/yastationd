package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
)

// The app's extra tabs (docs/web-ui.md): Wi-Fi (the saved networks; add only, from a running
// speaker), moments (each with what it shows here, and a button to show it), and the LED preview
// (the frame tap under /tap/, behind the login).

// features tells the app which tabs this speaker has.
func (a *App) features() map[string]bool {
	return map[string]bool{
		"wifi":        a.setup != nil,
		"led_preview": a.tap != nil && a.cfg.LEDPreview,
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

// momentView is a moment and what it shows on this speaker ("" = nothing here).
type momentView struct {
	Name       string   `json:"name"`
	Candidates []string `json:"candidates"`
	Shows      string   `json:"shows"`
}

// momentNames: every moment with a mapping here — the defaults of this display's kind, the
// board's own, and the expressions (rule 3).
func (a *App) momentNames() []string {
	set := map[string]bool{}
	if a.front != nil {
		kind := a.front.Kind()
		if strings.HasPrefix(kind, "pixels") {
			kind = "pixels"
		}
		for n := range momentDefaults[kind] {
			set[n] = true
		}
	}
	for n := range a.prof.Moments {
		set[n] = true
	}
	for _, n := range expressionNames {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (a *App) apiMoments(w http.ResponseWriter, r *http.Request) {
	var out []momentView
	for _, n := range a.momentNames() {
		v := momentView{Name: n, Candidates: a.momentCandidates(n)}
		for _, c := range v.Candidates {
			if a.momentAvailable(c) {
				v.Shows = c
				break
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// momentPreview is how long a previewed moment may stay (a looping one would never end).
const momentPreview = 6 * time.Second

func (a *App) apiMoment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/api/moment/")
	known := false
	for _, n := range a.momentNames() {
		known = known || n == name
	}
	if !known {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such moment"})
		return
	}
	shown := a.moment("ov_moment", name, momentPreview)
	writeJSON(w, http.StatusOK, map[string]any{"shown": shown})
}

// tapHandler is the frame tap behind the login; the token may come as ?token= (an EventSource
// cannot send headers).
func (a *App) tapHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.tap == nil || !a.cfg.LEDPreview {
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
