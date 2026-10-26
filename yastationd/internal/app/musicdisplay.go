package app

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// The music on the front display (docs/music-sendspin.md, "On the display"): from sendspin-host's
// PLAYBACK / META / LEVELS events. A new track runs its title once (not a resume of the same one),
// a pause and a resume play their animations (moments media_pause / media_resume), and on a pixel
// screen the spectrum sits under the clock while music plays.

const (
	// levelsFresh: the spectrum is drawn while LEVELS keep coming; after this without one (paused,
	// stopped, the host gone) the clock is back.
	levelsFresh = 1500 * time.Millisecond
	// levelsFall: a bar falls this fast (full height per second), so it does not flicker between
	// frames; it rises at once.
	levelsFall = 2.5
	// The bars' range: a band at levelsLowDB or below is empty, at levelsHighDB full (dB from
	// sendspin-host's 0..255 = -60..0 dBFS). Music sits mostly in between.
	levelsLowDB, levelsHighDB = -50.0, -6.0
	// musicTitleMin / Max bound how long the title stays when it fits (or is very long).
	musicTitleMin = 3 * time.Second
	musicTitleMax = 30 * time.Second
	// musicPauseAfter: a stop shorter than this is not a pause (a track change restarts the stream).
	musicPauseAfter = 1500 * time.Millisecond
	// musicTitleLate: a track first seen this far in was already playing (no title).
	musicTitleLate = 10_000 // ms
)

type musicTrack struct {
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Duration int    `json:"duration_ms"`
	Progress int    `json:"progress_ms"`
}

func (t musicTrack) id() string { return t.Title + "\x00" + t.Artist + "\x00" + t.Album }

// line is what the running line says: "Artist - Title" (either alone when the other is missing).
func (t musicTrack) line() string {
	switch {
	case t.Artist != "" && t.Title != "":
		return t.Artist + " - " + t.Title
	case t.Title != "":
		return t.Title
	}
	return t.Artist
}

type musicScreen struct {
	mu       sync.Mutex
	playing  bool
	track    string // id of the track last shown (its title ran, or it was playing)
	pending  string // a new track's line, waiting for playback to start
	bands    []float64
	shown    []float64 // what the bars show, falling towards bands
	levelsAt time.Time
	drawnAt  time.Time
	gen      int  // playback changes, to drop a pause that turned out to be a track change
	paused   bool // the pause animation was shown: the next start is a resume
}

// musicLevelsWanted: the spectrum is on and this display can draw it (sendspin-host computes it
// only then).
func (a *App) musicLevelsWanted() bool {
	return a.cfg.MusicLevels && a.front != nil && a.front.HasLevels() && a.screen != nil
}

// musicBandCount is how many bands sendspin-host computes: one per column.
func (a *App) musicBandCount() int {
	w, _ := a.screen.Size()
	return w
}

func (a *App) onMusicPlayback(playing bool) {
	m := &a.music.screen
	m.mu.Lock()
	was := m.playing
	m.playing = playing
	pending := m.pending
	m.pending = ""
	known := m.track != ""
	m.mu.Unlock()
	if playing == was {
		return
	}
	a.log.Debug("music: playback", "playing", playing)
	m.mu.Lock()
	m.gen++ // a pause waiting to be shown is off
	gen, paused := m.gen, m.paused
	m.paused = false
	m.mu.Unlock()
	switch {
	case playing && pending != "":
		a.showMusicTitle(pending)
	case playing && paused:
		a.musicMoment("media_resume")
	case !playing && known:
		// Music Assistant restarts its stream on a track change (stopped, then playing again
		// ~0.5 s later): only a stop that lasts is a pause, and only a shown pause gets a resume.
		time.AfterFunc(musicPauseAfter, func() {
			m.mu.Lock()
			still := m.gen == gen && !m.playing
			m.paused = still
			m.mu.Unlock()
			if still {
				a.musicMoment("media_pause")
			}
		})
	}
}

func (a *App) onMusicMeta(rest string) {
	var t musicTrack
	if rest == "" || json.Unmarshal([]byte(rest), &t) != nil || t.id() == "\x00\x00" {
		return // no track (cleared): the last one stays known, so its resume is not a new track
	}
	m := &a.music.screen
	m.mu.Lock()
	if t.id() == m.track {
		m.mu.Unlock()
		return // the same track: progress, a resume, a reconnect
	}
	m.track = t.id()
	if t.Duration > 0 && t.Progress > musicTitleLate {
		// Already well into it: first sight of a track that was playing (a reconnect, our
		// restart), not a new one starting.
		m.mu.Unlock()
		return
	}
	playing := m.playing
	if !playing {
		m.pending = t.line() // shown when it starts playing
	}
	m.mu.Unlock()
	a.log.Info("music: track", "title", t.Title, "artist", t.Artist)
	if playing {
		a.showMusicTitle(t.line())
	}
}

// showMusicTitle runs s once over the music's screen.
func (a *App) showMusicTitle(s string) {
	if !a.cfg.MusicTitle || a.front == nil || strings.TrimSpace(s) == "" {
		return
	}
	ttl := min(max(a.front.MarqueeTime(s, a.marqueeStep()), musicTitleMin), musicTitleMax)
	a.matrixStack.Push("music_title", display.PrioMusicTitle, ttl, a.front.Marquee(s, a.marqueeStep(), a.brightness))
}

func (a *App) musicMoment(name string) {
	if a.cfg.MusicClips {
		a.moment("music", name, 0)
	}
}

func (a *App) onMusicLevels(hexBands string) {
	b, err := hex.DecodeString(hexBands)
	if err != nil || len(b) == 0 {
		return
	}
	m := &a.music.screen
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bands) != len(b) {
		m.bands = make([]float64, len(b))
		m.shown = make([]float64, len(b))
	}
	for i, v := range b {
		db := float64(v)/255*60 - 60
		m.bands[i] = math.Max(0, math.Min(1, (db-levelsLowDB)/(levelsHighDB-levelsLowDB)))
	}
	m.levelsAt = time.Now()
}

// musicBands is the spectrum to draw at now, or nil when there is none to draw (no fresh levels).
func (a *App) musicBands(now time.Time) []float64 {
	m := &a.music.screen
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.levelsAt.IsZero() || now.Sub(m.levelsAt) > levelsFresh || !a.music.streaming.Load() {
		m.drawnAt = time.Time{}
		return nil
	}
	fall := 0.0
	if !m.drawnAt.IsZero() {
		fall = now.Sub(m.drawnAt).Seconds() * levelsFall
	}
	m.drawnAt = now
	for i, v := range m.bands {
		m.shown[i] = math.Max(v, m.shown[i]-fall)
	}
	return append([]float64(nil), m.shown...)
}
