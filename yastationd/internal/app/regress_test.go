package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Regression: every reaction moved into moments (d22993e) does what the code before it did, with
// no packs and no owner's moments. The expectations are the old call sites' own values: each
// subtest names the slot, priorities, effect and sound the old code used.

type regressApp struct {
	*App
	aplayLog string
	plays    atomic.Int32 // interrupting plays (Player.Play: OnState); blips do not report
	t0       time.Time    // when the reaction under test was triggered (mark)
}

// mark notes the trigger time ringIs samples from.
func (r *regressApp) mark() { r.t0 = time.Now() }

const ringPx = 12

func newRegressApp(t *testing.T) *regressApp {
	t.Helper()
	bin, root := t.TempDir(), t.TempDir()
	r := &regressApp{aplayLog: filepath.Join(bin, "aplay.log")}
	for name, body := range map[string]string{
		"aplay":  "#!/bin/sh\nfor a; do last=$a; done\necho \"$last\" >> " + r.aplayLog + "\nexec cat > /dev/null\n",
		"amixer": "#!/bin/sh\nexit 0\n",
	} {
		os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	cfg := config.Default()
	cfg.SysRoot = root
	cfg.VolumeBeep, cfg.AnnounceChime = true, true
	sounds := filepath.Join(root, "cfw/sounds")
	os.MkdirAll(sounds, 0o755)
	os.MkdirAll(filepath.Join(root, "tmp"), 0o755) // the once-per-boot markers
	for _, s := range []string{"volume_sound", "notification_sound", "something_went_wrong", "no_power",
		"calls_connected", "calls_ended", "calls_busy", "calls_ringing_incoming", "calls_ringing_outgoing"} {
		os.WriteFile(filepath.Join(sounds, s+".wav"), wavFile(make([]byte, 3200), 16000), 0o644) // 0.1 s
	}
	// the semantic animations the status moments use, as files (seg7's own clips lack them)
	clips := filepath.Join(root, "clips")
	os.MkdirAll(clips, 0o755)
	wink, err := os.ReadFile("../display/clips/wink.scled")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ringing", "setup", "sleep"} {
		os.WriteFile(filepath.Join(clips, n+".scled"), wink, 0o644)
	}
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ring:        &hw.Ring{},
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(ringPx) }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
		front:       display.Seg7Front{Clips: &display.ClipLibrary{Dirs: []string{clips}}, Packs: &display.ClipLibrary{NoBuiltin: true}},
		mixer:       hw.NewMixer("0"),
		store:       newStateStore(filepath.Join(root, "state"), defaultState(128, 128, 128, false)),
		hmon:        health.New("", nil),
	}
	a.player = &hw.Player{Device: "default", OnState: func(p bool) {
		if p {
			r.plays.Add(1)
		}
	}}
	a.prof.Platform.CFWRoot = "/cfw"
	a.conf.Store(&cfg)
	a.sat = &satellite{a: a}
	r.App = a
	return r
}

// ringIs checks the ring's top layer is slot at prio and draws want (built when the old code would
// have built it), sampled over its period.
func (r *regressApp) ringIs(t *testing.T, slot string, prio int, want display.RenderFunc[display.RingFrame], period time.Duration) {
	t.Helper()
	// Sampled over the first 90 ms in real time: a later sample would expire a layer whose TTL is
	// a short step's, and the shapes are continuous (pulse, comet) or flat (blink, solid) there.
	_ = period
	for _, ms := range []int{5, 25, 45, 65, 85} {
		time.Sleep(time.Until(r.t0.Add(time.Duration(ms) * time.Millisecond)))
		at := time.Now()
		got := r.ringStack.Frame(at)
		if top := r.ringStack.Top(); top.Name != slot || top.Prio != prio {
			t.Fatalf("ring top %s/%d, want %s/%d", top.Name, top.Prio, slot, prio)
		}
		exp, _ := want(at, display.Blank(ringPx))
		for i := range got {
			if d := absDiff(got[i], exp[i]); d > 8 {
				t.Fatalf("ring at %d ms, px %d: %v, want %v", ms, i, got[i], exp[i])
			}
		}
	}
}

