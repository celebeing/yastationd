package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

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
	api.HandleFunc("/api/diag", a.authed(a.apiDiag))
	api.HandleFunc("/api/assets", a.authed(a.apiAssets))
	api.HandleFunc("/api/assets/", a.authed(a.apiAssets))
	api.HandleFunc("/api/calibrate/light", a.authed(a.apiLightCal))
	api.HandleFunc("/api/packs", a.authed(a.apiPacks))
	api.HandleFunc("/api/packs/", a.authed(a.apiPacks))
	api.HandleFunc("/api/voices", a.authed(a.apiVoices))
	api.HandleFunc("/api/voices/", a.authed(a.apiVoices))

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
		w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition") // the diagnostics archive's name
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// authed lets a request through to h only with a valid login token.
func (a *App) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.web.tokens.Valid(bearerToken(r)) {
			writeErr(w, http.StatusUnauthorized, "login required")
			return
		}
		h(w, r)
	}
}

// bearerToken is the request's Authorization header without its "Bearer " prefix.
func bearerToken(r *http.Request) string {
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return tok
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr is the API's error reply: {"error": msg} with the status code.
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// API request body limits.
const (
	smallBody = 4 << 10   // a login, a Wi-Fi network, a sync toggle
	listBody  = 64 << 10  // a control's payload, a list of moment candidates, settings changes
	textBody  = 256 << 10 // the config file's text
)

func (a *App) selfInfo(r *http.Request) peerInfo {
	return peerInfo{ID: a.cfg().DeviceID, Name: a.cfg().DeviceName, Board: a.prof.Name, Model: a.prof.Model,
		Version: version.Version, URL: a.selfURL(r)}
}

// fullInfo is selfInfo with what only the app asks for: the tabs and the DDP details.
func (a *App) fullInfo(r *http.Request) peerInfo {
	p := a.selfInfo(r)
	p.Features = a.features()
	p.DDP = a.ddpInfo()
	return p
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
	writeJSON(w, http.StatusOK, a.fullInfo(r))
}

func (a *App) apiLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST")
		return
	}
	client, _, _ := net.SplitHostPort(r.RemoteAddr)
	if d := a.web.throttle.Wait(client); d > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts", "retry_s": int(d.Seconds()) + 1})
		return
	}
	var in struct{ User, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	a.web.mu.Lock()
	user, hash := a.web.user, a.web.password
	a.web.mu.Unlock()
	ok := in.User == user && web.CheckPassword(hash, in.Password)
	a.web.throttle.Result(client, ok)
	if !ok {
		a.log.Info("web: login failed", "client", client, "user", in.User)
		writeErr(w, http.StatusUnauthorized, "wrong user or password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": a.web.tokens.New(), "default_password": hash == ""})
}

// apiPassword changes the login: it is written into the config file (the daemon keeps running),
// every token is revoked and the caller gets a new one.
func (a *App) apiPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST")
		return
	}
	var in struct{ User, Password string }
	if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil || len(in.Password) < 4 {
		writeErr(w, http.StatusBadRequest, "a password of 4 characters or more")
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	path := a.cfwFile(webSecretFile)
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
	writeJSON(w, http.StatusOK, map[string]any{"info": a.fullInfo(r), "entities": a.webEntities()})
}

// apiControl runs a control's MQTT command handler with the request body as the payload.
func (a *App) apiControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST")
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
		writeErr(w, http.StatusNotFound, "no such control")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, listBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
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
