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
// WLED tools, xLights) take the strip over while they keep coming, exactly as sent (whoever
// streams wants that; ddp_port = 0 turns the receiver off), and the strip goes back to its HA
// light state when they stop. Without a stream, the music effects (ambilighteffects.go) draw it
// while music plays: the "Ambilight effects" switch, effect and brightness are runtime settings
// (persisted, HA, the web app).

// fxStale: without a new frame this long the stream is over and the light state is back.
const fxStale = 2500 * time.Millisecond

type ambilightFx struct {
	mu    sync.Mutex
	frame []hw.RGBW
	at    time.Time
	kick  chan struct{} // a new frame: the render loop shows it now, not on its next tick

	music     musicFx // the music effect in use (ambilighteffects.go), nil = none
	musicName string
}

// ambilightTick: the strip's render rate (its effects move; an unchanged frame costs nothing).
const ambilightTick = 25 * time.Millisecond

// musicEffect returns the selected music effect, made anew when the selection changed.
func (a *App) musicEffect() musicFx {
	name := a.store.get().AmbilightFxEffect
	a.fx.mu.Lock()
	defer a.fx.mu.Unlock()
	if name != a.fx.musicName {
		a.fx.musicName, a.fx.music = name, newMusicFx(name)
	}
	return a.fx.music
}

func (a *App) feedMusicFx(bands []float64, now time.Time) {
	if !a.ambilightFxOn() || !a.store.get().AmbilightFx {
		return
	}
	if fx := a.musicEffect(); fx != nil {
		fx.feed(bands, now, a.ambilight.Pixels())
	}
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

// ambilightShown is what the strip shows at now: a fresh DDP frame as sent (a sender takes the
// strip over: no switch, no brightness of ours), else the music effect at the effects'
// brightness while music plays and effects are on, else the HA light state.
func (a *App) ambilightShown(now time.Time) []hw.RGBW {
	a.fx.mu.Lock()
	f, at := a.fx.frame, a.fx.at
	a.fx.mu.Unlock()
	if f != nil && now.Sub(at) <= fxStale {
		return f
	}
	s := a.store.get()
	if s.AmbilightFx && a.ambilightFxOn() && a.musicLevelsFresh(now) {
		if fx := a.musicEffect(); fx != nil {
			return scaleFrame(fx.frame(now, a.ambilight.Pixels()), s.AmbilightFxBright)
		}
	}
	return a.ambilightBase()
}

func scaleFrame(f []hw.RGBW, k int) []hw.RGBW {
	out := make([]hw.RGBW, len(f))
	for i, c := range f {
		out[i] = c.Scale(k)
	}
	return out
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

func (a *App) cmdAmbilightFxEffect(payload []byte) {
	name := strings.TrimSpace(string(payload))
	if newMusicFx(name) == nil && name != "none" {
		return
	}
	a.store.update(func(s *persistState) { s.AmbilightFxEffect = name })
	a.publish(a.topics.State("ambilight_fx_effect"), name)
}

func (a *App) publishAmbilightFx() {
	if !a.ambilightFxOn() {
		return
	}
	s := a.store.get()
	a.publish(a.topics.State("ambilight_fx_effect"), s.AmbilightFxEffect)
	a.publish(a.topics.State("ambilight_fx"), onOff(s.AmbilightFx))
	a.publish(a.topics.State("ambilight_fx_brightness"), strconv.Itoa(s.AmbilightFxBright))
}