func absDiff(a, b hw.RGB) int {
	d := 0
	for _, p := range [][2]uint8{{a.R, b.R}, {a.G, b.G}, {a.B, b.B}} {
		x := int(p[0]) - int(p[1])
		if x < 0 {
			x = -x
		}
		d = max(d, x)
	}
	return d
}

// frontIs checks the front's top layer is slot at prio and shows want's first frame.
func (r *regressApp) frontIs(t *testing.T, slot string, prio int, want display.RenderFunc[display.FrontFrame]) {
	t.Helper()
	now := time.Now()
	got := r.matrixStack.Frame(now)
	if top := r.matrixStack.Top(); top.Name != slot || top.Prio != prio {
		t.Fatalf("front top %s/%d, want %s/%d", top.Name, top.Prio, slot, prio)
	}
	exp, _ := want(now, display.FrontFrame{})
	if got.Seg != exp.Seg {
		t.Fatalf("front: %v, want %v", got.Seg, exp.Seg)
	}
}

func (r *regressApp) clip(t *testing.T, name string) display.RenderFunc[display.FrontFrame] {
	t.Helper()
	c, ok := r.front.Anim(name)
	if !ok {
		t.Fatalf("no %s animation", name)
	}
	return c.Play(true, false, r.brightness)
}

// played waits for aplay to have been given the sound's file.
func (r *regressApp) played(t *testing.T, sound string) bool {
	t.Helper()
	for i := 0; i < 50; i++ {
		b, _ := os.ReadFile(r.aplayLog)
		if strings.Contains(string(b), "/"+sound+".wav") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (r *regressApp) gone(t *testing.T, slot string) {
	t.Helper()
	if r.ringStack.Has(slot) || r.matrixStack.Has(slot) {
		t.Fatalf("%s still shows", slot)
	}
}

func TestRegressBooting(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.showLife(lifeBooting)
	r.ringIs(t, lifeSlot, display.PrioDegraded, display.Pulse(display.Blue, 2*time.Second), 2*time.Second)
	r.frontIs(t, lifeSlot, display.PrioDegraded, r.clip(t, "booting"))
	r.mark()
	r.showLife(lifeBootFailed)
	r.ringIs(t, lifeSlot, display.PrioDegraded, display.Pulse(display.Red, 2*time.Second), 2*time.Second)
	os.MkdirAll(filepath.Dir(r.cfg().Path(bootMarker)), 0o755)
	os.WriteFile(r.cfg().Path(bootMarker), nil, 0o644) // greeted already: ready only clears
	r.showLife(lifeReady)
	r.gone(t, lifeSlot)
}

func TestRegressBootingWithoutClip(t *testing.T) {
	r := newRegressApp(t)
	r.front = display.Seg7Front{Clips: &display.ClipLibrary{NoBuiltin: true}}
	r.matrixStack.Push("clock", display.PrioBase+1, 0, func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
		return display.FrontFrame{Seg: display.MatrixFrame{Seg: [hw.Digits]uint8{0x7f}, Brightness: 255}}, false
	})
	r.showLife(lifeBooting) // no animation: a dark front over the clock (the time is unknown)
	r.frontIs(t, lifeSlot, display.PrioDegraded, func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
		return display.FrontFrame{}, false
	})
}

func TestRegressDegraded(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.showDegraded(true)
	r.ringIs(t, "degraded", display.PrioDegraded, display.Pulse(display.Red.Scale(40), 3*time.Second), 3*time.Second)
	r.showDegraded(false)
	r.gone(t, "degraded")
}

