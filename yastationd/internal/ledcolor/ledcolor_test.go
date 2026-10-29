package ledcolor

import (
	"math"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// orion's profile values (measured on one unit, docs/ambilight-color.md).
const (
	orionPrimaries = "0.6878 0.2977 0.4822, 0.2664 0.5659 1.6303, 0.1177 0.0747 0.3036, 0.3388 0.3811 1.0000"
	orionResponse  = "1:0.0040702, 2:0.0078709, 3:0.011484, 4:0.014997, 6:0.022901, 8:0.031861, 12:0.045311, 16:0.060372, 24:0.095666, 32:0.12747, 48:0.18927, 64:0.25399, 96:0.37963, 128:0.50583, 192:0.75531, 255:1"
)

func orion(t *testing.T, white string) *Renderer {
	t.Helper()
	p, err := ParsePrimaries(orionPrimaries)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ParseResponse(orionResponse)
	if err != nil {
		t.Fatal(err)
	}
	cal := Calibration{Channels: p, Response: resp}
	w, err := White(white, cal)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRenderer(cal, w)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func upvp(v Vec) (float64, float64) {
	d := v[0] + 15*v[1] + 3*v[2]
	return 4 * v[0] / d, 9 * v[1] / d
}

func duv(a, b Vec) float64 {
	au, av := upvp(a)
	bu, bv := upvp(b)
	return math.Hypot(au-bu, av-bv)
}

// shown is what a drive gives, through the calibration (levels -> light -> XYZ).
func shown(r *Renderer, c hw.RGBW) Vec {
	light := func(l uint8) float64 {
		pl, py := 0.0, 0.0
		for _, p := range r.cal.Response {
			if float64(l) <= float64(p.Level) {
				return py + (float64(l)-pl)/(float64(p.Level)-pl)*(p.Light-py)
			}
			pl, py = float64(p.Level), p.Light
		}
		return 1
	}
	var v Vec
	for i, l := range []uint8{c.R, c.G, c.B, c.W} {
		v = v.add(r.cal.Channels[i].mul(light(l)))
	}
	return v
}

func near(a, b hw.RGBW, tol int) bool {
	d := func(x, y uint8) bool { return math.Abs(float64(x)-float64(y)) <= float64(tol) }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.W, b.W)
}

// The drives ledcal computed (and measured within Δu'v' 0.002) for orion, D65 white.
func TestMatchesLedcal(t *testing.T) {
	r := orion(t, "srgb")
	for _, c := range []struct {
		name string
		rgb  [3]float64 // sRGB 0..255
		want hw.RGBW
	}{
		{"cyan", [3]float64{0, 255, 255}, hw.RGBW{R: 0, G: 255, B: 192, W: 175}},
		{"teal", [3]float64{0, 128, 128}, hw.RGBW{R: 0, G: 54, B: 41, W: 37}},
		{"pink", [3]float64{255, 105, 180}, hw.RGBW{R: 255, G: 0, B: 84, W: 131}},
		{"white", [3]float64{255, 255, 255}, hw.RGBW{R: 255, G: 237, B: 178, W: 255}},
		{"orange", [3]float64{255, 165, 0}, hw.RGBW{R: 255, G: 137, B: 0, W: 0}}, // out of gamut
		{"purple", [3]float64{128, 0, 128}, hw.RGBW{R: 54, G: 0, B: 40, W: 0}},   // on its edge
	} {
		lin := Vec{SRGBDecode(c.rgb[0] / 255), SRGBDecode(c.rgb[1] / 255), SRGBDecode(c.rgb[2] / 255)}
		value := math.Max(lin[0], math.Max(lin[1], lin[2]))
		got := r.SRGB(lin, value, true)
		if !near(got, c.want, 3) {
			t.Errorf("%s: %v, ledcal %v", c.name, got, c.want)
		}
	}
}

func TestInGamutHitsTheChromaticity(t *testing.T) {
	r := orion(t, "srgb")
	for _, rgb := range [][3]float64{{0, 1, 1}, {1, 0.4, 0.7}, {0.3, 0.8, 1}, {1, 1, 1}, {0.2, 0.2, 1}} {
		want := SRGB(rgb[0], rgb[1], rgb[2])
		d, ok := r.exact(want, true)
		if !ok {
			t.Fatalf("%v: out of gamut", rgb)
		}
		var got Vec
		for i := range d {
			if d[i] < -1e-9 || d[i] > 1+1e-9 {
				t.Errorf("%v: drive %v out of 0..1", rgb, d)
			}
			got = got.add(r.cal.Channels[i].mul(d[i]))
		}
		if e := duv(got, want); e > 1e-6 {
			t.Errorf("%v: Δu'v' %.6f", rgb, e)
		}
		if mx := math.Max(math.Max(d[0], d[1]), math.Max(d[2], d[3])); math.Abs(mx-1) > 1e-6 {
			t.Errorf("%v: not as bright as possible: %v", rgb, d)
		}
	}
}

func TestWhiteIsBrighterWithW(t *testing.T) {
	r := orion(t, "srgb")
	with, _ := r.exact(D65, true)
	without, _ := r.exact(D65, false)
	if without[3] != 0 {
		t.Fatalf("useW false lit W: %v", without)
	}
	y := func(d [4]float64) (s float64) {
		for i := range d {
			s += d[i] * r.cal.Channels[i][1]
		}
		return
	}
	if y(with) <= y(without)*1.3 {
		t.Errorf("W does not help white: %v vs %v", with, without)
	}
}

func TestOutOfGamutMovesTowardWhite(t *testing.T) {
	r := orion(t, "srgb")
	// sRGB's green is outside the strip's (its green is paler): the result keeps the hue side.
	v := SRGB(0, 1, 0)
	got := shown(r, r.XYZ(v, 1, true))
	if duv(got, v) > 0.05 || duv(got, r.white) < duv(v, r.white)*0.5 {
		t.Errorf("green: Δu'v' %.4f to the target, %.4f to white", duv(got, v), duv(got, r.white))
	}
}

func TestKelvinAndWarm(t *testing.T) {
	r := orion(t, "srgb")
	for _, k := range []float64{2700, 4000, 6500} {
		got := shown(r, r.XYZ(Planck(k), 1, true))
		if e := duv(got, Planck(k)); e > 0.003 { // level rounding
			t.Errorf("%v K: Δu'v' %.4f", k, e)
		}
	}
	w := orion(t, "warm").white
	if c := CCT(w); math.Abs(c-5295) > 60 {
		t.Errorf("warm white at %v K, want W's ~5295 K", c)
	}
}

func TestParse(t *testing.T) {
	p, err := ParsePrimaries(orionPrimaries)
	if err != nil {
		t.Fatal(err)
	}
	q, err := ParsePrimaries(PrimariesString(p))
	if err != nil || duv(p[1], q[1]) > 1e-4 {
		t.Errorf("round trip: %v %v", q, err)
	}
	for _, bad := range []string{"", "0.6 0.3 1", "0.6 0.3 1, 0.3 0.6 1, 0.15 0.06 1, 0.6 0.6 1",
		"a b c, 0.3 0.6 1, 0.15 0.06 1, 0.33 0.33 1"} {
		if _, err := ParsePrimaries(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, bad := range []string{"", "1:0.1, 128:0.5", "1:0.5, 2:0.4, 255:1", "0:0, 255:1"} {
		if _, err := ParseResponse(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := White("6500k", Calibration{}); err != nil {
		t.Error(err)
	}
	if _, err := White("blue", Calibration{}); err == nil {
		t.Error("accepted white blue")
	}
}
