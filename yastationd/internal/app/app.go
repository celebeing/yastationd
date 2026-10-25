// Package app wires the hardware, display stacks, MQTT and health monitor into the daemon.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
	"github.com/celebeing/yastationd/yastationd/internal/timesync"
	"github.com/celebeing/yastationd/yastationd/internal/version"
	"github.com/celebeing/yastationd/yastationd/internal/wake"
	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

const (
	frameInterval   = 66 * time.Millisecond // ~15 Hz render loop
	volumeBubbleTTL = 2 * time.Second
	marqueeStep     = 350 * time.Millisecond // one character per step
)

type App struct {
	cfg   config.Config
	prof  board.Profile // this station's hardware
	log   *slog.Logger
	loc   *time.Location
	start time.Time

	ring      *hw.Ring
	ambilight *hw.Ambilight
	matrix    *hw.Matrix    // seg7 sysfs display
	screen    *hw.DotMatrix // fbdev pixel display
	mixer     *hw.Mixer
	player    *hw.Player
	mic       *hw.Mic
	spot      *wake.Spotter // nil when the wake word is off
	lifeState atomic.Int32  // lifecycle (lifecycle.go)
	clips     *display.ClipLibrary
	scen      scenarioRunner
	music     musicState  // Music Assistant player; music.host nil when off
	playing   atomic.Bool // our aplay is running
	sat       *satellite
	muted     atomic.Bool      // mic-mute switch latch
	clock     *timesync.Syncer // nil when time_sync is off
	setup     *wifisetup.Mode  // Wi-Fi setup mode; nil when off
	combo     keyCombo
	ic        *icState // intercom; nil when off

	ringStack   *display.Stack[display.RingFrame]
	matrixStack *display.Stack[display.MatrixFrame]

	topics  mqtt.Topics
	mqtt    *mqtt.Client
	builder interface {
		Publish(*mqtt.Client)
		SetIntercomPeers([]string)
	}

	hmon  *health.Monitor
	store *stateStore

	luxPath string
	pdDir   string // USB-C PD sink in sysfs ("" = none)

	autoBright atomic.Int32  // current auto clock brightness (0 = no reading yet)
	luxSmooth  atomic.Uint64 // smoothed light reading (float64 bits), published to HA

	baseFx    display.RenderFunc[display.RingFrame] // ring effect layer; render goroutine only
	baseFxKey string

	ambilightFrame      []hw.RGBW // cached solid frame; rebuilt only when the HA state changes
	ambilightFrameKey   ambilightVisual
	ambilightFrameKnown bool
	timers              timers
	animGen             atomic.Int64 // ring_animation selections, to reset the select only for the latest

	quit context.CancelFunc // ends Run: a deliberate restart (the supervisor starts us again)

	marquee     display.RenderFunc[display.MatrixFrame] // message-mode layer; render goroutine only
	marqueeText string
}

type ambilightVisual struct {
	On         bool
	Color      hw.RGBW
	Brightness int
}

