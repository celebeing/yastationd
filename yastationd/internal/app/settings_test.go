package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestApplyConfigText(t *testing.T) {
	a, _ := newWebApp(t)
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	var quits atomic.Int32
	a.quit = func() { quits.Add(1) }
	file := a.cfg().File

	// A comment only: written, nothing to apply, no restart.
	restart, changed, err := a.applyConfigText([]byte("# hello\ndevice_id = t1\n"), "test")
	if err != nil || restart || len(changed) != 0 {
		t.Fatalf("comment: %v %v %v", restart, changed, err)
	}
	if b, _ := os.ReadFile(file); string(b) != "# hello\ndevice_id = t1\n" {
		t.Fatalf("comment not written: %q", b)
	}
	// The same text: nothing at all.
	os.Remove(file + ".bak")
	if _, _, err := a.applyConfigText([]byte("# hello\ndevice_id = t1\n"), "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file + ".bak"); err == nil {
		t.Fatal("rewritten without a change")
	}
	// Invalid: refused, the file and the config in force untouched.
	if _, _, err := a.applyConfigText([]byte("volume_step = 9\n"), "test"); err == nil || a.cfg().VolumeStep != 1 {
		t.Fatal("invalid accepted")
	}
	if _, _, err := a.applyConfigText([]byte("no_such_key = 1\n"), "test"); err == nil {
		t.Fatal("unknown key accepted")
	}
	// Runtime keys only: live, with a .bak, the File kept.
	restart, changed, _ = a.applyConfigText([]byte("device_id = t1\nvolume_step = 2\nmusic_duck_db = -10\n"), "test")
	if restart || strings.Join(changed, ",") != "volume_step,music_duck_db" || a.cfg().VolumeStep != 2 || a.cfg().MusicDuckDB != -10 {
		t.Fatalf("runtime: %v %v", restart, changed)
	}
	if a.cfg().File != file {
		t.Fatal("the config in force lost its file")
	}
	if b, _ := os.ReadFile(file + ".bak"); string(b) != "# hello\ndevice_id = t1\n" {
		t.Fatalf("bak: %q", b)
	}
	// Runtime and restart keys together: one restart, nothing applied live meanwhile.
	restart, changed, _ = a.applyConfigText([]byte("device_id = t1\nvolume_step = 3\nwake_enabled = false\n"), "test")
	if !restart || a.cfg().VolumeStep != 2 || len(changed) != 3 {
		t.Fatalf("mixed: %v %v step %d", restart, changed, a.cfg().VolumeStep)
	}
	if _, err := os.Stat(a.trialMarker()); err != nil {
		t.Fatal("no trial marker")
	}
	// A hardware override written with the default's value still counts as a change (Has).
	a.adoptConfig([]byte("device_id = t1\n"))
	os.WriteFile(file, []byte("device_id = t1\n"), 0o600)
	if restart, _, _ := a.applyConfigText([]byte("device_id = t1\nmic_channel = 0\n"), "test"); !restart {
		t.Fatal("an override set to 0 must restart")
	}
}

// A line an older build took and this one refuses stays in the file without blocking other
// changes; a new bad line is still refused.
func TestOldBadLineTolerated(t *testing.T) {
	a, _ := newWebApp(t)
	text := []byte("device_id = t1\ntz = Mars/Base\n")
	os.WriteFile(a.cfg().File, text, 0o600)
	a.adoptConfig(text)
	if restart, _, err := a.applyConfigText([]byte("device_id = t1\ntz = Mars/Base\nvolume_step = 2\n"), "test"); err != nil || restart || a.cfg().VolumeStep != 2 {
		t.Fatalf("blocked by the old line: %v %v", restart, err)
	}
	if _, _, err := a.applyConfigText([]byte("device_id = t1\ntz = Mars/Base\nvolume_step = 2\nvolume_max = 1\n"), "test"); err == nil {
		t.Fatal("a new bad line went through")
	}
}

