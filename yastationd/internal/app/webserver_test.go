package app

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
	"github.com/celebeing/yastationd/yastationd/internal/web"
)

func newWebApp(t *testing.T) (*App, *httptest.Server) {
	cfg, _ := config.Parse(strings.NewReader("device_id = t1\n")) // as the file below
	cfg.File = filepath.Join(t.TempDir(), "yastationd.conf")
	os.WriteFile(cfg.File, []byte("device_id = t1\n"), 0o600)
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		topics: mqtt.NewTopics("yastation", "homeassistant", "t1"),
		store:  newStateStore("", defaultState(128, 128, 128, false))}
	a.conf.Store(&cfg)
	a.builder = mqtt.NewBuilder(a.topics, mqtt.Device{Name: "t"})
	a.hmon = health.New("", nil)
	a.web.tokens.TTL = tokenTTL
	a.web.user = cfg.WebUser
	a.publishBrightness()
	a.publish(a.topics.State("lux"), "12.5")
	srv := httptest.NewServer(a.webHandler())
	t.Cleanup(srv.Close)
	return a, srv
}

func call(t *testing.T, method, url, tok, body string) (int, map[string]any) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{"_raw": string(b)}
	}
	return resp.StatusCode, out
}

func TestWebLoginStateControl(t *testing.T) {
	a, srv := newWebApp(t)
	if code, _ := call(t, "GET", srv.URL+"/api/state", "", ""); code != 401 {
		t.Fatalf("state without login: %d", code)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"nope"}`); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	a.web.throttle = web.Throttle{} // the failure above made the next attempt wait
	code, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok, _ := out["token"].(string)
	if code != 200 || tok == "" || out["default_password"] != true {
		t.Fatalf("login: %d %v", code, out)
	}
	code, out = call(t, "GET", srv.URL+"/api/state", tok, "")
	ents, _ := out["entities"].([]any)
	if code != 200 || len(ents) == 0 {
		t.Fatalf("state: %d %v", code, out)
	}
	found := map[string]map[string]any{}
	for _, e := range ents {
		m := e.(map[string]any)
		found[m["object"].(string)] = m
	}
	if _, ok := found["ring"]; ok {
		t.Fatal("lights stay HA's")
	}
	if v := found["lux"]["value"]; v != "12.5" {
		t.Fatalf("lux mirrored: %v", v)
	}
	if found["clock_brightness"]["writable"] != true || found["lux"]["writable"] == true {
		t.Fatal("writability from the command topics")
	}
	if code, _ := call(t, "POST", srv.URL+"/api/control/clock_brightness", tok, "42"); code != 200 {
		t.Fatalf("control: %d", code)
	}
	if s := a.store.get(); s.ClockBright != 42 || s.ClockAuto {
		t.Fatalf("the MQTT handler ran: %+v", s)
	}
	if v := a.states.snapshot()["clock_brightness"].value; v != "42" {
		t.Fatalf("its state mirrored: %q", v)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/control/ring", tok, `{"state":"OFF"}`); code != 404 {
		t.Fatal("the ring is not a web control")
	}
	// Change the password: written to the config, old tokens die.
	code, out = call(t, "POST", srv.URL+"/api/password", tok, `{"password":"hunter22"}`)
	newTok, _ := out["token"].(string)
	if code != 200 || newTok == "" {
		t.Fatalf("password: %d %v", code, out)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/state", tok, ""); code != 401 {
		t.Fatal("old token revoked")
	}
	b, _ := os.ReadFile(a.cfg().File)
	if !strings.Contains(string(b), "web_password = pbkdf2-sha256$") {
		t.Fatalf("config: %s", b)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"hunter22"}`); code != 200 {
		t.Fatal("new password works")
	}
}

