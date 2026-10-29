package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// packZip builds a pack archive from path -> content.
func packZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// packTestApp is themeTestApp with a config file, so the theme key can be written.
func packTestApp(t *testing.T) *App {
	a := themeTestApp(t)
	f := filepath.Join(a.cfg().SysRoot, "cfw", "yastationd.conf")
	data := []byte("sysroot = " + a.cfg().SysRoot + "\n")
	os.WriteFile(f, data, 0o600)
	c := *a.cfg()
	c.File = f
	a.conf.Store(&c)
	a.adoptConfig(data) // the file's values in force: a later edit changes the theme key only
	return a
}

var tick = string(wavFile(make([]byte, 320), 16000))

func TestPackInstallApplyRemove(t *testing.T) {
	a := packTestApp(t)
	zipped := packZip(t, map[string]string{
		"hl/pack.json":          `{"id": "hl", "name": "Half-Life", "moments": {"any": {"volume_change": ["sound:hl_tick"]}}}`,
		"hl/sounds/hl_tick.wav": tick,
		"hl/README.md":          "not installed",
	})
	p, err := a.installPack(zipped)
	if err != nil || p.ID != "hl" {
		t.Fatalf("install: %v %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(a.packDir("hl"), "README.md")); err == nil {
		t.Error("the readme was installed")
	}
	if a.soundPath("hl_tick") != "" {
		t.Error("a pack out of the stack plays its sound")
	}
	// installed, not in the stack: the default theme rules
	if c := a.momentCandidates("volume_change"); len(c) != 1 || c[0].Ref != "sound:volume_sound" {
		t.Errorf("before apply: %+v", c)
	}
	if err := a.applyPack("hl", false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.cfg().Themes, []string{"hl", customPack}) {
		t.Errorf("stack %v", a.cfg().Themes)
	}
	if c := a.momentCandidates("volume_change"); len(c) != 1 || c[0].Ref != "sound:hl_tick" {
		t.Errorf("after apply: %+v", c)
	}
	if a.soundPath("hl_tick") == "" {
		t.Error("the applied pack's sound is not found")
	}
	if !a.hook("volume_change") {
		t.Error("the pack's moment did not fire")
	}
	// the owner's own moment sits below the applied theme until custom is put on top
	if err := a.setMoment("volume_change", []show{{Ref: "sound:mine"}}); err != nil {
		t.Fatal(err)
	}
	if c := a.momentCandidates("volume_change"); c[0].Ref != "sound:hl_tick" {
		t.Errorf("the theme should override the owner's moment: %+v", c)
	}
	a.applyPack(customPack, false)
	if c := a.momentCandidates("volume_change"); c[0].Ref != "sound:mine" {
		t.Errorf("custom on top: %+v", c)
	}
	if err := a.removePack("hl"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(a.cfg().Themes, "hl") || a.installedPacks()["hl"] != nil || a.soundPath("hl_tick") != "" {
		t.Error("the pack is still there")
	}
}

func TestPackRefused(t *testing.T) {
	a := packTestApp(t)
	man := `{"id": "x", "name": "x"}`
	for why, files := range map[string]map[string]string{
		"no manifest":      {"sounds/a.wav": tick},
		"bad id":           {"pack.json": `{"id": "Bad Id"}`},
		"custom id":        {"pack.json": `{"id": "custom"}`},
		"unknown moment":   {"pack.json": `{"id": "x", "moments": {"any": {"nope": ["wink"]}}}`},
		"unknown display":  {"pack.json": `{"id": "x", "moments": {"lcd": {"listening": ["wink"]}}}`},
		"outside the pack": {"pack.json": man, "../evil.wav": tick},
		"not a wav":        {"pack.json": man, "sounds/a.wav": "hello"},
		"stray file":       {"pack.json": man, "bin/run.sh": "#!/bin/sh"},
		"bad sound name":   {"pack.json": man, "sounds/A B.wav": tick},
		"bad pattern":      {"pack.json": man, "anims/pixels-21x12/a.led": "garbage"},
	} {
		if _, err := a.installPack(packZip(t, files)); err == nil {
			t.Errorf("%s: installed", why)
		}
	}
	if _, err := a.installPack([]byte("not a zip")); err == nil {
		t.Error("not a zip: installed")
	}
	if ents, _ := os.ReadDir(a.cfwFile(packsDir)); len(ents) != 0 {
		t.Errorf("leftovers: %v", ents)
	}
}

func TestExportCustom(t *testing.T) {
	a := packTestApp(t)
	a.setMoment("dialog_end", []show{{Ref: "happy"}})
	dir := filepath.Join(a.packDir(customPack), "sounds")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "mine.wav"), []byte(tick), 0o644)
	b, err := a.exportCustom("my-theme", "Mine")
	if err != nil {
		t.Fatal(err)
	}
	// it installs as a pack of its own
	p, err := a.installPack(b)
	if err != nil || p.ID != "my-theme" {
		t.Fatalf("re-install: %v %v", p, err)
	}
	if !strings.Contains(string(b), "sounds/mine.wav") {
		t.Error("the sound is not in the archive")
	}
}

