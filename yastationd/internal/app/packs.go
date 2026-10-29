package app

import (
	"archive/zip"
	"bytes"
	"cmp"
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
// or .scled. The stack is the theme key, top first; the owner's own pack, custom, has its moments
// in <cfw>/moments.json and sits just above the default theme unless the stack lists it.

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
	// Moments by display kind ("any" for every one): moment -> candidates.
	Moments map[string]map[string][]show `json:"moments,omitempty"`
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

func (a *App) installedPacks() map[string]*pack {
	if m := a.packs.Load(); m != nil {
		return *m
	}
	return nil
}

// themeStack is the packs in force, top first: this speaker's own theme_here, else the theme
// key; custom just above the default theme when the list does not place it.
func (a *App) themeStack() []string {
	if l := a.cfg().ThemesHere; len(l) > 0 {
		return withCustom(l)
	}
	return withCustom(a.cfg().Themes)
}

func withCustom(l []string) []string {
	s := slices.Clone(l)
	if !slices.Contains(s, customPack) {
		s = append(s, customPack)
	}
	return s
}

// themeMoment is a moment's candidates from the first pack in the stack that has the moment, and
// that pack's id.
func (a *App) themeMoment(name string) ([]show, string, bool) {
	packs := a.installedPacks()
	kind := a.packDisplay()
	for _, id := range a.themeStack() {
		if id == customPack {
			if c, ok := a.userMomentMap()[name]; ok {
				return c, id, true
			}
			continue
		}
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

// installPack checks a pack archive and installs it, replacing an installed one of the same id.
func (a *App) installPack(zipData []byte) (*pack, error) {
	p, files, err := readPack(zipData)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, b := range files {
		total += len(b)
	}
	root := a.cfwFile(packsDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if free := freeBytes(root); free >= 0 && int64(total) > free-assetFreeKeep {
		return nil, errors.New("not enough room on the speaker")
	}
	tmp, err := os.MkdirTemp(root, ".install-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	for name, b := range files {
		dst := filepath.Join(tmp, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return nil, err
		}
	}
	dir := a.packDir(p.ID)
	old := dir + ".old"
	os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(old, dir)
		return nil, err
	}
	os.RemoveAll(old)
	a.loadPacks()
	return p, nil
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

// removePack deletes an installed pack and takes it out of the theme keys.
func (a *App) removePack(id string) error {
	if id == customPack || a.installedPacks()[id] == nil {
		return errors.New("no such pack")
	}
	if err := os.RemoveAll(a.packDir(id)); err != nil {
		return err
	}
	for _, here := range []bool{false, true} {
		if l := a.themeList(here); slices.Contains(l, id) {
			l = slices.DeleteFunc(slices.Clone(l), func(s string) bool { return s == id })
			if here && len(l) == 0 {
				l = []string{customPack} // still this speaker's own theme: the default one
			}
			if err := a.setThemes(l, here); err != nil {
				return err
			}
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

// applyPack puts a pack on top of the shared theme, or (here) of this speaker's own, which starts
// as a copy of the stack it wears.
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

// setThemes writes the theme key (shared like any setting), or theme_here (this speaker's own;
// an empty list ends it), and points the animations at the result.
func (a *App) setThemes(ids []string, here bool) error {
	key := "theme"
	if here {
		key = "theme_here"
	}
	_, _, err := a.applyConfigKeys(map[string][]string{key: ids}, "set from the Packs page", "packs")
	a.refreshPackAnims()
	return err
}

// importPack copies a pack's moments for this display into the owner's own (custom) and puts
// custom on top, so editing starts from that theme.
func (a *App) importPack(id string) error {
	p := a.installedPacks()[id]
	if p == nil {
		return errors.New("no such pack")
	}
	for _, kind := range []string{"any", a.packDisplay()} {
		for name, shows := range p.Moments[kind] {
			if err := a.setMoment(name, shows); err != nil {
				return err
			}
		}
	}
	return a.applyPack(customPack, len(a.cfg().ThemesHere) > 0)
}

// exportCustom zips the owner's own theme as a pack: its moments, sounds and animations.
func (a *App) exportCustom(id, name string) ([]byte, error) {
	if !packID.MatchString(id) || id == customPack {
		return nil, fmt.Errorf("id %q: lower-case letters, digits and -, up to 40 (not %q)", id, customPack)
	}
	p := pack{ID: id, Name: cmp.Or(name, id), Moments: map[string]map[string][]show{}}
	if m := a.userMomentMap(); len(m) > 0 {
		p.Moments[cmp.Or(a.packDisplay(), "any")] = m // the moments were made for this display
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	dir := a.packDir(customPack)
	err := filepath.WalkDir(dir, func(fp string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, fp)
		rel = filepath.ToSlash(rel)
		if rel == "pack.json" {
			return nil
		}
		if strings.HasPrefix(rel, "anims/") {
			p.Displays = appendUnique(p.Displays, strings.Split(rel, "/")[1])
		}
		b, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(p.Displays) == 0 && a.packDisplay() != "" {
		p.Displays = []string{a.packDisplay()}
	}
	man, _ := json.MarshalIndent(p, "", "  ")
	w, err := zw.Create("pack.json")
	if err != nil {
		return nil, err
	}
	w.Write(append(man, '\n'))
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
		ids = append(ids, id)
	}
	sort.Strings(ids)
	ids = append([]string{customPack}, ids...)
	for _, id := range ids {
		v := packView{Position: slices.Index(stack, id)}
		if p := all[id]; p != nil {
			v.pack = *p
		} else {
			v.pack = pack{ID: customPack, Name: "Your own"}
		}
		v.pack.Moments = nil
		if id == customPack {
			for n := range a.userMomentMap() {
				v.Moments = append(v.Moments, n)
			}
		} else {
			for _, k := range []string{"any", kind} {
				for n := range all[id].Moments[k] {
					v.Moments = appendUnique(v.Moments, n)
				}
			}
		}
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
	_, shared := a.sharedGet("setting", "theme")
	return map[string]any{"packs": views, "stack": stack, "display": kind, "missing": missing,
		"here": len(a.cfg().ThemesHere) > 0, "shared_stack": withCustom(a.cfg().Themes), "shared": shared,
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
//	PUT    /api/packs                       install a pack (the zip as the body)
//	PUT    /api/packs/stack                 {"stack": [ids], "here": bool}: the theme key, top first
//	                                        (here: theme_here, this speaker's own; [] ends it)
//	POST   /api/packs/<id>/apply[?here=1]   put it on top
//	POST   /api/packs/<id>/import           its moments into custom, custom on top
//	DELETE /api/packs/<id>                  remove it
//	GET    /api/packs/custom/export?id=&name=  custom as a pack archive
//	PUT|DELETE /api/packs/custom/sounds/<name>  the owner's own sound (a WAV)
//	PUT|DELETE /api/packs/custom/anims/<name>   the owner's own animation for this display
func (a *App) apiPacks(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/packs"), "/"), "/")
	if parts[0] == "" {
		parts = nil
	}
	var err error
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
			if p, err = a.installPack(b); err == nil {
				a.log.Info("packs: installed", "pack", p.ID, "version", p.Version)
			}
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
	case len(parts) == 2 && r.Method == http.MethodPost && parts[1] == "import":
		err = a.importPack(parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		err = a.removePack(parts[0])
	case len(parts) == 2 && parts[0] == customPack && parts[1] == "export" && r.Method == http.MethodGet:
		b, err := a.exportCustom(r.URL.Query().Get("id"), r.URL.Query().Get("name"))
		if err != nil {
			writeErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+r.URL.Query().Get("id")+`.zip"`)
		w.Write(b)
		return
	case len(parts) == 3 && parts[0] == customPack && (parts[1] == "sounds" || parts[1] == "anims"):
		err = a.customAsset(r, parts[1], parts[2])
	default:
		writeErr(w, http.StatusNotFound, "no such packs call")
		return
	}
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a.packsNow())
}

// customAsset puts (PUT, the file as the body) or removes (DELETE) one of the owner's own sounds
// or animations in the custom pack.
func (a *App) customAsset(r *http.Request, kind, name string) error {
	if !packFileName.MatchString(name) {
		return errors.New("name: lower-case letters, digits, - and _, up to 40")
	}
	disp := a.packDisplay()
	rel := "sounds/" + name + ".wav"
	if kind == "anims" {
		if disp == "" {
			return errors.New("this speaker has no front display")
		}
		ext := ".led"
		if disp == "seg7" {
			ext = ".scled"
		}
		rel = "anims/" + disp + "/" + name + ext
	}
	dst := filepath.Join(a.packDir(customPack), filepath.FromSlash(rel))
	switch r.Method {
	case http.MethodDelete:
		if err := os.Remove(dst); err != nil {
			return errors.New("no such file")
		}
	case http.MethodPut:
		b, err := io.ReadAll(io.LimitReader(r.Body, maxPackFiles+1))
		if err != nil {
			return err
		}
		if len(b) > maxPackFiles {
			return fmt.Errorf("over %d MB", maxPackFiles>>20)
		}
		if err := checkPackFile(rel, b); err != nil {
			return err
		}
		if free := freeBytes(a.cfwFile("")); free >= 0 && int64(len(b)) > free-assetFreeKeep {
			return errors.New("not enough room on the speaker")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst+".new", b, 0o644); err != nil {
			return err
		}
		if err := os.Rename(dst+".new", dst); err != nil {
			return err
		}
	default:
		return errors.New("PUT or DELETE")
	}
	a.loadPacks() // drops the animation caches
	return nil
}
