// Package app wires the hardware, display stacks, MQTT and health monitor into the daemon.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/board"
	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/frametap"
	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/logship"
	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
	"github.com/celebeing/yastationd/yastationd/internal/timesync"
	"github.com/celebeing/yastationd/yastationd/internal/version"
	"github.com/celebeing/yastationd/yastationd/internal/wake"
	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

const (
	frameInterval = 66 * time.Millisecond // ~15 Hz render loop
	// The pixel screen runs at the stock patterns' own frame time (20 ms in every orion .led):
	// at 15 Hz two of three frames were dropped and one-frame details (the wink's sparkles) were
	// held three times too long, which reads as noise.
	screenInterval  = 20 * time.Millisecond
	volumeBubbleTTL = 2 * time.Second
	marqueeBaseStep = 350 * time.Millisecond // one character per step, at marquee_speed 1
)

type App struct {
	// conf: the config in force. Keys marked runtime in the registry (config.Keys) change while
	// the daemon runs (applyConfig), so read it through cfg() at the point of use.
	conf   atomic.Pointer[config.Config]
	confMu sync.Mutex // serialises writers of the config file
	shared shared     // the items shared with the other speakers (sync.go)
	// restartPending: a config was saved on trial and the daemon is about to restart
	restartPending atomic.Bool
	prof           board.Profile // this station's hardware
	log            *slog.Logger
	loc            *time.Location
	start          time.Time

	ring       *hw.Ring
	ambilight  *hw.Ambilight
	fx         ambilightFx   // streamed effects on it (ambilightfx.go)
	matrix     *hw.Matrix    // seg7 sysfs display
	screen     *hw.DotMatrix // fbdev pixel display
	mixer      *hw.Mixer
	player     *hw.Player
	mic        *hw.Mic
	spot       *wake.Spotter              // nil when the wake word is off
	lastSubhit atomic.Pointer[wake.Event] // the spotter's last near miss of the wake word (wakecapture.go)
	lifeState  atomic.Int32               // lifecycle (lifecycle.go)
	front      display.Front              // the front display's scenes and animations; nil = no front display
	seqs       sequences
	music      musicState  // Music Assistant player; music.host nil when off
	playing    atomic.Bool // our aplay is running
	sat        *satellite
	muted      atomic.Bool      // mic-mute switch latch
	clock      *timesync.Syncer // nil when time_sync is off
	setup      *wifisetup.Mode  // Wi-Fi setup mode; nil when off
	combo      keyCombo
	ic         *icState         // intercom; nil when off
	ship       *logship.Shipper // logs to the LAN syslog server; nil when off
	room       *roomSound       // mic levels (room sensors, arbitration); nil when not needed
	arb        arbiter
	states     stateMirror // the last value of every state object (statemirror.go)
	web        webServer

	ringStack   *display.Stack[display.RingFrame]
	matrixStack *display.Stack[display.FrontFrame]

	topics  mqtt.Topics
	mqtt    *mqtt.Client
	builder interface {
		Publish(*mqtt.Client)
		SetIntercomPeers([]string)
		Entities() []mqtt.Entity
	}

	hmon  *health.Monitor
	store *stateStore

	luxPath string
	pdDir   string // USB-C PD sink in sysfs ("" = none)

	autoBright  atomic.Uint64                     // auto clock brightness target (float64 bits; 0 = no reading yet)
	shownBright atomic.Uint64                     // the level shown now (float64 bits), gliding to the target
	brightAt    time.Time                         // last stepBrightness; render goroutine only
	backlight   *hw.Backlight                     // the front screen's backlight; nil = dim the frames
	blPrev      int                               // the backlight of the frame written last; render goroutine only
	luxSmooth   atomic.Uint64                     // smoothed light in lux (float64 bits), published to HA
	luxReading  atomic.Uint64                     // the same reading before the calibration (float64 bits)
	userMoments atomic.Pointer[map[string][]show] // <cfw>/moments.json (moments.go)
	luxUnitCal  atomic.Pointer[[]luxcal.Point]    // set from the web app (lightcal.go); nil = the config's
	luxCalBusy  atomic.Bool                       // a calibration measurement is running

	baseFx    display.RenderFunc[display.RingFrame] // ring effect layer; render goroutine only
	baseFxKey string

	ambilightFrame      []hw.RGBW // cached solid frame; rebuilt only when the HA state changes
	ambilightFrameKey   ambilightVisual
	ambilightFrameKnown bool
	timers              timers
	animGen             atomic.Int64 // ring_animation selections, to reset the select only for the latest

	quit context.CancelFunc // ends Run: a deliberate restart (the supervisor starts us again)

	marquee     display.RenderFunc[display.FrontFrame] // message-mode layer; render goroutine only
	marqueeText string

	tap *frametap.Tap // the LED preview stream; nil when frametap_addr is off

	vol     display.VolumeScene // the front display's volume indication, made on first use
	volOnce sync.Once

	video videoState // the video display mode (video.go)
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
	if cfg.SysRoot == "" {
		stopVendorLeftovers(prof.Platform.StopInitd, log)
	}
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
		if p.FB != "" {
			format := p.FBFormat
			if format == "" {
				format = "rgb48"
			}
			err := ambilight.UseFB(cfg.SysRoot, p.FB, format, p.FBCompatible, p.LED, p.Colors, p.Dies)
			if err != nil && format != "rgb48" {
				log.Warn("ambilight: framebuffer with white unavailable, trying rgb48", "err", err)
				format = "rgb48"
				err = ambilight.UseFB(cfg.SysRoot, p.FB, format, "", p.LED, p.Colors, p.Dies)
			}
			if err != nil {
				log.Warn("ambilight: framebuffer unavailable, all channels on sysfs", "err", err)
			} else {
				log.Info("ambilight: framebuffer", "format", format)
			}
		}
	}
	var matrix *hw.Matrix
	var screen *hw.DotMatrix
	switch m := prof.Matrix; m.Kind {
	case "seg7":
		matrix = hw.NewMatrix(cfg.SysRoot, m.Segment, m.Colon, m.Dot, m.Degree)
	case "dotmatrix-fb":
		if screen, err = openScreen(cfg, prof); err != nil {
			return nil, err
		}
		w, h := screen.Size()
		if display.Seg7ToPixels(hw.MatrixFrame{}, w, h, hw.RGB{}) == nil {
			screen.Close()
			return nil, fmt.Errorf("board %s: dot-matrix %dx%d is too small for clock renderer", prof.Name, w, h)
		}
	}
	a := &App{
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
		store:     newStateStore(cfg.Path(cfg.StateFile), initialState(cfg, prof)),
		luxPath:   cfg.LuxPath,
	}
	a.conf.Store(&cfg)
	if screen != nil && prof.Matrix.Backlight != "" {
		a.backlight = hw.NewBacklight(cfg.Path(prof.Matrix.Backlight))
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
	a.front = a.newFront()
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
	if err := a.loadMoments(); err != nil {
		a.log.Warn("moments: the owner's file is ignored", "err", err)
	}
	return a, nil
}

