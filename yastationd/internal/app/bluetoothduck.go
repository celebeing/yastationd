package app

import (
	"math"
	"strings"
	"sync"

	"github.com/celebeing/yastationd/yastationd/internal/bluetooth"
)

// btSource: a phone over Bluetooth as a music source (media.go). It plays while the phone's
// player (AVRCP) says so; it ducks through the board's Bluetooth softvol, never the master, so
// volume_max still caps it; it pauses through AVRCP. Its track and pauses reach the front display
// as Music Assistant's do (musicdisplay.go).
type btSource struct {
	a  *App
	bt *bluetooth.Manager

	mu     sync.Mutex
	status string // the phone's last status
	track  string // id of the track last shown
}

// bluetoothFadeSteps: a full swing of the softvol takes this many duckTicks (one second).
const bluetoothFadeSteps = 10

// alsa softvol's default range: raw 0..255 over -51..0 dB.
const softvolMinDB = -51.0

func (s *btSource) name() string  { return "bluetooth" }
func (s *btSource) playing() bool { return s.bt.Playing() }
func (s *btSource) pause() bool   { return s.bt.Pause() }

// setDuck moves the softvol one step towards level (going down in one go, coming back over about
// a second, so the music does not jump back in).
func (s *btSource) setDuck(level float32) {
	a, g := s.a, s.a.prof.Audio
	if g.BluetoothVolume == "" || g.BluetoothVolumeDefault == 0 || !a.bluetoothVolumeReady.Load() {
		return
	}
	target := softvolRaw(level, g.BluetoothVolumeDefault)
	cur := a.bluetoothDuckLevel()
	if cur == target {
		return
	}
	next := target
	if target > cur {
		next = min(cur+max(1, g.BluetoothVolumeDefault/bluetoothFadeSteps), target)
	}
	a.setBluetoothDuck(next)
}

// softvolRaw: the softvol value for an amplitude level under full (raw full = 0 dB).
func softvolRaw(level float32, full int) int {
	if level >= 1 {
		return full
	}
	if level <= 0 {
		return 0
	}
	db := 20 * math.Log10(float64(level))
	raw := float64(full) + db*255/-softvolMinDB
	return int(math.Round(math.Max(0, math.Min(float64(full), raw))))
}

// onPlayer: the phone's player changed: a pause or resume plays its moment, a new track runs its
// title (music_title, music_clips as for Music Assistant).
func (s *btSource) onPlayer(p bluetooth.Player) {
	s.mu.Lock()
	was, track := s.status, s.track
	s.status = p.Status
	newTrack := p.Track.ID() != "\x00\x00" && p.Track.ID() != track
	if newTrack && p.Status == "playing" {
		s.track = p.Track.ID()
	}
	s.mu.Unlock()
	a := s.a
	switch {
	case was == "playing" && p.Status == "paused":
		a.log.Info("bluetooth: paused")
		a.musicMoment("media_pause")
	case was == "paused" && p.Status == "playing" && !newTrack:
		a.log.Info("bluetooth: resumed")
		a.musicMoment("media_resume")
	}
	if newTrack && p.Status == "playing" {
		line := strings.TrimSpace(musicTrack{Title: p.Track.Title, Artist: p.Track.Artist}.line())
		a.log.Info("bluetooth: track", "title", p.Track.Title, "artist", p.Track.Artist)
		a.showMusicTitle(line)
	}
}

func (a *App) bluetoothDuckLevel() int {
	a.bluetoothDuckMu.Lock()
	defer a.bluetoothDuckMu.Unlock()
	return a.bluetoothDuckRaw
}

func (a *App) setBluetoothDuck(level int) bool {
	g := a.prof.Audio
	a.bluetoothDuckMu.Lock()
	defer a.bluetoothDuckMu.Unlock()
	if a.bluetoothDuckRaw == level {
		return true
	}
	if err := a.mixer.Set(g.BluetoothVolume, level); err != nil {
		a.log.Warn("set Bluetooth duck", "control", g.BluetoothVolume, "err", err)
		return false
	}
	a.bluetoothDuckRaw = level
	return true
}