func TestWebMetricsAndInfo(t *testing.T) {
	a, srv := newWebApp(t)
	a.cfg().SysRoot = t.TempDir()
	os.MkdirAll(filepath.Join(a.cfg().SysRoot, "proc"), 0o755)
	os.WriteFile(filepath.Join(a.cfg().SysRoot, "proc/meminfo"), []byte("MemTotal:  243908 kB\nMemFree:  97604 kB\nMemAvailable:  157196 kB\n"), 0o644)
	os.WriteFile(filepath.Join(a.cfg().SysRoot, "proc/loadavg"), []byte("1.27 1.23 1.24 1/143 5748\n"), 0o644)
	tz := filepath.Join(a.cfg().SysRoot, "sys/class/thermal/thermal_zone0")
	os.MkdirAll(tz, 0o755)
	os.WriteFile(filepath.Join(tz, "type"), []byte("soc_thermal\n"), 0o644)
	os.WriteFile(filepath.Join(tz, "temp"), []byte("48000\n"), 0o644)
	os.WriteFile(filepath.Join(a.cfg().SysRoot, "proc/uptime"), []byte("93784.21 180000.00\n"), 0o644)

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`yastation_info{device="t1"`, `yastation_lux{device="t1"} 12.5`, `yastation_auto_brightness{device="t1"} 0`, "yastation_rss_bytes",
		`yastation_memory_bytes{device="t1",kind="available"} 160968704`, `yastation_load{device="t1",span="1m"} 1.27`,
		`yastation_temperature_celsius{device="t1",zone="soc_thermal"} 48.0`, `yastation_system_uptime_seconds{device="t1"} 93784`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics lack %q:\n%s", want, b)
		}
	}
	code, out := call(t, "GET", srv.URL+"/api/info", "", "")
	if code != 200 || out["id"] != "t1" {
		t.Fatalf("info: %d %v", code, out)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/vitals", "", ""); code != 401 {
		t.Fatalf("vitals without a login: %d", code)
	}
	_, out = call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	code, out = call(t, "GET", srv.URL+"/api/vitals", out["token"].(string), "")
	if code != 200 || out["system_uptime_s"] != 93784.0 || out["mem_available"] != 160968704.0 ||
		len(out["load"].([]any)) != 3 || out["temps"].([]any)[0].(map[string]any)["c"] != 48.0 {
		t.Fatalf("vitals: %d %v", code, out)
	}
}

func TestWebConfigTrial(t *testing.T) {
	a, srv := newWebApp(t)
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	quit := make(chan struct{})
	a.quit = func() { close(quit) }
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)

	code, out := call(t, "GET", srv.URL+"/api/config", tok, "")
	if code != 200 || out["text"] != "device_id = t1\n" {
		t.Fatalf("get: %d %v", code, out)
	}
	if code, out := call(t, "PUT", srv.URL+"/api/config", tok, `{"text":"volume_max = 300\n"}`); code != 422 || !strings.Contains(out["error"].(string), "line 1") {
		t.Fatalf("a bad config is refused with its line: %d %v", code, out)
	}
	if b, _ := os.ReadFile(a.cfg().File); string(b) != "device_id = t1\n" {
		t.Fatal("nothing written on a refusal")
	}
	if code, out := call(t, "PUT", srv.URL+"/api/config", tok, `{"text":"device_id = t2\n"}`); code != 200 || out["restarting"] != true {
		t.Fatalf("save: %d %v", code, out)
	}
	if b, _ := os.ReadFile(a.cfg().File); string(b) != "device_id = t2\n" {
		t.Fatalf("saved: %q", b)
	}
	if b, _ := os.ReadFile(a.cfg().File + ".bak"); string(b) != "device_id = t1\n" {
		t.Fatalf("bak: %q", b)
	}
	if _, err := os.Stat(a.trialMarker()); err != nil {
		t.Fatal("trial marker")
	}
	select {
	case <-quit:
	case <-time.After(3 * time.Second):
		t.Fatal("the daemon restarts after a save")
	}
	// A failing trial (a server that cannot listen) puts the old config back.
	a.quit = func() {}
	a.trialFailed("test")
	if b, _ := os.ReadFile(a.cfg().File); string(b) != "device_id = t1\n" {
		t.Fatalf("rolled back: %q", b)
	}
	if _, err := os.Stat(a.trialMarker()); err == nil {
		t.Fatal("marker removed")
	}
}

