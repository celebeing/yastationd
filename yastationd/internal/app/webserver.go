package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/timesync"
	"github.com/celebeing/yastationd/yastationd/internal/version"
	"github.com/celebeing/yastationd/yastationd/internal/web"
	"github.com/celebeing/yastationd/yastationd/internal/webui"
)

// The speaker's HTTP server (docs/web-ui.md): the web app, its JSON API behind a login,
// Prometheus metrics, and, in Wi-Fi setup mode, the setup page on the access point's address.
// The controls and sensors are the MQTT discovery entities with their mirrored state and their
// MQTT command handlers — described once, served twice.

const (
	tokenTTL      = 30 * 24 * time.Hour
	webSecretFile = "web.secret"
)

type webServer struct {
	tokens   web.Tokens
	secret   []byte // signs the tokens with the login (webSecret)
	throttle web.Throttle

	mu       sync.Mutex
	user     string
	password string // hash; "" = the default password
	peers    map[string]peerInfo
}

// peerInfo is a speaker's web card (its retained web/info message).
type peerInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Board   string `json:"board"`
	Model   string `json:"model"`
	Version string `json:"version"`
	URL     string `json:"url"`
	Seen    int64  `json:"seen,omitempty"` // unix s, when this speaker last heard it
	// Features: the app's optional tabs this speaker has (only in /api/info and /api/state).
	Features map[string]bool `json:"features,omitempty"`
	// DDP: what a DDP sender (LedFx) must be told, since DDP has no discovery (ambilight-stream.md).
	DDP *ddpInfo `json:"ddp,omitempty"`
}

type ddpInfo struct {
	Port   int    `json:"port"`
	Pixels int    `json:"pixels"`
	Order  string `json:"order"` // channel order of the data: RGB (RGBW accepted)
	First  string `json:"first"` // where pixel 0 is
}

func (a *App) ddpInfo() *ddpInfo {
	if !a.ambilightFxOn() {
		return nil
	}
	return &ddpInfo{Port: a.cfg().DDPPort, Pixels: a.ambilight.Pixels(), Order: "RGB", First: "top"}
}