// startFrameTap serves the LED preview stream (internal/frametap) until ctx ends.
func (a *App) startFrameTap(ctx context.Context) {
	cmds := a.commands()
	info := frametap.Info{
		Device: a.cfg().DeviceID, Name: a.cfg().DeviceName, Board: a.prof.Name, Model: a.prof.Model,
		Version: version.Version, Expressions: a.expressionOptions(), RingEffects: ringEffects,
	}
	if a.ring != nil {
		info.Ring = a.ring.Pixels()
	}
	if a.ambilight != nil {
		info.Ambilight = a.ambilight.Pixels()
	}
	if a.screen != nil {
		info.Screen[0], info.Screen[1] = a.screen.Size()
	}
	if a.front != nil {
		info.Patterns = a.front.Anims()
		if a.screen == nil && a.matrix != nil {
			colon, dot, degree := a.matrix.Elements()
			info.Seg7 = &frametap.Seg7{Digits: hw.Digits, Colon: colon, Dot: dot, Degree: degree}
		}
	}
	for name := range cmds {
		info.Commands = append(info.Commands, name)
	}
	sort.Strings(info.Commands)
	a.tap = frametap.New(info, func(name string, payload []byte) bool {
		fn, ok := cmds[name]
		if ok {
			fn(payload)
		}
		return ok
	}, a.log)
	a.tap.Start(ctx)
	if a.cfg().FrameTapAddr != "" { // its own listener, no login (the host simulator, old configs)
		go func() {
			if err := a.tap.Serve(ctx, a.cfg().FrameTapAddr); err != nil {
				a.log.Warn("frame tap", "err", err)
			}
		}()
	}
}

// openScreen opens the board's dot-matrix framebuffer, or a virtual one for matrix.dev =
// "virtual:WxH" (the host simulator, cmd/ledsim).
// cfg is the config in force (see App.conf); the defaults when none was stored (tests).
func (a *App) cfg() *config.Config {
	if c := a.conf.Load(); c != nil {
		return c
	}
	d := config.Default()
	a.conf.CompareAndSwap(nil, &d)
	return a.conf.Load()
}

