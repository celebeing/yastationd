package bluetooth

import (
	"strings"

	"github.com/godbus/dbus/v5"
)

// A phone's media player as BlueZ shows it over AVRCP (org.bluez.MediaPlayer1, one per connected
// phone that has one): its status, the track, and Play/Pause. The speaker learns from it whether
// the phone plays, what, and can pause it.
const playerIface = "org.bluez.MediaPlayer1"

// Track is the phone's current track (any field may be empty).
type Track struct {
	Title, Artist, Album string
}

// ID tells tracks apart.
func (t Track) ID() string { return t.Title + "\x00" + t.Artist + "\x00" + t.Album }

// Player is what the phone reports: Status is "playing", "paused", "stopped" (or "").
type Player struct {
	Status string
	Track  Track
}

// OnPlayer sets what runs when a phone's player changes (status or track).
func (m *Manager) OnPlayer(fn func(Player)) {
	m.mu.Lock()
	m.onPlayer = fn
	m.mu.Unlock()
}

// Playing: a connected phone's player says it plays.
func (m *Manager) Playing() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.players {
		if p.Status == "playing" {
			return true
		}
	}
	return false
}

// Pause asks every playing phone to pause; false when none could be asked.
func (m *Manager) Pause() bool {
	m.mu.Lock()
	var paths []dbus.ObjectPath
	for path, p := range m.players {
		if p.Status == "playing" {
			paths = append(paths, path)
		}
	}
	m.mu.Unlock()
	ok := false
	for _, path := range paths {
		if err := m.conn.Object(bluezName, path).Call(playerIface+".Pause", 0).Err; err != nil {
			m.log.Warn("bluetooth: pause the phone", "player", string(path), "err", err)
			continue
		}
		ok = true
	}
	return ok
}

// loadPlayersLocked reads the players that exist (at attach).
func (m *Manager) loadPlayersLocked() {
	m.players = map[dbus.ObjectPath]Player{}
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if m.conn.Object(bluezName, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects) != nil {
		return
	}
	for path, ifaces := range objects {
		if p, ok := ifaces[playerIface]; ok {
			m.players[path] = applyPlayer(Player{}, p)
		}
	}
}

// playerChanged: a player appeared (added) or changed; props are its (changed) properties.
func (m *Manager) playerChanged(path dbus.ObjectPath, props map[string]dbus.Variant) {
	m.mu.Lock()
	if m.players == nil {
		m.players = map[dbus.ObjectPath]Player{}
	}
	before := m.players[path]
	p := applyPlayer(before, props)
	m.players[path] = p
	fn := m.onPlayer
	m.mu.Unlock()
	if fn != nil && p != before {
		fn(p)
	}
}

func (m *Manager) playerRemoved(path dbus.ObjectPath) {
	m.mu.Lock()
	_, had := m.players[path]
	delete(m.players, path)
	fn := m.onPlayer
	m.mu.Unlock()
	if had && fn != nil {
		fn(Player{Status: "stopped"})
	}
}

func applyPlayer(p Player, props map[string]dbus.Variant) Player {
	if v, ok := props["Status"]; ok {
		if s, ok := v.Value().(string); ok {
			p.Status = strings.ToLower(s)
		}
	}
	if v, ok := props["Track"]; ok {
		if t, ok := v.Value().(map[string]dbus.Variant); ok {
			p.Track = Track{Title: stringProp(t, "Title"), Artist: stringProp(t, "Artist"), Album: stringProp(t, "Album")}
		}
	}
	return p
}