// startWeb listens (an error when it cannot) and serves until ctx ends.
func (a *App) startWeb(ctx context.Context) error {
	a.web.tokens.TTL = tokenTTL
	a.web.user, a.web.password = a.cfg().WebUser, a.cfg().WebPassword
	a.web.secret = a.webSecret()
	a.web.tokens.SetKey(web.TokenKey(a.web.secret, a.web.user, a.web.password))
	ln, err := net.Listen("tcp", a.cfg().HTTPAddr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: a.webHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	go func() {
		a.log.Info("web: listening", "addr", a.cfg().HTTPAddr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.log.Warn("web: server stopped", "err", err)
		}
	}()
	return nil
}

func (a *App) webHandler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("/api/info", a.apiInfo)
	api.HandleFunc("/api/login", a.apiLogin)
	api.HandleFunc("/api/peers", a.authed(a.apiPeers))
	api.HandleFunc("/api/state", a.authed(a.apiState))
	api.HandleFunc("/api/control/", a.authed(a.apiControl))
	api.HandleFunc("/api/password", a.authed(a.apiPassword))
	api.HandleFunc("/api/config", a.authed(a.apiConfig))
	api.HandleFunc("/api/settings", a.authed(a.apiSettings))
	api.HandleFunc("/api/sync", a.authed(a.apiSync))
	api.HandleFunc("/api/wifi", a.authed(a.apiWifi))
	api.HandleFunc("/api/wifi/networks", a.authed(a.apiWifiAdd))
	api.HandleFunc("/api/moments", a.authed(a.apiMoments))
	api.HandleFunc("/api/moments/", a.authed(a.apiMoments))
	api.HandleFunc("/api/moment/", a.authed(a.apiMoment))
	api.HandleFunc("/api/log", a.authed(a.apiLog))
	api.HandleFunc("/api/vitals", a.authed(a.apiVitals))
	api.HandleFunc("/api/calibrate/light", a.authed(a.apiLightCal))

	mux := http.NewServeMux()
	mux.Handle("/api/", cors(api))
	mux.HandleFunc("/metrics", a.serveMetrics)
	mux.Handle("/tap/", a.tapHandler())
	mux.Handle("/", webui.Handler())

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.setup != nil {
			if h, ip := a.setup.Portal(); h != nil && localIP(r).Equal(ip) {
				h.ServeHTTP(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func localIP(r *http.Request) net.IP {
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok {
		return la.IP
	}
	return nil
}

// cors: the app manages every speaker from whichever one served it, so the API answers any
// origin; it takes a bearer token, never a cookie, so there is nothing to forge requests with.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (a *App) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !a.web.tokens.Valid(tok) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login required"})
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (a *App) selfInfo(r *http.Request) peerInfo {
	return peerInfo{ID: a.cfg().DeviceID, Name: a.cfg().DeviceName, Board: a.prof.Name, Model: a.prof.Model,
		Version: version.Version, URL: a.selfURL(r)}
}

// selfURL is how the LAN reaches this server: the request's host when there is one, else the
// address the default route leaves from.
func (a *App) selfURL(r *http.Request) string {
	port := ""
	if _, p, err := net.SplitHostPort(a.cfg().HTTPAddr); err == nil && p != "80" && p != "" {
		port = ":" + p
	}
	if r != nil && r.Host != "" {
		return "http://" + r.Host + "/"
	}
	if ip := outboundIP(a.cfg().Path("/proc/net/route")); ip != "" {
		return "http://" + ip + port + "/"
	}
	return ""
}

func outboundIP(routeFile string) string {
	gw := timesync.DefaultGateway(routeFile)
	if gw == "" {
		return ""
	}
	c, err := net.Dial("udp4", net.JoinHostPort(gw, "9")) // no packet is sent
	if err != nil {
		return ""
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func (a *App) apiInfo(w http.ResponseWriter, r *http.Request) {
	p := a.selfInfo(r)
	p.Features = a.features()
	p.DDP = a.ddpInfo()
	writeJSON(w, http.StatusOK, p)
}

func (a *App) apiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	client, _, _ := net.SplitHostPort(r.RemoteAddr)
	if d := a.web.throttle.Wait(client); d > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts", "retry_s": int(d.Seconds()) + 1})
		return
	}
	var in struct{ User, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	a.web.mu.Lock()
	user, hash := a.web.user, a.web.password
	a.web.mu.Unlock()
	ok := in.User == user && web.CheckPassword(hash, in.Password)
	a.web.throttle.Result(client, ok)
	if !ok {
		a.log.Info("web: login failed", "client", client, "user", in.User)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "wrong user or password"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": a.web.tokens.New(), "default_password": hash == ""})
}

// apiPassword changes the login: it is written into the config file (the daemon keeps running),
// every token is revoked and the caller gets a new one.
func (a *App) apiPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	var in struct{ User, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || len(in.Password) < 4 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a password of 4 characters or more"})
		return
	}
	a.web.mu.Lock()
	user := a.web.user
	a.web.mu.Unlock()
	if in.User != "" {
		user = in.User
	}
	hash := web.HashPassword(in.Password)
	if err := a.editConfig(map[string][]string{"web_user": {user}, "web_password": {hash}}, "set from the web UI"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	a.web.mu.Lock()
	a.web.user, a.web.password = user, hash
	a.web.mu.Unlock()
	a.web.tokens.SetKey(web.TokenKey(a.web.secret, user, hash)) // everyone else is logged out
	a.log.Info("web: login changed", "user", user)
	writeJSON(w, http.StatusOK, map[string]string{"token": a.web.tokens.New()})
}

// webSecret is the speaker's token secret, <cfw>/web.secret: 32 random bytes written on first use,
// so a login survives restarts and redeploys. When it cannot be kept, a secret for this run only
// (a restart logs everyone out, as before).
func (a *App) webSecret() []byte {
	path := a.cfg().Path(a.prof.Platform.CFWRoot + "/" + webSecretFile)
	if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
		return b
	}
	b := make([]byte, 32)
	rand.Read(b)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		a.log.Warn("web: token secret not kept, a restart logs everyone out", "err", err)
	}
	return b
}

// editConfig changes keys in the config file in place (validated, the old file kept as .bak).
func (a *App) editConfig(changes map[string][]string, note string) error {
	a.confMu.Lock()
	defer a.confMu.Unlock()
	if a.cfg().File == "" {
		return errors.New("no config file")
	}
	old, err := os.ReadFile(a.cfg().File)
	if err != nil {
		return err
	}
	data := config.EditNote(old, changes, note)
	if _, err := checkConfigEdit(old, data); err != nil {
		return err
	}
	if err := writeConfigFile(a.cfg().File, old, data); err != nil {
		return err
	}
	a.adoptConfig(data)
	return nil
}

// webEntity is a control or sensor as the app sees it.
type webEntity struct {
	Object    string          `json:"object"`
	Component string          `json:"component"`
	Config    map[string]any  `json:"config"` // the discovery config, minus the topics and device
	Value     *string         `json:"value"`  // the last published state (null = none yet)
	Attrs     json.RawMessage `json:"attributes,omitempty"`
	Writable  bool            `json:"writable"`
	Updated   int64           `json:"updated,omitempty"` // unix s
}

// webEntities: the discovery entities the web shows — all but the lights (the ring and the
// ambilight stay HA's), with their state.
func (a *App) webEntities() []webEntity {
	if a.builder == nil {
		return nil
	}
	cmds := a.commands()
	st := a.states.snapshot()
	var out []webEntity
	for _, e := range a.builder.Entities() {
		if e.Component == "light" {
			continue
		}
		we := webEntity{Object: e.Object, Component: e.Component, Config: map[string]any{}}
		for k, v := range e.Config {
			switch k {
			case "state_topic", "command_topic", "json_attributes_topic", "availability_topic", "device", "unique_id":
				continue
			}
			we.Config[k] = v
		}
		if t, _ := e.Config["state_topic"].(string); t != "" {
			if m, ok := st[a.topics.ObjectOf(t)]; ok {
				v := m.value
				we.Value, we.Updated = &v, m.at.Unix()
			}
		}
		if t, _ := e.Config["json_attributes_topic"].(string); t != "" {
			if m, ok := st[a.topics.ObjectOf(t)]; ok && json.Valid([]byte(m.value)) {
				we.Attrs = json.RawMessage(m.value)
			}
		}
		if t, _ := e.Config["command_topic"].(string); t != "" {
			_, we.Writable = cmds[a.topics.ObjectOf(t)]
		}
		out = append(out, we)
	}
	return out
}

func (a *App) apiState(w http.ResponseWriter, r *http.Request) {
	p := a.selfInfo(r)
	p.Features = a.features()
	p.DDP = a.ddpInfo()
	writeJSON(w, http.StatusOK, map[string]any{"info": p, "entities": a.webEntities()})
}

// apiControl runs a control's MQTT command handler with the request body as the payload.
func (a *App) apiControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST"})
		return
	}
	obj := strings.TrimPrefix(r.URL.Path, "/api/control/")
	allowed := false
	for _, e := range a.webEntities() {
		if e.Object == obj && e.Writable {
			allowed = true
		}
	}
	fn := a.commands()[obj]
	if !allowed || fn == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such control"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
		return
	}
	fn(body)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Discovery: our retained card on <prefix>/<id>/web/info, everyone's on <prefix>/+/web/info.

func (a *App) publishWebInfo() {
	if a.cfg().HTTPAddr == "" {
		return
	}
	b, _ := json.Marshal(a.selfInfo(nil))
	a.publish(a.topics.WebInfo(), string(b))
}

func (a *App) onWebInfo(topic string, payload []byte) {
	var p peerInfo
	if json.Unmarshal(payload, &p) != nil || p.ID == "" {
		return
	}
	p.Seen = time.Now().Unix()
	a.web.mu.Lock()
	if a.web.peers == nil {
		a.web.peers = map[string]peerInfo{}
	}
	a.web.peers[p.ID] = p
	a.web.mu.Unlock()
}

func (a *App) apiPeers(w http.ResponseWriter, r *http.Request) {
	self := a.selfInfo(r)
	a.web.mu.Lock()
	out := []peerInfo{self}
	for id, p := range a.web.peers {
		if id != self.ID {
			out = append(out, p)
		}
	}
	a.web.mu.Unlock()
	sort.Slice(out[1:], func(i, j int) bool { return out[1+i].Name < out[1+j].Name })
	writeJSON(w, http.StatusOK, out)
}

// serveMetrics: Prometheus text — the numeric and on/off states the app publishes, and a few
// process numbers. No login (read-only, the exporter convention).
func (a *App) serveMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	dev := a.cfg().DeviceID
	fmt.Fprintf(w, "# TYPE yastation_info gauge\nyastation_info{device=%q,board=%q,version=%q} 1\n", dev, a.prof.Name, version.Version)
	fmt.Fprintf(w, "# TYPE yastation_uptime_seconds gauge\nyastation_uptime_seconds{device=%q} %d\n", dev, int(time.Since(a.start).Seconds()))
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "# TYPE yastation_go_heap_bytes gauge\nyastation_go_heap_bytes{device=%q} %d\n", dev, ms.HeapAlloc)
	fmt.Fprintf(w, "# TYPE yastation_goroutines gauge\nyastation_goroutines{device=%q} %d\n", dev, runtime.NumGoroutine())
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 1 {
			if pages, err := strconv.Atoi(f[1]); err == nil {
				fmt.Fprintf(w, "# TYPE yastation_rss_bytes gauge\nyastation_rss_bytes{device=%q} %d\n", dev, pages*os.Getpagesize())
			}
		}
	}
	a.systemMetrics(w, dev)
	st := a.states.snapshot()
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := st[k].value
		var f float64
		switch v {
		case "ON":
			f = 1
		case "OFF":
			f = 0
		default:
			var err error
			if f, err = strconv.ParseFloat(v, 64); err != nil {
				if k == "health" { // a state as a label
					fmt.Fprintf(w, "yastation_health{device=%q,state=%q} 1\n", dev, v)
				}
				continue
			}
		}
		name := "yastation_" + metricName(k)
		fmt.Fprintf(w, "# TYPE %s gauge\n%s{device=%q} %s\n", name, name, dev, strconv.FormatFloat(f, 'g', -1, 64))
	}
}

