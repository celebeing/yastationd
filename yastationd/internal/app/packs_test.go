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

// A batch import leaves the same version of an installed pack as it is, and updates another one.
func TestPackImportSkipsSameVersionUpdatesOthers(t *testing.T) {
	a := packTestApp(t)
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "hl", "name": "First", "version": "1.0"}`})); err != nil {
		t.Fatal(err)
	}
	r, err := a.importPack(packZip(t, map[string]string{"pack.json": `{"id": "hl", "name": "Again", "version": "1.0"}`}), true)
	if err != nil || !r.skipped || r.pack.ID != "hl" {
		t.Fatalf("same version: %+v %v", r, err)
	}
	if got := a.installedPacks()["hl"].Name; got != "First" {
		t.Errorf("the same version was written over: %q", got)
	}
	r, err = a.importPack(packZip(t, map[string]string{"pack.json": `{"id": "hl", "name": "Newer", "version": "1.1"}`}), true)
	if err != nil || r.skipped || !r.updated || r.was != "1.0" {
		t.Fatalf("new version: %+v %v", r, err)
	}
	if got := a.installedPacks()["hl"].Name; got != "Newer" {
		t.Errorf("not updated: %q", got)
	}
	r, err = a.importPack(packZip(t, map[string]string{"pack.json": `{"id": "fresh", "version": "1"}`}), true)
	if err != nil || r.skipped || r.updated {
		t.Errorf("a new pack: %+v %v", r, err)
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
	b, err := a.exportPack(customPack, "my-theme", "Mine")
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

func TestPackAnimationView(t *testing.T) {
	a := pixelPackApp(t)
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "neco"}`, "anims/pixels-21x12/happy.led": ledFrame()})); err != nil {
		t.Fatal(err)
	}
	v, err := a.packAnimationView("neco", "happy")
	if err != nil || v.Screen != [2]int{21, 12} || v.Seg7 != nil || len(v.Frames) != 1 {
		t.Fatalf("preview: %+v %v", v, err)
	}
	if len(v.Frames[0].Screen) != 21*12*6 || v.Frames[0].Screen[:6] != "ffffff" || v.Frames[0].MS != 100 {
		t.Errorf("frame: %+v", v.Frames[0])
	}
	if err := os.WriteFile(filepath.Join(a.packDir("neco"), "anims", "pixels-21x12", "happy.led"), []byte("not an animation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.packAnimationView("neco", "happy"); err == nil {
		t.Error("corrupted animation produced a browser preview")
	}
}

func TestPackAnimationViewSeg7(t *testing.T) {
	a := packTestApp(t)
	a.matrix = &hw.Matrix{}
	a.front = display.Seg7Front{}
	// A of digit 0 lit; extras DH, DL, DM, degree = 10 20 30 40
	clock := "255 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 10 20 30 40 | 25\n"
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "clock"}`, "anims/seg7/tick.scled": clock})); err != nil {
		t.Fatal(err)
	}
	v, err := a.packAnimationView("clock", "tick")
	if err != nil || v.Seg7 == nil || len(v.Frames) != 1 {
		t.Fatalf("preview: %+v %v", v, err)
	}
	// the LED stream's order (hw.MatrixFrame.Drive): 28 segments, the colon (DH and DL as one), DM, the degree
	f := v.Frames[0]
	if len(f.Seg7) != 2*hw.Seg7Elems || f.Seg7[:2] != "ff" || f.Seg7[56:] != "141e28" || f.MS != 25 {
		t.Errorf("frame: %+v", f)
	}
}

func TestWebPackAnimationPreview(t *testing.T) {
	a, srv := newWebApp(t)
	m := pixelPackApp(t)
	a.screen, a.front, a.store, a.prof = m.screen, m.front, m.store, m.prof
	a.conf.Store(m.cfg())
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "neco"}`, "anims/pixels-21x12/happy.led": ledFrame()})); err != nil {
		t.Fatal(err)
	}
	_, login := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	token, _ := login["token"].(string)
	code, out := call(t, "GET", srv.URL+"/api/packs/neco/anims/happy", token, "")
	frames, _ := out["frames"].([]any)
	screen, _ := out["screen"].([]any)
	if code != 200 || len(screen) != 2 || screen[0] != float64(21) || screen[1] != float64(12) || len(frames) != 1 {
		t.Fatalf("preview response: %d %v", code, out)
	}
	if code, _ := call(t, "GET", srv.URL+"/api/packs/neco/anims/missing", token, ""); code != 422 {
		t.Errorf("missing animation: %d", code)
	}
}

