package app

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Clock auto-brightness: the light sensor is read every second, smoothed in the log domain (so a
// hand over the speaker or a passing shadow doesn't flicker the clock), mapped through the
// configured curve, and applied with a little hysteresis. Off = the manual level from HA.

const (
	luxEvery    = time.Second
	luxTau      = 4 * time.Second // EMA time constant
	luxMinLux   = 0.005           // floor before log(): the sensor reads 0 in the dark
	brightDelta = 0.08            // relative change needed to move the level (and ≥ 1 step)
)

// brightness is the clock/matrix brightness every display layer uses.
func (a *App) brightness() int {
	s := a.store.get()
	if s.ClockAuto && a.luxPath != "" {
		if v := a.autoBright.Load(); v > 0 {
			return int(v)
		}
	}
	return s.ClockBright
}

func (a *App) luxLoop(stop <-chan struct{}) {
	if a.luxPath == "" {
		return
	}
	if err := hw.ConfigureLux(a.luxPath, a.prof.Lux.Gain, a.prof.Lux.IntegrationTime); err != nil {
		a.log.Warn("lux: configure", "err", err)
	}
	alpha := 1 - math.Exp(-float64(luxEvery)/float64(luxTau))
	var ema float64
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
		l := math.Log(math.Max(lx, luxMinLux))
		if !have {
			ema, have = l, true
		} else {
			ema += alpha * (l - ema)
		}
		a.luxSmooth.Store(math.Float64bits(math.Exp(ema)))
		target := curveLevel(a.cfg.BrightnessCurve, math.Exp(ema))
		cur := int(a.autoBright.Load())
		if cur == 0 || moveBrightness(cur, target) {
			a.autoBright.Store(int32(target))
		}
	}
}

// moveBrightness: hysteresis — only follow the target once it differs enough.
func moveBrightness(cur, target int) bool {
	d := target - cur
	if d < 0 {
		d = -d
	}
	return d >= 1 && float64(d) >= brightDelta*float64(cur)
}

// curveLevel interpolates the brightness for a light reading on log(lux), clamped at the ends.
func curveLevel(c []config.CurvePoint, lux float64) int {
	if len(c) == 0 {
		return 128
	}
	if lux <= c[0].Lux {
		return max(c[0].Level, 1)
	}
	for i := 1; i < len(c); i++ {
		if lux <= c[i].Lux {
			p, q := c[i-1], c[i]
			f := (math.Log(lux) - math.Log(p.Lux)) / (math.Log(q.Lux) - math.Log(p.Lux))
			return max(int(math.Round(float64(p.Level)+f*float64(q.Level-p.Level))), 1)
		}
	}
	return max(c[len(c)-1].Level, 1)
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
	if a.mqtt == nil {
		return
	}
	s := a.store.get()
	a.mqtt.Publish(a.topics.State("auto_brightness"), onOff(s.ClockAuto))
	a.mqtt.Publish(a.topics.State("clock_brightness"), strconv.Itoa(s.ClockBright))
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