func metricName(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

// The config editor (docs/web-ui.md, "Saving the config"): the file's text in, validated by the
// daemon's own parser, saved with the old one as .bak and a trial marker, then the daemon
// restarts. It removes the marker after configTrial up; a crash before that makes the supervisor
// put the .bak back.

const configTrial = time.Minute

func (a *App) trialMarker() string {
	return a.cfg().Path(a.prof.Platform.CFWRoot + "/config.trial")
}

func (a *App) apiConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		b, err := os.ReadFile(a.cfg().File)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_, terr := os.Stat(a.trialMarker())
		writeJSON(w, http.StatusOK, map[string]any{"file": a.cfg().File, "text": string(b), "trial": terr == nil})
	case http.MethodPut:
		var in struct{ Text string }
		if json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&in) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		restart, changed, err := a.applyConfigText([]byte(in.Text), "config editor")
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

func (a *App) saveConfigTrial(old, data []byte) error {
	// A restart already pending (several shared keys arriving at once): the .bak and the marker
	// keep the config and the health from before the first change, so a rollback goes all the way.
	if _, err := os.Stat(a.trialMarker()); err == nil && a.restartPending.Load() {
		tmp := a.cfg().File + ".new"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, a.cfg().File)
	}
	if err := os.WriteFile(a.cfg().File+".bak", old, 0o600); err != nil {
		return err
	}
	tmp := a.cfg().File + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// The marker holds the health before the save: the new config must do at least as well.
	if err := os.WriteFile(a.trialMarker(), []byte(strconv.Itoa(int(a.hmon.State()))), 0o644); err != nil {
		return err
	}
	a.restartPending.Store(true)
	return os.Rename(tmp, a.cfg().File)
}

