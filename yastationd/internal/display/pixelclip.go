package display

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// PixelClip is a dot-matrix animation in the firmware's text pattern format (the .led files
// under ledpatterns/screen/, docs/boards/orion.md "LEDs"): one frame per line, RRGGBB per pixel
// row-major and the frame's duration in ms, with directive lines in between ("loop", "smooth N",
// "no_size_optimization"). "loop" marks where the repeating part starts: on the first line the
// whole file loops, further down the frames above it are an intro played once (call_incoming,
// configure). The frames before LoopFrom are that intro; several files can be chained the same way.
type PixelClip struct {
	Name     string
	Loop     bool
	LoopFrom int
	Frames   []PixelFrame
}

// PixelFrame is one frame, gamma-corrected for the glass, at full brightness.
type PixelFrame struct {
	Px  []hw.RGB
	Dur time.Duration
}

// ParseLed reads one pattern file for a w x h screen and corrects it with gamma (the firmware
// applies the screen's gamma when it plays a pattern; the files hold uncorrected levels).
// "smooth" (interpolation between frames) is not implemented: the screen patterns don't use it.
func ParseLed(name string, r io.Reader, w, h int, gamma float64) (PixelClip, error) {
	c := PixelClip{Name: name}
	lut := gammaLUT(gamma)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		tok := strings.Fields(sc.Text())
		if len(tok) == 0 {
			continue
		}
		if !isHexRGB(tok[0]) { // a directive
			if tok[0] == "loop" {
				c.Loop, c.LoopFrom = true, len(c.Frames)
			}
			continue
		}
		if len(tok) != w*h+1 {
			return PixelClip{}, fmt.Errorf("%s:%d: %d fields, want %d pixels and a duration", name, n, len(tok), w*h)
		}
		ms, err := strconv.Atoi(tok[w*h])
		if err != nil || ms < 0 {
			return PixelClip{}, fmt.Errorf("%s:%d: bad duration %q", name, n, tok[w*h])
		}
		f := PixelFrame{Px: make([]hw.RGB, w*h), Dur: time.Duration(ms) * time.Millisecond}
		for i := range f.Px {
			v, err := strconv.ParseUint(tok[i], 16, 32)
			if err != nil || !isHexRGB(tok[i]) {
				return PixelClip{}, fmt.Errorf("%s:%d: pixel %d: bad colour %q", name, n, i, tok[i])
			}
			f.Px[i] = hw.RGB{R: lut[v>>16&0xff], G: lut[v>>8&0xff], B: lut[v&0xff]}
		}
		c.Frames = append(c.Frames, f)
	}
	if err := sc.Err(); err != nil {
		return PixelClip{}, err
	}
	if len(c.Frames) == 0 {
		return PixelClip{}, fmt.Errorf("%s: no frames", name)
	}
	return c, nil
}

func isHexRGB(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func gammaLUT(g float64) [256]uint8 {
	var t [256]uint8
	for i := range t {
		if g <= 0 || g == 1 {
			t[i] = uint8(i)
			continue
		}
		t[i] = uint8(math.Round(255 * math.Pow(float64(i)/255, g)))
	}
	return t
}

// Chain plays the clips one after another; the last one decides whether (and what) repeats.
func Chain(name string, parts ...PixelClip) PixelClip {
	c := PixelClip{Name: name}
	for i, p := range parts {
		if i == len(parts)-1 {
			c.LoopFrom = len(c.Frames) + p.LoopFrom
			c.Loop = p.Loop
		}
		c.Frames = append(c.Frames, p.Frames...)
	}
	return c
}

// Blank reports whether no frame lights anything.
func (c PixelClip) Blank() bool {
	for _, f := range c.Frames {
		for _, p := range f.Px {
			if p != (hw.RGB{}) {
				return false
			}
		}
	}
	return true
}

func (c PixelClip) Looping() bool { return c.Loop }

func (c PixelClip) Duration() time.Duration { return sumDur(c.Frames) }

func sumDur(fs []PixelFrame) time.Duration {
	var d time.Duration
	for _, f := range fs {
		d += f.Dur
	}
	return d
}

// Play renders the clip; brightness scales every pixel, so it follows the display brightness.
func (c PixelClip) Play(loop, hold bool, brightness func() int) RenderFunc[FrontFrame] {
	loop = loop && !hold
	intro := sumDur(c.Frames[:c.LoopFrom])
	body := sumDur(c.Frames[c.LoopFrom:])
	total := intro + body
	var start time.Time
	return func(now time.Time, _ FrontFrame) (FrontFrame, bool) {
		if start.IsZero() {
			start = now
		}
		el := now.Sub(start)
		switch {
		case total <= 0:
			return c.frame(0, brightness()), !loop && !hold
		case el >= total && loop && body > 0:
			el = intro + (el-intro)%body
		case el >= total:
			return c.frame(len(c.Frames)-1, brightness()), !hold
		}
		for i, f := range c.Frames {
			if el < f.Dur {
				return c.frame(i, brightness()), false
			}
			el -= f.Dur
		}
		return c.frame(len(c.Frames)-1, brightness()), false
	}
}

func (c PixelClip) frame(i, brightness int) FrontFrame {
	px := make([]hw.RGB, len(c.Frames[i].Px))
	k := min(max(brightness, 0), 255)
	for j, p := range c.Frames[i].Px {
		px[j] = p.Scale(k)
	}
	return FrontFrame{Px: px}
}

// PixelLibrary finds screen patterns by name (a path relative to one of Dirs, without ".led"):
// in the CFW's own directory first, then the firmware's. A name "a+b" chains a and b. Parsed
// patterns are cached; a missing one is looked up again next time.
type PixelLibrary struct {
	Dirs  []string
	W, H  int
	Gamma float64

	mu    sync.Mutex
	cache map[string]PixelClip
}

// Load returns the first pattern found among names that lights something.
func (l *PixelLibrary) Load(names ...string) (PixelClip, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cache == nil {
		l.cache = map[string]PixelClip{}
	}
	for _, n := range names {
		if c, ok := l.cache[n]; ok {
			return c, true
		}
		if c, err := l.chain(n); err == nil && !c.Blank() {
			l.cache[n] = c
			return c, true
		}
	}
	return PixelClip{}, false
}

func (l *PixelLibrary) chain(name string) (PixelClip, error) {
	var parts []PixelClip
	for _, n := range strings.Split(name, "+") {
		c, err := l.find(n)
		if err != nil {
			return PixelClip{}, err
		}
		parts = append(parts, c)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return Chain(name, parts...), nil
}

func (l *PixelLibrary) find(name string) (PixelClip, error) {
	if !validAnimName(name) {
		return PixelClip{}, fmt.Errorf("bad pattern name %q", name)
	}
	for _, d := range l.Dirs {
		f, err := os.Open(filepath.Join(d, filepath.FromSlash(name)+".led"))
		if err != nil {
			continue
		}
		c, err := ParseLed(name, f, l.W, l.H, l.Gamma)
		f.Close()
		if err == nil {
			return c, nil
		}
	}
	return PixelClip{}, fmt.Errorf("pattern %q not found", name)
}

// Names lists every pattern in the directories (relative names), sorted, without duplicates.
func (l *PixelLibrary) Names() []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range l.Dirs {
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(p, ".led") {
				return nil
			}
			rel, err := filepath.Rel(d, p)
			if err != nil {
				return nil
			}
			n := strings.TrimSuffix(filepath.ToSlash(rel), ".led")
			if validAnimName(n) && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}
