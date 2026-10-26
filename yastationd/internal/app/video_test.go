package app

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// newVideoApp: a 2x1 pixel screen, a fake aplay that plays for 10 s (until cut), and frames of
// 20 ms each lighting pixel 0 at 10, 20, 30, … (gamma 1).
func newVideoApp(t *testing.T, frames int) *App {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "aplay"), []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	dir := t.TempDir()
	var led []byte
	for i := 0; i < frames; i++ {
		v := byte(10 * (i + 1))
		led = display.AppendLedFrame(led, []byte{v, v, v, 0, 0, 0}, 20)
	}
	cfg := config.Default()
	cfg.VideoFrames, cfg.VideoAudio = filepath.Join(dir, "video.led"), filepath.Join(dir, "video.wav")
	if frames > 0 {
		os.WriteFile(cfg.VideoFrames, led, 0o644)
	}
	os.WriteFile(cfg.VideoAudio, []byte("RIFF"), 0o644)
	st := defaultState(128, 128, 255, false)
	a := &App{
		cfg:         cfg,
		log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:       newStateStore("", st),
		screen:      hw.NewVirtualDotMatrix(2, 1),
		front:       display.PixelFront{W: 2, H: 1},
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
	}
	a.player = &hw.Player{Device: "default", OnState: a.onPlayState}
	return a
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestVideoPlaysInSyncAndEndsWithItsAudio(t *testing.T) {
	a := newVideoApp(t, 500) // 10 s of picture
	if !slices.Contains(a.displayModes(), videoMode) {
		t.Fatal("no video mode on a pixel screen")
	}
	a.cmdDisplayMode([]byte(videoMode))
	waitFor(t, "playback", func() bool {
		a.video.mu.Lock()
		defer a.video.mu.Unlock()
		return a.video.run != nil && a.video.run.v != nil
	})
	if a.displayMode() != videoMode || a.store.get().Mode != "clock" || !a.playing.Load() {
		t.Fatalf("mode %q, persisted %q, playing %v", a.displayMode(), a.store.get().Mode, a.playing.Load())
	}
	// The frame is picked by the time since the audio started, not by how often it is asked for.
	a.video.mu.Lock()
	start := a.video.run.start
	a.video.mu.Unlock()
	f := a.matrixBase(start.Add(105 * time.Millisecond)) // frame 5
	if len(f.Px) != 2 || f.Px[0].R != 60 {
		t.Fatalf("frame at 105 ms = %+v, want level 60", f.Px)
	}
	// «стоп» (or a reply, an announcement) cuts the audio: the video ends with it.
	a.player.Stop()
	waitFor(t, "the end", func() bool { return !a.videoActive() })
	if a.displayMode() != "clock" {
		t.Errorf("mode after = %q", a.displayMode())
	}
}

func TestVideoStopsForVoiceAndModes(t *testing.T) {
	a := newVideoApp(t, 500)
	a.cmdDisplayMode([]byte(videoMode))
	waitFor(t, "audio", func() bool { return a.playing.Load() })
	a.cmdDisplayMode([]byte("off"))
	if a.videoActive() || a.store.get().Mode != "off" {
		t.Fatal("another mode did not end the video")
	}
	waitFor(t, "audio stopped", func() bool { return !a.playing.Load() })

	if !a.startVideo() {
		t.Fatal("restart refused")
	}
	if !a.stopVideo("voice") || a.videoActive() {
		t.Fatal("voice did not stop the video")
	}
	waitFor(t, "audio stopped", func() bool { return !a.playing.Load() })
}

func TestVideoRefusalsAndFallbacks(t *testing.T) {
	a := newVideoApp(t, 0) // no frames file
	a.cmdDisplayMode([]byte(videoMode))
	waitFor(t, "fallback", func() bool { return !a.videoActive() })
	if a.playing.Load() || a.displayMode() != "clock" {
		t.Error("audio started without frames, or the mode stuck")
	}

	a = newVideoApp(t, 3) // 60 ms: the picture ends before the 10 s track, which is then cut
	os.Remove(a.cfg.VideoAudio)
	a.startVideo()
	waitFor(t, "the end of a silent video", func() bool { return !a.videoActive() })

	a = newVideoApp(t, 3)
	a.music.streaming.Store(true)
	if a.startVideo() {
		t.Error("started over music")
	}

	a = newVideoApp(t, 3)
	a.screen, a.front = nil, display.Seg7Front{}
	if a.startVideo() || slices.Contains(a.displayModes(), videoMode) {
		t.Error("video offered without a pixel screen")
	}
}

func TestClockDateOnlyWhereItFits(t *testing.T) {
	a := &App{front: display.Seg7Front{}, store: newStateStore("", defaultState(128, 128, 128, false))}
	if slices.Contains(a.displayModes(), "clock_date") {
		t.Fatal("seg7 offers clock_date")
	}
	a.store.update(func(s *persistState) { s.Mode = "clock_date" })
	if m := a.displayMode(); m != "clock" {
		t.Fatalf("a stale clock_date state reads %q on seg7", m)
	}
	a.front = display.PixelFront{W: 21, H: 12}
	if !slices.Contains(a.displayModes(), "clock_date") || a.displayMode() != "clock_date" {
		t.Fatal("orion's screen offers clock_date")
	}
}
