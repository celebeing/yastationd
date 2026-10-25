package display

import (
	"bufio"
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Clip is a 7-segment animation in the stock firmware's clock-clip format
// (docs/display-expressions.md): frames of per-segment levels with a duration each.
type Clip struct {
	Name   string
	Loop   bool
	Frames []ClipFrame
}

// ClipFrame is one frame: levels of segments A..G of digits 0..3, the four extra LEDs
// (DH, DL, DM, DEGREE) and how long the frame stays.
type ClipFrame struct {
	Seg   [hw.Digits][7]uint8
	Extra [4]uint8
	Dur   time.Duration
}

// Blank reports whether no frame lights anything (pickle's stock voice clips are a single dark
// frame: its firmware blanks the clock while the assistant talks).
func (c Clip) Blank() bool {
	for _, f := range c.Frames {
		for _, d := range f.Seg {
			for _, v := range d {
				if v != 0 {
					return false
				}
			}
		}
		for _, v := range f.Extra {
			if v != 0 {
				return false
			}
		}
	}
	return true
}

// normalized scales the clip so its brightest element is 255. Stock clips peak at very
// different levels (the voice ones barely reach a third), and on top of the display's own
// brightness a dim one would vanish; the animation's relative levels stay as drawn.
func (c Clip) normalized() Clip {
	peak := uint8(0)
	for _, f := range c.Frames {
		for _, d := range f.Seg {
			for _, v := range d {
				peak = max(peak, v)
			}
		}
		for _, v := range f.Extra {
			peak = max(peak, v)
		}
	}
	if peak == 0 || peak == 255 {
		return c
	}
	out := c
	out.Frames = make([]ClipFrame, len(c.Frames))
	k := func(v uint8) uint8 { return uint8(int(v) * 255 / int(peak)) }
	for i, f := range c.Frames {
		for d := range f.Seg {
			for s := range f.Seg[d] {
				f.Seg[d][s] = k(f.Seg[d][s])
			}
		}
		for x := range f.Extra {
			f.Extra[x] = k(f.Extra[x])
		}
		out.Frames[i] = f
	}
	return out
}

// Duration is one pass through the clip.
func (c Clip) Duration() time.Duration {
	var d time.Duration
	for _, f := range c.Frames {
		d += f.Dur
	}
	return d
}

// matrixFrame turns a clip frame into a graded MatrixFrame; the colon is DH and DL together.
func (f ClipFrame) matrixFrame(brightness int) MatrixFrame {
	return MatrixFrame{Brightness: brightness, Graded: true, Level: hw.SegLevels{
		Seg:    f.Seg,
		Colon:  max(f.Extra[0], f.Extra[1]),
		DM:     f.Extra[2],
		Degree: f.Extra[3],
	}}
}

// ParseScled reads the text clip format: an optional first line "loop", then one frame per line,
// "A B C D E F G | … | … | … | x0 x1 x2 x3 | ms" (four digits, the extras, the duration).
func ParseScled(name string, r io.Reader) (Clip, error) {
	c := Clip{Name: name}
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" {
			continue
		}
		if s == "loop" {
			c.Loop = true
			continue
		}
		parts := strings.Split(s, "|")
		if len(parts) != 6 {
			return Clip{}, fmt.Errorf("%s:%d: want 6 |-separated fields, got %d", name, line, len(parts))
		}
		var f ClipFrame
		for d := 0; d < hw.Digits; d++ {
			if err := parseLevels(parts[d], f.Seg[d][:]); err != nil {
				return Clip{}, fmt.Errorf("%s:%d: digit %d: %w", name, line, d, err)
			}
		}
		if err := parseLevels(parts[4], f.Extra[:]); err != nil {
			return Clip{}, fmt.Errorf("%s:%d: extras: %w", name, line, err)
		}
		ms, err := strconv.Atoi(strings.TrimSpace(parts[5]))
		if err != nil || ms < 0 {
			return Clip{}, fmt.Errorf("%s:%d: bad duration %q", name, line, parts[5])
		}
		f.Dur = time.Duration(ms) * time.Millisecond
		c.Frames = append(c.Frames, f)
	}
	if err := sc.Err(); err != nil {
		return Clip{}, err
	}
	if len(c.Frames) == 0 {
		return Clip{}, fmt.Errorf("%s: no frames", name)
	}
	return c, nil
}

