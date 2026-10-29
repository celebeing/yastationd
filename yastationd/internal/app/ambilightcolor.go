package app

import (
	"math"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/ledcolor"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
)

// The ambilight's colour (docs/ambilight-color.md): HA asks for a hue and saturation or a colour
// temperature; with the board's calibration (or the unit's own) the strip is driven to exactly
// that colour, the white LED used as much as it helps; without one the colour goes out as plain
// RGB (or W alone for a temperature), as before.

const (
	defaultAmbilightKelvin = 5000
	ambilightMinKelvin     = mqtt.AmbilightMinKelvin
	ambilightMaxKelvin     = mqtt.AmbilightMaxKelvin
)

// migrateAmbilight turns a state file's raw RGBW (from before the modes) into a mode: its RGB
// part as a hue, or, white only, the default colour temperature.
func migrateAmbilight(s *persistState, dflt persistState) {
	s.AmbilightMode, s.AmbilightKelvin = dflt.AmbilightMode, dflt.AmbilightKelvin
	if c := s.AmbilightLegacy; c != nil && max(c.R, c.G, c.B) > 0 {
		s.AmbilightMode = "hs"
		s.AmbilightHue, s.AmbilightSat = rgbToHS(float64(c.R), float64(c.G), float64(c.B))
	}
	s.AmbilightLegacy = nil
}

func rgbToHS(r, g, b float64) (h, s float64) {
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	if mx <= 0 {
		return 0, 0
	}
	s = (mx - mn) / mx * 100
	d := mx - mn
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = math.Mod((g-b)/d, 6) * 60
	case mx == g:
		h = ((b-r)/d + 2) * 60
	default:
		h = ((r-g)/d + 4) * 60
	}
	if h < 0 {
		h += 360
	}
	return h, s
}

// hsToRGB: full-value sRGB-encoded r, g, b (0..1) for a hue and saturation (0..100).
func hsToRGB(h, s float64) (r, g, b float64) {
	s = math.Max(0, math.Min(100, s)) / 100
	h = math.Mod(math.Mod(h, 360)+360, 360) / 60
	x := 1 - math.Abs(math.Mod(h, 2)-1)
	var c [3]float64
	switch int(h) {
	case 0:
		c = [3]float64{1, x, 0}
	case 1:
		c = [3]float64{x, 1, 0}
	case 2:
		c = [3]float64{0, 1, x}
	case 3:
		c = [3]float64{0, x, 1}
	case 4:
		c = [3]float64{x, 0, 1}
	default:
		c = [3]float64{1, 0, x}
	}
	for i := range c {
		c[i] = 1 - s*(1-c[i])
	}
	return c[0], c[1], c[2]
}

// ambilightRenderer is the renderer for the current calibration and white, nil when the board
// has none (or a setting is broken: logged once per change). Cached by what makes it.
func (a *App) ambilightRenderer() *ledcolor.Renderer {
	p := a.prof.Ambilight
	key := a.cfg().AmbilightCalibration + "|" + a.cfg().AmbilightWhite
	a.colorMu.Lock()
	defer a.colorMu.Unlock()
	if a.colorKnown && key == a.colorKey {
		return a.colorRenderer
	}
	a.colorKnown, a.colorKey, a.colorRenderer = true, key, nil
	cal := ledcolor.Calibration{Response: p.Response}
	switch {
	case a.cfg().AmbilightCalibration != "":
		c, err := ledcolor.ParsePrimaries(a.cfg().AmbilightCalibration)
		if err != nil {
			a.log.Warn("ambilight_calibration", "err", err)
			return nil
		}
		cal.Channels = c
	case p.Primaries != nil:
		cal.Channels = *p.Primaries
	default:
		return nil
	}
	w, err := ledcolor.White(a.cfg().AmbilightWhite, cal)
	if err != nil {
		a.log.Warn("ambilight_white", "err", err)
		w, _ = ledcolor.White("srgb", cal)
	}
	if a.colorRenderer, err = ledcolor.NewRenderer(cal, w); err != nil {
		a.log.Warn("ambilight calibration", "err", err)
	}
	return a.colorRenderer
}

// ambilightDrive is the strip's drive for the HA light's colour at full brightness 0..255
// (HA's brightness is sRGB-encoded: scaled before the decode, so half looks half).
func ambilightDrive(r *ledcolor.Renderer, s persistState) hw.RGBW {
	v := float64(s.AmbilightBright) / 255
	if r == nil {
		if s.AmbilightMode == "color_temp" {
			return hw.RGBW{W: uint8(s.AmbilightBright)}
		}
		cr, cg, cb := hsToRGB(s.AmbilightHue, s.AmbilightSat)
		return hw.RGBW{R: u8(cr * v), G: u8(cg * v), B: u8(cb * v)}
	}
	value := ledcolor.SRGBDecode(v)
	if s.AmbilightMode == "color_temp" {
		return r.XYZ(ledcolor.Planck(float64(s.AmbilightKelvin)), value, true)
	}
	cr, cg, cb := hsToRGB(s.AmbilightHue, s.AmbilightSat)
	lin := ledcolor.Vec{ledcolor.SRGBDecode(cr), ledcolor.SRGBDecode(cg), ledcolor.SRGBDecode(cb)}
	return r.SRGB(lin, value, true)
}

// calibrateFx drives a music effect's frame through the calibration: its RGB taken as linear
// sRGB and its W as the target white (the effects were tuned by eye on the linear strip, so
// their brightness stays linear; only the colours are corrected), at brightness k (1..255).
// useW false keeps the white LED dark (ambilight_fx_white off).
func calibrateFx(r *ledcolor.Renderer, f []hw.RGBW, k int, useW bool) []hw.RGBW {
	out := make([]hw.RGBW, len(f))
	for i, c := range f {
		m := max(c.R, c.G, c.B, c.W)
		if m == 0 {
			continue
		}
		lin := ledcolor.Vec{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
		out[i] = r.SRGBW(lin, float64(c.W)/255, float64(m)/255*float64(k)/255, useW)
	}
	return out
}

func u8(x float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, x)) * 255)) }
