package display

import (
	"math"
	"time"
)

// The analog clock of a pixel screen (the clock_analog display mode): bare hands and twelve hour
// marks, no numbers. docs/display-expressions.md, "The analog clock" has the design and the study
// it came from. Drawn in perceived intensity like the digital faces; no antialiasing (on a 21x12
// screen half-tones read as blur): a pixel is lit when the hand covers at least half of it.

const (
	analogMajor = 0.4 // the 12, 3, 6, 9 marks
	analogMinor = 0.2 // the other eight
	// The minute hand runs from the centre toward the screen's edge in its true direction and stops
	// analogGap px short of it (in the pixel next to the hour mark); only its outer part is drawn,
	// from analogTip of its length: a tip apart from the hour hand.
	analogGap = 1.0
	analogTip = 0.6
	// The hour hand: analogHourShare of the minute hand's length in the same direction, so the
	// two keep their proportion all round a non-square dial; analogHourWidth px thick.
	analogHourShare = 0.65
	analogHourWidth = 1.6
	analogSub       = 8 // coverage samples per pixel side
)

// analogDial is the dial's centre: on a pixel, so the 3 and 9 marks and the horizontal hands
// fall on one row. A screen with an even number of rows (orion's 12) gives its last row up.
func analogDial(w, h int) (cx, cy float64) {
	if h%2 == 0 {
		h--
	}
	return float64(w-1) / 2, float64(h-1) / 2
}

// HasClockAnalog: the dial needs a few pixels each way from the centre.
func (f PixelFront) HasClockAnalog() bool { return f.W >= 11 && f.H >= 9 }

// ClockAnalog is the analog clock, with the digital clock's cross-fade on minute changes.
func (f PixelFront) ClockAnalog(now time.Time, loc *time.Location, brightness int) FrontFrame {
	if !f.HasClockAnalog() {
		return f.Clock(now, loc, brightness)
	}
	return FrontFrame{Px: fadeClock(f.W, f.H, now, loc, analogFace).pixels(f.Gamma, brightness)}
}

// analogFace draws the marks, then the hands over them, at time t.
func analogFace(c canvas, t time.Time) {
	cx, cy := analogDial(c.w, c.h)
	// edge: how far the dial's rectangle reaches from the centre in direction a (clockwise
	// from 12); dir: the unit step that way (y grows downward).
	edge := func(a float64) float64 {
		s, k := math.Abs(math.Sin(a)), math.Abs(math.Cos(a))
		r := math.Inf(1)
		if s > 1e-9 {
			r = cx / s
		}
		if k > 1e-9 {
			r = math.Min(r, cy/k)
		}
		return r
	}
	dir := func(a float64) (float64, float64) { return math.Sin(a), -math.Cos(a) }

	for k := 0; k < 12; k++ {
		a := 2 * math.Pi * float64(k) / 12
		dx, dy := dir(a)
		r := edge(a)
		level := analogMinor
		if k%3 == 0 {
			level = analogMajor
		}
		c.set(int(math.Round(cx+r*dx)), int(math.Round(cy+r*dy)), level)
	}

	m := float64(t.Minute())
	ma := 2 * math.Pi * m / 60
	mr := edge(ma) - analogGap
	dx, dy := dir(ma)
	c.hand(cx+analogTip*mr*dx, cy+analogTip*mr*dy, cx+mr*dx, cy+mr*dy, 0.5)

	ha := 2 * math.Pi * (float64(t.Hour()%12) + m/60) / 12
	hr := analogHourShare * (edge(ha) - analogGap)
	dx, dy = dir(ha)
	c.hand(cx, cy, cx+hr*dx, cy+hr*dy, analogHourWidth/2)
}

// set lights pixel (x, y) at level, keeping a brighter one.
func (c canvas) set(x, y int, level float64) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	if i := y*c.w + x; level > c.v[i] {
		c.v[i] = level
	}
}

// hand draws the segment (x0,y0)-(x1,y1) thickened to radius r at full level: a pixel is lit when
// at least half of it is covered.
func (c canvas) hand(x0, y0, x1, y1, r float64) {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	if l2 < 1e-9 {
		l2 = 1e-9
	}
	minX, maxX := int(math.Floor(math.Min(x0, x1)-r)), int(math.Ceil(math.Max(x0, x1)+r))
	minY, maxY := int(math.Floor(math.Min(y0, y1)-r)), int(math.Ceil(math.Max(y0, y1)+r))
	for y := max(minY, 0); y <= min(maxY, c.h-1); y++ {
		for x := max(minX, 0); x <= min(maxX, c.w-1); x++ {
			in := 0
			for j := 0; j < analogSub; j++ {
				for i := 0; i < analogSub; i++ {
					sx := float64(x) + (float64(i)+0.5)/analogSub - 0.5
					sy := float64(y) + (float64(j)+0.5)/analogSub - 0.5
					t := ((sx-x0)*dx + (sy-y0)*dy) / l2
					t = math.Max(0, math.Min(1, t))
					ex, ey := sx-x0-t*dx, sy-y0-t*dy
					if ex*ex+ey*ey <= r*r {
						in++
					}
				}
			}
			if 2*in >= analogSub*analogSub {
				c.set(x, y, 1)
			}
		}
	}
}