func TestRegressVoiceUnavailable(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.voiceUnavailable()
	r.ringIs(t, "voice", display.PrioError, display.Pulse(display.Red, 700*time.Millisecond), 700*time.Millisecond)
	if !r.played(t, "something_went_wrong") || r.plays.Load() != 1 {
		t.Errorf("the error phrase, played as speech: plays=%d", r.plays.Load())
	}
	r.ringStack.Frame(time.Now().Add(2100 * time.Millisecond))
	if r.ringStack.Has("voice") {
		t.Error("the red pulse outlived its 2 s")
	}
}

func TestRegressMute(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.setMute(true)
	r.ringIs(t, "mute", display.PrioLocal, effect("mute"), time.Second)
	r.setMute(false)
	r.gone(t, "mute")
}

func TestRegressCalls(t *testing.T) {
	r := newRegressApp(t)
	r.ic = &icState{} // the window's clock only
	r.mark()
	r.hook("call_outgoing")
	r.ringIs(t, "intercom", display.PrioCall, display.Comet(display.Green, 1200*time.Millisecond), 1200*time.Millisecond)
	r.frontIs(t, "intercom", prioCallFront, r.clip(t, "call_outgoing"))
	if !r.played(t, "calls_ringing_outgoing") || r.plays.Load() != 0 {
		t.Error("the outgoing ring tone, mixed")
	}
	r.endShow("intercom")
	r.mark()
	r.hook("call_incoming")
	r.ringIs(t, "intercom", display.PrioCall, display.Blink(display.Green, time.Second), time.Second)
	r.frontIs(t, "intercom", prioCallFront, r.clip(t, "call_incoming"))
	if !r.played(t, "calls_ringing_incoming") {
		t.Error("the incoming ring tone")
	}
	// «Алиса» heard while it rings: the ring (not the clip) lets the listening stage through
	r.ic.wokeAt = time.Now()
	if f := r.ringStack.Frame(time.Now()); f[0] != (hw.RGB{}) {
		t.Errorf("the ringing ring did not yield in the command window: %v", f[0])
	}
	r.frontIs(t, "intercom", prioCallFront, r.clip(t, "call_incoming"))
	r.ic.wokeAt = time.Time{}
	r.endShow("intercom")
	r.mark()
	r.icConnected()
	r.ringIs(t, "intercom", display.PrioCall, display.Pulse(display.Blue, 2500*time.Millisecond), 2500*time.Millisecond)
	if !r.played(t, "calls_connected") {
		t.Error("the connected sound")
	}
	r.endShow("intercom")
	r.gone(t, "intercom")
}

func TestRegressCallSoundFallbacks(t *testing.T) {
	r := newRegressApp(t)
	snd := filepath.Join(r.cfg().SysRoot, "cfw/sounds")
	for _, s := range []string{"calls_ringing_incoming", "calls_connected", "calls_busy"} {
		os.Remove(filepath.Join(snd, s+".wav"))
	}
	r.hook("call_incoming")
	if !r.played(t, "notification_sound") {
		t.Error("incoming without its tone: the announce chime, repeated")
	}
	r.endShow("intercom")
	os.Remove(r.aplayLog)
	r.icConnected()
	if !r.played(t, "notification_sound") {
		t.Error("connected without its sound: the announce chime")
	}
	r.hook("call_busy")
	if !r.played(t, "calls_ended") {
		t.Error("busy without its sound: calls_ended")
	}
}

