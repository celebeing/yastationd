package app

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
)

// Clock auto-brightness: the light sensor is read every second; a median of the last few
// readings drops short spikes (a hand over the sensor, a passing shadow, a TV's flash), and an
// EMA in the log domain smooths what is left. The result goes through the configured curve and,
// past a little hysteresis, becomes the target. Off = the manual level from HA.
//
// The shown level glides to the target in the log domain (perceptually even: a doubling takes
// as long at the dark end as at the bright one), no faster than brightMaxRate, stepped by the render loop so a frame and its
// level match. On a screen with a backlight (orion's matrix.backlight) the level is mostly the
// backlight: frames are drawn near full scale and keep their half-tones, which a dimmed frame
// loses at the dark end (8-bit drive values collapse); the backlight takes the level rounded up
// and the frame the remaining fraction (2/3..1), so the level still moves smoothly.

const (
	luxEvery      = time.Second
	luxMedianN    = 5                // readings in the spike-rejecting median
	luxTau        = 10 * time.Second // EMA time constant
	luxMinReading = 0.005            // floor before log(): the sensor reads 0 in the dark
	brightDelta   = 0.08             // relative change needed to move the target (and ≥ 1 step)
	brightGlide   = time.Second      // the shown level's time constant towards the target
	// brightMaxRate caps the glide's speed (ln units per second): a big change runs at an even
	// pace for the eye, a doubling or halving per 1.5 s, however fast the target moved.
	brightMaxRate = math.Ln2 / 1.5
)

// brightnessTarget is the level the display should reach (1..255): auto from the light sensor
// when on and read, else the manual level.
func (a *App) brightnessTarget() float64 {
	s := a.store.get()
	if s.ClockAuto && a.luxPath != "" {
		if v := math.Float64frombits(a.autoBright.Load()); v > 0 {
			return v
		}
	}
	return float64(max(s.ClockBright, a.prof.Matrix.MinBrightness))
}

// brightnessCurve is the auto-brightness curve: the configured one, or the default with its
// darkest point at the board's minimum.
func brightnessCurve(cfg config.Config, prof board.Profile) []config.CurvePoint {
	c := cfg.BrightnessCurve
	if cfg.Has("auto_brightness_curve") || prof.Matrix.MinBrightness <= 0 || len(c) == 0 {
		return c
	}
	c = append([]config.CurvePoint(nil), c...)
	c[0].Level = prof.Matrix.MinBrightness
	return c
}

// stepBrightness moves the shown level towards the target; the render loop calls it once per
// tick, before drawing.
func (a *App) stepBrightness(now time.Time) {
	target := math.Max(a.brightnessTarget(), 1)
	cur := math.Float64frombits(a.shownBright.Load())
	if cur <= 0 || a.brightAt.IsZero() {
		cur = target
	} else {
		dt := now.Sub(a.brightAt)
		k := 1 - math.Exp(-float64(dt)/float64(brightGlide))
		step := (math.Log(target) - math.Log(cur)) * min(max(k, 0), 1)
		lim := brightMaxRate * dt.Seconds()
		cur = math.Exp(math.Log(cur) + min(max(step, -lim), lim))
		if math.Abs(cur-target) < 0.01 {
			cur = target
		}
	}
	a.brightAt = now
	a.shownBright.Store(math.Float64bits(cur))
}

func (a *App) shownLevel() float64 {
	if v := math.Float64frombits(a.shownBright.Load()); v > 0 {
		return v
	}
	return math.Max(a.brightnessTarget(), 1)
}

// splitLevel divides a level (1..255) between a backlight (an integer, rounded up) and the
// frame's drive scale (0..255) that makes up the rest.
func splitLevel(level float64) (backlight, drive int) {
	level = min(max(level, 1), 255)
	backlight = int(math.Ceil(level - 1e-9))
	return backlight, int(math.Round(255 * level / float64(backlight)))
}

// brightness is the drive scale every display layer draws at (0..255): the shown level, or with
// a backlight its frame part.
func (a *App) brightness() int {
	l := a.shownLevel()
	if a.backlight != nil {
		_, d := splitLevel(l)
		return d
	}
	return max(int(math.Round(l)), 1)
}

// backlightFor is the backlight value that goes with the frame being drawn now.
func (a *App) backlightFor() int {
	b, _ := splitLevel(a.shownLevel())
	return b
}

func (a *App) luxLoop(stop <-chan struct{}) {
	if a.luxPath == "" {
		return
	}
	if err := hw.ConfigureLux(a.luxPath, a.prof.Lux.Gain, a.prof.Lux.IntegrationTime); err != nil {
		a.log.Warn("lux: configure", "err", err)
	}
	alpha := 1 - math.Exp(-float64(luxEvery)/float64(luxTau))
	curve := brightnessCurve(a.cfg, a.prof)
	var ema float64
	var recent []float64
	have := false
	t := time.NewTicker(luxEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		lx, err := hw.ReadLux(a.luxPath)
		if err != nil {
			continue
		}
		recent = append(recent, math.Log(math.Max(lx, luxMinReading)))
		if len(recent) > luxMedianN {
			recent = recent[1:]
		}
		l := median(recent)
		if !have {
			ema, have = l, true
		} else {
			ema += alpha * (l - ema)
		}
		reading := math.Exp(ema)
		if reading <= luxMinReading*1.0001 {
			reading = 0
		}
		a.luxReading.Store(math.Float64bits(reading))
		lux := luxcal.Lux(a.luxCalibration(), reading) // read each time: the web app may set it
		a.luxSmooth.Store(math.Float64bits(lux))
		target := curveLevel(curve, math.Max(lux, 1e-6))
		cur := math.Float64frombits(a.autoBright.Load())
		if cur == 0 || moveBrightness(cur, target, a.backlight != nil) {
			a.autoBright.Store(math.Float64bits(target))
		}
	}
}