func New(cfg config.Config, log *slog.Logger) (*App, error) {
	prof, err := resolveBoard(cfg)
	if err != nil {
		return nil, err
	}
	log.Info("board", "profile", prof.Name, "model", prof.Model)
	var ring *hw.Ring
	if prof.Ring.Kind == "sysfs-rgb" {
		if ring, err = hw.NewRing(cfg.SysRoot, prof.Ring.Pixels, prof.Ring.LED, prof.Ring.Colors); err != nil {
			return nil, err
		}
	}
	var ambilight *hw.Ambilight
	if p := prof.Ambilight; p.Kind == "sysfs-rgbw" {
		if ambilight, err = hw.NewAmbilight(cfg.SysRoot, p.Pixels, p.LED, p.Colors, p.Dies); err != nil {
			return nil, err
		}
	}
	var matrix *hw.Matrix
	var screen *hw.DotMatrix
	switch m := prof.Matrix; m.Kind {
	case "seg7":
		matrix = hw.NewMatrix(cfg.SysRoot, m.Segment, m.Colon, m.Dot, m.Degree)
	case "dotmatrix-fb":
		dev := m.Dev
		if dev == "" {
			dev = hw.FindDotMatrix(cfg.SysRoot)
		} else if cfg.SysRoot != "" {
			dev = cfg.SysRoot + dev
		}
		if dev == "" {
			return nil, fmt.Errorf("board %s: dot-matrix framebuffer not found", prof.Name)
		}
		if screen, err = hw.OpenDotMatrix(dev); err != nil {
			return nil, err
		}
		w, h := screen.Size()
		if display.Seg7ToPixels(hw.MatrixFrame{}, w, h, hw.RGB{}) == nil {
			screen.Close()
			return nil, fmt.Errorf("board %s: dot-matrix %dx%d is too small for clock renderer", prof.Name, w, h)
		}
	}
	a := &App{
		cfg:       cfg,
		prof:      prof,
		log:       log,
		loc:       cfg.Location(),
		start:     time.Now(),
		ring:      ring,
		ambilight: ambilight,
		matrix:    matrix,
		screen:    screen,
		mixer:     hw.NewMixer(prof.Audio.Card),
		topics:    mqtt.NewTopics(cfg.TopicPrefix, cfg.DiscoveryPrefix, cfg.DeviceID),
		store:     newStateStore(cfg.Path(cfg.StateFile), initialState(cfg)),
		luxPath:   cfg.LuxPath,
	}
	if a.luxPath == "" {
		a.luxPath = hw.FindLux(cfg.SysRoot, prof.Lux.IIOPrefix)
	}
	a.pdDir = hw.FindPD(cfg.SysRoot, prof.Power.PD)
	a.player = &hw.Player{Device: prof.Audio.Playback, OnState: a.onPlayState,
		OnExit: func(err error, stderr string) {
			if err != nil || strings.TrimSpace(stderr) != "" {
				log.Warn("aplay exited", "err", err, "stderr", strings.TrimSpace(stderr))
			}
		}}
	a.mic = &hw.Mic{Device: prof.Mic.Device, Channels: prof.Mic.Channels, Rate: prof.Mic.Rate,
		Channel: prof.Mic.Voice, Log: log}
	a.sat = &satellite{a: a}
	a.clips = a.newClipLibrary()
	a.spot = a.newSpotter()
	a.music.host = a.newMusic()
	a.setup = a.newWifiSetup()
	a.ic = a.newIntercom()

	if cfg.TimeSync {
		a.clock = &timesync.Syncer{Servers: cfg.NTPServers, ChronyConf: cfg.Path(cfg.ChronyConf),
			Root: cfg.SysRoot, Log: log}
	}

	a.ringStack = display.NewStack(a.ringBase)
	a.matrixStack = display.NewStack(a.matrixBase)
	return a, nil
}

// ---- base layers ----

func (a *App) matrixBase(now time.Time) display.MatrixFrame {
	s := a.store.get()
	switch s.Mode {
	case "off":
		return display.MatrixFrame{}
	case "message":
		// Long messages scroll; keep the marquee's phase across frames until the text changes.
		if s.Message != a.marqueeText || a.marquee == nil {
			a.marqueeText, a.marquee = s.Message, display.Marquee(s.Message, marqueeStep, a.brightness)
		}
		f, _ := a.marquee(now, display.MatrixFrame{})
		return f
	default:
		// Until the first sync the time is nonsense (a stale or zero clock): no digits, only a
		// blinking colon. The digits appear once the time is confirmed.
		if a.clock != nil && !a.clock.Synced() {
			return display.MatrixFrame{Colon: now.Second()%2 == 0, Brightness: a.brightness()}
		}
		return display.Clock(now, a.loc, a.brightness())
	}
}

func (a *App) ringBase(now time.Time) display.RingFrame {
	n := a.ring.Pixels() // the base fixes the frame size every layer above works with
	s := a.store.get()
	if !s.RingOn {
		return display.Blank(n)
	}
	c := s.RingRGB.Scale(s.RingBright)
	// Animated effects keep their phase across frames: rebuild only when the selection changes.
	key := s.RingEffect + "/" + c.Hex() + "/" + strconv.Itoa(s.RingBright)
	if key != a.baseFxKey {
		a.baseFxKey, a.baseFx = key, baseEffect(s.RingEffect, c, s.RingBright)
	}
	if a.baseFx != nil {
		f, _ := a.baseFx(now, display.Blank(n))
		return f
	}
	return display.Solid(n, c)
}

