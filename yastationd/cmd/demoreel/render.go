package main

import (
	"image"
	"math"
)

// painter turns one recorded frame into a grey picture of the glass.
type painter interface {
	paint(data []byte) *image.Gray
}

// The glass, after photos of the units: the light of a lit element spreads a little into its
// neighbours (how much depends on the exposure; this is a moderate one) and is seen through the
// fabric's weave, which also shows, dim, over the unlit front. White LEDs, so grey is enough.
type glass struct {
	w, h   int
	weave  []float32 // how much of the light gets through, per pixel
	fabric []float32 // the unlit fabric's own grey
	edge   float64   // px: the blur that softens the edges
	halo   float64   // px: the wide glow
	haloK  float32
	light  []float32 // scratch
}

func newGlass(w, h int, period float64, stripes bool, edge, halo float64) *glass {
	g := &glass{w: w, h: h, weave: make([]float32, w*h), fabric: make([]float32, w*h),
		edge: edge, halo: halo, haloK: 0.3, light: make([]float32, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a := 0.5 + 0.5*math.Cos(2*math.Pi*float64(x+y)/period) // threads along one diagonal
			b := 0.5 + 0.5*math.Cos(2*math.Pi*float64(x-y)/period) // and the other
			var hole, thread float64
			if stripes { // the minis: the light shows dark diagonal stripes
				hole = math.Pow(1-a, 2)
				thread = a
			} else { // Station 3: a fine square weave turned 45°, dark where the threads cross
				hole = math.Pow((1-a)*(1-b), 0.7)
				thread = a * b
			}
			g.weave[y*w+x] = float32(1 - 0.55*hole)
			g.fabric[y*w+x] = float32(0.07 + 0.05*thread)
		}
	}
	return g
}

// show composes the light (0..1 per pixel, perceived) on the fabric.
func (g *glass) show(light []float32) *image.Gray {
	edge := blur(light, g.w, g.h, g.edge)
	halo := blur(light, g.w, g.h, g.halo)
	img := image.NewGray(image.Rect(0, 0, g.w, g.h))
	for i := range img.Pix {
		v := g.fabric[i] + (edge[i]+g.haloK*halo[i])*g.weave[i]
		// 64 greys (the GIF's palette): LZW takes the texture several times better than 256
		img.Pix[i] = uint8(math.Round(float64(min(max(v, 0), 1))*(levels-1)) * 255 / (levels - 1))
	}
	return img
}

// blur is a Gaussian of sigma px, approximated by three box blurs per axis.
func blur(src []float32, w, h int, sigma float64) []float32 {
	out := append([]float32(nil), src...)
	if sigma <= 0 {
		return out
	}
	r := int(math.Round(math.Sqrt(4*sigma*sigma+1) / 2)) // box radius for 3 passes
	if r < 1 {
		return out
	}
	tmp := make([]float32, len(src))
	for pass := 0; pass < 3; pass++ {
		boxH(out, tmp, w, h, r)
		boxV(tmp, out, w, h, r)
	}
	return out
}

func boxH(src, dst []float32, w, h, r int) {
	k := 1 / float32(2*r+1)
	for y := 0; y < h; y++ {
		row := src[y*w : y*w+w]
		var acc float32
		at := func(x int) float32 {
			if x < 0 || x >= w {
				return 0
			}
			return row[x]
		}
		for x := -r; x <= r; x++ {
			acc += at(x)
		}
		for x := 0; x < w; x++ {
			dst[y*w+x] = acc * k
			acc += at(x+r+1) - at(x-r)
		}
	}
}

func boxV(src, dst []float32, w, h, r int) {
	k := 1 / float32(2*r+1)
	for x := 0; x < w; x++ {
		at := func(y int) float32 {
			if y < 0 || y >= h {
				return 0
			}
			return src[y*w+x]
		}
		var acc float32
		for y := -r; y <= r; y++ {
			acc += at(y)
		}
		for y := 0; y < h; y++ {
			dst[y*w+x] = acc * k
			acc += at(y+r+1) - at(y-r)
		}
	}
}

// perceived turns a drive level (linear light) into a display level, like the LED preview: the
// gamma undone, and a lit LED starting at a visible grey.
var perceived = func() (t [256]float32) {
	for i := 1; i < 256; i++ {
		t[i] = float32((48 + 207*math.Pow(float64(i)/255, 1/2.2)) / 255)
	}
	return
}()

// screenPainter draws a pixel screen (Station 3): square LEDs that touch.
type screenPainter struct {
	w, h, cell, pad int
	g               *glass
}

func newScreenPainter(w, h int, scale float64) *screenPainter {
	cell := int(scale)
	if cell <= 0 {
		cell = 18
	}
	p := &screenPainter{w: w, h: h, cell: cell, pad: cell}
	c := float64(cell)
	p.g = newGlass(w*cell+2*p.pad, h*cell+2*p.pad, c/4.5, false, c*0.12, c*0.7)
	return p
}

