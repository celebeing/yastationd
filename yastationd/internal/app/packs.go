package app

import (
	"archive/zip"
	"bytes"
	"cmp"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Asset packs and the theme stack (docs/asset-packs.md). A pack is <cfw>/packs/<id>/: pack.json
// (its manifest and, optionally, moments) with sounds/<name>.wav and anims/<display>/<name>.led
// or .scled. The stack is the theme key, top first, over level 0 (the built-in behaviour). The
// owner's packs are editable (ownpacks.go); the first, custom ("Your own"), is worn on top unless
// the stack places it or turns it off ("-custom").

const (
	packsDir   = "packs"
	customPack = "custom"
	// maxPackZip bounds an uploaded pack archive, maxPackFiles its unpacked size.
	maxPackZip   = 32 << 20
	maxPackFiles = 64 << 20
)

var (
	packID       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	packFileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	// packAnimDir is a pack's animation directory per display kind: seg7, or pixels-<w>x<h>.
	packAnimDir = regexp.MustCompile(`^(seg7|pixels-(\d+)x(\d+))$`)
)

// pack is an installed pack's manifest.
type pack struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version,omitempty"`
	Author   string   `json:"author,omitempty"`
	Source   string   `json:"source,omitempty"`
	License  string   `json:"license,omitempty"`
	Displays []string `json:"displays,omitempty"`
	// Editable: one of the owner's packs, made on this speaker (ownpacks.go); an installed one is
	// read-only.
	Editable bool `json:"editable,omitempty"`
	// Moments by display kind ("any" for every one): moment -> candidates.
	Moments map[string]map[string][]show `json:"moments,omitempty"`
}

// packAnimationView is one installed animation, parsed into the screen's native frame bytes for
// the browser preview. Frames are hex to keep the JSON compact and avoid base64 padding.
type packAnimationView struct {
	Display string               `json:"display"`
	Width   int                  `json:"width,omitempty"`
	Height  int                  `json:"height,omitempty"`
	Frames  []packAnimationFrame `json:"frames"`
}

type packAnimationFrame struct {
	Data string `json:"data"`
	MS   int64  `json:"ms"`
}

// packDisplay is this speaker's display kind as packs name it: seg7, pixels-<w>x<h>, "" (none).
func (a *App) packDisplay() string {
	switch {
	case a.screen != nil:
		w, h := a.screen.Size()
		return fmt.Sprintf("pixels-%dx%d", w, h)
	case a.matrix != nil:
		return "seg7"
	}
	return ""
}

func (a *App) packDir(id string) string { return filepath.Join(a.cfwFile(packsDir), id) }

// loadPacks reads every installed pack's manifest (a broken one is logged and left out).
func (a *App) loadPacks() {
	m := map[string]*pack{}
	ents, _ := os.ReadDir(a.cfwFile(packsDir))
	a.packsSeen.Store(a.packsStamp(ents))
	for _, e := range ents {
		if !e.IsDir() || !packID.MatchString(e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(a.packDir(e.Name()), "pack.json"))
		if errors.Is(err, os.ErrNotExist) && e.Name() == customPack {
			continue // the owner's sounds or animations, no manifest of its own
		}
		var p pack
		if err == nil {
			err = json.Unmarshal(b, &p)
		}
		if err != nil || p.ID != e.Name() {
			a.log.Warn("packs: left out", "pack", e.Name(), "err", err)
			continue
		}
		m[p.ID] = &p
	}
	a.packs.Store(&m)
	a.packPreviews.Range(func(k, _ any) bool { a.packPreviews.Delete(k); return true })
	a.refreshPackAnims()
}

// packsStamp sums up what is on disk under packs/ (each pack's manifest time), so a pack put
// there by hand or by a script, not through the API, is seen without a restart.
func (a *App) packsStamp(ents []os.DirEntry) string {
	var b strings.Builder
	for _, e := range ents {
		var t int64
		if fi, err := os.Stat(filepath.Join(a.packDir(e.Name()), "pack.json")); err == nil {
			t = fi.ModTime().UnixNano()
		}
		fmt.Fprintf(&b, "%s:%d;", e.Name(), t)
	}
	return b.String()
}

