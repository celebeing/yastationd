package app

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/sendspin"
)

// Music Assistant player: sendspin-host as a child, playing through the same dmix PCM as our own
// output. The daemon steers it: music ducks while the assistant listens, thinks, talks, announces
// or rings a timer, and (volume "linked") the player volume in Music Assistant is the speaker's.

const (
	duckTick = 100 * time.Millisecond
	// The duck holds this long after the assistant goes quiet, so music does not swell back
	// between a reply and its follow-up listening, or between a chime and the announcement.
	duckHold = 1500 * time.Millisecond
)

type musicState struct {
	host      *sendspin.Host
	version   atomic.Value // string: sendspin-host's version, from its READY line
	streaming atomic.Bool
	duck      atomic.Uint32 // math.Float32bits of the level last sent

	reported atomic.Int64 // UnixNano of the last local volume change sent to the server

	screen musicScreen // the music on the front display (musicdisplay.go)
}

// musicEchoQuiet: after a local volume change the server's volume events are ignored this long.
// The server answers our reports — late, and sometimes not exactly (Music Assistant sent 16 back
// for our 15) — and taking those as commands threw the level back mid-turn or let it creep.
const musicEchoQuiet = 1500 * time.Millisecond

func (a *App) newMusic() *sendspin.Host {
	c := a.cfg
	if !c.Sendspin {
		return nil
	}
	bin := c.Path(c.SendspinBin)
	if _, err := os.Stat(bin); err != nil {
		a.log.Warn("music player disabled: sendspin-host missing", "path", c.SendspinBin)
		return nil
	}
	state := c.Path(c.SendspinState)
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		a.log.Warn("music: state dir", "err", err)
	}
	name := c.SendspinName
	if name == "" {
		name = c.DeviceName
	}
	h := &sendspin.Host{Bin: bin, Log: a.log, OnEvent: a.onMusicEvent}
	h.Args = func() []string {
		args := []string{"-n", name, "-s", state, "-p", strconv.Itoa(c.SendspinPort),
			"-D", a.prof.Audio.Playback, "-l", "warn", "-V", strconv.Itoa(a.musicVolume())}
		if c.SendspinUnpaired {
			args = append(args, "-a")
		}
		if c.SendspinServer != "" {
			args = append(args, "-u", c.SendspinServer)
		}
		if a.linkedVolume() {
			args = append(args, "-G")
		}
		if a.musicLevelsNeeded() {
			args = append(args, "-b", strconv.Itoa(a.musicBandCount()))
		}
		return args
	}
	h.OnStart = func() { // a restarted host starts unducked: resend the current level
		h.Send("duck %.3f", math.Float32frombits(a.music.duck.Load()))
	}
	a.music.duck.Store(math.Float32bits(1))
	return h
}

func (a *App) linkedVolume() bool { return a.cfg.SendspinVolume == "linked" }

// musicVolume is the player volume announced to the server at start: the speaker's volume when
// linked, else the last music volume the server set.
func (a *App) musicVolume() int {
	if !a.linkedVolume() {
		return a.store.get().MusicVolume
	}
	if v, err := a.mixer.Get(a.prof.Audio.Volume); err == nil {
		return a.percentFromRaw(v)
	}
	return 100
}

func (a *App) onMusicEvent(ev sendspin.Event) {
	switch ev.Kind {
	case "READY":
		v := "unknown" // a host older than the version field
		if len(ev.Fields) > 1 {
			v = ev.Fields[1]
		}
		a.music.version.Store(v)
		a.log.Info("music: player ready", "client_id", firstField(ev), "version", v)
		a.publishVersion()
	case "CONNECTED":
		a.log.Info("music: server", "connected", ev.Int(0, 0) == 1)
	case "STREAM":
		on := firstField(ev) == "start"
		a.music.streaming.Store(on)
		if on && a.stopVideo("music") { // music started on this player: the user wants that instead
			a.publishDisplayMode()
		}
		a.log.Info("music: stream", "on", on, "rate", ev.Int(1, 0), "channels", ev.Int(2, 0))
	case "PLAYBACK":
		a.onMusicPlayback(firstField(ev) == "playing")
	case "META":
		a.onMusicMeta(ev.Rest)
	case "LEVELS":
		a.onMusicLevels(firstField(ev))
	case "VOLUME":
		v := ev.Int(0, -1)
		if v < 0 || v > 100 {
			return
		}
		if !a.linkedVolume() {
			a.store.update(func(s *persistState) { s.MusicVolume = v })
			return
		}
		if time.Since(time.Unix(0, a.music.reported.Load())) < musicEchoQuiet {
			return // the speaker is being turned here: local wins, the server's events are echoes
		}
		cur, err := a.mixer.Get(a.prof.Audio.Volume)
		if err == nil && abs(a.percentFromRaw(cur)-v) <= 1 {
			return // our own report coming back (possibly rounded by the server)
		}
		a.log.Info("music: server set the volume", "pct", v)
		a.setMaster(a.rawFromPercent(v), 0, false)
	}
}

func firstField(ev sendspin.Event) string {
	if len(ev.Fields) == 0 {
		return ""
	}
	return ev.Fields[0]
}

// reportMusicVolume tells the server about a local volume change (linked mode).
func (a *App) reportMusicVolume(pct int) {
	if a.music.host != nil && a.linkedVolume() {
		a.music.reported.Store(time.Now().UnixNano())
		a.music.host.Send("volume %d", pct)
	}
}

// assistantBusy: the assistant has the speaker — a voice session, a reply being computed, our own
// playback (TTS, announcements, local sounds) or a ringing timer.
func (a *App) assistantBusy() bool {
	return a.speaking() || a.sat.inDialog() || a.icActive()
}

// duckLoop ducks the music while the assistant is busy and brings it back duckHold after.
func (a *App) duckLoop(stop <-chan struct{}) {
	low := float32(math.Pow(10, float64(a.cfg.MusicDuckDB)/20))
	t := time.NewTicker(duckTick)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			level := float32(1)
			if a.assistantBusy() {
				last = now
			}
			if !last.IsZero() && now.Sub(last) < duckHold {
				level = low
			}
			if prev := math.Float32frombits(a.music.duck.Swap(math.Float32bits(level))); prev != level {
				a.music.host.Send("duck %.3f", level)
				if a.music.streaming.Load() {
					a.log.Debug("music: duck", "level", level)
				}
			}
		}
	}
}