// ledFrame is one .led line: the first dot lit, the rest dark, 100 ms.
func ledFrame() string {
	return "ffffff" + strings.Repeat(" 000000", 21*12-1) + " 100\n"
}

// pixelPackApp is packTestApp with a 21x12 screen, so packs' pixel animations apply.
func pixelPackApp(t *testing.T) *App {
	a := packTestApp(t)
	a.screen = hw.NewVirtualDotMatrix(21, 12)
	a.front = display.PixelFront{W: 21, H: 12, PackPatterns: &display.PixelLibrary{W: 21, H: 12}}
	return a
}

// A pack put on the disk by hand (not through the API) shows up, and its animations play by
// "@<pack>/<name>" before the pack is applied.
func TestPackByHandAndPreview(t *testing.T) {
	a := pixelPackApp(t)
	a.loadPacks()
	dir := filepath.Join(a.packDir("neco"), "anims", "pixels-21x12")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(a.packDir("neco"), "pack.json"), []byte(`{"id": "neco", "name": "Neco"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "happy.led"), []byte(ledFrame()), 0o644)
	if a.installedPacks()["neco"] != nil {
		t.Fatal("seen before a rescan")
	}
	now := a.packsNow()
	if a.installedPacks()["neco"] == nil {
		t.Fatalf("not seen after GET /api/packs: %v", now)
	}
	if _, ok := a.frontAnim("@neco/happy"); !ok {
		t.Error("the pack's animation does not play by @neco/happy")
	}
	if _, ok := a.frontAnim("happy"); ok {
		t.Error("an animation of a pack out of the theme plays by its bare name")
	}
	for _, bad := range []string{"@neco/nope", "@nope/happy", "@neco", "@../neco/happy"} {
		if _, ok := a.frontAnim(bad); ok {
			t.Errorf("%s resolved", bad)
		}
	}
}

// Apply here: this speaker's own stack (theme_here), the shared key untouched; ending it goes
// back to the shared one.
func TestApplyHere(t *testing.T) {
	a := pixelPackApp(t)
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "neco"}`, "anims/pixels-21x12/happy.led": ledFrame()})); err != nil {
		t.Fatal(err)
	}
	if err := a.applyPack("neco", true); err != nil {
		t.Fatal(err)
	}
	if len(a.cfg().Themes) != 0 || !slices.Equal(a.cfg().ThemesHere, []string{"neco", customPack}) {
		t.Fatalf("theme %v, here %v", a.cfg().Themes, a.cfg().ThemesHere)
	}
	if _, ok := a.frontAnim("happy"); !ok {
		t.Error("the pack applied here does not play by its bare name")
	}
	if err := a.setThemes(nil, true); err != nil {
		t.Fatal(err)
	}
	if len(a.cfg().ThemesHere) != 0 || !slices.Equal(a.themeStack(), []string{customPack}) {
		t.Errorf("not back to the shared theme: %v", a.themeStack())
	}
	if _, ok := a.frontAnim("happy"); ok {
		t.Error("the pack still plays after its stack ended")
	}
}