func openScreen(cfg config.Config, prof board.Profile) (*hw.DotMatrix, error) {
	dev := prof.Matrix.Dev
	var w, h int
	if _, err := fmt.Sscanf(dev, "virtual:%dx%d", &w, &h); err == nil && w > 0 && h > 0 {
		return hw.NewVirtualDotMatrix(w, h), nil
	}
	if dev == "" {
		dev = hw.FindDotMatrix(cfg.SysRoot)
	} else if cfg.SysRoot != "" {
		dev = cfg.SysRoot + dev
	}
	if dev == "" {
		return nil, fmt.Errorf("board %s: dot-matrix framebuffer not found", prof.Name)
	}
	return hw.OpenDotMatrix(dev)
}

// stopVendorLeftovers runs the vendor's own "stop" of each init.d script in the board's
// platform.stop_initd, before the daemon takes over the LEDs and the screen.
func stopVendorLeftovers(names []string, log *slog.Logger) {
	for _, n := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		out, err := exec.CommandContext(ctx, "/etc/init.d/"+n, "stop").CombinedOutput()
		cancel()
		log.Info("vendor leftover stopped", "initd", n, "err", err, "out", strings.TrimSpace(string(out)))
	}
}

// ---- base layers ----

// marqueeStep is the running line's time per character, scaled by marquee_speed.
func (a *App) marqueeStep() time.Duration {
	if a.cfg().MarqueeSpeed <= 0 {
		return marqueeBaseStep
	}
	return time.Duration(float64(marqueeBaseStep) / a.cfg().MarqueeSpeed)
}