func (a *App) ambilightBase() []hw.RGBW {
	s := a.store.get()
	key := ambilightVisual{On: s.AmbilightOn, Color: s.AmbilightRGBW, Brightness: s.AmbilightBright}
	if a.ambilightFrameKnown && key == a.ambilightFrameKey {
		return a.ambilightFrame
	}
	a.ambilightFrameKnown = true
	a.ambilightFrameKey = key
	a.ambilightFrame = make([]hw.RGBW, a.ambilight.Pixels())
	if key.On {
		color := key.Color.Scale(key.Brightness)
		for i := range a.ambilightFrame {
			a.ambilightFrame[i] = color
		}
	}
	return a.ambilightFrame
}

// Run owns the daemon until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	ctx, a.quit = context.WithCancel(ctx)
	defer a.quit()
	dev := mqtt.Device{
		Identifiers:  []string{"yastation_" + a.cfg.DeviceID},
		Name:         a.cfg.DeviceName,
		Model:        a.prof.Model + " (" + a.prof.Name + ")",
		Manufacturer: "Yandex (de-Yandexed)",
		SWVersion:    version.Version,
	}
	b := mqtt.NewBuilder(a.topics, dev)
	b.RingEffects = ringEffects
	b.Animations = ringAnimations()
	b.FollowModes = followModes
	b.HasAmbilight = a.ambilight != nil
	b.Expressions = a.expressionOptions()
	b.HasFaces = a.frontClipsPossible()
	b.Intercom = a.ic != nil
	a.builder = b

	a.hmon = health.New(a.cfg.Path(a.cfg.HeartbeatFile), a.onHealthChange)
	for _, p := range a.cfg.Probes {
		name, addr, role := parseProbe(p)
		a.hmon.AddProbe(name, addr, role)
	}

	if a.cfg.MQTTHost != "" {
		a.mqtt = mqtt.New(mqtt.Options{
			Host: a.cfg.MQTTHost, Port: a.cfg.MQTTPort,
			User: a.cfg.MQTTUser, Pass: a.cfg.MQTTPass,
			ClientID:  "yastationd-" + a.cfg.DeviceID,
			Topics:    a.topics,
			Log:       a.log,
			OnConnect: a.onMQTTConnect,
			OnBroker:  a.hmon.SetBrokerConnected,
		})
		a.mqtt.Connect()
	} else {
		a.log.Warn("no mqtt_host configured; running local-only")
	}

	if a.setup != nil {
		a.setup.Recover() // a previous run that died in setup mode left the Wi-Fi client stopped
	}
	go a.restoreVolume()
	if g := a.prof.Audio; g.MicGain != "" && g.MicGainValue >= 0 {
		if err := a.mixer.Set(g.MicGain, g.MicGainValue); err != nil {
			a.log.Warn("set mic gain", "control", g.MicGain, "err", err)
		}
	}

	stop := make(chan struct{})
	renderDone := make(chan struct{})
	go func() {
		a.renderLoop(stop)
		close(renderDone)
	}()
	for _, dev := range a.prof.Input.Devices {
		go a.inputLoop(stop, dev)
	}
	go a.statusLoop(stop)
	if a.setup != nil {
		go a.setupSignalLoop(stop)
	}
	go a.hmon.Run(stop, 10*time.Second, 2*time.Second)
	go a.telemetryLoop(stop)
	go a.powerLoop(stop)
	go a.luxLoop(stop)
	if a.clock != nil {
		go a.clock.Run(stop)
	}
	if a.music.host != nil {
		go a.music.host.Run(stop)
		go a.duckLoop(stop)
	}
	if a.spot != nil {
		go a.spot.Run(stop)
		unsubRaw := a.mic.SubscribeRaw(a.spot.Feed) // the mic stays open while the wake word is on
		defer unsubRaw()
	}

	var wy *wyoming.Server
	if a.cfg.WyomingPort > 0 {
		wy = &wyoming.Server{Handler: a.sat, Log: a.log}
		go func() {
			if err := wy.Serve(":" + strconv.Itoa(a.cfg.WyomingPort)); err != nil {
				a.log.Error("wyoming server", "err", err)
			}
		}()
	}

	<-ctx.Done()
	if a.ic != nil {
		a.ic.m.Hangup("shutdown") // tells the peer
	}
	if a.setup != nil {
		a.setup.Leave() // never leave the speaker as an access point without its daemon
	}
	close(stop)
	<-renderDone // no final clock frame may race with the screen blank below
	// A failed blank is only logged: an error from Run means exit 1, which the supervisor
	// counts as a crash, and this is a deliberate stop.
	if a.screen != nil {
		w, h := a.screen.Size()
		for _, step := range []struct {
			op  string
			err error
		}{
			{"blank", a.screen.Show(make([]hw.RGB, w*h))},
			{"flush", a.screen.Flush()}, // glass is one write behind
			{"close", a.screen.Close()},
		} {
			if step.err != nil {
				a.log.Warn("dot-matrix shutdown", "op", step.op, "err", step.err)
			}
		}
	}
	if wy != nil {
		wy.Close()
	}
	if a.mqtt != nil {
		a.mqtt.Disconnect()
	}
	a.player.Stop()
	a.store.flush()
	return nil
}