func TestWebSettings(t *testing.T) {
	a, srv := newWebApp(t)
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	quit := make(chan struct{})
	a.quit = func() { close(quit) }
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)

	code, out := call(t, "GET", srv.URL+"/api/settings", tok, "")
	keys, _ := out["keys"].([]any)
	if code != 200 || len(keys) < 50 {
		t.Fatalf("get: %d, %d keys", code, len(keys))
	}
	for _, k := range keys {
		if n := k.(map[string]any)["name"]; n == "web_password" || n == "lux_calibration" {
			t.Fatalf("%s is set elsewhere, not listed", n)
		}
	}
	// A runtime key applies at once and stays in the file, the comments kept.
	os.WriteFile(a.cfg().File, []byte("# mine\ndevice_id = t1\n"), 0o600)
	code, out = call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"volume_step":["3"],"intercom_secret":["••••••••"]}}`)
	if code != 200 || out["restarting"] != false || a.cfg().VolumeStep != 3 {
		t.Fatalf("runtime key: %d %v, step %d", code, out, a.cfg().VolumeStep)
	}
	if b, _ := os.ReadFile(a.cfg().File); !strings.HasPrefix(string(b), "# mine\ndevice_id = t1\n") || !strings.Contains(string(b), "volume_step = 3") {
		t.Fatalf("file: %q", b)
	}
	// The config editor takes the same path: a runtime change, no restart.
	b, _ := os.ReadFile(a.cfg().File)
	text, _ := json.Marshal(map[string]string{"text": strings.Replace(string(b), "volume_step = 3", "volume_step = 2", 1)})
	if code, out := call(t, "PUT", srv.URL+"/api/config", tok, string(text)); code != 200 || out["restarting"] != false || a.cfg().VolumeStep != 2 {
		t.Fatalf("editor, runtime: %d %v", code, out)
	}
	// Out of range: refused, nothing written.
	if code, _ := call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"volume_step":["9"]}}`); code != 422 || a.cfg().VolumeStep != 2 {
		t.Fatalf("range: %d", code)
	}
	if code, _ := call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"web_password":["x"]}}`); code != 422 {
		t.Fatalf("hidden key: %d", code)
	}
	// A restart key: saved on trial, the daemon restarts.
	code, out = call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"device_name":["Kitchen"]}}`)
	if code != 200 || out["restarting"] != true {
		t.Fatalf("restart key: %d %v", code, out)
	}
	select {
	case <-quit:
	case <-time.After(3 * time.Second):
		t.Fatal("no restart")
	}
	if b, _ := os.ReadFile(a.cfg().File); !strings.Contains(string(b), "device_name = Kitchen") {
		t.Fatalf("file: %q", b)
	}
}

func TestWebMomentsAndTapLogin(t *testing.T) {
	_, srv := newWebApp(t)
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	req, _ := http.NewRequest("GET", srv.URL+"/api/moments", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("moments: %v %v", err, resp)
	}
	var got struct {
		Moments []momentView
		Catalog momentCatalogView
	}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	names := map[string]bool{}
	for _, m := range got.Moments {
		names[m.Name] = true
	}
	if !names["listening"] || !names["dialog_end"] || names["sad"] {
		t.Fatalf("moments: %v", names)
	}
	if len(got.Catalog.Scenarios) == 0 || len(got.Catalog.RingEffects) == 0 {
		t.Fatalf("catalog: %+v", got.Catalog)
	}
	if code, _ := call(t, "GET", srv.URL+"/tap/info", "", ""); code != 404 {
		t.Fatalf("no tap without led_preview: %d", code)
	}
}

func TestWebLog(t *testing.T) {
	a, srv := newWebApp(t)
	root := t.TempDir()
	a.cfg().SysRoot = root
	os.MkdirAll(filepath.Join(root, "tmp"), 0o755)
	os.MkdirAll(filepath.Join(root, "var/log"), 0o755)
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(filepath.Join(root, "tmp/yastationd.log"), []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(root, "var/log/messages.0"), []byte("old\n"), 0o644)
	os.WriteFile(filepath.Join(root, "var/log/messages"), []byte("new 1\nnew 2\n"), 0o644)

	if code, _ := call(t, "GET", srv.URL+"/api/log", "", ""); code != 401 {
		t.Fatalf("log without a login: %d", code)
	}
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	_, out = call(t, "GET", srv.URL+"/api/log?lines=3", tok, "")
	if l := out["lines"].([]any); len(l) != 3 || l[0] != "line 498" || l[2] != "line 500" {
		t.Fatalf("daemon tail: %v", l)
	}
	_, out = call(t, "GET", srv.URL+"/api/log?source=system", tok, "")
	if l := out["lines"].([]any); len(l) != 3 || l[0] != "old" || l[2] != "new 2" {
		t.Fatalf("system log: %v", l)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/log?source=nope", tok, ""); code != 400 {
		t.Fatalf("unknown source: %d", code)
	}
	if l := tailLines(filepath.Join(root, "tmp/yastationd.log"), 20); !strings.HasPrefix(l[0], "line ") {
		t.Fatalf("a cut first line kept: %q", l[0])
	}
}