// configTrialLoop judges a config on trial after configTrial: accepted when the speaker is at least
// as healthy (broker, HA's pipeline) as before the save, else rolled back.
func (a *App) configTrialLoop(stop <-chan struct{}) {
	b, err := os.ReadFile(a.trialMarker())
	if err != nil {
		return
	}
	before, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		before = int(health.OfflineNoLAN) // no record: anything goes
	}
	a.log.Info("config: on trial", "for", configTrial, "health_before", health.State(before).String())
	select {
	case <-stop:
		return
	case <-time.After(configTrial):
	}
	if now := a.hmon.State(); int(now) > before {
		a.trialFailed("health " + now.String() + ", was " + health.State(before).String())
		return
	}
	os.Remove(a.trialMarker())
	a.syncAccepted()
	a.log.Info("config: accepted")
}

// trialFailed rolls a config on trial back (no-op when none is): the .bak goes back, the marker
// goes, the daemon restarts.
func (a *App) trialFailed(why string) {
	if _, err := os.Stat(a.trialMarker()); err != nil {
		return
	}
	a.log.Error("config: the config on trial fails, rolling back", "why", why)
	if old, err := os.ReadFile(a.cfg().File + ".bak"); err == nil {
		tmp := a.cfg().File + ".restore"
		if os.WriteFile(tmp, old, 0o600) == nil {
			os.Rename(tmp, a.cfg().File)
		}
	}
	os.Remove(a.trialMarker())
	if a.quit != nil {
		a.quit()
	}
}

