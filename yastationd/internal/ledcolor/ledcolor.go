// Package ledcolor drives an RGBW LED strip to a wanted colour from a measured calibration
// (docs/ambilight-color.md): each channel's chromaticity and luminance at full, and the
// response from a drive level to light. A colour is asked for as CIE XYZ; the solver finds the
// channel drives that give exactly its chromaticity as bright as the channels allow, using the
// white LED as much as it helps; a colour outside the channels' gamut is moved toward the target
// white along its hue line, as little as it takes.
package ledcolor

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Vec is a CIE XYZ tristimulus value.
type Vec [3]float64

func (v Vec) add(o Vec) Vec           { return Vec{v[0] + o[0], v[1] + o[1], v[2] + o[2]} }
func (v Vec) mul(k float64) Vec       { return Vec{v[0] * k, v[1] * k, v[2] * k} }
func (v Vec) norm() Vec               { return v.mul(1 / v[1]) } // Y = 1
func (v Vec) XY() (x, y float64)      { s := v[0] + v[1] + v[2]; return v[0] / s, v[1] / s }
func fromXY(x, y, Y float64) Vec      { return Vec{x / y * Y, Y, (1 - x - y) / y * Y} }
func (m mat3) apply(v Vec) Vec        { return Vec{dot(m[0], v), dot(m[1], v), dot(m[2], v)} }
func dot(a [3]float64, b Vec) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }

type mat3 [3][3]float64

func (m mat3) inv() (mat3, bool) {
	a, b, c := m[0], m[1], m[2]
	det := a[0]*(b[1]*c[2]-b[2]*c[1]) - a[1]*(b[0]*c[2]-b[2]*c[0]) + a[2]*(b[0]*c[1]-b[1]*c[0])
	if math.Abs(det) < 1e-12 {
		return mat3{}, false
	}
	return mat3{
		{(b[1]*c[2] - b[2]*c[1]) / det, (a[2]*c[1] - a[1]*c[2]) / det, (a[1]*b[2] - a[2]*b[1]) / det},
		{(b[2]*c[0] - b[0]*c[2]) / det, (a[0]*c[2] - a[2]*c[0]) / det, (a[2]*b[0] - a[0]*b[2]) / det},
		{(b[0]*c[1] - b[1]*c[0]) / det, (a[1]*c[0] - a[0]*c[1]) / det, (a[0]*b[1] - a[1]*b[0]) / det},
	}, true
}

func (m mat3) mul(o mat3) mat3 {
	var r mat3
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = m[i][0]*o[0][j] + m[i][1]*o[1][j] + m[i][2]*o[2][j]
		}
	}
	return r
}

// sRGB (D65) linear RGB to XYZ, and the Bradford cone space for white adaptation.
var (
	srgbToXYZ = mat3{{0.4124, 0.3576, 0.1805}, {0.2126, 0.7152, 0.0722}, {0.0193, 0.1192, 0.9505}}
	bradford  = mat3{{0.8951, 0.2664, -0.1614}, {-0.7502, 1.7135, 0.0367}, {0.0389, -0.0685, 1.0296}}
	D65       = srgbToXYZ.apply(Vec{1, 1, 1})
)