// rescanPacks reloads the packs when the directory changed since the last load.
func (a *App) rescanPacks() {
	ents, _ := os.ReadDir(a.cfwFile(packsDir))
	if seen, _ := a.packsSeen.Load().(string); seen != a.packsStamp(ents) {
		a.loadPacks()
	}
}

// packAnim loads "@<pack>/<name>": an installed pack's animation for this display, whether the
// pack is in the theme or not (the LED page previews a pack before it is applied).
func (a *App) packAnim(ref string) (display.Anim, bool) {
	id, name, ok := strings.Cut(strings.TrimPrefix(ref, "@"), "/")
	if !ok || id != customPack && a.installedPacks()[id] == nil {
		return nil, false
	}
	dir := filepath.Join(a.packDir(id), "anims", a.packDisplay())
	switch f := a.front.(type) {
	case display.PixelFront:
		l, _ := a.packPreviews.LoadOrStore(id, &display.PixelLibrary{Dirs: []string{dir}, W: f.W, H: f.H, Gamma: f.Gamma})
		return l.(*display.PixelLibrary).Load(name)
	case display.Seg7Front:
		l, _ := a.packPreviews.LoadOrStore(id, &display.ClipLibrary{Dirs: []string{dir}, NoBuiltin: true})
		return l.(*display.ClipLibrary).Load(name)
	}
	return nil, false
}

// packAnimationView reads an installed pack's animation for this display. The upload path already
// validates it; parsing again keeps a hand-copied or later-corrupted file out of the browser.
func (a *App) packAnimationView(id, name string) (packAnimationView, error) {
	a.rescanPacks()
	if !packFileName.MatchString(name) || id != customPack && a.installedPacks()[id] == nil {
		return packAnimationView{}, errors.New("no such pack animation")
	}
	kind := a.packDisplay()
	m := packAnimDir.FindStringSubmatch(kind)
	if m == nil {
		return packAnimationView{}, errors.New("this speaker has no supported display")
	}
	ext := ".led"
	if kind == "seg7" {
		ext = ".scled"
	}
	b, err := os.ReadFile(filepath.Join(a.packDir(id), "anims", kind, name+ext))
	if err != nil {
		return packAnimationView{}, errors.New("no such pack animation")
	}
	v := packAnimationView{Display: kind}
	if kind == "seg7" {
		c, err := display.ParseScled(name, bytes.NewReader(b))
		if err != nil {
			return packAnimationView{}, err
		}
		for _, f := range c.Frames {
			data := make([]byte, 0, 32)
			for _, d := range f.Seg {
				data = append(data, d[:]...)
			}
			data = append(data, f.Extra[:]...)
			v.Frames = append(v.Frames, packAnimationFrame{Data: hex.EncodeToString(data), MS: f.Dur.Milliseconds()})
		}
		return v, nil
	}
	v.Width, _ = strconv.Atoi(m[2])
	v.Height, _ = strconv.Atoi(m[3])
	gamma := 1.0
	if f, ok := a.front.(display.PixelFront); ok {
		gamma = f.Gamma
	}
	c, err := display.ParseLed(name, bytes.NewReader(b), v.Width, v.Height, gamma)
	if err != nil {
		return packAnimationView{}, err
	}
	for _, f := range c.Frames {
		data := make([]byte, 0, len(f.Px)*3)
		for _, p := range f.Px {
			data = append(data, p.R, p.G, p.B)
		}
		v.Frames = append(v.Frames, packAnimationFrame{Data: hex.EncodeToString(data), MS: f.Dur.Milliseconds()})
	}
	return v, nil
}

func (a *App) installedPacks() map[string]*pack {
	if m := a.packs.Load(); m != nil {
		return *m
	}
	return nil
}

// themeStack is the packs in force, top first: theme_here, else the theme key; custom on top when
// the list does not place it, left out when the list says "-custom".
func (a *App) themeStack() []string {
	if l := a.cfg().ThemesHere; len(l) > 0 {
		return withCustom(l)
	}
	return withCustom(a.cfg().Themes)
}

func withCustom(l []string) []string {
	if slices.Contains(l, customOff) {
		return slices.DeleteFunc(slices.Clone(l), func(s string) bool { return s == customOff })
	}
	if slices.Contains(l, customPack) {
		return slices.Clone(l)
	}
	return append([]string{customPack}, l...)
}

// customOff in a theme list turns custom off (unlisted, it is worn on top).
const customOff = "-" + customPack

