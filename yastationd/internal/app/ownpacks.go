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
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The owner's packs (docs/asset-packs.md, "The owner's packs"): packs marked editable in their
// pack.json, made on the speaker (New pack, Copy to my pack; custom, "Your own", is the first one).
// They sit in the theme stack like any pack; the Moments page edits one of them, the Sounds page
// adds sounds and animations to one. Installed packs are read-only.

// packEditable: the pack is the owner's (custom always is, with or without a manifest yet).
func (a *App) packEditable(id string) bool {
	if id == customPack {
		return true
	}
	p := a.installedPacks()[id]
	return p != nil && p.Editable
}

// editablePacks lists the owner's packs: custom first, then the others by id.
func (a *App) editablePacks() []string {
	out := []string{customPack}
	for id, p := range a.installedPacks() {
		if p.Editable && id != customPack {
			out = append(out, id)
		}
	}
	slices.Sort(out[1:])
	return out
}

// editTarget is the pack the Moments page edits by default: the highest of the owner's packs in
// the stack, else custom.
func (a *App) editTarget() string {
	for _, id := range a.themeStack() {
		if a.packEditable(id) {
			return id
		}
	}
	return customPack
}

// momentKey is where an editable pack keeps this speaker's moments: its display kind ("any"
// without a display).
func (a *App) momentKey() string { return cmp.Or(a.packDisplay(), "any") }

// packMoment is a pack's own candidates for a moment on this speaker (its display kind, then any).
func (a *App) packMoment(id, name string) ([]show, bool) {
	p := a.installedPacks()[id]
	if p == nil {
		return nil, false
	}
	for _, k := range []string{a.packDisplay(), "any"} {
		if c, ok := p.Moments[k][name]; ok {
			return c, true
		}
	}
	return nil, false
}