// systemMetrics: the speaker's memory, load, temperatures (every thermal zone, by its type) and
// uptime.
func (a *App) systemMetrics(w io.Writer, dev string) {
	v := a.readVitals()
	if v.MemTotal > 0 {
		fmt.Fprintf(w, "# TYPE yastation_memory_bytes gauge\n")
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"total\"} %d\n", dev, v.MemTotal)
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"free\"} %d\n", dev, v.MemFree)
		fmt.Fprintf(w, "yastation_memory_bytes{device=%q,kind=\"available\"} %d\n", dev, v.MemAvailable)
	}
	if len(v.Load) == 3 {
		fmt.Fprintf(w, "# TYPE yastation_load gauge\n")
		for i, span := range []string{"1m", "5m", "15m"} {
			fmt.Fprintf(w, "yastation_load{device=%q,span=%q} %s\n", dev, span, strconv.FormatFloat(v.Load[i], 'f', 2, 64))
		}
	}
	if len(v.Temps) > 0 {
		fmt.Fprintf(w, "# TYPE yastation_temperature_celsius gauge\n")
		for _, t := range v.Temps {
			fmt.Fprintf(w, "yastation_temperature_celsius{device=%q,zone=%q} %s\n", dev, t.Zone, strconv.FormatFloat(t.C, 'f', 1, 64))
		}
	}
	if v.SystemUptime > 0 {
		fmt.Fprintf(w, "# TYPE yastation_system_uptime_seconds gauge\nyastation_system_uptime_seconds{device=%q} %d\n", dev, v.SystemUptime)
	}
}
