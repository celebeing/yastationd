// Command packtool builds and checks an asset pack (docs/asset-packs.md) on the host, with the
// same checks a speaker runs before it installs one.
//
//	packtool <pack-dir>  [-o out.zip]   zip a pack directory (pack.json at its root) and check it
//	packtool <pack.zip>                 check an archive
package main

import (
	"archive/zip"
	"bytes"
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
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: packtool [-o out.zip] <pack-dir | pack.zip>")
		flag.PrintDefaults()
	}
	flag.Parse()
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

func fail(err error) {
	fmt.Fprintln(os.Stderr, "packtool:", err)
	os.Exit(1)
}