// manifest reads a pack's pack.json from the disk (custom without one: a fresh "Your own").
func (a *App) manifest(id string) (*pack, error) {
	b, err := os.ReadFile(filepath.Join(a.packDir(id), "pack.json"))
	if errors.Is(err, os.ErrNotExist) && id == customPack {
		return &pack{ID: customPack, Name: "Your own", Editable: true}, nil
	}
	if err != nil {
		return nil, err
	}
	var p pack
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// writeManifest saves an editable pack's pack.json (through a temporary file) and reloads the
// packs.
func (a *App) writeManifest(p *pack) error {
	dir := a.packDir(p.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "pack.json")
	if err := os.WriteFile(path+".tmp", append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	a.loadPacks()
	return nil
}

// setPackMoment sets an editable pack's candidates for a moment on this speaker (nil: its entry
// goes, for this display and for any).
func (a *App) setPackMoment(id, name string, shows []show) error {
	if !a.packEditable(id) {
		return fmt.Errorf("%s is an installed pack: copy it to one of yours to change it", id)
	}
	if _, ok := momentByName[name]; !ok {
		return fmt.Errorf("no moment %q", name)
	}
	p, err := a.manifest(id)
	if err != nil {
		return err
	}
	if p.Moments == nil {
		p.Moments = map[string]map[string][]show{}
	}
	if shows == nil {
		for _, k := range []string{a.momentKey(), "any"} {
			delete(p.Moments[k], name)
			if len(p.Moments[k]) == 0 {
				delete(p.Moments, k)
			}
		}
	} else {
		k := a.momentKey()
		if p.Moments[k] == nil {
			p.Moments[k] = map[string][]show{}
		}
		p.Moments[k][name] = shows
	}
	return a.writeManifest(p)
}

var slugBad = regexp.MustCompile(`[^a-z0-9]+`)

// freeID is base made a pack id (lower-case letters, digits, -) that no pack has yet.
func (a *App) freeID(base string) string {
	s := strings.Trim(slugBad.ReplaceAllString(strings.ToLower(base), "-"), "-")
	if s == "" || !packID.MatchString(s) {
		s = "mine"
	}
	s = s[:min(len(s), 34)]
	id := s
	for n := 2; ; n++ {
		if _, err := os.Stat(a.packDir(id)); errors.Is(err, os.ErrNotExist) && id != customPack {
			return id
		}
		id = fmt.Sprintf("%s-%d", s, n)
	}
}

// wearOnTop puts id on top of the stack the speaker wears (theme_here when that is in force).
func (a *App) wearOnTop(id string) error {
	rest := slices.DeleteFunc(a.themeStack(), func(s string) bool { return s == id })
	return a.setThemes(append([]string{id}, rest...), len(a.cfg().ThemesHere) > 0)
}

// createPack makes a new, empty pack of the owner's, worn on top.
func (a *App) createPack(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "My pack"
	}
	id := a.freeID(name)
	if err := a.writeManifest(&pack{ID: id, Name: name, Editable: true}); err != nil {
		return "", err
	}
	return id, a.wearOnTop(id)
}

// renamePack renames one of the owner's packs (its id stays).
func (a *App) renamePack(id, name string) error {
	name = strings.TrimSpace(name)
	if !a.packEditable(id) || name == "" {
		return errors.New("only your own packs are renamed, to a name that is not empty")
	}
	p, err := a.manifest(id)
	if err != nil {
		return err
	}
	p.Name = name
	return a.writeManifest(p)
}

// copyPack makes an editable copy of a pack, with all its files: "<id>-mine", "<name> (mine)". It
// takes the original's place in the stack (the original stays installed, worn no more), or goes
// on top.
func (a *App) copyPack(id string) (string, error) {
	src := a.installedPacks()[id]
	if src == nil {
		return "", errors.New("no such pack")
	}
	nid := a.freeID(id + "-mine")
	tmp, err := os.MkdirTemp(a.cfwFile(packsDir), ".copy-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	from := a.packDir(id)
	err = filepath.WalkDir(from, func(fp string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, fp)
		dst := filepath.Join(tmp, rel)
		if e.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(fp)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, a.packDir(nid)); err != nil {
		return "", err
	}
	p := *src
	p.ID, p.Name, p.Editable = nid, src.Name+" (mine)", true
	if err := a.writeManifest(&p); err != nil {
		return "", err
	}
	stack := a.themeStack()
	if i := slices.Index(stack, id); i >= 0 {
		stack[i] = nid
		return nid, a.setThemes(stack, len(a.cfg().ThemesHere) > 0)
	}
	return nid, a.wearOnTop(nid)
}

// exportPack zips a pack (one of the owner's, or an installed one) for another speaker: its
// manifest, without the editable mark (installed elsewhere it is read-only), its sounds and
// animations. id and name, when given, rename the copy (custom must be given an id).
func (a *App) exportPack(src, id, name string) ([]byte, error) {
	p, err := a.manifest(src)
	if err != nil || (src != customPack && a.installedPacks()[src] == nil) {
		return nil, errors.New("no such pack")
	}
	p.ID, p.Name, p.Editable = cmp.Or(id, p.ID), cmp.Or(name, p.Name), false
	if !packID.MatchString(p.ID) || p.ID == customPack {
		return nil, fmt.Errorf("id %q: lower-case letters, digits and -, up to 40 (not %q)", p.ID, customPack)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	dir := a.packDir(src)
	err = filepath.WalkDir(dir, func(fp string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, fp)
		rel = filepath.ToSlash(rel)
		if !strings.HasPrefix(rel, "sounds/") && !strings.HasPrefix(rel, "anims/") {
			return nil // the manifest is written below; notes, sources and previews stay home
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

// packAsset puts (PUT, the file as the body) or removes (DELETE) a sound or an animation of one of
// the owner's packs.
func (a *App) packAsset(r *http.Request, id, kind, name string) error {
	if !a.packEditable(id) {
		return fmt.Errorf("%s is an installed pack: copy it to one of yours to change it", id)
	}
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
	dst := filepath.Join(a.packDir(id), filepath.FromSlash(rel))
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
	if _, err := os.Stat(filepath.Join(a.packDir(id), "pack.json")); errors.Is(err, os.ErrNotExist) {
		if p, err := a.manifest(id); err == nil {
			return a.writeManifest(p) // custom's first file: it becomes a pack with a manifest
		}
	}
	a.loadPacks() // drops the animation caches
	return nil
}
