// Package hw talks to the Station's hardware: LED sysfs nodes, evdev, ALSA tools, IIO.
package hw

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ledNode writes a sysfs LED brightness only when the value changes. The first write reads the
// current value so a restart does not blink everything (the "flicker-free rule").
type ledNode struct {
	path  string
	known bool
	cur   int
}

func (n *ledNode) set(v int) error {
	if !n.known {
		if b, err := os.ReadFile(n.path); err == nil {
			if x, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				n.cur, n.known = x, true
			}
		}
	}
	if n.known && n.cur == v {
		return nil
	}
	if err := os.WriteFile(n.path, []byte(strconv.Itoa(v)), 0o644); err != nil {
		n.known = false
		return err
	}
	n.cur, n.known = v, true
	return nil
}

const ledBase = "/sys/class/leds"

func ledPath(root, name string) string {
	return filepath.Join(root+ledBase, name, "brightness")
}

// setTriggerNone takes an LED away from its kernel trigger (the ring defaults to ledfb-logo).
func setTriggerNone(root, name string) error {
	p := filepath.Join(root+ledBase, name, "trigger")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if strings.Contains(string(b), "[none]") {
		return nil
	}
	return os.WriteFile(p, []byte("none"), 0o644)
}

type RGB struct{ R, G, B uint8 }

func (c RGB) Scale(k int) RGB {
	return RGB{uint8(int(c.R) * k / 255), uint8(int(c.G) * k / 255), uint8(int(c.B) * k / 255)}
}

func (c RGB) Hex() string { return fmt.Sprintf("%02x%02x%02x", c.R, c.G, c.B) }

// RGBW is one red/green/blue/white pixel.
type RGBW struct{ R, G, B, W uint8 }

func (c RGBW) Scale(k int) RGBW {
	return RGBW{
		R: uint8(int(c.R) * k / 255),
		G: uint8(int(c.G) * k / 255),
		B: uint8(int(c.B) * k / 255),
		W: uint8(int(c.W) * k / 255),
	}
}

func (c RGBW) Hex() string { return fmt.Sprintf("%02x%02x%02x%02x", c.R, c.G, c.B, c.W) }

// Ring is an RGB LED ring of n pixels, one sysfs LED per pixel per colour. Names come from a
// pattern with {color} and {i}, e.g. "bergamot:{color}:logo{i}"; colors are the sysfs colour
// names in R,G,B order. A nil *Ring (no ring on this board) shows nothing.
type Ring struct {
	px [][3]ledNode
}

func NewRing(root string, pixels int, pattern string, colors []string) (*Ring, error) {
	if len(colors) != 3 {
		return nil, fmt.Errorf("ring: need 3 colours, got %d", len(colors))
	}
	r := &Ring{px: make([][3]ledNode, pixels)}
	for i := 0; i < pixels; i++ {
		for c, col := range colors {
			name := strings.NewReplacer("{color}", col, "{i}", strconv.Itoa(i)).Replace(pattern)
			if err := setTriggerNone(root, name); err != nil {
				return nil, fmt.Errorf("ring %s: %w", name, err)
			}
			r.px[i][c].path = ledPath(root, name)
		}
	}
	return r, nil
}

// Pixels is the ring size (0 for no ring).
func (r *Ring) Pixels() int {
	if r == nil {
		return 0
	}
	return len(r.px)
}