// themeMoment is a moment's candidates from the first pack in the stack that has the moment, and
// that pack's id.
func (a *App) themeMoment(name string) ([]show, string, bool) {
	packs := a.installedPacks()
	kind := a.packDisplay()
	for _, id := range a.themeStack() {
		p := packs[id]
		if p == nil {
			continue
		}
		for _, k := range []string{kind, "any"} {
			if c, ok := p.Moments[k][name]; ok {
				return c, id, true
			}
		}
	}
	return nil, "", false
}

// packSoundDirs are the stack's sound directories, top first.
func (a *App) packSoundDirs() []string {
	var out []string
	for _, id := range a.themeStack() {
		out = append(out, filepath.Join(a.packDir(id), "sounds"))
	}
	return out
}

// refreshPackAnims points the front display's pack layer at the stack's animation directories
// (on start, on a change of the theme key, on an install or a removal).
func (a *App) refreshPackAnims() {
	kind := a.packDisplay()
	var dirs []string
	for _, id := range a.themeStack() {
		dirs = append(dirs, filepath.Join(a.packDir(id), "anims", kind))
	}
	switch f := a.front.(type) {
	case display.PixelFront:
		if f.PackPatterns != nil {
			f.PackPatterns.SetDirs(dirs)
		}
	case display.Seg7Front:
		if f.Packs != nil {
			f.Packs.SetDirs(dirs)
		}
	}
}

// CheckPack checks a pack archive the way a speaker does before it installs it (cmd/packtool),
// and returns its manifest.
func CheckPack(zipData []byte) (id, name string, files int, err error) {
	p, f, err := readPack(zipData)
	if err != nil {
		return "", "", 0, err
	}
	return p.ID, p.Name, len(f), nil
}

// readPack checks a pack archive: its manifest, every file parsed by the loader that will play
// it. It returns the manifest and the files by their path in the pack.
func readPack(zipData []byte) (*pack, map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, nil, fmt.Errorf("not a zip archive: %w", err)
	}
	files, err := packFiles(zr)
	if err != nil {
		return nil, nil, err
	}
	man, ok := files["pack.json"]
	if !ok {
		return nil, nil, errors.New("no pack.json in the archive")
	}
	var p pack
	if err := json.Unmarshal(man, &p); err != nil {
		return nil, nil, fmt.Errorf("pack.json: %w", err)
	}
	if !packID.MatchString(p.ID) || p.ID == customPack {
		return nil, nil, fmt.Errorf("pack.json: id %q: lower-case letters, digits and -, up to 40 (not %q)", p.ID, customPack)
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	for kind, ms := range p.Moments {
		if kind != "any" && kind != "seg7" && !packAnimDir.MatchString(kind) {
			return nil, nil, fmt.Errorf("pack.json: moments for an unknown display %q", kind)
		}
		for name := range ms {
			if _, ok := momentByName[name]; !ok {
				return nil, nil, fmt.Errorf("pack.json: no moment %q", name)
			}
		}
	}
	for name, b := range files {
		if err := checkPackFile(name, b); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return &p, files, nil
}

// installPack checks a pack archive and installs it, replacing an installed one of the same id
// (never one of the owner's). Installed, a pack is read-only: an editable mark in it is dropped.
func (a *App) installPack(zipData []byte) (*pack, error) {
	p, _, err := a.importPack(zipData, false)
	return p, err
}

// importPack checks a pack archive and optionally leaves a same-id installed pack untouched.
// It returns whether it skipped the archive. Owner's packs are protected from replacement either
// way; skipExisting treats them as existing too.
func (a *App) importPack(zipData []byte, skipExisting bool) (*pack, bool, error) {
	p, files, err := readPack(zipData)
	if err != nil {
		return nil, false, err
	}
	if skipExisting && a.installedPacks()[p.ID] != nil {
		return p, true, nil
	}
	if a.packEditable(p.ID) {
		return nil, false, fmt.Errorf("%s is one of your packs here: export it under another id, or remove yours first", p.ID)
	}
	if p.Editable {
		p.Editable = false
		man, _ := json.MarshalIndent(p, "", "  ")
		files["pack.json"] = append(man, '\n')
	}
	total := 0
	for _, b := range files {
		total += len(b)
	}
	root := a.cfwFile(packsDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, false, err
	}
	if free := freeBytes(root); free >= 0 && int64(total) > free-assetFreeKeep {
		return nil, false, errors.New("not enough room on the speaker")
	}
	tmp, err := os.MkdirTemp(root, ".install-*")
	if err != nil {
		return nil, false, err
	}
	defer os.RemoveAll(tmp)
	for name, b := range files {
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, false, err
		}
	}
	dir := a.packDir(p.ID)
	old := dir + ".old"
	os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(old, dir)
		return nil, false, err
	}
	os.RemoveAll(old)
	a.loadPacks()
	return p, false, nil
}

