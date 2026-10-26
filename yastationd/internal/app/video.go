package app

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// The "video" display mode (docs/video-mode.md): a frames file on the pixel front screen with its
// audio track through the speaker, in sync. The picture is clocked by the audio: the frame shown is
// the one under the time since aplay started (plus video_av_offset_ms), never a free-running
// frame counter. It plays once and the display returns to the mode it had (the persisted mode is
// never "video", so a restart does not replay it).
//
// Anything that takes the speaker or the user's attention ends it: a voice session (wake word,
// key), «стоп», a reply or an announcement (they cut our aplay), a timer ringing, an intercom call,
// music starting on the Music Assistant player, and any other display mode. It does not start while
// music is playing (sendspin-host has no pause; the two tracks would mix).

const videoMode = "video"

type videoState struct {
	mu  sync.Mutex
	run *videoRun // the current playback (loading or playing); nil = none
}

// videoRun is one playback. v and start are set once the frames are loaded and the audio started.
type videoRun struct {
	v     *display.Video
	start time.Time     // frame time zero: the audio's start, shifted by the A/V offset
	quit  chan struct{} // closed when the run is ended from outside (or by itself)
}

// videoPossible: the mode needs a pixel front screen.
func (a *App) videoPossible() bool { return a.screen != nil && a.front != nil }

// startVideo begins a playback (frames load in the background). false = refused.
func (a *App) startVideo() bool {
	if !a.videoPossible() {
		a.log.Warn("video: no pixel front screen")
		return false
	}
	if a.music.streaming.Load() {
		a.log.Warn("video: music is playing; pause it first")
		return false
	}
	a.video.mu.Lock()
	if a.video.run != nil {
		a.video.mu.Unlock()
		return true
	}
	r := &videoRun{quit: make(chan struct{})}
	a.video.run = r
	a.video.mu.Unlock()
	go a.playVideo(r)
	return true
}

// stopVideo ends the current playback, if any; its audio stops with it. It does not publish the
// display mode (the caller sets the next one, or calls publishDisplayMode).
func (a *App) stopVideo(reason string) bool {
	a.video.mu.Lock()
	r := a.video.run
	if r != nil {
		a.video.run = nil
		close(r.quit)
	}
	a.video.mu.Unlock()
	if r != nil {
		a.log.Info("video: stopped", "reason", reason)
	}
	return r != nil
}

// endVideo ends run r (if it is still the current one) from the playback itself.
func (a *App) endVideo(r *videoRun, reason string) {
	a.video.mu.Lock()
	cur := a.video.run == r
	if cur {
		a.video.run = nil
		close(r.quit)
	}
	a.video.mu.Unlock()
	if cur {
		a.log.Info("video: ended", "reason", reason)
		a.publishDisplayMode()
	}
}

func (a *App) videoActive() bool {
	a.video.mu.Lock()
	defer a.video.mu.Unlock()
	return a.video.run != nil
}

func (a *App) playVideo(r *videoRun) {
	w, h := a.screen.Size()
	frames := a.cfg.Path(a.cfg.VideoFrames)
	v, err := display.LoadVideo(frames, w, h, a.prof.Matrix.Gamma)
	if err != nil {
		a.log.Warn("video: no frames", "path", frames, "err", err)
		a.endVideo(r, "no frames")
		return
	}
	select {
	case <-r.quit: // stopped while loading
		return
	default:
	}

	var done <-chan error // nil = silent
	stopAudio := func() {}
	if audio := a.cfg.Path(a.cfg.VideoAudio); fileExists(audio) {
		a.unmute()
		d, stop, err := a.player.Start(audio)
		if err != nil {
			a.log.Warn("video: no audio, playing silently", "err", err)
		} else {
			done, stopAudio = d, stop
		}
	} else {
		a.log.Warn("video: no audio file, playing silently", "path", audio)
	}
	start := time.Now().Add(a.cfg.VideoAVOffset)
	a.video.mu.Lock()
	cur := a.video.run == r
	if cur {
		r.v, r.start = v, start
	}
	a.video.mu.Unlock()
	if !cur {
		stopAudio()
		return
	}
	a.log.Info("video: playing", "frames", v.Frames(), "length", v.Duration(), "audio", done != nil)

	end := time.NewTimer(time.Until(start.Add(v.Duration())))
	defer end.Stop()
	for {
		select {
		case <-r.quit:
			stopAudio()
			return
		case err := <-done:
			done = nil
			switch {
			case errors.Is(err, hw.ErrCut): // «стоп», a reply, an announcement, an alarm took the speaker
				a.endVideo(r, "audio cut")
				return
			case err != nil: // aplay could not play (no sound device on a host): the picture goes on
				a.log.Warn("video: audio failed, playing silently", "err", err)
			}
			// a clean end of the track: the picture plays out
		case <-end.C:
			stopAudio() // a track longer than the picture
			a.endVideo(r, "end")
			return
		}
	}
}

// videoFrame is the video mode's base frame; ok false = no video running.
func (a *App) videoFrame(now time.Time) (display.FrontFrame, bool) {
	a.video.mu.Lock()
	r := a.video.run
	var v *display.Video
	var start time.Time
	if r != nil {
		v, start = r.v, r.start
	}
	a.video.mu.Unlock()
	if r == nil {
		return display.FrontFrame{}, false
	}
	if v != nil {
		if f, ok := v.Frame(now.Sub(start), a.brightness()); ok {
			return f, true
		}
	}
	w, h := a.screen.Size()
	return display.FrontFrame{Px: make([]hw.RGB, w*h)}, true // loading, or past the last frame: dark
}

// displayMode is the mode HA sees: "video" while one plays, else the persisted mode.
func (a *App) displayMode() string {
	if a.videoActive() {
		return videoMode
	}
	m := a.store.get().Mode
	if m == "clock_date" && (a.front == nil || !a.front.HasClockDate()) {
		return "clock" // a state from a display that had it; this one shows the clock
	}
	return m
}

func (a *App) publishDisplayMode() {
	a.mqtt.Publish(a.topics.State("display_mode"), a.displayMode())
}

// displayModes are the Display select's options on this speaker.
func (a *App) displayModes() []string {
	m := []string{"clock"}
	if a.front != nil && a.front.HasClockDate() {
		m = append(m, "clock_date")
	}
	m = append(m, "off", "message")
	if a.videoPossible() {
		m = append(m, videoMode)
	}
	return m
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