func (r *Ring) Show(f []RGB) error {
	if r == nil {
		return nil
	}
	var first error
	for i, c := range f {
		if i >= len(r.px) {
			break
		}
		for ch, v := range []uint8{c.R, c.G, c.B} {
			if err := r.px[i][ch].set(int(v)); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// Ambilight is a logical RGBW strip. Each position may contain multiple physical dies;
// Show writes every die identically. Names come from a pattern with {color}, {i}, and {die}.
// A nil *Ambilight (no rear strip on this board) is a no-op.
//
// With a framebuffer (UseFB), R, G and B go through it instead — one write per frame instead of
// one per LED, which on orion is 3.5 ms against 25–34 ms for a full frame — and only W stays on
// sysfs (the framebuffer has no white).
type Ambilight struct {
	px [][][]ledNode // position -> die -> R,G,B,W

	fb      *os.File
	fbBuf   []byte
	fbLast  []RGBW
	fbDirty bool // the last frame was written once: write it again to latch it
}

func NewAmbilight(root string, pixels int, pattern string, colors, dies []string) (*Ambilight, error) {
	if len(colors) != 4 {
		return nil, fmt.Errorf("ambilight: need 4 colours, got %d", len(colors))
	}
	if len(dies) == 0 {
		return nil, fmt.Errorf("ambilight: need at least one die")
	}
	a := &Ambilight{px: make([][][]ledNode, pixels)}
	for i := range pixels {
		a.px[i] = make([][]ledNode, len(dies))
		for d, die := range dies {
			a.px[i][d] = make([]ledNode, len(colors))
			for c, col := range colors {
				name := strings.NewReplacer(
					"{color}", col,
					"{i}", strconv.Itoa(i),
					"{die}", die,
				).Replace(pattern)
				if err := setTriggerNone(root, name); err != nil {
					return nil, fmt.Errorf("ambilight %s: %w", name, err)
				}
				a.px[i][d][c].path = ledPath(root, name)
			}
		}
	}
	return a, nil
}

// UseFB drives R, G and B through the ledfb framebuffer named name (fbdev "name", e.g.
// orion's "ambilight"): the RGB LEDs get its trigger (ledfb-<name>), W stays on sysfs. The
// layout is orion's: per position B, G, R as 16-bit little-endian, the LED level being half the
// value (510 = full). Without the framebuffer it returns an error and Show keeps to sysfs.
func (a *Ambilight) UseFB(root, name, pattern string, colors, dies []string) error {
	dev := ""
	for i := 0; i < 16 && dev == ""; i++ {
		b, err := os.ReadFile(root + "/sys/class/graphics/fb" + strconv.Itoa(i) + "/name")
		if err == nil && strings.TrimSpace(string(b)) == name {
			dev = root + "/dev/fb" + strconv.Itoa(i)
		}
	}
	if dev == "" {
		return fmt.Errorf("ambilight: no framebuffer named %q", name)
	}
	f, err := os.OpenFile(dev, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	for i := range a.px {
		for _, die := range dies {
			for _, col := range colors[:3] {
				led := strings.NewReplacer("{color}", col, "{i}", strconv.Itoa(i), "{die}", die).Replace(pattern)
				tp := filepath.Join(root+ledBase, led, "trigger")
				if err := os.WriteFile(tp, []byte("ledfb-"+name), 0o644); err != nil {
					f.Close()
					return fmt.Errorf("ambilight %s: trigger: %w", led, err)
				}
			}
		}
	}
	a.fb, a.fbBuf = f, make([]byte, 6*len(a.px))
	return nil
}

func (a *Ambilight) Pixels() int {
	if a == nil {
		return 0
	}
	return len(a.px)
}

func (a *Ambilight) Show(f []RGBW) error {
	if a == nil {
		return nil
	}
	var first error
	if a.fb != nil {
		first = a.showFB(f)
	}
	for i, color := range f {
		if i >= len(a.px) {
			break
		}
		channels := [4]uint8{color.R, color.G, color.B, color.W}
		for d := range a.px[i] {
			for c, value := range channels {
				if a.fb != nil && c < 3 {
					continue // R, G, B went through the framebuffer
				}
				if err := a.px[i][d][c].set(int(value)); err != nil && first == nil {
					first = err
				}
			}
		}
	}
	return first
}

// showFB writes R, G and B when the frame changed, and once more after that (like the screen,
// the ledfb glass may take a frame only on the next write).
func (a *Ambilight) showFB(f []RGBW) error {
	same := len(f) == len(a.fbLast)
	for i := 0; same && i < len(f); i++ {
		same = f[i].R == a.fbLast[i].R && f[i].G == a.fbLast[i].G && f[i].B == a.fbLast[i].B
	}
	if same && !a.fbDirty {
		return nil
	}
	a.fbDirty = !same
	a.fbLast = append(a.fbLast[:0], f...)
	for i := range a.px {
		var c RGBW
		if i < len(f) {
			c = f[i]
		}
		for k, v := range [3]uint8{c.B, c.G, c.R} {
			x := uint16(v) * 2
			a.fbBuf[6*i+2*k], a.fbBuf[6*i+2*k+1] = byte(x), byte(x>>8)
		}
	}
	_, err := a.fb.WriteAt(a.fbBuf, 0)
	return err
}

// 7-segment bits: A=top, B=top-right, C=bottom-right, D=bottom, E=bottom-left, F=top-left, G=middle.
const (
	SegA = 1 << iota
	SegB
	SegC
	SegD
	SegE
	SegF
	SegG
)

const Digits = 4

// MatrixFrame is one picture of the front clock: 4 seven-segment digits (0 = leftmost) plus the
// colon (DH+DL), the DM dot and the degree mark. Plain frames are on/off masks at one
// Brightness. A Graded frame (animation clips) carries a level 0..255 per element instead, scaled
// by Brightness, and the masks are ignored.
type MatrixFrame struct {
	Seg        [Digits]uint8
	Colon      bool
	DM, Degree bool
	Brightness int

	Graded bool
	Level  SegLevels
}

// SegLevels is a level 0..255 per segment (A..G of digits 0..3) and per extra LED.
type SegLevels struct {
	Seg               [Digits][7]uint8
	Colon, DM, Degree uint8
}

// Matrix is a 4-digit 7-segment front display, one sysfs LED per segment. Segment LED names
// come from a pattern with {seg} (A..G) and {digit} (0..3), e.g.
// "bergamot:white:clock{seg}{digit}"; the colon may be several LEDs; dot/degree may be empty.
// A nil *Matrix (no display on this board) shows nothing.
type Matrix struct {
	seg       [Digits][7]ledNode
	colon     []ledNode
	dot, degr ledNode
	hasDot    bool
	hasDegree bool
}

func NewMatrix(root, segment string, colon []string, dot, degree string) *Matrix {
	m := &Matrix{}
	for p := 0; p < Digits; p++ {
		for s := 0; s < 7; s++ {
			name := strings.NewReplacer("{seg}", string(rune('A'+s)), "{digit}", strconv.Itoa(p)).Replace(segment)
			m.seg[p][s].path = ledPath(root, name)
		}
	}
	for _, c := range colon {
		m.colon = append(m.colon, ledNode{path: ledPath(root, c)})
	}
	if dot != "" {
		m.dot.path, m.hasDot = ledPath(root, dot), true
	}
	if degree != "" {
		m.degr.path, m.hasDegree = ledPath(root, degree), true
	}
	return m
}

func (m *Matrix) Show(f MatrixFrame) error {
	if m == nil {
		return nil
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	on := func(b bool) int {
		if b {
			return f.Brightness
		}
		return 0
	}
	if f.Graded {
		lv := func(l uint8) int { return int(l) * f.Brightness / 255 }
		for p := 0; p < Digits; p++ {
			for s := 0; s < 7; s++ {
				keep(m.seg[p][s].set(lv(f.Level.Seg[p][s])))
			}
		}
		for i := range m.colon {
			keep(m.colon[i].set(lv(f.Level.Colon)))
		}
		if m.hasDot {
			keep(m.dot.set(lv(f.Level.DM)))
		}
		if m.hasDegree {
			keep(m.degr.set(lv(f.Level.Degree)))
		}
		return first
	}
	for p := 0; p < Digits; p++ {
		for s := 0; s < 7; s++ {
			keep(m.seg[p][s].set(on(f.Seg[p]&(1<<s) != 0)))
		}
	}
	for i := range m.colon {
		keep(m.colon[i].set(on(f.Colon)))
	}
	if m.hasDot {
		keep(m.dot.set(on(f.DM)))
	}
	if m.hasDegree {
		keep(m.degr.set(on(f.Degree)))
	}
	return first
}
