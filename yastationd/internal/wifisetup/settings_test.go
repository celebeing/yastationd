package wifisetup

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
)

func settingsMode(t *testing.T, conf string) (*Mode, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "yastationd.conf")
	os.WriteFile(p, []byte(conf), 0o600)
	m := New(Options{ConfFile: p, RunDir: filepath.Join(dir, "run"),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	os.MkdirAll(m.o.RunDir, 0o700)
	return m, p
}

func post(m *Mode, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	m.apiSettings(w, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	return w
}

func TestSettingsSaveAndView(t *testing.T) {
	orig := "# mine\ndevice_id = mini3\nmqtt_host = old.lan\nmqtt_pass = secret\nprobe = broker=old.lan:1883@broker\n"
	m, p := settingsMode(t, orig)
	w := post(m, `{"mqtt_host":"new.lan","probes":["broker=new.lan:1883@broker","hass=new.lan:8123"],"ntp_servers":["ntp.lan"],"tz":"Europe/Berlin","sendspin":true}`)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got, _ := os.ReadFile(p)
	for _, want := range []string{"# mine\n", "mqtt_host = new.lan\n", "mqtt_pass = secret\n",
		"probe = hass=new.lan:8123\n", "ntp_server = ntp.lan\n", "tz = Europe/Berlin\n", "sendspin = true\n"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if b, _ := os.ReadFile(p + ".before-setup"); string(b) != orig {
		t.Errorf("backup %q", b)
	}
	if !m.settingsChanged {
		t.Error("change not flagged")
	}

	r := httptest.NewRecorder()
	m.apiSettings(r, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	if strings.Contains(r.Body.String(), "secret") {
		t.Fatal("the password left the speaker")
	}
	var v settingsView
	json.Unmarshal(r.Body.Bytes(), &v)
	if !v.MQTTPassSet || v.MQTTHost != "new.lan" || len(v.Probes) != 2 || !v.Pending {
		t.Errorf("%+v", v)
	}

	// The second save of the session keeps the first backup.
	post(m, `{"mqtt_pass":""}`)
	if b, _ := os.ReadFile(p + ".before-setup"); string(b) != orig {
		t.Errorf("backup overwritten")
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "mqtt_pass") {
		t.Errorf("password not cleared:\n%s", got)
	}
}

func TestSettingsRejected(t *testing.T) {
	orig := "device_id = mini3\n"
	m, p := settingsMode(t, orig)
	for _, body := range []string{
		`{"mqtt_port":70000}`,
		`{"mqtt_host":"bad host"}`,
		`{"probes":["nonsense"]}`,
		`{"tz":"Mars/Olympus"}`,
		`{"sendspin_server":"http://x"}`,
		`{"device_name":"a\nmqtt_host = evil"}`,
		`{"setup_psk":"short"}`,
		`{"device_name":""}`,
	} {
		if w := post(m, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Errorf("file changed by a rejected edit:\n%s", b)
	}
}
