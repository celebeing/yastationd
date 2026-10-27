package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
)

func TestMergeLightPoint(t *testing.T) {
	p := luxcal.Point{Reading: 10, Lux: 200}
	// No points: p alone. A point in similar light: replaced. One in clearly other light: kept.
	if got := mergeLightPoint(nil, p); len(got) != 1 {
		t.Fatalf("alone: %v", got)
	}
	if got := mergeLightPoint([]luxcal.Point{{Reading: 8, Lux: 150}}, p); len(got) != 1 || got[0] != p {
		t.Fatalf("similar light: %v", got)
	}
	got := mergeLightPoint([]luxcal.Point{{Reading: 0.5, Lux: 10}}, p)
	if len(got) != 2 || got[0].Lux != 10 || got[1] != p {
		t.Fatalf("other light: %v", got)
	}
	// An older point that contradicts the new one (more light, lower reading): p alone.
	if got := mergeLightPoint([]luxcal.Point{{Reading: 20, Lux: 20}}, p); len(got) != 1 || got[0] != p {
		t.Fatalf("contradicting: %v", got)
	}
}

func TestWebLightCalibration(t *testing.T) {
	a, srv := newWebApp(t)
	lightCalSamples, lightCalEvery = 3, time.Millisecond
	a.matrixStack = display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} })
	a.ringStack = display.NewStack(func(time.Time) display.RingFrame { return nil })
	a.luxPath = filepath.Join(t.TempDir(), "in_intensity_input")
	os.WriteFile(a.luxPath, []byte("12.5\n"), 0o644)
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)

	if code, _ := call(t, "POST", srv.URL+"/api/calibrate/light", tok, `{"lux": 10}`); code != 422 {
		t.Fatalf("too dark accepted: %d", code)
	}
	code, out := call(t, "POST", srv.URL+"/api/calibrate/light", tok, `{"lux": 250}`)
	if code != 200 || out["calibration"] != "12.5:250" || out["source"] != "unit" {
		t.Fatalf("calibrate: %d %v", code, out)
	}
	if got := luxcal.Lux(a.luxCalibration(), 12.5); got != 250 {
		t.Fatalf("not live: %v", got)
	}
	b, _ := os.ReadFile(a.cfg.File)
	if !strings.Contains(string(b), "lux_calibration = 12.5:250") || !strings.Contains(string(b), "device_id = t1") {
		t.Fatalf("config file:\n%s", b)
	}
	if code, out = call(t, "DELETE", srv.URL+"/api/calibrate/light", tok, ""); code != 200 || out["source"] == "unit" {
		t.Fatalf("remove: %d %v", code, out)
	}
	if b, _ = os.ReadFile(a.cfg.File); strings.Contains(string(b), "lux_calibration =") {
		t.Fatalf("key left:\n%s", b)
	}
}
