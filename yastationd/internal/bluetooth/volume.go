package bluetooth

import (
	"strings"

	"github.com/godbus/dbus/v5"
)

// The phone's volume (AVRCP absolute volume) as BlueALSA 4 keeps it: org.bluealsa.PCM1 "Volume", a
// uint16 with one byte per channel, bit 7 the mute and bits 0..6 the level (0..127). With
// "SoftVolume" true BlueALSA scales the samples by it (the firmware's default: the phone's slider
// sits under the speaker's volume); false leaves the samples alone, and the level is only reported.
const (
	bluealsaName = "org.bluealsa"
	pcmIface     = "org.bluealsa.PCM1"
	a2dpMaxLevel = 127
)

// LinkVolume makes the phone's volume the speaker's: a phone's A2DP stream plays unscaled, its
// slider changes reach onPhone (0..100), and SetPhoneVolume moves the slider. current gives the
// speaker's volume for a phone that connects. Called once, before or after phones connect.
func (m *Manager) LinkVolume(current func() int, onPhone func(pct int)) {
	m.mu.Lock()
	m.volCurrent, m.volOnPhone = current, onPhone
	m.mu.Unlock()
	_ = m.conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"), dbus.WithMatchMember("InterfacesAdded"), dbus.WithMatchSender(bluealsaName))
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if m.conn.Object(bluealsaName, "/org/bluealsa").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects) != nil {
		return
	}
	for path, ifaces := range objects {
		if p, ok := ifaces[pcmIface]; ok {
			m.pcmAdded(path, p)
		}
	}
}

// phonePCM: BlueALSA's PCM of a phone playing to us (A2DP sink; BlueALSA calls it a "source" PCM:
// the stream comes out of it).
func phonePCM(p map[string]dbus.Variant) bool {
	return strings.HasPrefix(stringProp(p, "Transport"), "A2DP-sink") && stringProp(p, "Mode") == "source"
}

func (m *Manager) pcmAdded(path dbus.ObjectPath, p map[string]dbus.Variant) {
	m.mu.Lock()
	linked, current := m.volOnPhone != nil, m.volCurrent
	if linked && phonePCM(p) {
		if m.pcms == nil {
			m.pcms = map[dbus.ObjectPath]bool{}
		}
		m.pcms[path] = true
	}
	m.mu.Unlock()
	if !linked || !phonePCM(p) {
		return
	}
	obj := m.conn.Object(bluealsaName, path)
	if err := obj.Call(propsName+".Set", 0, pcmIface, "SoftVolume", dbus.MakeVariant(false)).Err; err != nil {
		m.log.Warn("bluetooth: phone volume link: SoftVolume off", "pcm", string(path), "err", err)
	}
	m.setPCMVolume(path, current())
	m.log.Info("bluetooth: phone volume linked", "pcm", string(path))
}

func (m *Manager) pcmRemoved(path dbus.ObjectPath) {
	m.mu.Lock()
	delete(m.pcms, path)
	m.mu.Unlock()
}

// pcmChanged: a phone moved its slider (or our own SetPhoneVolume coming back).
func (m *Manager) pcmChanged(path dbus.ObjectPath, changed map[string]dbus.Variant) {
	v, ok := changed["Volume"]
	if !ok {
		return
	}
	raw, ok := v.Value().(uint16)
	if !ok {
		return
	}
	m.mu.Lock()
	known, onPhone := m.pcms[path], m.volOnPhone
	m.mu.Unlock()
	if !known || onPhone == nil {
		return
	}
	onPhone(pctFromVolume(raw))
}

// SetPhoneVolume moves the sliders of the connected phones to pct (0..100); a no-op unless linked.
func (m *Manager) SetPhoneVolume(pct int) {
	m.mu.Lock()
	paths := make([]dbus.ObjectPath, 0, len(m.pcms))
	for p := range m.pcms {
		paths = append(paths, p)
	}
	m.mu.Unlock()
	for _, p := range paths {
		m.setPCMVolume(p, pct)
	}
}

func (m *Manager) setPCMVolume(path dbus.ObjectPath, pct int) {
	obj := m.conn.Object(bluealsaName, path)
	var cur dbus.Variant
	if obj.Call(propsName+".Get", 0, pcmIface, "Volume").Store(&cur) == nil {
		if raw, ok := cur.Value().(uint16); ok && abs(pctFromVolume(raw)-pct) <= 1 {
			return // already there (also how a change of ours echoing back ends)
		}
	}
	if err := obj.Call(propsName+".Set", 0, pcmIface, "Volume", dbus.MakeVariant(volumeFromPct(pct))).Err; err != nil {
		m.log.Warn("bluetooth: set the phone's volume", "pcm", string(path), "err", err)
	}
}

// pctFromVolume: the louder channel's level as 0..100; muted reads as 0.
func pctFromVolume(raw uint16) int {
	level := 0
	for _, b := range []uint16{raw >> 8, raw & 0xff} {
		if b&0x80 == 0 && int(b&0x7f) > level {
			level = int(b & 0x7f)
		}
	}
	return (level*100 + a2dpMaxLevel/2) / a2dpMaxLevel
}

func volumeFromPct(pct int) uint16 {
	pct = min(max(pct, 0), 100)
	level := uint16((pct*a2dpMaxLevel + 50) / 100)
	return level<<8 | level
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