// SRGBDecode turns an sRGB-encoded value (0..1) into linear light.
func SRGBDecode(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// SRGB is linear sRGB (0..1 each) as XYZ.
func SRGB(r, g, b float64) Vec { return srgbToXYZ.apply(Vec{r, g, b}) }

// Planck is the Planckian locus at k kelvin (Krystek's approximation, 1000-15000 K), Y = 1.
func Planck(k float64) Vec {
	u := (0.860117757 + 1.54118254e-4*k + 1.28641212e-7*k*k) / (1 + 8.42420235e-4*k + 7.08145163e-7*k*k)
	v := (0.317398726 + 4.22806245e-5*k + 4.20481691e-8*k*k) / (1 - 2.89741816e-5*k + 1.61456053e-7*k*k)
	d := 2*u - 8*v + 4
	return fromXY(3*u/d, 2*v/d, 1)
}

// CCT is the colour temperature closest to v on the Planckian locus (a search, 1500-15000 K).
func CCT(v Vec) float64 {
	x, y := v.XY()
	d := -2*x + 12*y + 3
	u, w := 4*x/d, 6*y/d
	best, bk := math.Inf(1), 0.0
	for k := 1500.0; k <= 15000; k += 10 {
		px, py := Planck(k).XY()
		pd := -2*px + 12*py + 3
		if e := math.Hypot(4*px/pd-u, 6*py/pd-w); e < best {
			best, bk = e, k
		}
	}
	return bk
}

// adapt moves XYZ seen under white src to how it looks under white dst (Bradford).
func adapt(v, src, dst Vec) Vec {
	s, d := bradford.apply(src), bradford.apply(dst)
	inv, _ := bradford.inv()
	diag := mat3{{d[0] / s[0], 0, 0}, {0, d[1] / s[1], 0}, {0, 0, d[2] / s[2]}}
	return inv.mul(diag).mul(bradford).apply(v)
}

// Point is one step of the response: a drive level and the light it gives (1 = level 255).
type Point struct {
	Level int
	Light float64
}

// Calibration is a measured strip: Channels are R, G, B, W at full as XYZ (luminance relative to
// one another; the scale does not matter), Response the drive level -> light table, shared by
// the channels.
type Calibration struct {
	Channels [4]Vec
	Response []Point
}

// ParsePrimaries reads "x y Y, x y Y, x y Y, x y Y" for R, G, B, W: chromaticity and luminance.
func ParsePrimaries(v string) ([4]Vec, error) {
	var out [4]Vec
	parts := strings.Split(v, ",")
	if len(parts) != 4 {
		return out, fmt.Errorf("want 4 channels (R, G, B, W) of \"x y Y\", got %d", len(parts))
	}
	for i, p := range parts {
		f := strings.Fields(p)
		if len(f) != 3 {
			return out, fmt.Errorf("channel %d: want \"x y Y\", got %q", i+1, strings.TrimSpace(p))
		}
		var n [3]float64
		for j, s := range f {
			x, err := strconv.ParseFloat(s, 64)
			if err != nil || x <= 0 {
				return out, fmt.Errorf("channel %d: bad number %q", i+1, s)
			}
			n[j] = x
		}
		if n[0]+n[1] >= 1 {
			return out, fmt.Errorf("channel %d: x + y must be under 1", i+1)
		}
		out[i] = fromXY(n[0], n[1], n[2])
	}
	if _, ok := (mat3{{out[0][0], out[1][0], out[2][0]}, {out[0][1], out[1][1], out[2][1]},
		{out[0][2], out[1][2], out[2][2]}}).inv(); !ok {
		return out, fmt.Errorf("R, G and B must not lie on one line")
	}
	return out, nil
}

// PrimariesString is the inverse of ParsePrimaries.
func PrimariesString(c [4]Vec) string {
	s := make([]string, 4)
	for i, v := range c {
		x, y := v.XY()
		s[i] = fmt.Sprintf("%.4f %.4f %.4g", x, y, v[1])
	}
	return strings.Join(s, ", ")
}

// ParseResponse reads "level:light, …" (levels 1..255 rising, light rising; 255 is scaled to 1).
func ParseResponse(v string) ([]Point, error) {
	var out []Point
	for _, f := range strings.Split(v, ",") {
		l, x, ok := strings.Cut(strings.TrimSpace(f), ":")
		if !ok {
			return nil, fmt.Errorf("want level:light pairs, got %q", f)
		}
		lv, err1 := strconv.Atoi(strings.TrimSpace(l))
		y, err2 := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err1 != nil || err2 != nil || lv < 1 || lv > 255 || y <= 0 {
			return nil, fmt.Errorf("bad point %q", f)
		}
		out = append(out, Point{lv, y})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Level < out[j].Level })
	for i := 1; i < len(out); i++ {
		if out[i].Level == out[i-1].Level || out[i].Light <= out[i-1].Light {
			return nil, fmt.Errorf("points must rise together")
		}
	}
	if len(out) == 0 || out[len(out)-1].Level != 255 {
		return nil, fmt.Errorf("the table must end at level 255")
	}
	top := out[len(out)-1].Light
	for i := range out {
		out[i].Light /= top
	}
	return out, nil
}