func (a *App) matrixBase(now time.Time) display.FrontFrame {
	s := a.store.get()
	if a.front == nil {
		return display.FrontFrame{}
	}
	if f, ok := a.videoFrame(now); ok { // the video mode overrides the persisted one while it plays
		return f
	}
	if s.Mode == "off" {
		return display.FrontFrame{}
	}
	switch s.Mode {
	case "message":
		// Long messages scroll; keep the marquee's phase across frames until the text changes.
		if s.Message != a.marqueeText || a.marquee == nil {
			a.marqueeText, a.marquee = s.Message, a.front.Marquee(s.Message, a.marqueeStep(), a.brightness)
		}
		f, _ := a.marquee(now, display.FrontFrame{})
		return f
	default:
		// Until the first sync the time is nonsense (a stale or zero clock): no digits, only a
		// blinking colon. The digits appear once the time is confirmed — or at once on a board
		// whose RTC kept the time, unless it plainly did not.
		if a.clock != nil && !a.clock.Synced() && !a.rtcTimeTrusted(now) {
			return a.front.Unsynced(now, a.brightness())
		}
		if a.musicLevelsWanted() {
			if bands := a.musicBands(now); bands != nil { // music plays: the spectrum under the clock
				return a.front.ClockLevels(now, a.loc, bands, a.brightness())
			}
		}
		if s.Mode == "clock_date" {
			return a.front.ClockDate(now, a.loc, a.brightness())
		}
		return a.front.Clock(now, a.loc, a.brightness())
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
		Identifiers:  []string{"yastation_" + a.cfg().DeviceID},
		Name:         a.cfg().DeviceName,
		Model:        a.prof.Model + " (" + a.prof.Name + ")",
		Manufacturer: "Yandex (de-Yandexed)",
		SWVersion:    version.Version,
	}
	b := mqtt.NewBuilder(a.topics, dev)
	b.RingEffects = ringEffects
	b.Animations = ringAnimations()
	b.FollowModes = followModes
	b.HasAmbilight = a.ambilight != nil
	b.HasAmbilightFx = a.ambilightFxOn()
	b.AmbilightFx = musicFxNames
	b.Expressions = a.expressionOptions()
	b.HasFaces = a.frontClipsPossible()
	b.DisplayModes = a.displayModes()
	b.Intercom = a.ic != nil
	b.RoomSound = a.roomSoundEnabled()
	b.SoundSPL = a.splOffset() != 0
	a.builder = b

	a.hmon = health.New(a.cfg().Path(a.cfg().HeartbeatFile), a.onHealthChange)
	for _, p := range a.cfg().Probes {
		name, addr, role := parseProbe(p)
		a.hmon.AddProbe(name, addr, role)
	}

	a.loadSyncRejected() // before the broker hands over the shared values
	if a.cfg().MQTTHost != "" {
		a.mqtt = mqtt.New(mqtt.Options{
			Host: a.cfg().MQTTHost, Port: a.cfg().MQTTPort,
			User: a.cfg().MQTTUser, Pass: a.cfg().MQTTPass,
			ClientID:  "yastationd-" + a.cfg().DeviceID,
			Topics:    a.topics,
			Log:       a.log,
			OnConnect: a.onMQTTConnect,
			OnBroker:  a.hmon.SetBrokerConnected,
		})
		a.mqtt.Connect()
	} else {
		a.log.Warn("no mqtt_host configured; running local-only")
	}
	a.publishAllState() // fills the state mirror for the web too, broker or not
	if a.cfg().HTTPAddr != "" {
		if err := a.startWeb(ctx); err != nil {
			a.log.Warn("web: cannot listen", "addr", a.cfg().HTTPAddr, "err", err)
			a.trialFailed("the web server cannot listen on " + a.cfg().HTTPAddr)
		}
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

	if a.cfg().FrameTapAddr != "" || (a.cfg().LEDPreview && a.cfg().HTTPAddr != "") {
		a.startFrameTap(ctx)
	}

	a.startAmbilightFx(ctx) // before the render loop: it waits on the receiver's kicks

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
	if a.cfg().LogRemote != "" {
		cfw := a.prof.Platform.CFWRoot
		a.ship = &logship.Shipper{Addr: a.cfg().LogRemote, Host: a.cfg().DeviceID, Log: a.log,
			Sources: []logship.Source{
				{Path: a.cfg().Path("/tmp/yastationd.log"), App: "yastationd"},
				{Path: a.cfg().Path("/var/log/messages"), App: "syslog"},
			},
			Spool: a.cfg().Path(cfw + "/log-spool"), SpoolMax: a.cfg().LogSpoolKB << 10,
			PosFile: a.cfg().Path("/tmp/yastationd.logship")}
		go a.ship.Run(stop)
	}
	go a.netWatchLoop(stop)
	go a.configTrialLoop(stop)
	if a.micLevelsNeeded() {
		p := a.prof.Mic
		a.room = newRoomSound(p.Rate, p.Channels, p.Voice, func() bool { return a.speaking() || a.micMuted() })
		go a.roomSoundLoop(stop)
	}
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
	if a.cfg().WyomingPort > 0 {
		wy = &wyoming.Server{Handler: a.sat, Log: a.log}
		go func() {
			if err := wy.Serve(":" + strconv.Itoa(a.cfg().WyomingPort)); err != nil && ctx.Err() == nil {
				a.log.Error("wyoming server", "err", err)
				a.trialFailed("the Wyoming server cannot listen")
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
	a.stopVideo("shutdown")
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
	v = min(v, a.volumeCap()) // a state saved before volume_max was lowered
	for i := 0; i < 20; i++ {
		if err := a.mixer.Set(a.prof.Audio.Volume, v); err == nil {
			a.log.Info("volume restored", "master", v, "pct", a.percentFromRaw(v))
			a.publish(a.topics.State("volume"), strconv.Itoa(a.percentFromRaw(v)))
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
	var screenTick <-chan time.Time // nil (never fires) without a pixel screen
	if a.screen != nil && a.front != nil {
		st := time.NewTicker(screenInterval)
		defer st.Stop()
		screenTick = st.C
	}
	var ambiTick <-chan time.Time // the ambilight on its own, faster tick (its effects move)
	if a.ambilight != nil {
		at := time.NewTicker(ambilightTick)
		defer at.Stop()
		ambiTick = at.C
	}
	fade := display.Fader{Up: frontFadeUp, Down: frontFadeDown}                             // pixel screens
	smooth := display.Seg7Smoother{Up: frontFadeUp, Down: frontFadeDown, Morph: frontMorph} // seg7
	for {
		select {
		case <-stop:
			return
		case <-a.fx.kick: // a streamed frame: at the sender's rate, not the loop's 15 Hz
			a.showAmbilight(time.Now())
		case now := <-ambiTick:
			a.showAmbilight(now)
		case now := <-screenTick:
			a.stepBrightness(now)
			if err := a.showScreen(a.matrixStack.Frame(now), fade.Level(now, a.matrixStack.Top())); err != nil {
				a.log.Debug("matrix show", "err", err)
			}
		case now := <-t.C:
			if screenTick == nil {
				a.stepBrightness(now)
			}
			rf := a.ringStack.Frame(now)
			if err := a.ring.Show(rf); err != nil {
				a.log.Debug("ring show", "err", err)
			}
			a.tap.Ring(rf)

			if a.front == nil || a.screen != nil {
				continue
			}
			frame := smooth.Frame(now, a.matrixStack.Frame(now).Seg, a.matrixStack.Top())
			if err := a.matrix.Show(frame); err != nil {
				a.log.Debug("matrix show", "err", err)
			}
			a.tap.Seg7(frame)
		}
	}
}

// showScreen draws a front frame on the pixel screen, k (0..1) being the fade-in level.
func (a *App) showScreen(frame display.FrontFrame, k float64) error {
	w, h := a.screen.Size()
	px := display.ScreenPixels(frame, w, h, k)
	if px == nil {
		return fmt.Errorf("dot-matrix %dx%d is too small for display font", w, h)
	}
	bl := 0
	if a.backlight != nil {
		bl = a.backlightFor()
	}
	a.tap.Screen(display.ScalePixels(px, bl))
	err := a.screen.Show(px)
	if a.backlight != nil {
		// The screen shows a frame one write late: this write put the previous frame up, so its
		// backlight goes with it now.
		if a.blPrev > 0 {
			a.backlight.Set(a.blPrev)
		}
		a.blPrev = bl
	}
	return err
}

// onMQTTConnect re-publishes discovery and the current state on every (re)connect.
func (a *App) onMQTTConnect() {
	a.hmon.Recheck(2 * time.Second)
	a.builder.Publish(a.mqtt)
	a.subscribe()
	a.publishAllState()
	a.publishWebInfo()
}

func (a *App) publishAllState() {
	s := a.store.get()
	a.publish(a.topics.State("ring"), ringStateJSON(s))
	if a.ambilight != nil {
		a.publish(a.topics.State("ambilight"), ambilightStateJSON(s))
	}
	a.publish(a.topics.State("display_mode"), a.displayMode())
	a.publish(a.topics.State("display_message"), s.Message)
	a.publish(a.topics.State("ring_animation"), animOff)
	a.publish(a.topics.State("expression"), animOff)
	a.publish(a.topics.State("display_caps"), a.displayCaps())
	a.publishVersion()
	if a.frontClipsPossible() {
		a.publish(a.topics.State("display_faces"), onOff(s.Faces))
	}
	a.publishAmbilightFx()
	a.publish(a.topics.State("follow_up"), s.FollowUp)
	a.publish(a.topics.State("text_duration"), strconv.Itoa(s.TextSecs))
	a.publish(a.topics.State("animation_duration"), strconv.Itoa(s.AnimSecs))
	a.publishBrightness()
	a.publish(a.topics.State("health"), a.hmon.State().String())
	a.publish(a.topics.State("pipeline_ok"), onOff(a.hmon.State() == health.Online))
	if v, err := a.mixer.Get(a.prof.Audio.Volume); err == nil {
		a.publish(a.topics.State("volume"), strconv.Itoa(a.percentFromRaw(v)))
	}
	a.publishMicMute()
}

func (a *App) telemetryLoop(stop <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	var lux luxGate
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			a.publish(a.topics.State("uptime"), strconv.Itoa(int(time.Since(a.start).Seconds())))
			if a.luxPath != "" {
				if b := a.luxSmooth.Load(); b != 0 && lux.due(math.Float64frombits(b), time.Now()) {
					a.publish(a.topics.State("lux"), strconv.FormatFloat(math.Float64frombits(b), 'f', 2, 64))
					a.publish(a.topics.State("lux_attr"), a.luxAttrs())
				}
			}
		}
	}
}

func (a *App) onHealthChange(s health.State) {
	a.log.Info("health", "state", s) // the ring indication follows in statusLoop
	a.publish(a.topics.State("health"), s.String())
	a.publish(a.topics.State("pipeline_ok"), onOff(s == health.Online))
}

func (a *App) onPlayState(playing bool) {
	a.playing.Store(playing)
	if a.mqtt != nil {
		st := "idle"
		if playing {
			st = "playing"
		}
		a.publish(a.topics.State("speaker"), st)
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
	info := map[string]string{"yastationd": version.Version, "board": a.prof.Name}
	if v, ok := a.music.version.Load().(string); ok {
		info["sendspin_host"] = v
	}
	b, _ := json.Marshal(info)
	a.publish(a.topics.State("version_info"), string(b))
	a.publish(a.topics.State("version"), version.Version)
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
