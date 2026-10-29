package app

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/ledcolor"
)

func TestParseRGB(t *testing.T) {
	for in, want := range map[string]hw.RGB{
		`[255,0,10]`:          {R: 255, B: 10},
		`{"r":1,"g":2,"b":3}`: {R: 1, G: 2, B: 3},
		`[300,-5,0]`:          {R: 255},
	} {
		if got, ok := parseRGB(json.RawMessage(in)); !ok || got != want {
			t.Errorf("parseRGB(%s) = %v, %v", in, got, ok)
		}
	}
	for _, bad := range []string{``, `null`, `[1,2]`, `{"r":1}`} {
		if _, ok := parseRGB(json.RawMessage(bad)); ok {
			t.Errorf("parseRGB(%q) accepted", bad)
		}
	}
}

func TestAmbilightCommandAndState(t *testing.T) {
	command, err := parseAmbilightCommand([]byte(`{"state":"ON","brightness":300,"color":{"h":180,"s":100}}`))
	if err != nil {
		t.Fatal(err)
	}
	state := persistState{}
	command.apply(&state)
	if !state.AmbilightOn || state.AmbilightBright != 255 || state.AmbilightMode != "hs" ||
		state.AmbilightHue != 180 || state.AmbilightSat != 100 {
		t.Fatalf("ambilight command state = %+v", state)
	}
	var payload struct {
		State      string             `json:"state"`
		Brightness int                `json:"brightness"`
		ColorMode  string             `json:"color_mode"`
		Color      map[string]float64 `json:"color"`
		ColorTemp  int                `json:"color_temp"`
	}
	if err := json.Unmarshal([]byte(ambilightStateJSON(state)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.State != "ON" || payload.Brightness != 255 || payload.ColorMode != "hs" ||
		payload.Color["h"] != 180 || payload.Color["s"] != 100 {
		t.Fatalf("ambilight state payload = %+v", payload)
	}

	command, _ = parseAmbilightCommand([]byte(`{"color_temp":1000}`))
	command.apply(&state)
	payload.Color = nil
	_ = json.Unmarshal([]byte(ambilightStateJSON(state)), &payload)
	if state.AmbilightMode != "color_temp" || payload.ColorMode != "color_temp" ||
		payload.ColorTemp != ambilightMinKelvin || payload.Color != nil {
		t.Fatalf("color_temp: state %+v, payload %+v", state, payload)
	}
}

// A state file from before the modes keeps its colour.
func TestAmbilightMigration(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/state.json"
	dflt := defaultState(128, 128, 255, true)
	for _, c := range []struct {
		file string
		mode string
		hue  float64
	}{
		{`{"ambilight_on":true,"ambilight_rgbw":{"R":0,"G":128,"B":128,"W":0}}`, "hs", 180},
		{`{"ambilight_on":true,"ambilight_rgbw":{"R":0,"G":0,"B":0,"W":255}}`, "color_temp", 0},
		{`{"ambilight_on":true}`, "color_temp", 0},
	} {
		if err := os.WriteFile(path, []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		s := newStateStore(path, dflt).get()
		if s.AmbilightMode != c.mode || s.AmbilightHue != c.hue || s.AmbilightLegacy != nil ||
			(c.mode == "color_temp" && s.AmbilightKelvin != defaultAmbilightKelvin) {
			t.Errorf("%s: %+v", c.file, s)
		}
	}
}

func TestAmbilightDrive(t *testing.T) {
	p, err := ledcolor.ParsePrimaries("0.6878 0.2977 0.4822, 0.2664 0.5659 1.6303, 0.1177 0.0747 0.3036, 0.3388 0.3811 1")
	if err != nil {
		t.Fatal(err)
	}
	w, _ := ledcolor.White("srgb", ledcolor.Calibration{})
	r, err := ledcolor.NewRenderer(ledcolor.Calibration{Channels: p}, w)
	if err != nil {
		t.Fatal(err)
	}
	s := persistState{AmbilightMode: "hs", AmbilightHue: 180, AmbilightSat: 100, AmbilightBright: 255}
	// HA's cyan: less blue than green, some W (docs/ambilight-color.md).
	if c := ambilightDrive(r, s); c.R != 0 || c.G != 255 || c.B < 150 || c.B > 220 || c.W < 120 {
		t.Errorf("cyan: %+v", c)
	}
	// Half brightness is sRGB half: about a fifth of the light.
	s.AmbilightBright = 128
	if c := ambilightDrive(r, s); c.G < 45 || c.G > 62 {
		t.Errorf("cyan at 128: %+v", c)
	}
	// Uncalibrated: plain RGB, W for a temperature.
	if c := ambilightDrive(nil, s); c != (hw.RGBW{G: 128, B: 128}) {
		t.Errorf("uncalibrated cyan at 128: %+v", c)
	}
	s.AmbilightMode = "color_temp"
	if c := ambilightDrive(nil, s); c != (hw.RGBW{W: 128}) {
		t.Errorf("uncalibrated temperature: %+v", c)
	}
	// Effects: a white pixel lights W (with some R/B to take its tint off); off, W stays dark.
	f := calibrateFx(r, []hw.RGBW{{W: 255}, {}}, 255, true)
	if f[0].W != 255 || f[1] != (hw.RGBW{}) {
		t.Errorf("fx white: %+v", f)
	}
	if f := calibrateFx(r, []hw.RGBW{{W: 255}}, 255, false); f[0].W != 0 || f[0].G == 0 {
		t.Errorf("fx white without W: %+v", f)
	}
}