func parseLevels(s string, out []uint8) error {
	f := strings.Fields(s)
	if len(f) != len(out) {
		return fmt.Errorf("want %d levels, got %d", len(out), len(f))
	}
	for i, v := range f {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 255 {
			return fmt.Errorf("bad level %q", v)
		}
		out[i] = uint8(n)
	}
	return nil
}

// binscledFrame is the binary clip frame: [flag][32 levels][duration u16 big-endian, ms].
const binscledFrame = 35

// ParseBinscled reads the binary clip format of firmware 0.369 (already zstd-decompressed). The
// flag byte is 1 on the first frame of a looping clip.
func ParseBinscled(name string, b []byte) (Clip, error) {
	if len(b) == 0 || len(b)%binscledFrame != 0 {
		return Clip{}, fmt.Errorf("%s: %d bytes is not a whole number of %d-byte frames", name, len(b), binscledFrame)
	}
	c := Clip{Name: name, Loop: b[0] == 1}
	for off := 0; off < len(b); off += binscledFrame {
		r := b[off : off+binscledFrame]
		var f ClipFrame
		for d := 0; d < hw.Digits; d++ {
			copy(f.Seg[d][:], r[1+d*7:1+d*7+7])
		}
		copy(f.Extra[:], r[29:33])
		f.Dur = time.Duration(int(r[33])<<8|int(r[34])) * time.Millisecond
		c.Frames = append(c.Frames, f)
	}
	return c, nil
}

// FormatScled writes c in the text format (what `make clips` stores on the device).
func FormatScled(c Clip) []byte {
	var b bytes.Buffer
	if c.Loop {
		b.WriteString("loop\n")
	}
	for _, f := range c.Frames {
		for d := 0; d < hw.Digits; d++ {
			writeLevels(&b, f.Seg[d][:])
			b.WriteString("| ")
		}
		writeLevels(&b, f.Extra[:])
		fmt.Fprintf(&b, "| %d\n", f.Dur.Milliseconds())
	}
	return b.Bytes()
}

func writeLevels(b *bytes.Buffer, l []uint8) {
	for _, v := range l {
		fmt.Fprintf(b, "%-3d ", v)
	}
}

// Looping: the clip is drawn to repeat (the "loop" line).
func (c Clip) Looping() bool { return c.Loop }

// Play makes the clip a front-display Anim.
func (c Clip) Play(loop, hold bool, brightness func() int) RenderFunc[FrontFrame] {
	return SegLayer(playClip(c, loop && !hold, hold, brightness))
}

// PlayClip renders c from the first frame it is asked for. With loop it repeats until the
// layer is removed; otherwise the layer pops after one pass. Brightness scales every level,
// so a clip follows the clock's auto-brightness.
func PlayClip(c Clip, loop bool, brightness func() int) RenderFunc[MatrixFrame] {
	return playClip(c, loop, false, brightness)
}

// HoldClip plays c once and then keeps its last frame until the layer's TTL removes it (a short
// clip shown "for 5 s").
func HoldClip(c Clip, brightness func() int) RenderFunc[MatrixFrame] {
	return playClip(c, false, true, brightness)
}

func playClip(c Clip, loop, hold bool, brightness func() int) RenderFunc[MatrixFrame] {
	c = c.normalized()
	total := c.Duration()
	var start time.Time
	return func(now time.Time, _ MatrixFrame) (MatrixFrame, bool) {
		if start.IsZero() {
			start = now
		}
		el := now.Sub(start)
		if total <= 0 {
			return c.Frames[0].matrixFrame(brightness()), !loop && !hold
		}
		if el >= total {
			if !loop {
				return c.Frames[len(c.Frames)-1].matrixFrame(brightness()), !hold
			}
			el %= total
		}
		for _, f := range c.Frames {
			if el < f.Dur {
				return f.matrixFrame(brightness()), false
			}
			el -= f.Dur
		}
		return c.Frames[len(c.Frames)-1].matrixFrame(brightness()), false
	}
}

//go:embed clips/*.scled
var ownClips embed.FS

// ClipLibrary finds clips by name: in the given directories first (the CFW's converted clips,
// then the firmware's own), then among our embedded clips. Parsed clips are cached; a missing
// one is looked up again next time (`make clips` may have run since).
type ClipLibrary struct {
	Dirs []string

	mu    sync.Mutex
	cache map[string]Clip
}