// packFiles reads an archive's files, by their path in the pack (a single top directory that
// holds everything, as a zipped folder has, is taken off).
func packFiles(zr *zip.Reader) (map[string][]byte, error) {
	var names []string
	for _, f := range zr.File {
		if !f.FileInfo().IsDir() {
			names = append(names, f.Name)
		}
	}
	prefix := ""
	if !slices.Contains(names, "pack.json") {
		for _, n := range names {
			if d, ok := strings.CutSuffix(n, "/pack.json"); ok && !strings.Contains(d, "/") {
				prefix = d + "/"
			}
		}
	}
	out := map[string][]byte{}
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, ok := strings.CutPrefix(f.Name, prefix)
		if !ok || name != path.Clean(name) || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
			return nil, fmt.Errorf("%s: outside the pack", f.Name)
		}
		if base := path.Base(name); strings.HasPrefix(base, ".") || strings.HasPrefix(strings.ToUpper(base), "README") || strings.HasPrefix(strings.ToUpper(base), "LICENSE") {
			continue // dot files, a readme, a licence: not installed
		}
		total += f.UncompressedSize64
		if total > maxPackFiles {
			return nil, fmt.Errorf("unpacked over %d MB", maxPackFiles>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxPackFiles+1))
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = b
	}
	return out, nil
}

// checkPackFile parses one file of a pack with the loader that will play it.
func checkPackFile(name string, b []byte) error {
	if name == "pack.json" {
		return nil
	}
	parts := strings.Split(name, "/")
	switch {
	case len(parts) == 2 && parts[0] == "sounds":
		n, ok := strings.CutSuffix(parts[1], ".wav")
		if !ok || !packFileName.MatchString(n) {
			return errors.New("a sound is sounds/<name>.wav: lower-case letters, digits, - and _")
		}
		return checkWAV(b)
	case len(parts) == 3 && parts[0] == "anims":
		m := packAnimDir.FindStringSubmatch(parts[1])
		if m == nil {
			return errors.New("an animation is anims/seg7/<name>.scled or anims/pixels-<w>x<h>/<name>.led")
		}
		if m[1] == "seg7" {
			n, ok := strings.CutSuffix(parts[2], ".scled")
			if !ok || !packFileName.MatchString(n) {
				return errors.New("a seg7 animation is <name>.scled")
			}
			_, err := display.ParseScled(n, bytes.NewReader(b))
			return err
		}
		n, ok := strings.CutSuffix(parts[2], ".led")
		if !ok || !packFileName.MatchString(n) {
			return errors.New("a pixel animation is <name>.led")
		}
		w, _ := strconv.Atoi(m[2])
		h, _ := strconv.Atoi(m[3])
		_, err := display.ParseLed(n, bytes.NewReader(b), w, h, 1)
		return err
	}
	return errors.New("not a file a pack carries (pack.json, sounds/, anims/)")
}

// checkWAV: b is a PCM WAV aplay plays (RIFF/WAVE, format 1, 8 or 16 bit).
func checkWAV(b []byte) error {
	if len(b) < wavHeaderLen || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return errors.New("not a WAV file")
	}
	if string(b[12:16]) == "fmt " {
		if f := int(b[20]) | int(b[21])<<8; f != 1 {
			return errors.New("not PCM (compressed WAV)")
		}
	}
	return nil
}

// removePack deletes a pack (an installed one, or one of the owner's) and takes it out of the
// theme keys.
func (a *App) removePack(id string) error {
	_, err := os.Stat(a.packDir(id))
	if a.installedPacks()[id] == nil && (id != customPack || err != nil) {
		return errors.New("no such pack")
	}
	if err := os.RemoveAll(a.packDir(id)); err != nil {
		return err
	}
	for _, here := range []bool{false, true} {
		l := a.themeList(here)
		if len(l) == 0 && (here || id != customPack) || !slices.Contains(withCustom(l), id) {
			continue
		}
		stack := slices.DeleteFunc(withCustom(l), func(s string) bool { return s == id })
		if err := a.setThemes(stack, here); err != nil {
			return err
		}
	}
	a.loadPacks()
	return nil
}

