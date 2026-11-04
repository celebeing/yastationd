package app

import (
	"math"
	"sync"
	"time"
)

// Music sources (docs/music-sources.md): everything other than the assistant that plays music
// through the speaker — Music Assistant's player (sendspin-host) and a phone over Bluetooth. The
// rest of the daemon asks this layer, not a source, whether music plays (video, the volume tick,
// the acoustic measurement), and one loop ducks every source the same way and decides what plays
// when two would at once (music_overlap).

// mediaSource is one source.
type mediaSource interface {
	name() string
	// playing: music comes out of it now.
	playing() bool
	// setDuck sets its level, 1 = full, 0 = silent; called every duckTick (a source that changes
	// its level in steps takes one step per call).
	setDuck(level float32)
	// pause asks it to pause; false when it can't (it is muted instead while another plays).
	pause() bool
}

type mediaState struct {
	mu        sync.Mutex
	sources   []mediaSource
	since     map[string]time.Time // when each source started playing (its last start)
	paused    map[string]bool      // paused by us for another source (not paused again until it stops)
	wakeUntil time.Time            // the wake word ducks at once, before the assistant is busy
}

// addMedia registers a source (at start, before the loop runs).
func (a *App) addMedia(s mediaSource) {
	a.media.mu.Lock()
	a.media.sources = append(a.media.sources, s)
	a.media.mu.Unlock()
}

// musicPlaying: some source plays music now.
func (a *App) musicPlaying() bool { return a.playingSource() != "" }

// playingSource: the name of a source that plays ("" = none), the newest one if several.
func (a *App) playingSource() string {
	a.media.mu.Lock()
	defer a.media.mu.Unlock()
	best, at := "", time.Time{}
	for _, s := range a.media.sources {
		if s.playing() {
			if t := a.media.since[s.name()]; best == "" || t.After(at) {
				best, at = s.name(), t
			}
		}
	}
	return best
}

// duckNow: the wake word was heard. Music goes down at once, before arbitration, voice ID or HA
// get the speaker's own sound, and stays down until the assistant's session holds it.
func (a *App) duckNow() {
	a.media.mu.Lock()
	a.media.wakeUntil = time.Now().Add(wakeDuckBridge)
	a.media.mu.Unlock()
}

// wakeDuckBridge: how long the wake word's duck lasts by itself, until the session starts.
const wakeDuckBridge = 2 * time.Second

// mediaLoop: every duckTick, the level of each source. Under the assistant (and duckHold after it)
// every source goes to music_duck_db; with music_overlap = newest, a source that is not the newest
// playing one is paused once, or muted when it can't be paused.
func (a *App) mediaLoop(stop <-chan struct{}) {
	t := time.NewTicker(duckTick)
	defer t.Stop()
	var busyAt time.Time
	for {
		select {
		case <-stop:
			for _, s := range a.mediaSources() {
				s.setDuck(1)
			}
			return
		case now := <-t.C:
			if a.assistantBusy() {
				busyAt = now
			}
			a.media.mu.Lock()
			wake := now.Before(a.media.wakeUntil)
			a.media.mu.Unlock()
			level := float32(1)
			if wake || (!busyAt.IsZero() && now.Sub(busyAt) < duckHold) {
				level = float32(math.Pow(10, float64(a.cfg().MusicDuckDB)/20)) // may change live
			}
			a.mediaTick(now, level)
		}
	}
}

func (a *App) mediaSources() []mediaSource {
	a.media.mu.Lock()
	defer a.media.mu.Unlock()
	return append([]mediaSource(nil), a.media.sources...)
}

// mediaTick applies level to every source, and the overlap policy.
func (a *App) mediaTick(now time.Time, level float32) {
	sources := a.mediaSources()
	playing := map[string]bool{}
	for _, s := range sources {
		playing[s.name()] = s.playing()
	}
	a.media.mu.Lock()
	if a.media.since == nil {
		a.media.since, a.media.paused = map[string]time.Time{}, map[string]bool{}
	}
	newest, at, started := "", time.Time{}, false
	for _, s := range sources {
		n := s.name()
		if !playing[n] {
			delete(a.media.since, n)
			delete(a.media.paused, n)
			continue
		}
		if _, ok := a.media.since[n]; !ok {
			a.media.since[n] = now
			a.log.Info("music: source playing", "source", n)
			started = true
		}
		if t := a.media.since[n]; newest == "" || t.After(at) {
			newest, at = n, t
		}
	}
	overlap := a.cfg().MusicOverlap
	var toPause []mediaSource
	levels := make([]float32, len(sources))
	for i, s := range sources {
		n := s.name()
		levels[i] = level
		if overlap != "newest" || !playing[n] || n == newest {
			continue
		}
		if !a.media.paused[n] {
			a.media.paused[n] = true
			toPause = append(toPause, s)
		}
		levels[i] = 0 // muted while the newest plays, or until its pause takes effect
	}
	a.media.mu.Unlock()
	if started && a.stopVideo("music") { // music started: the user wants that instead
		a.publishDisplayMode()
	}
	for _, s := range toPause {
		ok := s.pause()
		a.log.Info("music: another source started", "playing", newest, "paused", s.name(), "pause_ok", ok)
	}
	for i, s := range sources {
		s.setDuck(levels[i])
	}
}