func TestRegressCallEnds(t *testing.T) {
	r := newRegressApp(t)
	r.hook("call_busy") // refused, no answer, no audio: the error expression and the busy sound
	r.ringStack.Frame(time.Now())
	if top := r.ringStack.Top(); top.Name != "voice" {
		t.Errorf("busy: ring top %s", top.Name)
	}
	if !r.ringStack.Has("voice") || !r.played(t, "calls_busy") {
		t.Error("busy: no error expression or no sound")
	}
	r.ringStack.Frame(time.Now().Add(errorTTL + 100*time.Millisecond))
	if r.ringStack.Has("voice") {
		t.Error("busy: the error outlived errorTTL")
	}
	r.mark()
	r.hook("call_ended")
	r.ringIs(t, "intercom-end", display.PrioCall, display.Flash(display.White, 500*time.Millisecond), 400*time.Millisecond)
	if !r.played(t, "calls_ended") {
		t.Error("ended: no sound")
	}
	os.Remove(r.aplayLog)
	r.ringStack.Remove("intercom-end")
	r.mark()
	r.hook("call_cancelled") // hung up before it was up: the flash, no sound
	r.ringIs(t, "intercom-end", display.PrioCall, display.Flash(display.White, 500*time.Millisecond), 400*time.Millisecond)
	if r.played(t, "calls_ended") {
		t.Error("cancelled: a sound")
	}
	if r.hook("call_missed") {
		t.Error("missed: something shown")
	}
}

func TestRegressAlarm(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.startAlarm()
	r.ringIs(t, "alarm", display.PrioError, display.Blink(display.Amber, 400*time.Millisecond), 400*time.Millisecond)
	r.frontIs(t, "alarm", display.PrioLocal, r.clip(t, "ringing"))
	if !r.stopAlarm() {
		t.Fatal("no alarm to stop")
	}
	r.gone(t, "alarm")
}

func TestRegressWifiSetup(t *testing.T) {
	r := newRegressApp(t)
	r.mark()
	r.showSetup(true)
	r.ringIs(t, "wifisetup", display.PrioSetup, display.Comet(display.Yellow, 1200*time.Millisecond), 1200*time.Millisecond)
	r.frontIs(t, "wifisetup", display.PrioSetup, r.elseFront("setup")) // seg7 has no setup clip: the setup scene
	r.showSetup(false)
	r.gone(t, "wifisetup")
}

func TestRegressVolumeTick(t *testing.T) {
	r := newRegressApp(t)
	r.volumeBeep()
	if !r.played(t, "volume_sound") || r.plays.Load() != 0 {
		t.Error("the volume tick, mixed")
	}
	os.Remove(r.aplayLog)
	c := *r.cfg()
	c.VolumeBeep = false
	r.conf.Store(&c)
	r.volumeBeep()
	if r.played(t, "volume_sound") {
		t.Error("volume_beep off: a tick")
	}
	c.VolumeBeep = true
	r.conf.Store(&c)
	r.music.streaming.Store(true)
	r.volumeBeep()
	if r.played(t, "volume_sound") {
		t.Error("over music: a tick")
	}
}

func TestRegressAnnounceChime(t *testing.T) {
	r := newRegressApp(t)
	if d := r.announceChime(); d < 90*time.Millisecond || d > 110*time.Millisecond {
		t.Errorf("the announcement waits %v, want the chime's 0.1 s", d)
	}
	if !r.played(t, "notification_sound") || r.plays.Load() != 0 {
		t.Error("the chime, mixed")
	}
	c := *r.cfg()
	c.AnnounceChime = false
	r.conf.Store(&c)
	if d := r.announceChime(); d != 0 {
		t.Error("announce_chime off: a wait")
	}
}

func TestRegressPowerAlert(t *testing.T) {
	r := newRegressApp(t)
	r.powerAlert(hw.PD{Health: "Dead"})
	if !r.played(t, "no_power") || r.plays.Load() != 1 {
		t.Error("the no-power line, as speech")
	}
	r.powerAlert(hw.PD{Health: "Dead"})
	time.Sleep(100 * time.Millisecond)
	if r.plays.Load() != 1 {
		t.Error("said twice in one boot")
	}
}