// The owner's packs: custom is worn on top unless placed or turned off; installed packs are
// read-only; a copy is editable and takes the original's place; new packs, renames, exports.
func TestOwnPacks(t *testing.T) {
	a := packTestApp(t)
	hl := packZip(t, map[string]string{
		"pack.json":         `{"id": "hl", "name": "Half-Life", "editable": true, "moments": {"any": {"volume_change": ["sound:hl_tick"]}}}`,
		"sounds/hl_tick.wav": tick,
	})
	if _, err := a.installPack(hl); err != nil {
		t.Fatal(err)
	}
	if a.packEditable("hl") {
		t.Fatal("an installed pack came in editable")
	}
	// read-only: its moments and files are not changed here
	if err := a.setPackMoment("hl", "volume_change", nil); err == nil {
		t.Error("a moment of an installed pack was changed")
	}
	// the owner's moment goes into custom, worn on top by default: it wins over hl
	a.applyPack("hl", false)
	if err := a.setMoment("volume_change", []show{{Ref: "sound:mine"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(a.themeStack(), []string{"hl", customPack}) {
		t.Fatalf("stack after apply: %v", a.themeStack())
	}
	a.setThemes([]string{customPack, "hl"}, false)
	if c := a.momentCandidates("volume_change"); c[0].Ref != "sound:mine" {
		t.Errorf("custom on top: %v", c)
	}
	// custom off: the stack without it says so, and the pack's moment shows
	a.setThemes([]string{"hl"}, false)
	if slices.Contains(a.themeStack(), customPack) {
		t.Fatalf("custom still worn: %v (%v)", a.themeStack(), a.cfg().Themes)
	}
	if c := a.momentCandidates("volume_change"); c[0].Ref != "sound:hl_tick" {
		t.Errorf("custom off: %v", c)
	}
	// a copy: editable, in hl's place, with hl's files; hl stays installed and read-only
	id, err := a.copyPack("hl")
	if err != nil || id != "hl-mine" || !a.packEditable(id) || a.installedPacks()[id].Name != "Half-Life (mine)" {
		t.Fatalf("copy: %q %v", id, err)
	}
	if !slices.Equal(a.themeStack(), []string{"hl-mine"}) {
		t.Errorf("stack after the copy: %v", a.themeStack())
	}
	if _, err := os.Stat(filepath.Join(a.packDir(id), "sounds", "hl_tick.wav")); err != nil {
		t.Error("the copy lacks the pack's sound")
	}
	if err := a.setPackMoment(id, "volume_change", []show{{Ref: "sound:hl_tick"}, {Ref: "wink"}}); err != nil {
		t.Fatal(err)
	}
	if c := a.momentCandidates("volume_change"); len(c) != 2 {
		t.Errorf("the copy's edit: %v", c)
	}
	// installing over one of the owner's packs is refused
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "hl-mine"}`})); err == nil {
		t.Error("an install replaced one of the owner's packs")
	}
	// a new pack goes on top; a rename keeps its id
	nid, err := a.createPack("Ночные звуки")
	if err != nil || a.themeStack()[0] != nid || a.editTarget() != nid {
		t.Fatalf("new pack %q: %v, stack %v", nid, err, a.themeStack())
	}
	if err := a.renamePack(nid, "Night"); err != nil || a.installedPacks()[nid].Name != "Night" {
		t.Errorf("rename: %v", err)
	}
	if err := a.renamePack("hl", "x"); err == nil {
		t.Error("an installed pack was renamed")
	}
	// export and back: it installs elsewhere read-only, with its moments and files
	b, err := a.exportPack(id, "hl-v2", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.installPack(b)
	if err != nil || p.ID != "hl-v2" || a.packEditable("hl-v2") || p.Name != "Half-Life (mine)" {
		t.Fatalf("round trip: %+v %v", p, err)
	}
	if c, ok := a.packMoment("hl-v2", "volume_change"); !ok || len(c) != 2 {
		t.Errorf("round trip moments: %v", c)
	}
	// removing one of the owner's packs takes it out of the stack
	if err := a.removePack(nid); err != nil || slices.Contains(a.themeStack(), nid) {
		t.Errorf("remove: %v %v", err, a.themeStack())
	}
}

// The moments API edits one of the owner's packs (the edit target, or ?pack=), never an
// installed one.
func TestWebMomentsInPacks(t *testing.T) {
	a, srv := newWebApp(t)
	m := packTestApp(t)
	a.ringStack, a.matrixStack, a.matrix, a.front, a.store = m.ringStack, m.matrixStack, m.matrix, m.front, m.store
	a.conf.Store(m.cfg())
	a.prof = m.prof
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	if _, err := a.installPack(packZip(t, map[string]string{"pack.json": `{"id": "ro", "moments": {"any": {"dialog_end": ["happy"]}}}`})); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, "PUT", srv.URL+"/api/moments/dialog_end?pack=ro", tok, `["wink"]`); code != 422 {
		t.Errorf("an installed pack's moment was set: %d", code)
	}
	code, out := call(t, "PUT", srv.URL+"/api/moments/dialog_end", tok, `["wink"]`)
	if code != 200 || out["target"] != customPack {
		t.Fatalf("set in the edit target: %d %v", code, out)
	}
	if c, ok := a.packMoment(customPack, "dialog_end"); !ok || c[0].Ref != "wink" {
		t.Errorf("custom's entry: %v", c)
	}
	if code, _ := call(t, "DELETE", srv.URL+"/api/moments/dialog_end?pack=custom", tok, ""); code != 200 {
		t.Errorf("reset: %d", code)
	}
	if _, ok := a.packMoment(customPack, "dialog_end"); ok {
		t.Error("the entry stayed after the reset")
	}
	_, out = call(t, "GET", srv.URL+"/api/moments", tok, "")
	if ed, _ := out["editable"].([]any); len(ed) != 1 {
		t.Errorf("the owner's packs: %v", out["editable"])
	}
}
