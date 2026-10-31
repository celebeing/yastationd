package wifisetup

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const portalConf = `ctrl_interface=/var/run/wpa_supplicant

network={
	ssid="home"
	psk="secret123"
	key_mgmt=WPA-PSK
	priority=5
}

network={
	ssid="guest"
	key_mgmt=NONE
}
`

func portalMode(t *testing.T) (*Mode, string, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wpa_supplicant.conf")
	if err := os.WriteFile(path, []byte(portalConf), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(Options{WPAConf: path, RunDir: filepath.Join(dir, "run"), DeviceName: "Station", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	m.conf = path
	if err := os.MkdirAll(m.o.RunDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return m, path, m.portalHandler(net.IPv4(10, 42, 0, 1))
}

func portalRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "10.42.0.1"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func readPortalConf(t *testing.T, path string) *Conf {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := ParseConf(data)
	if err != nil {
		t.Fatal(err)
	}
	return conf
}

func TestPortalStateRedactsPasswords(t *testing.T) {
	_, _, h := portalMode(t)
	res := portalRequest(h, http.MethodGet, "/api/state", "")
	if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("state response = %d headers=%v", res.Code, res.Header())
	}
	if strings.Contains(res.Body.String(), "secret123") || strings.Contains(res.Body.String(), "key_mgmt") {
		t.Fatalf("state exposed raw configuration: %s", res.Body.String())
	}
	var state stateView
	if err := json.NewDecoder(res.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if state.Device != "Station" || len(state.Networks) != 2 || state.Networks[0] != (netView{ID: 0, SSID: "home", Security: "psk", Priority: 5}) {
		t.Fatalf("state = %+v", state)
	}
}

func TestPortalCreatesReplacesAndBacksUpNetworks(t *testing.T) {
	_, path, h := portalMode(t)
	res := portalRequest(h, http.MethodPost, "/api/networks", `{"ssid":"office","psk":"password123","hidden":true,"priority":7}`)
	if res.Code != http.StatusOK {
		t.Fatalf("create = %d %s", res.Code, res.Body)
	}
	backup, err := os.ReadFile(path + ".before-setup")
	if err != nil || string(backup) != portalConf {
		t.Fatalf("backup = %q, err=%v", backup, err)
	}
	conf := readPortalConf(t, path)
	if i := conf.Index("office"); i < 0 || len(conf.Networks) != 3 || conf.Networks[i].Security() != "psk" || !conf.Networks[i].Hidden() || conf.Networks[i].Priority() != 7 {
		t.Fatalf("created networks = %+v", conf.Networks)
	}

	res = portalRequest(h, http.MethodPost, "/api/networks", `{"ssid":"office","psk":"","priority":0}`)
	if res.Code != http.StatusOK {
		t.Fatalf("replace = %d %s", res.Code, res.Body)
	}
	conf = readPortalConf(t, path)
	i := conf.Index("office")
	if len(conf.Networks) != 3 || conf.Networks[i].Security() != "open" || conf.Networks[i].Priority() != 0 {
		t.Fatalf("replacement networks = %+v", conf.Networks)
	}
	backup, err = os.ReadFile(path + ".before-setup")
	if err != nil || string(backup) != portalConf {
		t.Fatalf("backup changed = %q, err=%v", backup, err)
	}
}

func TestPortalRejectsStaleOrInvalidNetworkEdits(t *testing.T) {
	_, path, h := portalMode(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res := portalRequest(h, http.MethodPost, "/api/networks/0", `{"ssid":"guest","priority":9}`)
	if res.Code != http.StatusConflict {
		t.Fatalf("stale edit = %d %s", res.Code, res.Body)
	}
	res = portalRequest(h, http.MethodDelete, "/api/networks/0?ssid=guest", "")
	if res.Code != http.StatusConflict {
		t.Fatalf("stale delete = %d %s", res.Code, res.Body)
	}
	res = portalRequest(h, http.MethodPost, "/api/networks/0", `{"ssid":"home","psk":"short"}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("invalid edit = %d %s", res.Code, res.Body)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatalf("rejected edit changed configuration: %q, err=%v", got, err)
	}
}

func TestPortalPartialEditAndCaptiveHostRouting(t *testing.T) {
	_, path, h := portalMode(t)
	res := portalRequest(h, http.MethodPost, "/api/networks/0", `{"ssid":"home","priority":0,"disabled":true}`)
	if res.Code != http.StatusOK {
		t.Fatalf("partial edit = %d %s", res.Code, res.Body)
	}
	conf := readPortalConf(t, path)
	home := conf.Networks[conf.Index("home")]
	if home.Priority() != 0 || !home.Disabled() || home.Security() != "psk" {
		t.Fatalf("partial edit network = %+v", home)
	}
	res = portalRequest(h, http.MethodPost, "/api/networks/0", `{"ssid":"home","disabled":false}`)
	if res.Code != http.StatusOK || readPortalConf(t, path).Networks[0].Disabled() {
		t.Fatalf("enable = %d %s", res.Code, res.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req.Host = "connectivity-check.example"
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusFound || res.Header().Get("Location") != "http://10.42.0.1/" {
		t.Fatalf("captive redirect = %d %q", res.Code, res.Header().Get("Location"))
	}
}

func TestPortalFinishAndRebootReplyBeforeAction(t *testing.T) {
	m, _, h := portalMode(t)
	delays := make(chan chan time.Time, 2)
	m.after = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		delays <- ch
		return ch
	}
	left := make(chan struct{}, 1)
	m.leave = func() { left <- struct{}{} }
	rebooted := make(chan struct{}, 1)
	m.o.Reboot = func() { rebooted <- struct{}{} }

	res := portalRequest(h, http.MethodPost, "/api/finish", "")
	if res.Code != http.StatusOK {
		t.Fatalf("finish response = %d", res.Code)
	}
	var finishDelay chan time.Time
	select {
	case finishDelay = <-delays:
	case <-time.After(time.Second):
		t.Fatal("finish did not schedule delayed leave")
	}
	select {
	case <-left:
		t.Fatal("finish left before the response delay")
	default:
	}
	finishDelay <- time.Now()
	select {
	case <-left:
	case <-time.After(time.Second):
		t.Fatal("finish did not leave after delay")
	}

	res = portalRequest(h, http.MethodPost, "/api/reboot", "")
	if res.Code != http.StatusOK {
		t.Fatalf("reboot response = %d", res.Code)
	}
	var rebootDelay chan time.Time
	select {
	case rebootDelay = <-delays:
	case <-time.After(time.Second):
		t.Fatal("reboot did not schedule delayed action")
	}
	select {
	case <-rebooted:
		t.Fatal("rebooted before the response delay")
	default:
	}
	rebootDelay <- time.Now()
	select {
	case <-rebooted:
	case <-time.After(time.Second):
		t.Fatal("reboot did not run after delay")
	}
}

func TestPortalKeys(t *testing.T) {
	m, _, h := portalMode(t)
	file := filepath.Join(t.TempDir(), "authorized_keys")
	m.o.AuthorizedKeys = file
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ8kH3F6sQb2r0m8Yy0d1m9t8xq3u4Vv1pY7cW2nZk5T owner@laptop"
	for _, bad := range []string{"", "hello", "ssh-ed25519 notbase64!", "ssh-rsa AAAAC3NzaC1lZDI1NTE5AAAAIJ8k", `command="sh" ` + key} {
		body, _ := json.Marshal(map[string]string{"key": bad})
		if r := portalRequest(h, "POST", "/api/keys", string(body)); r.Code != http.StatusBadRequest {
			t.Errorf("%q: code %d", bad, r.Code)
		}
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("a refused key wrote the file")
	}
	body, _ := json.Marshal(map[string]string{"key": "  " + key + "\n"})
	for range 2 { // the second time changes nothing
		if r := portalRequest(h, "POST", "/api/keys", string(body)); r.Code != http.StatusOK {
			t.Fatalf("add: %d %s", r.Code, r.Body)
		}
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != key+"\n" {
		t.Fatalf("file %q, %v", got, err)
	}
	if fi, _ := os.Stat(file); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	var st stateView
	r := portalRequest(h, "GET", "/api/state", "")
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if len(st.Keys) != 1 || st.Keys[0] != "ssh-ed25519 owner@laptop" {
		t.Errorf("keys %q", st.Keys)
	}
}

func TestNoNetworks(t *testing.T) {
	m, path, _ := portalMode(t)
	if m.NoNetworks() {
		t.Error("a conf with networks reads as none")
	}
	if err := os.WriteFile(path, []byte("ctrl_interface=/var/run/wpa_supplicant\nupdate_config=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !m.NoNetworks() {
		t.Error("an empty conf reads as having networks")
	}
	m.o.WPAConf = path + ".missing"
	if m.NoNetworks() {
		t.Error("an unreadable conf reads as none")
	}
}