// The login and the light calibration write their keys themselves; a later settings change must
// not mistake them for changes that need a restart.
func TestOwnWritersAdoptTheirKeys(t *testing.T) {
	a, srv := newWebApp(t)
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	a.quit = func() { t.Fatal("restarted") }
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	code, out := call(t, "POST", srv.URL+"/api/password", tok, `{"user":"admin","password":"secret12"}`)
	if code != 200 {
		t.Fatalf("password: %d %v", code, out)
	}
	tok = out["token"].(string)
	if err := a.saveLuxCalibration(nil); err != nil {
		t.Fatal(err)
	}
	code, out = call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"volume_step":["2"]}}`)
	if code != 200 || out["restarting"] != false {
		t.Fatalf("after the login change: %d %v", code, out)
	}
}

func TestWebSettingsKinds(t *testing.T) {
	a, srv := newWebApp(t)
	a.prof.Platform.CFWRoot = filepath.Dir(a.cfg().File)
	a.quit = func() {}
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	get := func(name string) map[string]any {
		_, out := call(t, "GET", srv.URL+"/api/settings", tok, "")
		for _, k := range out["keys"].([]any) {
			if m := k.(map[string]any); m["name"] == name {
				return m
			}
		}
		t.Fatalf("%s not listed", name)
		return nil
	}
	// A list key: several lines; [] removes it.
	if code, out := call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"ntp_server":["a.lan","b.lan"]}}`); code != 200 {
		t.Fatalf("list: %d %v", code, out)
	}
	if b, _ := os.ReadFile(a.cfg().File); !strings.Contains(string(b), "ntp_server = a.lan\nntp_server = b.lan\n") {
		t.Fatalf("list in the file: %q", b)
	}
	// (ntp_server needs a restart: the config in force keeps the old value until then)
	if v := get("ntp_server")["value"].([]any); len(v) != 0 {
		t.Fatalf("a restart key applied live: %v", v)
	}
	call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"ntp_server":[]}}`)
	if b, _ := os.ReadFile(a.cfg().File); strings.Contains(string(b), "ntp_server") {
		t.Fatalf("list not removed: %q", b)
	}
	// One value for a plain key.
	if code, _ := call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"volume_step":["1","2"]}}`); code != 422 {
		t.Fatalf("two values: %d", code)
	}
	// A secret is never sent back; the mask leaves it as it is.
	text := []byte("device_id = t1\nintercom_secret = 0123456789abcdef\n")
	os.WriteFile(a.cfg().File, text, 0o600)
	a.adoptConfig(text) // as after the restart
	k := get("intercom_secret")
	if v := k["value"].([]any); len(v) != 1 || v[0] != secretMask {
		t.Fatalf("secret shown: %v", v)
	}
	call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"intercom_secret":["`+secretMask+`"]}}`)
	if b, _ := os.ReadFile(a.cfg().File); !strings.Contains(string(b), "intercom_secret = 0123456789abcdef") {
		t.Fatalf("the mask changed the secret: %q", b)
	}
	// Changed: set, and other than the default — or, for a hardware override, the board's value.
	call(t, "PUT", srv.URL+"/api/settings", tok, `{"changes":{"volume_step":["2"],"volume_max":["100"]}}`)
	if get("volume_step")["changed"] != true || get("volume_max")["changed"] != false || get("arbitration")["changed"] != false {
		t.Fatal("changed against the default")
	}
	a.prof.Name = "bergamot"
	board := get("mic_channel")["board"].([]any)
	text = []byte("device_id = t1\nmic_channel = " + board[0].(string) + "\nmic_device = hw:9,9\n")
	os.WriteFile(a.cfg().File, text, 0o600)
	a.adoptConfig(text)
	if get("mic_channel")["changed"] != false || get("mic_device")["changed"] != true {
		t.Fatalf("changed against the board: %v %v", get("mic_channel"), get("mic_device"))
	}
	if c := get("auto_brightness_curve"); c["board"] == nil || c["changed"] != false {
		t.Fatalf("the curve's board value: %v", c)
	}
	// Keys for hardware this board lacks are not listed.
	listed := func(name string) bool {
		_, out := call(t, "GET", srv.URL+"/api/settings", tok, "")
		for _, k := range out["keys"].([]any) {
			if k.(map[string]any)["name"] == name {
				return true
			}
		}
		return false
	}
	if listed("ambilight_fx_white") || listed("text_lines") {
		t.Fatal("ambilight / pixel-screen keys on a board without them")
	}
	a.prof.Ambilight.Kind, a.prof.Matrix.Kind = "sysfs-rgbw", "dotmatrix-fb"
	if !listed("ambilight_fx_white") || !listed("text_lines") || get("ambilight_fx_white")["default"].([]any)[0] != "true" {
		t.Fatal("ambilight / pixel-screen keys missing on a board with them, or the white LED off by default")
	}
	// Flags as the pages need them.
	if k := get("volume_step"); k["runtime"] != true || k["sync"] != true {
		t.Fatalf("volume_step flags: %v", k)
	}
	if k := get("device_name"); k["runtime"] != false || k["sync"] != false {
		t.Fatalf("device_name flags: %v", k)
	}
	if k := get("mic_rate"); k["value"].([]any)[0] != "" || k["advanced"] != true {
		t.Fatalf("an unset override shows empty: %v", k)
	}
}
