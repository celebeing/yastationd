// Command clipconv converts the stock firmware's binary clock clips (.binscled, already
// zstd-decompressed) to the text .scled form yastationd reads. Host-side, used by `make clips`.
//
//	clipconv in.binscled > out.scled
//	clipconv -check a.scled b.scled …   (parse only; exit 1 on the first bad file)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

func main() {
	check := flag.Bool("check", false, "only parse the given .scled files")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: clipconv in.binscled > out.scled | clipconv -check f.scled…")
		os.Exit(2)
	}
	if *check {
		for _, p := range flag.Args() {
			f, err := os.Open(p)
			if err != nil {
				fail(err)
			}
			c, err := display.ParseScled(filepath.Base(p), f)
			f.Close()
			if err != nil {
				fail(err)
			}
			fmt.Printf("%s: %d frames, %v, loop=%v\n", p, len(c.Frames), c.Duration(), c.Loop)
		}
		return
	}
	p := flag.Arg(0)
	b, err := os.ReadFile(p)
	if err != nil {
		fail(err)
	}
	c, err := display.ParseBinscled(strings.TrimSuffix(filepath.Base(p), ".binscled"), b)
	if err != nil {
		fail(err)
	}
	os.Stdout.Write(display.FormatScled(c))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "clipconv:", err)
	os.Exit(1)
}