// level is the drive level giving light (0..1): the response table inverted, linear between
// its points (and from 0:0); a light between two levels rounds to the nearer.
func (c *Calibration) level(light float64) uint8 {
	if light <= 0 {
		return 0
	}
	if light >= 1 {
		return 255
	}
	if len(c.Response) == 0 {
		return uint8(math.Round(light * 255))
	}
	pl, py := 0.0, 0.0
	for _, p := range c.Response {
		if light <= p.Light {
			l := pl + (light-py)/(p.Light-py)*(float64(p.Level)-pl)
			return uint8(math.Round(l))
		}
		pl, py = float64(p.Level), p.Light
	}
	return 255
}

// Renderer turns colours into drives for one calibration and target white.
type Renderer struct {
	cal   Calibration
	white Vec // the white sRGB's white is shown as (Y = 1)
	inv   mat3
	wb    Vec // inv · W
}

// White names a target white: "srgb" (D65: sRGB's own white, colours as specified), "warm"
// (the white LED's colour temperature on the Planckian locus: the LED's white without its
// tint), or a colour temperature in kelvin (1500-15000).
func White(name string, cal Calibration) (Vec, error) {
	switch strings.TrimSpace(strings.ToLower(name)) {
	case "", "srgb":
		return D65.norm(), nil
	case "warm":
		return Planck(CCT(cal.Channels[3])), nil
	}
	k, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(name)), "k"), 64)
	if err != nil || k < 1500 || k > 15000 {
		return Vec{}, fmt.Errorf("want srgb, warm or a colour temperature 1500-15000 K, got %q", name)
	}
	return Planck(k), nil
}

// NewRenderer makes a renderer for cal with the target white.
func NewRenderer(cal Calibration, white Vec) (*Renderer, error) {
	ch := cal.Channels
	m := mat3{{ch[0][0], ch[1][0], ch[2][0]}, {ch[0][1], ch[1][1], ch[2][1]}, {ch[0][2], ch[1][2], ch[2][2]}}
	inv, ok := m.inv()
	if !ok {
		return nil, fmt.Errorf("R, G and B must not lie on one line")
	}
	return &Renderer{cal: cal, white: white.norm(), inv: inv, wb: inv.apply(ch[3])}, nil
}

// SRGB is the drive for an sRGB colour given in linear light (0..1 each), its chromaticity
// adapted from D65 to the target white, at value (0..1, linear: the share of the brightest
// drive this chromaticity allows). useW false keeps the white LED dark.
func (r *Renderer) SRGB(lin Vec, value float64, useW bool) hw.RGBW {
	if lin[0] <= 0 && lin[1] <= 0 && lin[2] <= 0 {
		return hw.RGBW{}
	}
	return r.XYZ(adapt(SRGB(lin[0], lin[1], lin[2]), D65, r.white), value, useW)
}

// XYZ is the drive for the chromaticity of v at value (0..1, linear).
func (r *Renderer) XYZ(v Vec, value float64, useW bool) hw.RGBW {
	if value <= 0 || v[1] <= 0 {
		return hw.RGBW{}
	}
	d := r.solve(v, useW)
	c := &r.cal
	return hw.RGBW{R: c.level(d[0] * value), G: c.level(d[1] * value), B: c.level(d[2] * value),
		W: c.level(d[3] * value)}
}

