package display

import (
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

type MatrixFrame = hw.MatrixFrame

const (
	a = hw.SegA
	b = hw.SegB
	c = hw.SegC
	d = hw.SegD
	e = hw.SegE
	f = hw.SegF
	g = hw.SegG
)

// glyphs is what a 7-segment digit can show legibly. Letters without a usable shape (k, m, v,
// w, x, z) are missing and render blank. Case picks the form where both exist (b/B, c/C, …).
var glyphs = map[rune]uint8{
	'0': a | b | c | d | e | f, '1': b | c, '2': a | b | d | e | g, '3': a | b | c | d | g,
	'4': b | c | f | g, '5': a | c | d | f | g, '6': a | c | d | e | f | g, '7': a | b | c,
	'8': a | b | c | d | e | f | g, '9': a | b | c | d | f | g,
	'A': a | b | c | e | f | g, 'a': a | b | c | d | e | g, 'b': c | d | e | f | g, 'B': c | d | e | f | g,
	'C': a | d | e | f, 'c': d | e | g, 'd': b | c | d | e | g, 'D': b | c | d | e | g,
	'E': a | d | e | f | g, 'e': a | b | d | e | f | g, 'F': a | e | f | g, 'f': a | e | f | g,
	'G': a | c | d | e | f, 'g': a | b | c | d | f | g, 'H': b | c | e | f | g, 'h': c | e | f | g,
	'I': e | f, 'i': e, 'J': b | c | d | e, 'j': b | c | d, 'L': d | e | f, 'l': e | f,
	'N': a | b | c | e | f, 'n': c | e | g, 'O': a | b | c | d | e | f, 'o': c | d | e | g,
	'P': a | b | e | f | g, 'p': a | b | e | f | g, 'q': a | b | c | f | g, 'Q': a | b | c | f | g,
	'r': e | g, 'R': e | g, 'S': a | c | d | f | g, 's': a | c | d | f | g,
	't': d | e | f | g, 'T': d | e | f | g, 'U': b | c | d | e | f, 'u': c | d | e,
	'y': b | c | d | f | g, 'Y': b | c | d | f | g,
	'-': g, '_': d, '=': d | g, '"': b | f, '\'': f, ' ': 0,
	'°': a | b | f | g, // shown as a raised "o" when used inside text
}

// cyrGlyphs: Cyrillic capitals with a usable 7-segment shape (lowercase folds to these). Ж, М,
// Ф, Ц, Ш, Щ, Ъ, Ы, Ю have none and render blank.
var cyrGlyphs = map[rune]uint8{
	'А': a | b | c | e | f | g, 'Б': a | c | d | e | f | g, 'В': a | b | c | d | e | f | g,
	'Г': a | e | f, 'Д': b | c | d | e | g, 'Е': a | d | e | f | g, 'Ё': a | d | e | f | g,
	'З': a | b | c | d | g, 'И': b | c | d | e | f, 'Й': b | c | d | e | f, 'К': b | c | e | f | g,
	'Л': a | b | c | e | f, 'Н': b | c | e | f | g, 'О': a | b | c | d | e | f, 'П': a | b | c | e | f,
	'Р': a | b | e | f | g, 'С': a | d | e | f, 'Т': d | e | f | g, 'У': b | c | d | f | g,
	'Х': b | c | e | f | g, 'Ч': b | c | f | g, 'Ь': c | d | e | f | g, 'Э': a | b | c | d | g,
	'Я': a | b | c | f | g,
}

func glyph(r rune) uint8 {
	if s, ok := glyphs[r]; ok {
		return s
	}
	return cyrGlyphs[unicode.ToUpper(r)]
}

// Text renders s onto the 4 digits, left-aligned. A ':' between characters lights the colon
// and a '.' sets the DM dot; neither takes a digit position. Extra characters are cut.
func Text(s string, brightness int) MatrixFrame {
	fr := MatrixFrame{Brightness: brightness}
	p := 0
	for _, r := range s {
		switch r {
		case ':':
			fr.Colon = true
			continue
		case '.':
			fr.DM = true
			continue
		}
		if p >= hw.Digits {
			break
		}
		fr.Seg[p] = glyph(r)
		p++
	}
	return fr
}

// textCells counts digit positions s needs (':' and '.' take none).
func textCells(s string) int {
	n := 0
	for _, r := range s {
		if r != ':' && r != '.' {
			n++
		}
	}
	return n
}

// Marquee returns a render func that shows s statically if it fits, else scrolls it by one
// character every step, padded with blanks, looping until the layer's TTL ends it.
func Marquee(s string, step time.Duration, brightness func() int) RenderFunc[MatrixFrame] {
	if textCells(s) <= hw.Digits {
		return func(time.Time, MatrixFrame) (MatrixFrame, bool) { return Text(s, brightness()), false }
	}
	rs := []rune(strings.NewReplacer(":", "", ".", "").Replace(s))
	padded := append(append([]rune("    "), rs...), []rune("    ")...)
	start := time.Now()
	return func(now time.Time, _ MatrixFrame) (MatrixFrame, bool) {
		i := int(now.Sub(start)/step) % (len(padded) - hw.Digits + 1)
		return Text(string(padded[i:i+hw.Digits]), brightness()), false
	}
}

// Number shows n right-aligned; values that don't fit show "----".
func Number(n int, brightness int) MatrixFrame {
	s := strconv.Itoa(n)
	if len(s) > hw.Digits {
		s = "----"
	}
	return Text(strings.Repeat(" ", hw.Digits-len(s))+s, brightness)
}

// Clock is the base layer: HH:MM in loc.
func Clock(now time.Time, loc *time.Location, brightness int) MatrixFrame {
	return Text(now.In(loc).Format("15:04"), brightness)
}

// Countdown renders the time left to deadline as MM:SS (H:MM once it is an hour or more) and
// finishes when it reaches zero, so a timer keeps ticking with no HA connection.
func Countdown(deadline time.Time, brightness func() int) RenderFunc[MatrixFrame] {
	return func(now time.Time, _ MatrixFrame) (MatrixFrame, bool) {
		left := deadline.Sub(now)
		if left <= 0 {
			return MatrixFrame{}, true
		}
		secs := int((left + time.Second - 1) / time.Second)
		var s string
		if secs >= 3600 {
			s = strconv.Itoa(secs/3600) + ":" + twoDigits(secs/60%60)
			if secs/3600 < 10 {
				s = " " + s
			}
		} else {
			s = twoDigits(secs/60) + ":" + twoDigits(secs%60)
		}
		return Text(s, brightness()), false
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