func TestRegressVoiceExpressions(t *testing.T) {
	for moment, expr := range map[string]string{"voice_error": "error", "voice_muted": "mute", "voice_cancelled": "blink",
		"call_failed": "error", "wifi_setup_failed": "error"} {
		r := newRegressApp(t)
		r.hook(moment)
		old := newRegressApp(t)
		ttl := momentByName[moment].ttl
		old.express("voice", expr, ttl)
		now := time.Now()
		if g, w := r.ringStack.Frame(now), old.ringStack.Frame(now); absDiff(g[0], w[0]) > 8 || r.ringStack.Top() != old.ringStack.Top() {
			t.Errorf("%s: ring %v %+v, the old express %v %+v", moment, g[0], r.ringStack.Top(), w[0], old.ringStack.Top())
		}
		if g, w := r.matrixStack.Frame(now), old.matrixStack.Frame(now); g.Seg != w.Seg {
			t.Errorf("%s: front differs from the old express", moment)
		}
	}
	r := newRegressApp(t)
	r.hook("wake_busy")
	if !r.ringStack.Has("busy") {
		t.Error("wake_busy: not in the busy slot")
	}
}

// Station 3's profile kept its silence: no tick, no chime, no error phrase, the red pulse stays.
func TestRegressOrionSilence(t *testing.T) {
	r := newRegressApp(t)
	r.prof.Moments = map[string][]string{"volume_change": {}, "announce": {}, "voice_unavailable": {"ring:unavailable"}}
	r.volumeBeep()
	if d := r.announceChime(); d != 0 {
		t.Error("a chime")
	}
	r.mark()
	r.voiceUnavailable()
	r.ringIs(t, "voice", display.PrioError, display.Pulse(display.Red, 700*time.Millisecond), 700*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if b, _ := os.ReadFile(r.aplayLog); len(b) > 0 || r.plays.Load() != 0 {
		t.Errorf("a sound: %q", b)
	}
}

// «стоп» with nothing to show for voice_cancelled (no blink clip on this display) still ends the
// listening stage at once, as express("voice", "blink") did.
func TestRegressCancelClearsTheStage(t *testing.T) {
	r := newRegressApp(t)
	if _, ok := r.front.Anim("blink"); ok {
		t.Skip("this display has a blink clip")
	}
	r.express("voice", "listening", 0)
	r.hook("voice_cancelled")
	r.gone(t, "voice")
}

// A voice stage fired again (HA's next event of it) goes on, its sound not played again; a hook
// fired again (a volume key) starts over, sound and all.
func TestStageAgainNoSecondSound(t *testing.T) {
	r := newRegressApp(t)
	if err := r.setMoment("listening", []show{{Steps: []scenarioStep{{Express: "listening", Sound: "volume_sound"}, {Express: "listening", Hold: true}}}}); err != nil {
		t.Fatal(err)
	}
	count := func(sound string) int {
		b, _ := os.ReadFile(r.aplayLog)
		return strings.Count(string(b), "/"+sound+".wav")
	}
	r.moment("voice", "listening", stageTTL)
	if !r.played(t, "volume_sound") {
		t.Fatal("the stage's sound did not play")
	}
	r.moment("voice", "listening", stageTTL) // HA's voice-started
	r.moment("voice", "listening", stageTTL) // and its transcribe
	time.Sleep(300 * time.Millisecond)
	if n := count("volume_sound"); n != 1 {
		t.Errorf("the stage's sound played %d times", n)
	}
	r.moment("voice", "thinking", stageTTL)
	r.moment("voice", "listening", stageTTL) // a new listening (a follow-up): its sound again
	time.Sleep(300 * time.Millisecond)
	if n := count("volume_sound"); n != 2 {
		t.Errorf("a new listening played its sound %d times in all, want 2", n)
	}

	r.hook("volume_change")
	time.Sleep(50 * time.Millisecond)
	r.hook("volume_change")
	time.Sleep(300 * time.Millisecond)
	if n := count("volume_sound"); n != 4 {
		t.Errorf("two volume keys: %d sounds in all, want 4", n)
	}
}
