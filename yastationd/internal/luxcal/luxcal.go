// Package luxcal turns a light sensor's reading into lux. A calibration is a few points
// "reading:lux" measured against a lux meter in white light (docs/sensor-calibration.md);
// between them and beyond them the reading is interpolated on log-log axes, so two points make a
// power law (the ltr308 behind the grille: lux ∝ reading^0.96..0.98) and one point a plain
// scale. No points: the reading as is.
package luxcal

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Point maps a sensor reading to the lux a meter measured at it.
type Point struct {
	Reading, Lux float64
}

// Parse reads "reading:lux, reading:lux, …" (both > 0).
func Parse(v string) ([]Point, error) {
	var out []Point
	for _, f := range strings.Split(v, ",") {
		r, l, ok := strings.Cut(strings.TrimSpace(f), ":")
		if !ok {
			return nil, fmt.Errorf("want reading:lux pairs, got %q", f)
		}
		x, err1 := strconv.ParseFloat(strings.TrimSpace(r), 64)
		y, err2 := strconv.ParseFloat(strings.TrimSpace(l), 64)
		if err1 != nil || err2 != nil || x <= 0 || y <= 0 {
			return nil, fmt.Errorf("bad point %q", f)
		}
		out = append(out, Point{x, y})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reading < out[j].Reading })
	for i := 1; i < len(out); i++ {
		if out[i].Reading == out[i-1].Reading || out[i].Lux <= out[i-1].Lux {
			return nil, fmt.Errorf("points must rise together: %v", out)
		}
	}
	return out, nil
}

// Lux converts a reading.
func Lux(c []Point, reading float64) float64 {
	if reading <= 0 || len(c) == 0 {
		return math.Max(reading, 0)
	}
	if len(c) == 1 {
		return reading * c[0].Lux / c[0].Reading
	}
	// the segment the reading falls in, or the end segment it extends
	i := sort.Search(len(c), func(i int) bool { return c[i].Reading >= reading })
	i = min(max(i, 1), len(c)-1)
	p, q := c[i-1], c[i]
	slope := math.Log(q.Lux/p.Lux) / math.Log(q.Reading/p.Reading)
	return p.Lux * math.Exp(slope*math.Log(reading/p.Reading))
}

// String formats points back into the config syntax.
func String(c []Point) string {
	s := make([]string, len(c))
	for i, p := range c {
		s[i] = strconv.FormatFloat(p.Reading, 'g', 4, 64) + ":" + strconv.FormatFloat(p.Lux, 'g', 4, 64)
	}
	return strings.Join(s, ", ")
}
