package app

import (
	"encoding/json"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
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
	command, err := parseAmbilightCommand([]byte(
		`{"state":"ON","brightness":300,"color":{"r":1,"g":2,"b":3,"w":4}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	state := persistState{}
	command.apply(&state)
	if !state.AmbilightOn || state.AmbilightBright != 255 ||
		state.AmbilightRGBW != (hw.RGBW{R: 1, G: 2, B: 3, W: 4}) {
		t.Fatalf("ambilight command state = %+v", state)
	}

	var payload struct {
		State      string  `json:"state"`
		Brightness int     `json:"brightness"`
		ColorMode  string  `json:"color_mode"`
		Color      hw.RGBW `json:"color"`
	}
	if err := json.Unmarshal([]byte(ambilightStateJSON(state)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.State != "ON" || payload.Brightness != 255 || payload.ColorMode != "rgbw" ||
		payload.Color != state.AmbilightRGBW {
		t.Fatalf("ambilight state payload = %+v", payload)
	}
}