// themeList is the theme key (here: theme_here) as written.
func (a *App) themeList(here bool) []string {
	if here {
		return a.cfg().ThemesHere
	}
	return a.cfg().Themes
}

// applyPack puts a pack on top of the theme key, or (here) of theme_here, which starts as a copy of
// the stack the speaker wears. The Packs page edits whichever is in force.
func (a *App) applyPack(id string, here bool) error {
	if id != customPack && a.installedPacks()[id] == nil {
		return errors.New("no such pack")
	}
	base := withCustom(a.cfg().Themes)
	if here {
		base = a.themeStack()
	}
	rest := slices.DeleteFunc(base, func(s string) bool { return s == id })
	return a.setThemes(append([]string{id}, rest...), here)
}

// setThemes writes the stack a speaker wears into the theme key, or theme_here (an empty list ends
// it), and points the animations at the result. A stack without custom says "-custom" (unlisted,
// custom would be worn on top).
func (a *App) setThemes(ids []string, here bool) error {
	key := "theme"
	if here {
		key = "theme_here"
	}
	ids = slices.DeleteFunc(slices.Clone(ids), func(s string) bool { return s == customOff })
	if (len(ids) > 0 || !here) && !slices.Contains(ids, customPack) {
		ids = append(ids, customOff)
	}
	if !here && slices.Equal(ids, []string{customPack}) {
		ids = nil // the default: custom alone
	}
	_, _, err := a.applyConfigKeys(map[string][]string{key: ids}, "set from the Packs page", "packs")
	a.refreshPackAnims()
	return err
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// packView is a pack as the Packs page shows it.
type packView struct {
	pack
	Moments  []string `json:"moments"` // the moment names it sets for this speaker
	Sounds   []string `json:"sounds"`
	Anims    []string `json:"anims"` // its animations for this speaker's display
	Fits     bool     `json:"fits"`  // it has something for this speaker
	Bytes    int64    `json:"bytes"`
	Position int      `json:"position"` // in the stack, from 0 at the top; -1 not in it
}

// packsNow is GET /api/packs: the installed packs, the stack, the ids it names but this speaker
// lacks.
func (a *App) packsNow() map[string]any {
	a.rescanPacks()
	stack := a.themeStack()
	kind := a.packDisplay()
	views := []packView{}
	all := a.installedPacks()
	ids := make([]string, 0, len(all)+1)
	for id := range all {
		if id != customPack {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	ids = append([]string{customPack}, ids...)
	for _, id := range ids {
		v := packView{Position: slices.Index(stack, id)}
		if p := all[id]; p != nil {
			v.pack = *p
		} else {
			v.pack = pack{ID: customPack, Name: "Your own", Editable: true}
		}
		for _, k := range []string{"any", kind} {
			for n := range v.pack.Moments[k] {
				v.Moments = appendUnique(v.Moments, n)
			}
		}
		v.pack.Moments = nil
		sort.Strings(v.Moments)
		v.Sounds = listNames(filepath.Join(a.packDir(id), "sounds"), ".wav")
		ext := ".led"
		if kind == "seg7" {
			ext = ".scled"
		}
		v.Anims = listNames(filepath.Join(a.packDir(id), "anims", kind), ext)
		v.Fits = len(v.Moments)+len(v.Sounds)+len(v.Anims) > 0
		v.Bytes = dirBytes(a.packDir(id))
		views = append(views, v)
	}
	var missing []string
	for _, id := range stack {
		if id != customPack && all[id] == nil {
			missing = append(missing, id)
		}
	}
	return map[string]any{"packs": views, "stack": stack, "display": kind, "missing": missing,
		"here": len(a.cfg().ThemesHere) > 0, "target": a.editTarget(),
		"free": freeBytes(a.cfwFile("")), "max_bytes": maxPackZip}
}

// listNames lists dir's files with ext, without it, sorted.
func listNames(dir, ext string) []string {
	out := []string{}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ext); ok && !e.IsDir() {
			out = append(out, n)
		}
	}
	return out
}

func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if fi, err := e.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// apiPacks serves the Packs page (docs/asset-packs.md):
//
//	GET    /api/packs                       the packs, the stack, what the stack names but is missing
//	PUT    /api/packs[?skip_existing=1]     install a pack (the zip as the body; read-only here)
//	POST   /api/packs/new {"name"}          a new pack of the owner's, worn on top
//	PUT    /api/packs/stack                 {"stack": [ids], "here": bool}: the stack worn, top first
//	                                        (here: theme_here; [] ends it); custom left out = off
//	POST   /api/packs/<id>/apply[?here=1]   put it on top
//	POST   /api/packs/<id>/copy             an editable copy of it, in its place in the stack
//	POST   /api/packs/<id>/rename {"name"}  rename one of the owner's
//	DELETE /api/packs/<id>                  remove it
//	GET    /api/packs/<id>/export?id=&name= the pack as an archive (read-only where installed)
//	GET    /api/packs/<id>/anims/<name>     parsed frames for browser preview
//	PUT|DELETE /api/packs/<id>/sounds/<name>  a sound of one of the owner's packs (a WAV)
//	PUT|DELETE /api/packs/<id>/anims/<name>   an animation of one of them, for this display
func (a *App) apiPacks(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/packs"), "/"), "/")
	if parts[0] == "" {
		parts = nil
	}
	var name struct{ Name string }
	readName := func() error {
		if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&name) != nil {
			return errors.New(`want {"name": …}`)
		}
		return nil
	}
	var (
		err     error
		skipped string
		didSkip bool
	)
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
	case len(parts) == 0 && r.Method == http.MethodPut:
		var b []byte
		b, err = io.ReadAll(io.LimitReader(r.Body, maxPackZip+1))
		if err == nil && len(b) > maxPackZip {
			err = fmt.Errorf("over %d MB", maxPackZip>>20)
		}
		if err == nil {
			var p *pack
			if p, didSkip, err = a.importPack(b, r.URL.Query().Get("skip_existing") == "1"); err == nil {
				if didSkip {
					skipped = p.ID
				} else {
					a.log.Info("packs: installed", "pack", p.ID, "version", p.Version)
				}
			}
		}
	case len(parts) == 1 && parts[0] == "new" && r.Method == http.MethodPost:
		if err = readName(); err == nil {
			_, err = a.createPack(name.Name)
		}
	case len(parts) == 1 && parts[0] == "stack" && r.Method == http.MethodPut:
		var in struct {
			Stack []string
			Here  bool
		}
		if json.NewDecoder(io.LimitReader(r.Body, smallBody)).Decode(&in) != nil {
			err = errors.New(`want {"stack": [ids], "here": bool}`)
			break
		}
		for _, id := range in.Stack {
			if id != customPack && a.installedPacks()[id] == nil {
				err = fmt.Errorf("no pack %q here", id)
			}
		}
		if err == nil {
			err = a.setThemes(in.Stack, in.Here)
		}
	case len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "apply":
		err = a.applyPack(parts[0], r.URL.Query().Get("here") == "1")
	case len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "copy":
		_, err = a.copyPack(parts[0])
	case len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "rename":
		if err = readName(); err == nil {
			err = a.renamePack(parts[0], name.Name)
		}
	case len(parts) == 1 && r.Method == http.MethodDelete:
		err = a.removePack(parts[0])
	case len(parts) == 2 && parts[1] == "export" && r.Method == http.MethodGet:
		q := r.URL.Query()
		b, err := a.exportPack(parts[0], q.Get("id"), q.Get("name"))
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+cmp.Or(q.Get("id"), parts[0])+`.zip"`)
		w.Write(b)
		return
	case len(parts) == 3 && parts[1] == "anims" && r.Method == http.MethodGet:
		v, err := a.packAnimationView(parts[0], parts[2])
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
		return
	case len(parts) == 3 && (parts[1] == "sounds" || parts[1] == "anims"):
		err = a.packAsset(r, parts[0], parts[1], parts[2])
	default:
		writeErr(w, http.StatusNotFound, "no such packs call")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	out := a.packsNow()
	if skipped != "" {
		out["skipped"] = skipped
	}
	writeJSON(w, http.StatusOK, out)
}
