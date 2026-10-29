// Command packtool builds and checks an asset pack (docs/asset-packs.md) on the host, with the
// same checks a speaker runs before it installs one.
//
//	packtool <pack-dir>  [-o out.zip]   zip a pack directory (pack.json at its root) and check it
//	packtool <pack.zip>                 check an archive
//	packtool -get <field> <pack-dir>    print a manifest field (id, name, version, author, source,
//	                                    license; displays comma-separated): scripts need no jq
//	packtool -index <url> <pack-dir>... print packs.json, an index of the packs' release assets at
//	                                    <url>/<id>-v<version>/<id>-<version>.zip
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/app"
)

func main() {
	out := flag.String("o", "", "the archive to write (a directory's default: <id>.zip)")
	get := flag.String("get", "", "print this manifest field of <pack-dir> (id, name, version, author, source, license, displays)")
	index := flag.String("index", "", "print packs.json for the <pack-dir>s, their assets under this release download URL")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: packtool [-o out.zip] <pack-dir | pack.zip>\n       packtool -get <field> <pack-dir>\n       packtool -index <download-url> <pack-dir>...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *get != "" {
		if flag.NArg() != 1 {
			flag.Usage()
			os.Exit(2)
		}
		m, err := readManifest(flag.Arg(0))
		if err != nil {
			fail(err)
		}
		fmt.Println(m.field(*get))
		return
	}
	if *index != "" {
		printIndex(strings.TrimRight(*index, "/"), flag.Args())
		return
	}
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	src := flag.Arg(0)
	st, err := os.Stat(src)
	if err != nil {
		fail(err)
	}
	var data []byte
	if st.IsDir() {
		data, err = zipDir(src)
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		fail(err)
	}
	id, name, n, err := app.CheckPack(data)
	if err != nil {
		fail(err)
	}
	fmt.Printf("%s (%s): %d files, %d KB, ok\n", id, name, n, len(data)>>10)
	if st.IsDir() {
		dst := *out
		if dst == "" {
			dst = id + ".zip"
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			fail(err)
		}
		fmt.Println("wrote", dst)
	}
}

// zipDir zips dir's files under their paths in it, sorted, leaving dot files out.
func zipDir(dir string) ([]byte, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(e.Name(), ".") && p != dir {
			if e.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !e.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(n)))
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// manifest is the part of pack.json the release scripts read.
type manifest struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Author   string   `json:"author"`
	Source   string   `json:"source"`
	License  string   `json:"license"`
	Displays []string `json:"displays"`
}

// readManifest reads <pack-dir>/pack.json (or a pack.json path itself).
func readManifest(path string) (manifest, error) {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "pack.json")
	}
	var m manifest
	b, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(b, &m)
	}
	if err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m, nil
}

func (m manifest) field(f string) string {
	switch f {
	case "id":
		return m.ID
	case "name":
		return m.Name
	case "version":
		return m.Version
	case "author":
		return m.Author
	case "source":
		return m.Source
	case "license":
		return m.License
	case "displays":
		return strings.Join(m.Displays, ",")
	}
	fail(fmt.Errorf("no field %q (id, name, version, author, source, license, displays)", f))
	return ""
}

// printIndex prints packs.json: each pack's current version and its release asset, by id.
func printIndex(base string, dirs []string) {
	type entry struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Version  string   `json:"version"`
		Displays []string `json:"displays"`
		Tag      string   `json:"tag"`
		Asset    string   `json:"asset"`
		URL      string   `json:"url"`
	}
	list := []entry{}
	for _, d := range dirs {
		m, err := readManifest(d)
		if err != nil {
			fail(err)
		}
		tag, asset := m.ID+"-v"+m.Version, m.ID+"-"+m.Version+".zip"
		list = append(list, entry{m.ID, m.Name, m.Version, append([]string{}, m.Displays...), tag, asset, base + "/" + tag + "/" + asset})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, _ := json.MarshalIndent(map[string]any{"packs": list}, "", "  ")
	fmt.Println(string(b))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "packtool:", err)
	os.Exit(1)
}
