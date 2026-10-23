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