// restoreVolume puts back the last volume after boot. hardvold creates the Master control
// itself (with its own default), so retry briefly in case we start before it. Before the user
// ever set a volume, the board's volume_default applies (pickle boots at the amp's maximum).
func (a *App) restoreVolume() {
	v := a.store.get().Volume
	if v < 0 {
		v = a.prof.Audio.VolumeDefault
	}
	if v <= 0 {
		return
	}
	for i := 0; i < 20; i++ {
		if err := a.mixer.Set(a.prof.Audio.Volume, v); err == nil {
			a.log.Info("volume restored", "master", v, "pct", a.prof.Audio.PercentFromRaw(v))
			if a.mqtt != nil {
				a.mqtt.Publish(a.topics.State("volume"), strconv.Itoa(a.prof.Audio.PercentFromRaw(v)))
			}
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	a.log.Warn("volume restore failed: no Master control")
}

// Front-display transitions: a layer coming on top fades in fast, a lower one coming back slowly;
// a plain picture changing within a layer (digits, scrolling text) morphs.
const (
	frontFadeUp   = 80 * time.Millisecond
	frontFadeDown = 400 * time.Millisecond
	frontMorph    = 120 * time.Millisecond
)

func (a *App) renderLoop(stop <-chan struct{}) {
	t := time.NewTicker(frameInterval)
	defer t.Stop()
	fade := display.Fader{Up: frontFadeUp, Down: frontFadeDown}                             // pixel screens
	smooth := display.Seg7Smoother{Up: frontFadeUp, Down: frontFadeDown, Morph: frontMorph} // seg7
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			if err := a.ring.Show(a.ringStack.Frame(now)); err != nil {
				a.log.Debug("ring show", "err", err)
			}
			if a.ambilight != nil {
				if err := a.ambilight.Show(a.ambilightBase()); err != nil {
					a.log.Debug("ambilight show", "err", err)
				}
			}
			frame := a.matrixStack.Frame(now)
			if a.screen == nil {
				frame = smooth.Frame(now, frame, a.matrixStack.Top())
			} else if k := fade.Level(now, a.matrixStack.Top()); k < 1 {
				frame.Brightness = int(float64(frame.Brightness) * k)
			}
			if err := a.showMatrix(frame); err != nil {
				a.log.Debug("matrix show", "err", err)
			}
		}
	}
}

func (a *App) showMatrix(frame hw.MatrixFrame) error {
	if a.screen == nil {
		return a.matrix.Show(frame)
	}
	w, h := a.screen.Size()
	px := display.Seg7ToPixels(frame, w, h, hw.RGB{R: 255, G: 255, B: 255})
	if px == nil {
		return fmt.Errorf("dot-matrix %dx%d is too small for display font", w, h)
	}
	return a.screen.Show(px)
}

// onMQTTConnect re-publishes discovery and the current state on every (re)connect.
func (a *App) onMQTTConnect() {
	a.hmon.Recheck(2 * time.Second)
	a.builder.Publish(a.mqtt)
	a.subscribe()
	a.publishAllState()
}