// luxCalibration: the unit's own (config, or set from the web app since) or the board's
// (profile).
func (a *App) luxCalibration() []luxcal.Point {
	if c := a.unitLuxCalibration(); c != nil {
		return c
	}
	return a.prof.Lux.Calibration
}

// unitLuxCalibration: this speaker's own calibration, nil without one.
func (a *App) unitLuxCalibration() []luxcal.Point {
	if p := a.luxUnitCal.Load(); p != nil {
		if len(*p) == 0 {
			return nil // removed from the web app
		}
		return *p
	}
	return a.cfg.LuxCalibration
}

// luxAttrs are the Illuminance sensor's attributes: the smoothed raw reading (what a
// calibration is made from) and where the calibration came from.
func (a *App) luxAttrs() string {
	src := "none"
	switch {
	case a.unitLuxCalibration() != nil:
		src = "unit: " + luxcal.String(a.unitLuxCalibration())
	case len(a.prof.Lux.Calibration) > 0:
		src = "board: " + luxcal.String(a.prof.Lux.Calibration)
	}
	b, _ := json.Marshal(map[string]any{
		"reading":     math.Round(math.Float64frombits(a.luxReading.Load())*10000) / 10000,
		"calibration": src,
	})
	return string(b)
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if n := len(s); n%2 == 0 {
		return (s[n/2-1] + s[n/2]) / 2
	}
	return s[len(s)/2]
}

// moveBrightness: hysteresis — follow the target only once it differs enough. Levels that
// show only as whole steps (no backlight) also need a whole step; with a backlight the level is
// continuous and the small moves glide unseen.
func moveBrightness(cur, target float64, continuous bool) bool {
	d := math.Abs(target - cur)
	return d >= brightDelta*cur && (continuous || d >= 1)
}

// curveLevel interpolates the brightness for a light reading between the curve's points,
// log(level) on log(lux) — even steps for the eye between the calibrated points — clamped at
// the ends.
func curveLevel(c []config.CurvePoint, lux float64) float64 {
	if len(c) == 0 {
		return 128
	}
	if lux <= c[0].Lux {
		return float64(max(c[0].Level, 1))
	}
	for i := 1; i < len(c); i++ {
		if lux <= c[i].Lux {
			p, q := c[i-1], c[i]
			f := (math.Log(lux) - math.Log(p.Lux)) / (math.Log(q.Lux) - math.Log(p.Lux))
			lp, lq := math.Log(float64(max(p.Level, 1))), math.Log(float64(max(q.Level, 1)))
			return math.Exp(lp + f*(lq-lp))
		}
	}
	return float64(max(c[len(c)-1].Level, 1))
}

// HA: switch "Auto brightness" (ON/OFF) and number "Clock brightness" (1..255, manual; setting it
// turns auto off, as you'd expect from touching a slider).
func (a *App) cmdAutoBrightness(payload []byte) {
	on := strings.EqualFold(strings.TrimSpace(string(payload)), "ON")
	a.store.update(func(s *persistState) { s.ClockAuto = on })
	a.publishBrightness()
}

func (a *App) cmdClockBrightness(payload []byte) {
	f, err := strconv.ParseFloat(strings.TrimSpace(string(payload)), 64)
	if err != nil {
		return
	}
	v := clamp(int(f), 1, 255)
	a.store.update(func(s *persistState) { s.ClockBright, s.ClockAuto = v, false })
	a.publishBrightness()
}

func (a *App) publishBrightness() {
	s := a.store.get()
	a.publish(a.topics.State("auto_brightness"), onOff(s.ClockAuto))
	a.publish(a.topics.State("clock_brightness"), strconv.Itoa(s.ClockBright))
}

// luxGate keeps the Illuminance sensor from writing a row to HA's recorder every 30 s: the
// smoothed reading is re-published when it moved by luxReportRel (and by more than the dark
// end's noise, luxReportAbs) or after roomRefresh.
type luxGate struct {
	last float64
	at   time.Time
}

const (
	luxReportRel = 0.10
	luxReportAbs = 0.02
)

func (g *luxGate) due(v float64, now time.Time) bool {
	d := math.Abs(v - g.last)
	if !g.at.IsZero() && now.Sub(g.at) < roomRefresh && (d < luxReportAbs || d < luxReportRel*g.last) {
		return false
	}
	g.last, g.at = v, now
	return true
}