// solve: linear drives (0..1) with v's chromaticity as bright as possible; out of gamut, the
// colour moved toward the white as little as it takes.
func (r *Renderer) solve(v Vec, useW bool) [4]float64 {
	if d, ok := r.exact(v, useW); ok {
		return d
	}
	t, w := v.norm(), r.white
	lo, hi := 0.0, 1.0 // the share of the colour kept
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if _, ok := r.exact(t.mul(mid).add(w.mul(1-mid)), useW); ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	d, _ := r.exact(t.mul(lo).add(w.mul(1-lo)), useW)
	return d
}

// exact solves rgb = k·a − w·b (a = M⁻¹·v, b = M⁻¹·W) for the largest k with every channel in
// 0..1 and 0 <= w <= 1: a linear program in (w, k), its optimum on a vertex, so every w where
// two constraint lines cross is tried.
func (r *Renderer) exact(v Vec, useW bool) ([4]float64, bool) {
	a := r.inv.apply(v)
	amax := math.Max(math.Abs(a[0]), math.Max(math.Abs(a[1]), math.Abs(a[2])))
	for i := range a {
		if math.Abs(a[i]) < 1e-6*amax { // on the gamut's edge, not past it
			a[i] = 0
		}
	}
	b := r.wb
	// k bounds at a given w: lines k = (c + w·b_i)/a_i for c in {0, 1}.
	bounds := func(w float64) (lo, hi float64, ok bool) {
		lo, hi = 0, math.Inf(1)
		for i := 0; i < 3; i++ {
			bi := w * b[i]
			if a[i] == 0 {
				if -bi < -1e-9 || -bi > 1+1e-9 {
					return 0, 0, false
				}
				continue
			}
			k1, k2 := bi/a[i], (1+bi)/a[i]
			lo, hi = math.Max(lo, math.Min(k1, k2)), math.Min(hi, math.Max(k1, k2))
		}
		return lo, hi, hi >= lo-1e-9 && hi > 0 && !math.IsInf(hi, 1)
	}
	ws := []float64{0}
	if useW {
		ws = append(ws, 1)
		// Every crossing of two lines k = (c1 + w·b_i)/a_i and k = (c2 + w·b_j)/a_j.
		for i := 0; i < 3; i++ {
			for j := i + 1; j < 3; j++ {
				if a[i] == 0 || a[j] == 0 {
					continue
				}
				for _, c1 := range []float64{0, 1} {
					for _, c2 := range []float64{0, 1} {
						den := b[i]/a[i] - b[j]/a[j]
						if math.Abs(den) < 1e-12 {
							continue
						}
						if w := (c2/a[j] - c1/a[i]) / den; w > 0 && w < 1 {
							ws = append(ws, w)
						}
					}
				}
			}
			// A channel whose a is 0 bounds w itself (w·b_i must stay in -1..0).
			if a[i] == 0 && b[i] != 0 {
				for _, w := range []float64{0, -1 / b[i]} {
					if w > 0 && w < 1 {
						ws = append(ws, w)
					}
				}
			}
		}
	}
	bk, bw, found := 0.0, 0.0, false
	for _, w := range ws {
		if _, hi, ok := bounds(w); ok && (!found || hi > bk) {
			bk, bw, found = hi, w, true
		}
	}
	if !found {
		return [4]float64{}, false
	}
	var d [4]float64
	for i := 0; i < 3; i++ {
		d[i] = math.Min(1, math.Max(0, bk*a[i]-bw*b[i]))
	}
	d[3] = bw
	return d, true
}

// SRGBW is SRGB with a share of white added: w (0..1) of the target white, as bright as sRGB's
// white (1, 1, 1) would be. For frames drawn with a white channel (the music effects).
func (r *Renderer) SRGBW(lin Vec, w, value float64, useW bool) hw.RGBW {
	v := Vec{}
	if lin[0] > 0 || lin[1] > 0 || lin[2] > 0 {
		v = adapt(SRGB(lin[0], lin[1], lin[2]), D65, r.white)
	}
	return r.XYZ(v.add(r.white.mul(w)), value, useW)
}
