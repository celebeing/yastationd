package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// voiceStages are fired by the pipeline's code in the voice slot, resolved by expression name.
var voiceStages = map[string]bool{"startup": true, "listening": true, "thinking": true, "speaking": true,
	"waiting": true, "dialog_end": true, "arbitration_lost": true, "media_pause": true, "media_resume": true}

func TestDefaultThemeCoversTheCatalog(t *testing.T) {
	for _, m := range momentCatalog {
		if _, ok := defaultTheme[m.Name]; !ok && !voiceStages[m.Name] {
			t.Errorf("%s: in the catalogue, not in the default theme", m.Name)
		}
	}
	for name := range defaultTheme {
		if _, ok := momentByName[name]; !ok {
			t.Errorf("%s: in the default theme, not in the catalogue (the web app would not list it)", name)
		}
	}
}

// themeTestApp is an App with a ring, a front-less display and <cfw>/sounds under a temp sysroot.
func themeTestApp(t *testing.T, sounds ...string) *App {
	root := t.TempDir()
	cfg := config.Default()
	cfg.SysRoot = root
	a := &App{log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ring:        &hw.Ring{},
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(12) }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
	}
	a.prof.Platform.CFWRoot = "/cfw"
	a.conf.Store(&cfg)
	dir := filepath.Join(root, "cfw/sounds")
	os.MkdirAll(dir, 0o755)
	for _, s := range sounds {
		os.WriteFile(filepath.Join(dir, s+".wav"), wavFile(make([]byte, 3200), 16000), 0o644) // 0.1 s
	}
	return a
}

func TestSoundFallbacks(t *testing.T) {
	a := themeTestApp(t, "calls_ended")
	if p := a.soundPath("calls_busy|calls_ended"); filepath.Base(p) != "calls_ended.wav" {
		t.Errorf("fallback: %q", p)
	}
	if a.soundPath("calls_busy") != "" || a.soundPath("../etc/passwd") != "" {
		t.Error("a missing or bad name resolved")
	}
	if d := a.soundLen("calls_ended"); d < 90*time.Millisecond || d > 110*time.Millisecond {
		t.Errorf("length %v", d)
	}
	if got := a.soundNames(); len(got) != 1 || got[0] != "calls_ended" {
		t.Errorf("names %v", got)
	}
}

func TestHeldMomentEnds(t *testing.T) {
	a := themeTestApp(t)
	if !a.hook("mic_muted") || !a.ringStack.Has("mute") {
		t.Fatal("mic_muted: no amber ring")
	}
	time.Sleep(50 * time.Millisecond)
	if !a.ringStack.Has("mute") {
		t.Fatal("a held moment ended on its own")
	}
	a.endHook("mic_muted")
	if a.ringStack.Has("mute") {
		t.Fatal("endHook left the ring")
	}
}

func TestSilentMomentShowsNothing(t *testing.T) {
	a := themeTestApp(t)
	if a.hook("timer_started") {
		t.Error("timer_started is silent in the default theme")
	}
	// a sound-only moment without its sound here shows nothing either
	if a.hook("volume_change") {
		t.Error("volume_change without volume_sound.wav")
	}
}

func TestAnnounceWaitsForItsSound(t *testing.T) {
	a := themeTestApp(t, "notification_sound")
	if _, d := a.fire("announce"); d < 90*time.Millisecond {
		t.Errorf("announce: its sound ends after %v", d)
	}
}

func TestYieldingLayer(t *testing.T) {
	a := themeTestApp(t)
	yield := false
	a.setYield("y", func() bool { return yield })
	a.pushRing("y", display.PrioCall, 0, stepEffect("solid", []int{255, 0, 0}, 0))
	if f := a.ringStack.Frame(time.Now()); f[0] != (hw.RGB{R: 255}) {
		t.Fatalf("layer not shown: %v", f[0])
	}
	yield = true
	if f := a.ringStack.Frame(time.Now()); f[0] != (hw.RGB{}) {
		t.Fatalf("layer shown while yielding: %v", f[0])
	}
}

func TestHeldSoundLoopsOnlyWhenHeld(t *testing.T) {
	for name, shows := range defaultTheme {
		for _, s := range shows {
			for i, st := range s.Steps {
				if st.Sound != "" && st.Hold && i == len(s.Steps)-1 && !strings.HasPrefix(st.Sound, "calls_ringing") {
					t.Errorf("%s: a held step repeats its sound %q", name, st.Sound)
				}
			}
		}
	}
}
