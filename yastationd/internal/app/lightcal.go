package app

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
)

// Calibrating the light sensor from the web app (docs/sensor-calibration.md, "From the web app"):
// the user measures the light next to the speaker's top with a lux meter and types the lux in; the
// speaker takes its own reading over a few seconds and stores the pair as this unit's
// lux_calibration, live and in its config file. A point taken in a clearly different light
// (×3 or more) is kept alongside, so two such calibrations give the curve its slope too.

// The measurement: lightCalSamples readings lightCalEvery apart (vars for the tests).
var (
	lightCalSamples = 30
	lightCalEvery   = 500 * time.Millisecond
)

const (
	lightCalMinLux   = 30.0 // a calibration in the dark is mostly the sensor's noise
	lightCalKeepSpan = 3.0  // an older point this far off in lux is kept
	lightCalMaxPts   = 3
)

type lightCalView struct {
	Calibration string  `json:"calibration"` // the unit's points ("" = none: the board's or raw)
	Source      string  `json:"source"`      // unit | board | none
	Reading     float64 `json:"reading"`     // the smoothed reading now
	Lux         float64 `json:"lux"`         // what it reads as now
	Measured    float64 `json:"measured,omitempty"`
	Before      float64 `json:"before,omitempty"` // the measured light in lux by the old calibration
}

func (a *App) lightCalView() lightCalView {
	v := lightCalView{Source: "none",
		Reading: math.Float64frombits(a.luxReading.Load()), Lux: math.Float64frombits(a.luxSmooth.Load())}
	switch {
	case a.unitLuxCalibration() != nil:
		v.Source, v.Calibration = "unit", luxcal.String(a.unitLuxCalibration())
	case len(a.prof.Lux.Calibration) > 0:
		v.Source = "board"
	}
	return v
}

// apiLightCal: GET the state; POST {"lux": N} measures and stores a point; DELETE drops the unit's
// calibration (back to the board's).
func (a *App) apiLightCal(w http.ResponseWriter, r *http.Request) {
	if a.luxPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this speaker has no light sensor"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, a.lightCalView())
	case http.MethodPost:
		var in struct{ Lux float64 }
		if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || in.Lux <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "want {\"lux\": N}"})
			return
		}
		if in.Lux < lightCalMinLux {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "too dark: calibrate in a well-lit room (30 lx or more)"})
			return
		}
		if !a.luxCalBusy.CompareAndSwap(false, true) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "a measurement is already running"})
			return
		}
		defer a.luxCalBusy.Store(false)
		// The speaker's own front display and ring light the sensor a little: dark while measuring.
		dark := time.Duration(lightCalSamples)*lightCalEvery + time.Second
		a.matrixStack.Push("lightcal", display.PrioSetup, dark, func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
			return display.FrontFrame{}, false
		})
		a.ringStack.Push("lightcal", display.PrioSetup, dark, func(time.Time, display.RingFrame) (display.RingFrame, bool) {
			return display.Blank(a.ring.Pixels()), false
		})
		time.Sleep(lightCalEvery) // the lights go out first
		reading, err := a.measureLight()
		a.matrixStack.Remove("lightcal")
		a.ringStack.Remove("lightcal")
		if err != nil || reading <= 0 {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "the sensor reads nothing: is it covered?"})
			return
		}
		before := luxcal.Lux(a.luxCalibration(), reading)
		pts := mergeLightPoint(a.unitLuxCalibration(), luxcal.Point{Reading: reading, Lux: in.Lux})
		if err := a.saveLuxCalibration(pts); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		a.log.Info("light sensor calibrated", "lux", in.Lux, "reading", reading, "was", math.Round(before*10)/10,
			"calibration", luxcal.String(pts))
		v := a.lightCalView()
		v.Measured, v.Before = reading, before
		writeJSON(w, http.StatusOK, v)
	case http.MethodDelete:
		if err := a.saveLuxCalibration(nil); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		a.log.Info("light sensor calibration removed")
		writeJSON(w, http.StatusOK, a.lightCalView())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET, POST or DELETE"})
	}
}

// measureLight is the median raw reading over lightCalSamples (not the 10 s average, which still
// holds the light from before the user stepped back).
func (a *App) measureLight() (float64, error) {
	var v []float64
	var last error
	for i := 0; i < lightCalSamples; i++ {
		if i > 0 {
			time.Sleep(lightCalEvery)
		}
		x, err := hw.ReadLux(a.luxPath)
		if err != nil {
			last = err
			continue
		}
		v = append(v, x)
	}
	if len(v) == 0 {
		return 0, last
	}
	sort.Float64s(v)
	return v[len(v)/2], nil
}

// mergeLightPoint adds p to the unit's points: older points in a clearly different light are
// kept (up to lightCalMaxPts, the nearest to p first), the rest replaced; the result must rise
// with the reading, else p alone.
func mergeLightPoint(old []luxcal.Point, p luxcal.Point) []luxcal.Point {
	var keep []luxcal.Point
	for _, o := range old {
		if r := o.Lux / p.Lux; r >= lightCalKeepSpan || r <= 1/lightCalKeepSpan {
			keep = append(keep, o)
		}
	}
	sort.Slice(keep, func(i, j int) bool {
		return math.Abs(math.Log(keep[i].Lux/p.Lux)) < math.Abs(math.Log(keep[j].Lux/p.Lux))
	})
	if len(keep) > lightCalMaxPts-1 {
		keep = keep[:lightCalMaxPts-1]
	}
	pts := append(keep, p)
	sort.Slice(pts, func(i, j int) bool { return pts[i].Reading < pts[j].Reading })
	for i := 1; i < len(pts); i++ {
		if pts[i].Reading <= pts[i-1].Reading || pts[i].Lux <= pts[i-1].Lux {
			return []luxcal.Point{p} // inconsistent with the older points: they were wrong, or the light was
		}
	}
	return pts
}

// saveLuxCalibration applies pts live and writes them into the config file as lux_calibration
// (nil removes the key). No restart: the light loop reads the calibration on every reading.
func (a *App) saveLuxCalibration(pts []luxcal.Point) error {
	a.confMu.Lock()
	defer a.confMu.Unlock()
	old, err := os.ReadFile(a.cfg().File)
	if err != nil {
		return err
	}
	var vals []string
	if pts != nil {
		vals = []string{luxcal.String(pts)}
	}
	data := config.EditNote(old, map[string][]string{"lux_calibration": vals},
		"the light sensor, calibrated from the web app "+time.Now().Format("2006-01-02"))
	if _, err := config.Parse(strings.NewReader(string(data))); err != nil {
		return err
	}
	tmp := a.cfg().File + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, a.cfg().File); err != nil {
		return err
	}
	a.adoptConfig(data)
	if pts == nil {
		pts = []luxcal.Point{}
	}
	a.luxUnitCal.Store(&pts)
	return nil
}