// Load returns the first clip found among names (aliases across firmware versions) that lights
// something: a blank clip is skipped in favour of the next name.
func (l *ClipLibrary) Load(names ...string) (Clip, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cache == nil {
		l.cache = map[string]Clip{}
	}
	for _, n := range names {
		if c, ok := l.cache[n]; ok {
			return c, true
		}
		if c, err := l.find(n); err == nil && !c.Blank() {
			l.cache[n] = c
			return c, true
		}
	}
	return Clip{}, false
}

func (l *ClipLibrary) find(name string) (Clip, error) {
	if strings.ContainsAny(name, "/\\") || name == "" || name[0] == '.' {
		return Clip{}, fmt.Errorf("bad clip name %q", name)
	}
	for _, d := range l.Dirs {
		if f, err := os.Open(filepath.Join(d, name+".scled")); err == nil {
			c, err := ParseScled(name, f)
			f.Close()
			if err == nil {
				return c, nil
			}
		}
	}
	f, err := ownClips.Open("clips/" + name + ".scled")
	if err != nil {
		return Clip{}, err
	}
	defer f.Close()
	return ParseScled(name, f)
}

// Names lists every clip available: the directories' and ours, sorted, without duplicates.
func (l *ClipLibrary) Names() []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, d := range l.Dirs {
		m, _ := filepath.Glob(filepath.Join(d, "*.scled"))
		for _, p := range m {
			add(strings.TrimSuffix(filepath.Base(p), ".scled"))
		}
	}
	m, _ := fs.Glob(ownClips, "clips/*.scled")
	for _, p := range m {
		add(strings.TrimSuffix(filepath.Base(p), ".scled"))
	}
	sort.Strings(out)
	return out
}

// RawFrame is one hand-made seg7 frame from HA (display/cmd "frames"): either Text (rendered
// like the clock's text, ':' and '.' included) or Seg, one string per digit naming its lit
// segments ("ABFG" = the upper box), plus Colon / Dot / Degree. Ms is its duration.
type RawFrame struct {
	Text   string   `json:"text,omitempty"`
	Seg    []string `json:"seg,omitempty"`
	Colon  bool     `json:"colon,omitempty"`
	Dot    bool     `json:"dot,omitempty"`
	Degree bool     `json:"degree,omitempty"`
	Ms     int      `json:"ms"`
}

// maxRawFrames bounds a clip sent over MQTT.
const maxRawFrames = 500

// ClipFromRaw builds a clip from HA frames (full level for every lit element).
func ClipFromRaw(frames []RawFrame) (Clip, error) {
	if len(frames) == 0 || len(frames) > maxRawFrames {
		return Clip{}, fmt.Errorf("want 1..%d frames, got %d", maxRawFrames, len(frames))
	}
	c := Clip{Name: "raw"}
	for i, r := range frames {
		if r.Ms <= 0 || r.Ms > 600000 {
			return Clip{}, fmt.Errorf("frame %d: ms must be 1..600000", i)
		}
		var f ClipFrame
		f.Dur = time.Duration(r.Ms) * time.Millisecond
		masks := [hw.Digits]uint8{}
		colon, dot := r.Colon, r.Dot
		if r.Text != "" {
			t := Text(r.Text, 255)
			masks, colon, dot = t.Seg, colon || t.Colon, dot || t.DM
		}
		if len(r.Seg) > hw.Digits {
			return Clip{}, fmt.Errorf("frame %d: %d digits, the display has %d", i, len(r.Seg), hw.Digits)
		}
		for d, s := range r.Seg {
			for _, ch := range strings.ToUpper(s) {
				if ch < 'A' || ch > 'G' {
					return Clip{}, fmt.Errorf("frame %d digit %d: segment %q is not A..G", i, d, ch)
				}
				masks[d] |= 1 << (ch - 'A')
			}
		}
		for d := 0; d < hw.Digits; d++ {
			for s := 0; s < 7; s++ {
				if masks[d]&(1<<s) != 0 {
					f.Seg[d][s] = 255
				}
			}
		}
		if colon {
			f.Extra[0], f.Extra[1] = 255, 255
		}
		if dot {
			f.Extra[2] = 255
		}
		if r.Degree {
			f.Extra[3] = 255
		}
		c.Frames = append(c.Frames, f)
	}
	return c, nil
}
