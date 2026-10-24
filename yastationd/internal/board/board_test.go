package board

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBergamotBuiltin(t *testing.T) {
	p, err := Builtin("bergamot")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Ring.Pixels != 6 || p.Input.Keys[582] != "voice" || p.Input.Switches[14] != "mic_mute" ||
		len(p.Wake.Mics) != 3 || p.Wake.Ref != 6 || len(p.Audio.Unmute) != 1 ||
		p.Platform.CFWRoot != "/bkupgrade/cfw" || p.Platform.UserlandArch != "armhf" {
		t.Fatalf("unexpected profile: %+v", p)
	}
}

// Orion's odd bits: 13ch capture at 96 kHz through the stock plug chain, dot-matrix fb
// screen, three amp unmutes — and the vendor wake: 6 mics + 7 speaker refs (multi-ref,
// passed to spotter-host as -Q).
func TestOrionBuiltin(t *testing.T) {
	p, err := Builtin("orion")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Mic.Channels != 13 || p.Mic.Rate != 96000 || p.Mic.Voice != 0 {
		t.Errorf("mic = %+v", p.Mic)
	}
	if len(p.Audio.Unmute) != 3 {
		t.Errorf("unmute = %q", p.Audio.Unmute)
	}
	if p.Matrix.Kind != "dotmatrix-fb" || p.Ring.Pixels != 36 ||
		p.Ambilight.Kind != "sysfs-rgbw" || p.Ambilight.Pixels != 12 ||
		len(p.Ambilight.Colors) != 4 || len(p.Ambilight.Dies) != 2 ||
		p.Platform.CFWRoot != "/data/cfw" || p.Platform.UserlandArch != "aarch64" {
		t.Errorf("matrix %q / ring %d / ambilight %+v / platform %+v",
			p.Matrix.Kind, p.Ring.Pixels, p.Ambilight, p.Platform)
	}
	if p.Wake.VQEPreset != "orion" || len(p.Wake.Mics) != 6 {
		t.Errorf("wake mics/preset = %+v", p.Wake)
	}
	wantRefs := []int{6, 7, 8, 9, 10, 11, 12}
	if len(p.Wake.Refs) != len(wantRefs) {
		t.Fatalf("wake.refs = %v, want %v", p.Wake.Refs, wantRefs)
	}
	for i, r := range p.Wake.Refs {
		if r != wantRefs[i] {
			t.Errorf("wake.refs[%d] = %d, want %d", i, r, wantRefs[i])
		}
	}
}

func TestDetectAndOverride(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "proc/device-tree"), 0o755)
	os.WriteFile(filepath.Join(root, "proc/device-tree/compatible"), []byte("yandex,bergamot-evt1\x00yandex,bergamot\x00amlogic, axg\x00"), 0o644)
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/board.conf"), []byte("audio.mic_gain_value = 36\naudio.unmute = a 1\naudio.unmute = b 2\nkey.582 = none\n"), 0o644)

	if got := Detect(root); got != "bergamot" {
		t.Fatalf("Detect = %q", got)
	}
	p, err := Load(root, "auto", "/etc/board.conf")
	if err != nil {
		t.Fatal(err)
	}
	if p.Audio.MicGainValue != 36 {
		t.Errorf("override not applied: %d", p.Audio.MicGainValue)
	}
	if len(p.Audio.Unmute) != 2 || p.Audio.Unmute[0] != "a 1" { // a repeated key replaces the inherited list
		t.Errorf("unmute = %q", p.Audio.Unmute)
	}
	if _, ok := p.Input.Keys[582]; ok {
		t.Error("key.582 = none should drop the mapping")
	}
}

func TestUnknownBoard(t *testing.T) {
	if _, err := Load(t.TempDir(), "auto", ""); err == nil {
		t.Fatal("expected a detection error")
	}
	if _, err := Load("", "nope", ""); err == nil {
		t.Fatal("expected unknown profile error")
	}
}

// Every embedded profile must parse and validate — a new board's .conf is checked by go test.
func TestAllBuiltinProfilesValid(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("no built-in profiles")
	}
	for _, n := range names {
		p, err := Builtin(n)
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", n, err)
		}
		if p.Name != n || len(p.Match) == 0 {
			t.Errorf("%s: name %q / match %v — name must equal the file name and match must be set", n, p.Name, p.Match)
		}
	}
}

func TestPlatformMetadataValidation(t *testing.T) {
	p, err := Builtin("bergamot")
	if err != nil {
		t.Fatal(err)
	}
	p.Platform.CFWRoot = "relative/cfw"
	if err := p.Validate(); err == nil {
		t.Fatal("relative platform.cfw_root accepted")
	}
	p.Platform.CFWRoot = "/bkupgrade/cfw"
	p.Platform.UserlandArch = "arm64"
	if err := p.Validate(); err == nil {
		t.Fatal("unknown platform.userland_arch accepted")
	}
}

func TestVolumeCurve(t *testing.T) {
	a := AudioSpec{VolumeMax: 255, VolumeMap: []VolumePoint{{0, 0}, {1, 33}, {20, 255}}}
	for _, c := range []struct{ pct, raw int }{{0, 0}, {1, 33}, {5, 33}, {10, 45}, {50, 138}, {100, 255}} {
		if got := a.RawFromPercent(c.pct); got != c.raw {
			t.Errorf("RawFromPercent(%d) = %d, want %d", c.pct, got, c.raw)
		}
	}
	for pct := 5; pct <= 100; pct++ { // 1..4 % land on the first step
		if got := a.PercentFromRaw(a.RawFromPercent(pct)); got != pct {
			t.Errorf("%d%% -> %d -> %d%%", pct, a.RawFromPercent(pct), got)
		}
	}
	if a.PercentFromRaw(0) != 0 || a.PercentFromRaw(17) != 3 {
		t.Error("below the first step")
	}
	// Keys walk the stock grid from anywhere, and stop at the ends.
	for _, c := range []struct{ raw, n, want int }{
		{0, 1, 33}, {33, -1, 0}, {33, 1, 45}, {97, 1, a.RawFromLevel(7)}, {255, 1, 255}, {0, -1, 0}, {255, -2, a.RawFromLevel(18)},
	} {
		if got := a.StepVolume(c.raw, c.n); got != c.want {
			t.Errorf("StepVolume(%d, %d) = %d, want %d", c.raw, c.n, got, c.want)
		}
	}
	// No map: a straight line, as before.
	lin := AudioSpec{VolumeMax: 255}
	for pct := 0; pct <= 100; pct++ {
		if got := lin.PercentFromRaw(lin.RawFromPercent(pct)); got != pct {
			t.Errorf("linear %d%% -> %d%%", pct, got)
		}
	}
}
