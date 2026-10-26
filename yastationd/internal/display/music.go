package display

import (
	"math"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// The music on the front display (docs/music-sendspin.md): the track's title runs once
// (MarqueeTime is how long one pass takes), and on a pixel screen the spectrum sits under the
// clock (ClockLevels).

// MarqueeTime is how long Marquee takes to show s once: one pass of the scrolling line, or every
// page of a paged text; 0 when s fits and stands still.
func MarqueeTime(s string, step time.Duration) time.Duration {
	if textCells(s) <= hw.Digits {
		return 0
	}
	n := len([]rune(strings.NewReplacer(":", "", ".", "").Replace(s))) + 8 // the padding both sides
	return time.Duration(n-hw.Digits+1-marqueeLead) * step
}

func (Seg7Front) MarqueeTime(s string, step time.Duration) time.Duration { return MarqueeTime(s, step) }

// HasLevels: four digits have no room for a spectrum (and the clock looks better than one).
func (Seg7Front) HasLevels() bool { return false }

func (f Seg7Front) ClockLevels(now time.Time, loc *time.Location, _ []float64, brightness int) FrontFrame {
	return f.Clock(now, loc, brightness)
}

func (f PixelFront) MarqueeTime(s string, step time.Duration) time.Duration {
	if !f.usesPixelText() {
		return f.Seg7Front.MarqueeTime(s, step)
	}
	if f.bigDigitsText(s) != "" {
		return 0
	}
	lines, fits := f.textLayout(s)
	if lines == 2 {
		ls, _ := wrapText(font5, s, f.W)
		if pages := (len(ls) + 1) / 2; pages > 1 {
			return time.Duration(pages) * textPage
		}
		return 0
	}
	if fits {
		return 0
	}
	return time.Duration(runesWidth(font7, s)+f.W-f.W/2) * marqueePxStep(step)
}

// HasLevels: the small clock and at least four rows of bars fit.
func (f PixelFront) HasLevels() bool {
	return f.W >= textWidth(smallDigits, "00:00") && f.H >= 10
}

// levelsLevel is the bars' intensity: under the clock, like the date in clock_date.
const levelsLevel = 0.55

// ClockLevels is the clock in the small digits over a spectrum: bands (0..1, low to high
// frequency) as bars rising from the bottom row, resampled to the screen's width; the top pixel
// of a bar is lit by the fraction it reaches into it.
func (f PixelFront) ClockLevels(now time.Time, loc *time.Location, bands []float64, brightness int) FrontFrame {
	if !f.HasLevels() {
		return f.Clock(now, loc, brightness)
	}
	top := max(0, (f.H-11)/2)
	c := fadeClock(f.W, f.H, now, loc, func(c canvas, t time.Time) {
		c.text(smallDigits, hourMinute(t), top, 1)
	})
	y0 := top + 6 // first row under the digits and a gap
	rows := f.H - y0
	if rows > 0 && len(bands) > 0 {
		for x := 0; x < f.W; x++ {
			v := bands[min(len(bands)-1, x*len(bands)/f.W)]
			h := math.Max(0, math.Min(1, v)) * float64(rows)
			for r := 0; r < rows; r++ {
				lit := math.Max(0, math.Min(1, h-float64(r)))
				if lit > 0 {
					c.v[(f.H-1-r)*f.W+x] = levelsLevel * lit
				}
			}
		}
	}
	return FrontFrame{Px: c.pixels(f.Gamma, brightness)}
}
