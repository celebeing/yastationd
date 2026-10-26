package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
	"github.com/celebeing/yastationd/yastationd/internal/web"
)

func newWebApp(t *testing.T) (*App, *httptest.Server) {
	cfg := config.Default()
	cfg.DeviceID = "t1"
	cfg.File = filepath.Join(t.TempDir(), "yastationd.conf")
	os.WriteFile(cfg.File, []byte("device_id = t1\n"), 0o600)
	a := &App{cfg: cfg, log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		topics: mqtt.NewTopics("yastation", "homeassistant", "t1"),
		store:  newStateStore("", defaultState(128, 128, 128, false))}
	a.builder = mqtt.NewBuilder(a.topics, mqtt.Device{Name: "t"})
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
	b, _ := os.ReadFile(a.cfg.File)
	if !strings.Contains(string(b), "web_password = pbkdf2-sha256$") {
		t.Fatalf("config: %s", b)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"hunter22"}`); code != 200 {
		t.Fatal("new password works")
	}
}

func TestWebMetricsAndInfo(t *testing.T) {
	_, srv := newWebApp(t)
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`yastation_info{device="t1"`, `yastation_lux{device="t1"} 12.5`, `yastation_auto_brightness{device="t1"} 0`, "yastation_rss_bytes"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("metrics lack %q:\n%s", want, b)
		}
	}
	code, out := call(t, "GET", srv.URL+"/api/info", "", "")
	if code != 200 || out["id"] != "t1" {
		t.Fatalf("info: %d %v", code, out)
	}
}
