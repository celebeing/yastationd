package display

import (
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Text on a pixel screen in our own fonts (pixelfont.go), drawn like the clock: perceived
// intensity, the screen's gamma at the end. Layouts (PixelFront.TextLines):
//
//   - one line (1): font7 in the middle rows; a text that does not fit scrolls through
//     smoothly, a pixel at a time, entering at the right and leaving at the left;
//   - two lines (2): font5, wrapped at spaces, never inside a word, and punctuation standing
//     alone ("—", "»") stays with the word before it; more than two lines show as pages of two,
//     in turn; a word too long for a line makes it the scrolling line instead;
//   - auto (0): one line if it fits, else two lines if they fit, else the scrolling line.
//
// Digits alone (a number, a timer's "MM:SS") use the clock's 4x9 digits when they fit.

// textPage is how long a page of two lines shows.
const textPage = 2500 * time.Millisecond

// runesWidth is s's width in f: glyph widths plus a column between glyphs.
func runesWidth(f pfont, s string) int {
	n, first := 0, true
	for _, r := range s {
		if !first {
			n++
		}
		first = false
		if r == ' ' {
			n += f.space
			continue
		}
		n += len(f.glyph(r)[0])
	}
	return n
}

// drawRunes draws s with its left edge at x and its top at y.
func (c canvas) drawRunes(f pfont, s string, x, y int, level float64) {
	for _, r := range s {
		if r == ' ' {
			x += f.space + 1
			continue
		}
		g := f.glyph(r)
		for row, line := range g {
			for col := 0; col < len(line); col++ {
				px, py := x+col, y+row
				if line[col] != '#' || px < 0 || py < 0 || px >= c.w || py >= c.h {
					continue
				}
				c.v[py*c.w+px] = level
			}
		}
		x += len(g[0]) + 1
	}
}

// wrapText breaks s into lines no wider than w in f, at spaces only; ok is false when a word
// (with the punctuation attached to it) is wider than a line.
func wrapText(f pfont, s string, w int) (lines []string, ok bool) {
	var words []string
	for _, word := range strings.Fields(s) {
		if len(words) > 0 && !hasLetterOrDigit(word) && !opensQuote(word) {
			words[len(words)-1] += " " + word // "—", "»", "!" alone: keep with the word before
			continue
		}
		words = append(words, word)
	}
	cur := ""
	for _, word := range words {
		if runesWidth(f, word) > w {
			return nil, false
		}
		switch {
		case cur == "":
			cur = word
		case runesWidth(f, cur+" "+word) <= w:
			cur += " " + word
		default:
			lines, cur = append(lines, cur), word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines, true
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// opensQuote: an opening quote or bracket standing alone belongs to the word after it.
func opensQuote(s string) bool { return strings.ContainsAny(s, "«(\"[„“") }

// bigDigitsText is s for the clock's digits ("" when it has other characters or does not fit).
func (f PixelFront) bigDigitsText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if _, ok := bigDigits[s[i]]; !ok {
			return ""
		}
	}
	if textWidth(bigDigits, s) > f.W || f.H < 9 {
		return ""
	}
	return s
}

// textLayout picks the layout for s: 1 or 2 lines, and whether it fits without moving.
func (f PixelFront) textLayout(s string) (lines int, fits bool) {
	one := runesWidth(font7, s) <= f.W && f.H >= font7.h
	ls, wrapped := wrapText(font5, s, f.W)
	two := wrapped && len(ls) <= 2 && f.H >= 2*font5.h+1
	switch f.TextLines {
	case 1:
		return 1, one
	case 2:
		if wrapped && f.H >= 2*font5.h+1 {
			return 2, two // pages when more than two lines
		}
		return 1, one // a word too long for a line: the scrolling line
	}
	switch {
	case one:
		return 1, true
	case two:
		return 2, true
	}
	return 1, false
}

// textCanvas draws s at time t since it appeared (t moves a scrolling line and turns pages).
func (f PixelFront) textCanvas(s string, t, pxStep time.Duration) canvas {
	c := newCanvas(f.W, f.H)
	if d := f.bigDigitsText(s); d != "" {
		c.text(bigDigits, d, (f.H-9)/2, 1)
		return c
	}
	lines, fits := f.textLayout(s)
	if lines == 2 {
		ls, _ := wrapText(font5, s, f.W)
		if len(ls) > 2 { // pages of two lines
			pages := (len(ls) + 1) / 2
			p := int(t/textPage) % pages
			ls = ls[2*p : min(2*p+2, len(ls))]
		}
		top := (f.H - (2*font5.h + 1)) / 2
		for i, l := range ls {
			c.drawRunes(font5, l, (f.W-runesWidth(font5, l))/2, top+i*(font5.h+1), 1)
		}
		return c
	}
	y := (f.H - font7.h) / 2
	w := runesWidth(font7, s)
	if fits || pxStep <= 0 {
		c.drawRunes(font7, s, (f.W-w)/2, y, 1)
		return c
	}
	// In from the middle (so the start is not lost behind whatever was on screen before), out at
	// the left; a repeat comes in from the middle again.
	period := w + f.W - f.W/2
	x := f.W/2 - int(t/pxStep)%period
	c.drawRunes(font7, s, x, y, 1)
	return c
}

// usesPixelText: the screen is big enough for our text (else the seg7 picture).
func (f PixelFront) usesPixelText() bool { return f.W >= 12 && f.H >= font7.h }

// Text shows s without moving: the clock's digits for digits, else the layout's first view.
func (f PixelFront) Text(s string, brightness int) FrontFrame {
	if !f.usesPixelText() {
		return f.Seg7Front.Text(s, brightness)
	}
	return FrontFrame{Px: f.textCanvas(s, 0, 0).pixels(f.Gamma, brightness)}
}

// Marquee shows s, scrolling (one line) or paging (two lines) when it does not fit. step is the
// seg7 marquee's time per character; a character here is about six pixels wide, and the line
// moves 1.5 times as fast as the seg7 one (found on glass).
func (f PixelFront) Marquee(s string, step time.Duration, brightness func() int) RenderFunc[FrontFrame] {
	if !f.usesPixelText() {
		return f.Seg7Front.Marquee(s, step, brightness)
	}
	pxStep := marqueePxStep(step)
	var start time.Time
	return func(now time.Time, _ FrontFrame) (FrontFrame, bool) {
		if start.IsZero() {
			start = now
		}
		return FrontFrame{Px: f.textCanvas(s, now.Sub(start), pxStep).pixels(f.Gamma, brightness())}, false
	}
}

// marqueePxStep: ~1.5 characters per seg7 step.
func marqueePxStep(step time.Duration) time.Duration { return max(step/9, 20*time.Millisecond) }

// Number shows n in the clock's digits (or the text font when it is too wide).
func (f PixelFront) Number(n int, brightness int) FrontFrame {
	if !f.usesPixelText() {
		return f.Seg7Front.Number(n, brightness)
	}
	return f.Text(strconv.Itoa(n), brightness)
}

// Countdown: the time left as MM:SS (H:MM from an hour) in the clock's digits; done at zero.
func (f PixelFront) Countdown(deadline time.Time, brightness func() int) RenderFunc[FrontFrame] {
	if !f.usesPixelText() {
		return f.Seg7Front.Countdown(deadline, brightness)
	}
	return func(now time.Time, _ FrontFrame) (FrontFrame, bool) {
		left := deadline.Sub(now)
		if left <= 0 {
			return FrontFrame{}, true
		}
		secs := int((left + time.Second - 1) / time.Second)
		s := twoDigits(secs/60) + ":" + twoDigits(secs%60)
		if secs >= 3600 {
			s = strconv.Itoa(secs/3600) + ":" + twoDigits(secs/60%60)
		}
		return f.Text(s, brightness()), false
	}
}