func (a *App) publishAllState() {
	if a.mqtt == nil {
		return
	}
	s := a.store.get()
	a.mqtt.Publish(a.topics.State("ring"), ringStateJSON(s))
	if a.ambilight != nil {
		a.mqtt.Publish(a.topics.State("ambilight"), ambilightStateJSON(s))
	}
	a.mqtt.Publish(a.topics.State("display_mode"), s.Mode)
	a.mqtt.Publish(a.topics.State("display_message"), s.Message)
	a.mqtt.Publish(a.topics.State("ring_animation"), animOff)
	a.mqtt.Publish(a.topics.State("expression"), animOff)
	a.mqtt.Publish(a.topics.State("display_caps"), a.displayCaps())
	a.publishVersion()
	if a.frontClipsPossible() {
		a.mqtt.Publish(a.topics.State("display_faces"), onOff(s.Faces))
	}
	a.mqtt.Publish(a.topics.State("follow_up"), s.FollowUp)
	a.mqtt.Publish(a.topics.State("text_duration"), strconv.Itoa(s.TextSecs))
	a.mqtt.Publish(a.topics.State("animation_duration"), strconv.Itoa(s.AnimSecs))
	a.publishBrightness()
	a.mqtt.Publish(a.topics.State("health"), a.hmon.State().String())
	a.mqtt.Publish(a.topics.State("pipeline_ok"), onOff(a.hmon.State() == health.Online))
	if v, err := a.mixer.Get(a.prof.Audio.Volume); err == nil {
		a.mqtt.Publish(a.topics.State("volume"), strconv.Itoa(a.prof.Audio.PercentFromRaw(v)))
	}
	a.publishMicMute()
}

func (a *App) telemetryLoop(stop <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if a.mqtt == nil {
				continue
			}
			a.mqtt.Publish(a.topics.State("uptime"), strconv.Itoa(int(time.Since(a.start).Seconds())))
			if a.luxPath != "" {
				if b := a.luxSmooth.Load(); b != 0 {
					a.mqtt.Publish(a.topics.State("lux"), strconv.FormatFloat(math.Float64frombits(b), 'f', 3, 64))
				}
			}
		}
	}
}

func (a *App) onHealthChange(s health.State) {
	a.log.Info("health", "state", s) // the ring indication follows in statusLoop
	if a.mqtt != nil {
		a.mqtt.Publish(a.topics.State("health"), s.String())
		a.mqtt.Publish(a.topics.State("pipeline_ok"), onOff(s == health.Online))
	}
}

func (a *App) onPlayState(playing bool) {
	a.playing.Store(playing)
	if a.mqtt != nil {
		st := "idle"
		if playing {
			st = "playing"
		}
		a.mqtt.Publish(a.topics.State("speaker"), st)
	}
}

func parseProbe(s string) (name, addr string, role health.Role) {
	// "name=host:port@role"; role in broker|pipeline|lan (default lan).
	role = health.RoleLAN
	if n, rest, ok := strings.Cut(s, "="); ok {
		name, s = n, rest
	}
	if rest, r, ok := strings.Cut(s, "@"); ok {
		s = rest
		switch r {
		case "broker":
			role = health.RoleBroker
		case "pipeline":
			role = health.RolePipeline
		}
	}
	addr = s
	if name == "" {
		name = addr
	}
	return
}

func onOff(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

// publishVersion: the daemon's version as the Version sensor, the helpers' and the board as its
// attributes.
func (a *App) publishVersion() {
	if a.mqtt == nil {
		return
	}
	info := map[string]string{"yastationd": version.Version, "board": a.prof.Name}
	if v, ok := a.music.version.Load().(string); ok {
		info["sendspin_host"] = v
	}
	b, _ := json.Marshal(info)
	a.mqtt.Publish(a.topics.State("version_info"), string(b))
	a.mqtt.Publish(a.topics.State("version"), version.Version)
}

func ringStateJSON(s persistState) string {
	m := map[string]any{"state": "OFF"}
	if s.RingOn {
		m["state"] = "ON"
	}
	m["brightness"] = s.RingBright
	m["color"] = map[string]int{"r": int(s.RingRGB.R), "g": int(s.RingRGB.G), "b": int(s.RingRGB.B)}
	m["color_mode"] = "rgb"
	m["effect"] = "none"
	if s.RingEffect != "" {
		m["effect"] = s.RingEffect
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func ambilightStateJSON(s persistState) string {
	m := map[string]any{"state": "OFF"}
	if s.AmbilightOn {
		m["state"] = "ON"
	}
	m["brightness"] = s.AmbilightBright
	m["color"] = map[string]int{
		"r": int(s.AmbilightRGBW.R),
		"g": int(s.AmbilightRGBW.G),
		"b": int(s.AmbilightRGBW.B),
		"w": int(s.AmbilightRGBW.W),
	}
	m["color_mode"] = "rgbw"
	b, _ := json.Marshal(m)
	return string(b)
}