func (p *screenPainter) paint(rgb []byte) *image.Gray {
	l := p.g.light
	clear(l)
	for y := 0; y < p.h; y++ {
		for x := 0; x < p.w; x++ {
			o := (y*p.w + x) * 3
			if o+2 >= len(rgb) {
				continue
			}
			v := perceived[max(rgb[o], rgb[o+1], rgb[o+2])]
			if v == 0 {
				continue
			}
			for yy := 0; yy < p.cell; yy++ {
				row := (p.pad+y*p.cell+yy)*p.g.w + p.pad + x*p.cell
				for xx := 0; xx < p.cell; xx++ {
					l[row+xx] = v
				}
			}
		}
	}
	return p.g.show(l)
}

// seg7Painter draws the minis' 7-segment clock (shapes as in web/src/seg7.ts).
type seg7Painter struct {
	k      float64 // px per unit
	ox, oy float64
	cover  [][]elemPx // per element: the pixels it covers and how much
	g      *glass
}

type elemPx struct {
	i int
	c float32
}

func newSeg7Painter(colon, dot, degree bool, scale float64) *seg7Painter {
	k := scale
	if k <= 0 {
		k = 1.5
	}
	pad := 18 * k
	p := &seg7Painter{k: k, ox: pad - seg7Box[0]*k, oy: pad - seg7Box[1]*k, cover: make([][]elemPx, 31)}
	w := int(math.Ceil((seg7Box[2]-seg7Box[0])*k + 2*pad))
	h := int(math.Ceil((seg7Box[3]-seg7Box[1])*k + 2*pad))
	p.g = newGlass(w, h, 3*k, true, k*1.2, k*9)
	const ss = 4 // supersampling per axis
	for _, s := range seg7Shapes(colon, dot, degree) {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				n := 0
				for sy := 0; sy < ss; sy++ {
					for sx := 0; sx < ss; sx++ {
						ux := (float64(x) + (float64(sx)+0.5)/ss - p.ox) / k
						uy := (float64(y) + (float64(sy)+0.5)/ss - p.oy) / k
						if s.inside(ux, uy) {
							n++
						}
					}
				}
				if n > 0 {
					p.cover[s.el] = append(p.cover[s.el], elemPx{y*w + x, float32(n) / ss / ss})
				}
			}
		}
	}
	return p
}

func (p *seg7Painter) paint(levels []byte) *image.Gray {
	l := p.g.light
	clear(l)
	for el, px := range p.cover {
		if el >= len(levels) || levels[el] == 0 {
			continue
		}
		v := perceived[levels[el]]
		for _, c := range px {
			l[c.i] = max(l[c.i], v*c.c)
		}
	}
	return p.g.show(l)
}

// The shapes: units, a digit 100 high; elements in the tap's order (digits 0..3 × A..G, colon,
// DM, degree). Keep in step with web/src/seg7.ts.
var seg7Box = [4]float64{-2, -9, 303, 102}

type shape struct {
	el     int
	poly   [][2]float64
	circle [3]float64 // x, y, r when poly is nil
}

func (s shape) inside(x, y float64) bool {
	if s.poly == nil {
		dx, dy := x-s.circle[0], y-s.circle[1]
		return dx*dx+dy*dy <= s.circle[2]*s.circle[2]
	}
	in := false
	for i, j := 0, len(s.poly)-1; i < len(s.poly); j, i = i, i+1 {
		a, b := s.poly[i], s.poly[j]
		if (a[1] > y) != (b[1] > y) && x < (b[0]-a[0])*(y-a[1])/(b[1]-a[1])+a[0] {
			in = !in
		}
	}
	return in
}

func seg7Shapes(colon, dot, degree bool) []shape {
	const T, gap, W = 6.5, 2.0, 54.0
	hbar := func(x0, x1, y, t float64) [][2]float64 {
		return [][2]float64{{x0, y}, {x0 + t, y - t}, {x1 - t, y - t}, {x1, y}, {x1 - t, y + t}, {x0 + t, y + t}}
	}
	vbar := func(x, y0, y1 float64) [][2]float64 {
		return [][2]float64{{x, y0}, {x + T, y0 + T}, {x + T, y1 - T}, {x, y1}, {x - T, y1 - T}, {x - T, y0 + T}}
	}
	var out []shape
	for d, ox := range []float64{0, 62, 166, 228} {
		L, R := ox+T, ox+W-T
		for s, poly := range [][][2]float64{
			hbar(L+gap, R-gap, T, T),     // A
			vbar(R, T+gap, 50-gap),       // B
			vbar(R, 50+gap, 100-T-gap),   // C
			hbar(L+gap, R-gap, 100-T, T), // D
			vbar(L, 50+gap, 100-T-gap),   // E
			vbar(L, T+gap, 50-gap),       // F
			hbar(L+gap, R-gap, 50, T),    // G
		} {
			out = append(out, shape{el: d*7 + s, poly: poly})
		}
	}
	if colon {
		out = append(out, shape{el: 28, circle: [3]float64{141, 37, 8}}, shape{el: 28, circle: [3]float64{141, 82, 8}})
	}
	if dot {
		out = append(out, shape{el: 29, poly: hbar(127, 155, 60, 5.5)})
	}
	if degree {
		out = append(out, shape{el: 30, circle: [3]float64{294, 0, 6.5}})
	}
	return out
}
