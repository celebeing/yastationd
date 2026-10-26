package app

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/ddp"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Effects on the rear ambilight (docs/ambilight-stream.md): frames streamed over DDP (LedFx,
// WLED tools, xLights) take the strip while they keep coming, scaled by their own brightness, and
// the strip goes back to its HA light state when they stop. The "Ambilight effects" switch and
// brightness are runtime settings (persisted, HA, the web app).

// fxStale: without a new frame this long the stream is over and the light state is back.
const fxStale = 2500 * time.Millisecond

type ambilightFx struct {
	mu    sync.Mutex
	frame []hw.RGBW
	at    time.Time
	kick  chan struct{} // a new frame: the render loop shows it now, not on its next tick
}

// ambilightFxOn: this speaker takes effects (a strip, a DDP port).
func (a *App) ambilightFxOn() bool { return a.ambilight != nil && a.cfg.DDPPort > 0 }

func (a *App) startAmbilightFx(ctx context.Context) {
	if !a.ambilightFxOn() {
		return
	}
	a.fx.kick = make(chan struct{}, 1)
	go func() {
		err := ddp.Listen(ctx, ddp.Addr(a.cfg.DDPPort), a.ambilight.Pixels(), a.log, a.onDDPFrame)
		if err != nil {
			a.log.Warn("ddp: receiver stopped", "err", err)
		}
	}()
}

func (a *App) onDDPFrame(f ddp.Frame) {
	if !a.store.get().AmbilightFx {
		return
	}
	n := a.ambilight.Pixels()
	px := make([]hw.RGBW, n)
	for i := 0; i < n && (i+1)*f.Channels <= len(f.Data); i++ {
		d := f.Data[i*f.Channels:]
		px[i] = hw.RGBW{R: d[0], G: d[1], B: d[2]}
		if f.Channels == 4 {
			px[i].W = d[3]
		}
	}
	a.fx.mu.Lock()
	fresh := a.fx.at.IsZero() || time.Since(a.fx.at) > fxStale
	a.fx.frame, a.fx.at = px, time.Now()
	a.fx.mu.Unlock()
	if fresh {
		a.log.Info("ambilight: stream started")
	}
	select {
	case a.fx.kick <- struct{}{}:
	default:
	}
}

// ambilightShown is what the strip shows at now: a fresh streamed frame at the effects'
// brightness, else the HA light state.
func (a *App) ambilightShown(now time.Time) []hw.RGBW {
	s := a.store.get()
	if s.AmbilightFx {
		a.fx.mu.Lock()
		f, at := a.fx.frame, a.fx.at
		a.fx.mu.Unlock()
		if f != nil && now.Sub(at) <= fxStale {
			out := make([]hw.RGBW, len(f))
			for i, c := range f {
				out[i] = c.Scale(s.AmbilightFxBright)
			}
			return out
		}
	}
	return a.ambilightBase()
}

// showAmbilight writes the strip's frame (render loop only: the single writer).
func (a *App) showAmbilight(now time.Time) {
	af := a.ambilightShown(now)
	if err := a.ambilight.Show(af); err != nil {
		a.log.Debug("ambilight show", "err", err)
	}
	a.tap.Ambilight(af)
}

func (a *App) cmdAmbilightFx(payload []byte) {
	on := strings.EqualFold(strings.TrimSpace(string(payload)), "ON")
	a.store.update(func(s *persistState) { s.AmbilightFx = on })
	a.publish(a.topics.State("ambilight_fx"), onOff(on))
}

func (a *App) cmdAmbilightFxBrightness(payload []byte) {
	f, err := strconv.ParseFloat(strings.TrimSpace(string(payload)), 64)
	if err != nil {
		return
	}
	v := clamp(int(f), 1, 255)
	a.store.update(func(s *persistState) { s.AmbilightFxBright = v })
	a.publish(a.topics.State("ambilight_fx_brightness"), strconv.Itoa(v))
}

func (a *App) publishAmbilightFx() {
	if !a.ambilightFxOn() {
		return
	}
	s := a.store.get()
	a.publish(a.topics.State("ambilight_fx"), onOff(s.AmbilightFx))
	a.publish(a.topics.State("ambilight_fx_brightness"), strconv.Itoa(s.AmbilightFxBright))
}
