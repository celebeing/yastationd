package display

import (
	"math"
	"strconv"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// The pixel clock of a dot-matrix screen (orion's 21x12): our own bitmap fonts, drawn in
// perceived intensity (0..1) and turned into LED drive values with the screen's gamma and the
// display brightness at the end. "H:MM" has no leading zero and is centred; a minute change
// cross-fades the old picture into the new one. docs/display-expressions.md, "The pixel clock".

// bitmap rows: '#' on, '+' half (a softened corner), anything else off.
type bitmap []string

// bigDigits: 4x9, 1-px strokes, square corners, no serifs — the plain rectangular look of the
// Mini's seg7 clock (the 1 is its right-hand stroke); "HH:MM" is exactly 21 columns
// (4+1+4 +1+1+1 +4+1+4).
var bigDigits = map[byte]bitmap{
	'0': {"####", "#..#", "#..#", "#..#", "#..#", "#..#", "#..#", "#..#", "####"},
	'1': {"...#", "...#", "...#", "...#", "...#", "...#", "...#", "...#", "...#"},
	'2': {"####", "...#", "...#", "...#", "####", "#...", "#...", "#...", "####"},
	'3': {"####", "...#", "...#", "...#", "####", "...#", "...#", "...#", "####"},
	'4': {"#..#", "#..#", "#..#", "#..#", "####", "...#", "...#", "...#", "...#"},
	'5': {"####", "#...", "#...", "#...", "####", "...#", "...#", "...#", "####"},
	'6': {"####", "#...", "#...", "#...", "####", "#..#", "#..#", "#..#", "####"},
	'7': {"####", "...#", "...#", "...#", "...#", "...#", "...#", "...#", "...#"},
	'8': {"####", "#..#", "#..#", "#..#", "####", "#..#", "#..#", "#..#", "####"},
	'9': {"####", "#..#", "#..#", "#..#", "####", "...#", "...#", "...#", "####"},
	':': {".", ".", "#", ".", ".", ".", "#", ".", "."},
}

// smallDigits: 3x5, for the two-line clock (time over date).
var smallDigits = map[byte]bitmap{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"###", "..#", "###", "#..", "###"},
	'3': {"###", "..#", ".##", "..#", "###"},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "###", "..#", "###"},
	'6': {"###", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", "..#", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "###"},
	':': {".", "#", ".", "#", "."},
	'.': {".", ".", ".", ".", "#"},
}

// clockFade is how long a minute change cross-fades.
const clockFade = 600 * time.Millisecond

// dateLevel is the date line's intensity under the time in the two-line clock.
const dateLevel = 0.45

// canvas is a w x h picture in perceived intensity.
type canvas struct {
	w, h int
	v    []float64
}

func newCanvas(w, h int) canvas { return canvas{w, h, make([]float64, w*h)} }

func textWidth(font map[byte]bitmap, s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if g, ok := font[s[i]]; ok {
			if i > 0 {
				n++
			}
			n += len(g[0])
		}
	}
	return n
}

// text draws s centred horizontally with its top row at y, at intensity level.
func (c canvas) text(font map[byte]bitmap, s string, y int, level float64) {
	c.textAt(font, s, (c.w-textWidth(font, s))/2, y, level)
}

// textAt draws s with its left edge at x.
func (c canvas) textAt(font map[byte]bitmap, s string, x, y int, level float64) {
	for i := 0; i < len(s); i++ {
		g, ok := font[s[i]]
		if !ok {
			continue
		}
		for r, row := range g {
			for col := 0; col < len(row); col++ {
				px, py := x+col, y+r
				if px < 0 || py < 0 || px >= c.w || py >= c.h {
					continue
				}
				switch row[col] {
				case '#':
					c.v[py*c.w+px] = level
				case '+':
					c.v[py*c.w+px] = level * 0.4
				}
			}
		}
		x += len(g[0]) + 1
	}
}

// pixels turns perceived intensity into drive values: gamma, then brightness (0..255).
func (c canvas) pixels(gamma float64, brightness int) []hw.RGB {
	if gamma <= 0 {
		gamma = 1
	}
	k := float64(min(max(brightness, 0), 255))
	out := make([]hw.RGB, len(c.v))
	for i, v := range c.v {
		d := uint8(math.Round(k * math.Pow(min(max(v, 0), 1), gamma)))
		out[i] = hw.RGB{R: d, G: d, B: d}
	}
	return out
}

// blend mixes b into a by t (0..1), in perceived intensity.
func blend(a, b canvas, t float64) canvas {
	out := newCanvas(a.w, a.h)
	for i := range a.v {
		out.v[i] = a.v[i]*(1-t) + b.v[i]*t
	}
	return out
}

func hourMinute(t time.Time) string {
	return strconv.Itoa(t.Hour()) + ":" + twoDigits(t.Minute())
}

// pixelClock draws one picture of a clock face for the time t.
type faceFunc func(c canvas, t time.Time)

// fadeClock draws face for now; in the first clockFade of a minute it cross-fades from the
// previous minute's picture, so it needs no state.
func fadeClock(w, h int, now time.Time, loc *time.Location, face faceFunc) canvas {
	t := now.In(loc)
	cur := newCanvas(w, h)
	face(cur, t)
	into := time.Duration(t.Second())*time.Second + time.Duration(t.Nanosecond())
	if into >= clockFade {
		return cur
	}
	prev := newCanvas(w, h)
	face(prev, t.Add(-time.Minute))
	return blend(prev, cur, float64(into)/float64(clockFade))
}

// bigClockFace: "H:MM" in the 4x9 digits, vertically centred.
func bigClockFace(c canvas, t time.Time) {
	c.text(bigDigits, hourMinute(t), (c.h-9)/2, 1)
}

// clockDateFace: "H:MM" over "DD.MM" (dimmer) in the 3x5 digits.
func clockDateFace(c canvas, t time.Time) {
	top := (c.h - 11) / 2
	c.text(smallDigits, hourMinute(t), top, 1)
	c.text(smallDigits, twoDigits(t.Day())+"."+twoDigits(int(t.Month())), top+6, dateLevel)
}

// Clock is the pixel clock: square 4x9 digits, no leading zero, a cross-fade on minute changes.
func (f PixelFront) Clock(now time.Time, loc *time.Location, brightness int) FrontFrame {
	if f.W < textWidth(bigDigits, "00:00") || f.H < 9 {
		return f.Seg7Front.Clock(now, loc, brightness)
	}
	return FrontFrame{Px: fadeClock(f.W, f.H, now, loc, bigClockFace).pixels(f.Gamma, brightness)}
}

// ClockDate is the two-line clock: the time over the date.
func (f PixelFront) ClockDate(now time.Time, loc *time.Location, brightness int) FrontFrame {
	if f.W < textWidth(smallDigits, "00:00") || f.H < 11 {
		return f.Clock(now, loc, brightness)
	}
	return FrontFrame{Px: fadeClock(f.W, f.H, now, loc, clockDateFace).pixels(f.Gamma, brightness)}
}

// Unsynced: only the colon of the clock, blinking, until the time is confirmed.
func (f PixelFront) Unsynced(now time.Time, brightness int) FrontFrame {
	if f.W < textWidth(bigDigits, "00:00") || f.H < 9 {
		return f.Seg7Front.Unsynced(now, brightness)
	}
	c := newCanvas(f.W, f.H)
	if now.Second()%2 == 0 {
		c.text(bigDigits, ":", (f.H-9)/2, 1)
	}
	return FrontFrame{Px: c.pixels(f.Gamma, brightness)}
}