func TestWebMomentsEdit(t *testing.T) {
	a, srv := newWebApp(t)
	m := newMomentApp(t) // the surfaces and a CFW root
	a.ringStack, a.matrixStack, a.matrix, a.front, a.store = m.ringStack, m.matrixStack, m.matrix, m.front, m.store
	a.cfg().SysRoot, a.prof = m.cfg().SysRoot, m.prof
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	moment := func(name string) map[string]any {
		_, out := call(t, "GET", srv.URL+"/api/moments", tok, "")
		for _, x := range out["moments"].([]any) {
			if x := x.(map[string]any); x["name"] == name {
				return x
			}
		}
		t.Fatalf("no %s in %v", name, out)
		return nil
	}
	if x := moment("dialog_end"); x["custom"] != nil || x["shows"].(float64) != 0 {
		t.Fatalf("defaults: %v", x)
	}
	seq := `[{"steps":[{"express":"happy","ms":50},{"anim":"wink","hold":true}]},"wink"]`
	if code, out := call(t, "PUT", srv.URL+"/api/moments/dialog_end", tok, seq); code != 200 {
		t.Fatalf("put: %d %v", code, out)
	}
	x := moment("dialog_end")
	if c, _ := x["custom"].([]any); len(c) != 2 || c[1] != "wink" {
		t.Fatalf("custom: %v", x["custom"])
	}
	if b, err := os.ReadFile(a.momentPath()); err != nil || !strings.Contains(string(b), `"hold": true`) {
		t.Fatalf("file: %s %v", b, err)
	}
	// errors: unknown moment, not a list, a broken sequence, no login, wrong method
	for _, c := range []struct {
		method, path, tok, body string
		code                    int
	}{
		{"PUT", "/api/moments/nope", tok, `["wink"]`, 404},
		{"PUT", "/api/moments/dialog_end", tok, `"wink"`, 400},
		{"PUT", "/api/moments/dialog_end", tok, `[{"steps":[]}]`, 400},
		{"PUT", "/api/moments/dialog_end", "", `["wink"]`, 401},
		{"POST", "/api/moments/dialog_end", tok, `["wink"]`, 405},
		{"POST", "/api/moment/nope", tok, ``, 404},
		{"GET", "/api/moment/dialog_end", tok, ``, 405},
	} {
		if code, out := call(t, c.method, srv.URL+c.path, c.tok, c.body); code != c.code {
			t.Errorf("%s %s %s: %d, want %d (%v)", c.method, c.path, c.body, code, c.code, out)
		}
	}
	// play as saved, then a draft (not saved), then a draft of nothing
	if _, out := call(t, "POST", srv.URL+"/api/moment/dialog_end", tok, ""); out["shown"] != true || !a.ringStack.Has("ov_moment") {
		t.Fatalf("play saved: %v", out)
	}
	if _, out := call(t, "POST", srv.URL+"/api/moment/dialog_end", tok, `["wink"]`); out["shown"] != true || a.ringStack.Has("ov_moment") {
		t.Fatalf("play draft (front only, the saved happy ring must be gone): %v", out)
	}
	if _, out := call(t, "POST", srv.URL+"/api/moment/dialog_end", tok, `[]`); out["shown"] != false {
		t.Fatalf("an empty draft showed something: %v", out)
	}
	if c, _ := moment("dialog_end")["custom"].([]any); len(c) != 2 {
		t.Fatal("a draft was saved")
	}
	// back to the defaults
	if code, _ := call(t, "DELETE", srv.URL+"/api/moments/dialog_end", tok, ""); code != 200 || moment("dialog_end")["custom"] != nil {
		t.Fatal("reset")
	}
}

func TestWebDiag(t *testing.T) {
	a, srv := newWebApp(t)
	a.prof.Platform.CFWRoot = "/cfw"
	a.cfg().SysRoot = t.TempDir()
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	get := func(q string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/diag"+q, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}
	if resp, _ := get(""); resp.StatusCode != 404 {
		t.Fatalf("no script: %d", resp.StatusCode)
	}
	bin := filepath.Join(a.cfg().SysRoot, "cfw/bin")
	os.MkdirAll(bin, 0o755)
	// A stand-in: gzip magic, then its arguments, so the test sees what it was run with.
	os.WriteFile(filepath.Join(bin, "diag.sh"), []byte("printf '\\037\\213'; echo \"$@\"\n"), 0o755)
	resp, b := get("?raw=1")
	if resp.StatusCode != 200 || !strings.Contains(string(b), "-r -") || resp.Header.Get("Content-Type") != "application/gzip" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "diag-t1-") {
		t.Fatalf("raw: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if _, b := get(""); strings.Contains(string(b), "-r") {
		t.Fatalf("redacted run got -r: %q", b)
	}
	os.WriteFile(filepath.Join(bin, "diag.sh"), []byte("echo broken >&2; exit 3\n"), 0o755)
	if resp, b := get(""); resp.StatusCode != 500 || !strings.Contains(string(b), "broken") {
		t.Fatalf("a failing script: %d %q", resp.StatusCode, b)
	}
}
