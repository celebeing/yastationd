// Package bluetooth owns the deliberately bounded BlueZ pairing surface.
package bluetooth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezName  = "org.bluez"
	propsName  = "org.freedesktop.DBus.Properties"
	agentPath  = dbus.ObjectPath("/io/yastationd/BluetoothAgent")
	agentIface = "org.bluez.Agent1"
	pairFor    = 2 * time.Minute
	// The service UUIDs BlueZ asks the agent about (AuthorizeService) before a phone's profile
	// connects. For A2DP it passes the generic Advanced Audio UUID, not the sink's (BlueZ 5.70,
	// profiles/audio/a2dp.c; an iPhone's audio was refused with only the sink's here).
	advancedAudio = "0000110d-0000-1000-8000-00805f9b34fb"
	a2dpSink      = "0000110b-0000-1000-8000-00805f9b34fb"
	avrcp         = "0000110e-0000-1000-8000-00805f9b34fb" // A/V remote control (the phone's volume, play/pause)
	avrcpTgt      = "0000110c-0000-1000-8000-00805f9b34fb" // A/V remote control target
)

// audioService: what a phone being paired may connect (A2DP audio and its remote control).
func audioService(uuid string) bool {
	switch strings.ToLower(uuid) {
	case advancedAudio, a2dpSink, avrcp, avrcpTgt:
		return true
	}
	return false
}

type Device struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

type View struct {
	Available bool     `json:"available"`
	Off       bool     `json:"off,omitempty"` // switched off in the settings (bluetooth = false)
	Pairing   bool     `json:"pairing"`
	Pending   bool     `json:"pending"`
	Code      string   `json:"code,omitempty"`
	Device    string   `json:"device,omitempty"`
	Expires   int64    `json:"expires,omitempty"`
	Error     string   `json:"error,omitempty"`
	Devices   []Device `json:"devices"`
}

type Controller interface {
	View() View
	Pair() error
	Confirm() error
	Cancel()
	Disconnect(string) error
	Forget(string) error
	Close()
}

type Manager struct {
	mu   sync.Mutex
	conn *dbus.Conn
	name string // the adapter's alias; "" = leave it
	log  *slog.Logger

	onConnection func(name string, connected bool) // a paired phone connected or disconnected
	onPlayer     func(Player)                      // a phone's player changed (player.go)
	players      map[dbus.ObjectPath]Player

	// the phone volume link (volume.go); volOnPhone nil = not linked
	volCurrent func() int
	volOnPhone func(int)
	pcms       map[dbus.ObjectPath]bool // the phones' A2DP PCMs
	adapter    dbus.ObjectPath
	view       View
	pending    dbus.ObjectPath
	confirm    chan bool
	allowed    map[dbus.ObjectPath]bool
	pairSeq    uint64
	stop       context.CancelFunc
	onChange   func(View)
}

// Open connects to the existing system BlueZ instance. Failure leaves Bluetooth unavailable; it
// never starts vendor daemons or powers an adapter on.
func Open(parent context.Context, name string, log *slog.Logger, onChange func(View)) (*Manager, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}
	m := &Manager{conn: conn, name: name, log: log, onChange: onChange, allowed: map[dbus.ObjectPath]bool{}, view: View{Devices: []Device{}}}
	if err := conn.Export(&agent{m: m}, agentPath, agentIface); err != nil {
		conn.Close()
		return nil, err
	}
	m.mu.Lock()
	err = m.attachLocked()
	m.mu.Unlock()
	if err != nil {
		conn.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m.stop = cancel
	m.mu.Lock()
	m.emitLocked()
	m.mu.Unlock()
	go m.watch(ctx)
	return m, nil
}

// attachLocked binds to bluetoothd: the adapter, its alias, our agent as the default one, and the
// adapter hidden and not pairable. The firmware's main.conf has AlwaysPairable = true and its own
// agent accepts every pairing unasked, so a speaker is pairable by anyone who knows its address
// until this runs; it runs again whenever bluetoothd restarts.
func (m *Manager) attachLocked() error {
	if err := m.findAdapter(); err != nil {
		return err
	}
	if m.name != "" {
		if err := m.setLocked("Alias", m.name); err != nil {
			return fmt.Errorf("set BlueZ adapter alias: %w", err)
		}
	}
	mgr := m.conn.Object(bluezName, "/org/bluez")
	// A retry after a step below failed finds the agent registered already.
	if err := mgr.Call("org.bluez.AgentManager1.RegisterAgent", 0, agentPath, "DisplayYesNo").Err; err != nil && !isDBusError(err, "org.bluez.Error.AlreadyExists") {
		return err
	}
	if err := mgr.Call("org.bluez.AgentManager1.RequestDefaultAgent", 0, agentPath).Err; err != nil {
		_ = mgr.Call("org.bluez.AgentManager1.UnregisterAgent", 0, agentPath).Err
		return err
	}
	if err := m.setLocked("Powered", true); err != nil { // switched off by bluetooth = false before
		return fmt.Errorf("power the adapter on: %w", err)
	}
	if err := m.setLocked("Discoverable", false); err != nil {
		return fmt.Errorf("hide the adapter: %w", err)
	}
	if err := m.setLocked("Pairable", false); err != nil {
		return fmt.Errorf("make the adapter not pairable: %w", err)
	}
	m.view.Available = true
	m.refreshLocked()
	m.loadPlayersLocked()
	return nil
}

func isDBusError(err error, name string) bool {
	var e dbus.Error
	if errors.As(err, &e) {
		return e.Name == name
	}
	var p *dbus.Error
	return errors.As(err, &p) && p.Name == name
}

// OnConnection sets what runs when a phone connects or disconnects (the moments).
func (m *Manager) OnConnection(fn func(name string, connected bool)) {
	m.mu.Lock()
	m.onConnection = fn
	m.mu.Unlock()
}

// PowerOff switches the Bluetooth radio off (bluetooth = false): nothing pairs or connects. BlueZ
// keeps the pairings; Open powers it on again.
func PowerOff() error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	m := &Manager{conn: conn}
	if err := m.findAdapter(); err != nil {
		return err
	}
	return m.setLocked("Powered", false)
}

func (m *Manager) findAdapter() error {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err := m.conn.Object(bluezName, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects)
	if err != nil {
		return err
	}
	for path, ifaces := range objects {
		if _, ok := ifaces["org.bluez.Adapter1"]; ok {
			m.adapter = path
			return nil
		}
	}
	return fmt.Errorf("no BlueZ adapter")
}

func (m *Manager) View() View {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.view
	v.Devices = make([]Device, len(v.Devices))
	copy(v.Devices, m.view.Devices)
	return v
}

func (m *Manager) Pair() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.view.Available {
		return fmt.Errorf("Bluetooth is unavailable")
	}
	if m.view.Pairing {
		return fmt.Errorf("a Bluetooth pairing session is already active")
	}
	if err := m.setLocked("PairableTimeout", uint32(pairFor.Seconds())); err != nil {
		return err
	}
	if err := m.setLocked("DiscoverableTimeout", uint32(pairFor.Seconds())); err != nil {
		return err
	}
	if err := m.setLocked("Pairable", true); err != nil {
		return err
	}
	if err := m.setLocked("Discoverable", true); err != nil {
		_ = m.setLocked("Pairable", false)
		return err
	}
	m.pairSeq++
	seq := m.pairSeq
	m.view.Pairing, m.view.Error, m.view.Expires = true, "", time.Now().Add(pairFor).Unix()
	m.emitLocked()
	go func() {
		time.Sleep(pairFor)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.pairSeq == seq && m.view.Pairing {
			m.clearLocked()
		}
	}()
	return nil
}
func (m *Manager) Confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.view.Pending || m.confirm == nil {
		return fmt.Errorf("there is no Bluetooth pairing request to confirm")
	}
	m.confirm <- true
	close(m.confirm)
	m.confirm = nil
	m.view.Pending = false
	m.emitLocked()
	return nil
}

func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearLocked()
}

func (m *Manager) clearLocked() {
	m.pairSeq++
	_ = m.setLocked("Discoverable", false)
	if m.confirm != nil {
		m.confirm <- false
		close(m.confirm)
		m.confirm = nil
	}
	_ = m.setLocked("Pairable", false)
	m.pending = ""
	m.view.Pairing, m.view.Pending, m.view.Code, m.view.Device, m.view.Expires = false, false, "", "", 0
	m.emitLocked()
}

func (m *Manager) Disconnect(id string) error {
	return m.deviceCall(id, "org.bluez.Device1.Disconnect")
}
func (m *Manager) Forget(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, ok := m.devicePathLocked(id)
	if !ok {
		return fmt.Errorf("Bluetooth device not found")
	}
	if err := m.conn.Object(bluezName, m.adapter).Call("org.bluez.Adapter1.RemoveDevice", 0, path).Err; err != nil {
		return err
	}
	m.refreshLocked()
	m.emitLocked()
	return nil
}
func (m *Manager) deviceCall(id, method string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	path, ok := m.devicePathLocked(id)
	if !ok {
		return fmt.Errorf("Bluetooth device not found")
	}
	if err := m.conn.Object(bluezName, path).Call(method, 0).Err; err != nil {
		return err
	}
	m.refreshLocked()
	m.emitLocked()
	return nil
}
func (m *Manager) devicePathLocked(id string) (dbus.ObjectPath, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", false
	}
	path := dbus.ObjectPath(raw)
	if !strings.HasPrefix(string(path), string(m.adapter)+"/dev_") {
		return "", false
	}
	return path, true
}
func (m *Manager) setLocked(name string, value any) error {
	return m.conn.Object(bluezName, m.adapter).Call(propsName+".Set", 0, "org.bluez.Adapter1", name, dbus.MakeVariant(value)).Err
}
func (m *Manager) refreshLocked() { m.view.Devices = m.devicesLocked() }
func (m *Manager) devicesLocked() []Device {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := m.conn.Object(bluezName, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return []Device{}
	}
	out := []Device{}
	for path, ifaces := range objects {
		p, ok := ifaces["org.bluez.Device1"]
		if !ok || !boolProp(p, "Paired") || !strings.HasPrefix(string(path), string(m.adapter)+"/dev_") {
			continue
		}
		out = append(out, Device{ID: base64.RawURLEncoding.EncodeToString([]byte(path)), Name: stringProp(p, "Alias"), Connected: boolProp(p, "Connected")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func boolProp(p map[string]dbus.Variant, n string) bool {
	v, ok := p[n]
	if !ok {
		return false
	}
	b, _ := v.Value().(bool)
	return b
}
func stringProp(p map[string]dbus.Variant, n string) string {
	v, ok := p[n]
	if !ok {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}
func (m *Manager) emitLocked() {
	if m.onChange != nil {
		v := m.view
		v.Devices = append([]Device(nil), v.Devices...)
		go m.onChange(v)
	}
}

func (m *Manager) watch(ctx context.Context) {
	ch := make(chan *dbus.Signal, 8)
	m.conn.Signal(ch)
	defer m.conn.RemoveSignal(ch)
	_ = m.conn.AddMatchSignal(dbus.WithMatchInterface(propsName), dbus.WithMatchMember("PropertiesChanged"))
	_ = m.conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"), dbus.WithMatchMember("InterfacesRemoved"))
	_ = m.conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, bluezName))
	_ = m.conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus.ObjectManager"), dbus.WithMatchMember("InterfacesAdded"), dbus.WithMatchSender(bluezName))
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ch:
			if s == nil || len(s.Body) < 2 {
				continue
			}
			if s.Name == "org.freedesktop.DBus.NameOwnerChanged" {
				if len(s.Body) == 3 {
					if name, _ := s.Body[0].(string); name == bluezName {
						owner, _ := s.Body[2].(string)
						m.bluezRestarted(owner != "")
					}
				}
				continue
			}
			if s.Name == "org.freedesktop.DBus.ObjectManager.InterfacesRemoved" {
				interfaces, _ := s.Body[1].([]string)
				for _, iface := range interfaces {
					path, _ := s.Body[0].(dbus.ObjectPath)
					switch iface {
					case pcmIface:
						m.pcmRemoved(path)
					case playerIface:
						m.playerRemoved(path)
					}
				}
				removed := false
				for _, iface := range interfaces {
					removed = removed || iface == "org.bluez.Device1"
				}
				if removed {
					path, _ := s.Body[0].(dbus.ObjectPath)
					m.log.Info("bluetooth: device removed", "device", string(path))
					m.mu.Lock()
					m.refreshLocked()
					m.emitLocked()
					m.mu.Unlock()
				}
				continue
			}
			if s.Name == "org.freedesktop.DBus.ObjectManager.InterfacesAdded" {
				path, _ := s.Body[0].(dbus.ObjectPath)
				ifaces, _ := s.Body[1].(map[string]map[string]dbus.Variant)
				if p, ok := ifaces[pcmIface]; ok {
					m.pcmAdded(path, p)
				}
				if p, ok := ifaces[playerIface]; ok {
					m.playerChanged(path, p)
				}
				continue
			}
			iface, _ := s.Body[0].(string)
			changed, _ := s.Body[1].(map[string]dbus.Variant)
			if iface == pcmIface {
				m.pcmChanged(s.Path, changed)
				continue
			}
			if iface == playerIface {
				m.playerChanged(s.Path, changed)
				continue
			}
			if iface != "org.bluez.Device1" {
				continue
			}
			m.mu.Lock()
			if boolProp(changed, "Paired") && s.Path == m.pending {
				m.allowed[s.Path] = true
				err := m.conn.Object(bluezName, s.Path).Call(propsName+".Set", 0, "org.bluez.Device1", "Trusted", dbus.MakeVariant(true)).Err
				m.log.Info("bluetooth: paired", "device", string(s.Path), "trusted_err", err)
				m.clearLocked()
			}
			if c, ok := changed["Connected"]; ok && m.onConnection != nil {
				if on, ok := c.Value().(bool); ok {
					name := m.nameLocked(s.Path)
					m.log.Info("bluetooth: connection", "device", string(s.Path), "connected", on)
					go m.onConnection(name, on)
				}
			}
			if _, connected := changed["Connected"]; connected || boolProp(changed, "Paired") {
				m.refreshLocked()
				m.emitLocked()
			}
			m.mu.Unlock()
		}
	}
}

// bluezRestarted: bluetoothd went away (up = false) or came back. Its agent registration and adapter
// settings went with it, and its main.conf makes it pairable again, so attach once more.
func (m *Manager) bluezRestarted(up bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.confirm != nil {
		m.confirm <- false
		close(m.confirm)
		m.confirm = nil
	}
	m.pairSeq++
	m.pending = ""
	m.view = View{Devices: []Device{}}
	m.log.Info("bluetooth: bluetoothd changed", "up", up)
	if up {
		var err error
		for i := 0; i < 20; i++ { // the adapter shows up a moment after the name
			if err = m.attachLocked(); err == nil {
				break
			}
			m.log.Debug("bluetooth: attach", "try", i+1, "err", err)
			m.mu.Unlock()
			time.Sleep(500 * time.Millisecond)
			m.mu.Lock()
		}
		if err != nil {
			m.log.Warn("bluetooth: attach after a bluetoothd restart", "err", err, "tries", 20)
		} else {
			m.log.Info("bluetooth: attached again; hidden, not pairable")
		}
	}
	m.emitLocked()
}

func (m *Manager) incoming(path dbus.ObjectPath, code uint32) *dbus.Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.view.Pairing || m.pending != "" {
		m.log.Info("bluetooth: pairing request refused (no pairing window)", "device", string(path))
		return dbus.NewError("org.bluez.Error.Rejected", nil)
	}
	m.log.Info("bluetooth: pairing request", "device", string(path))
	m.pending, m.confirm = path, make(chan bool, 1)
	m.view.Pending, m.view.Code, m.view.Device = true, fmt.Sprintf("%06d", code), m.nameLocked(path)
	m.emitLocked()
	confirm := m.confirm
	m.mu.Unlock()
	select {
	case ok := <-confirm:
		m.mu.Lock()
		if !ok {
			return dbus.NewError("org.bluez.Error.Canceled", nil)
		}
		return nil
	case <-time.After(pairFor):
		m.mu.Lock()
		return dbus.NewError("org.bluez.Error.Canceled", nil)
	}
}
func (m *Manager) nameLocked(path dbus.ObjectPath) string {
	if m.conn == nil {
		return "Phone"
	}
	var v dbus.Variant
	if m.conn.Object(bluezName, path).Call(propsName+".Get", 0, "org.bluez.Device1", "Alias").Store(&v) == nil {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return "Phone"
}
func (m *Manager) Close() {
	m.mu.Lock()
	if m.conn == nil {
		m.mu.Unlock()
		return
	}
	m.clearLocked()
	if m.stop != nil {
		m.stop()
	}
	_ = m.conn.Export(nil, agentPath, agentIface)
	_ = m.conn.Object(bluezName, "/org/bluez").Call("org.bluez.AgentManager1.UnregisterAgent", 0, agentPath).Err
	m.conn.Close()
	m.conn = nil
	m.mu.Unlock()
}

type agent struct{ m *Manager }

func (a *agent) Release() *dbus.Error { return nil }
func (a *agent) Cancel() *dbus.Error  { a.m.Cancel(); return nil }
func (a *agent) RequestConfirmation(p dbus.ObjectPath, code uint32) *dbus.Error {
	return a.m.incoming(p, code)
}
func (a *agent) DisplayPasskey(dbus.ObjectPath, uint32, uint16) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", nil)
}
func (a *agent) RequestAuthorization(dbus.ObjectPath) *dbus.Error {
	return dbus.NewError("org.bluez.Error.Rejected", nil)
}
func (a *agent) RequestPinCode(dbus.ObjectPath) (string, *dbus.Error) {
	return "", dbus.NewError("org.bluez.Error.Rejected", nil)
}
func (a *agent) RequestPasskey(dbus.ObjectPath) (uint32, *dbus.Error) {
	return 0, dbus.NewError("org.bluez.Error.Rejected", nil)
}
func (a *agent) AuthorizeService(path dbus.ObjectPath, uuid string) *dbus.Error {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()
	ok := (a.m.allowed[path] || (path == a.m.pending && !a.m.view.Pending)) && audioService(uuid) // pending: confirmed, Paired not seen yet
	a.m.log.Info("bluetooth: service authorization", "device", string(path), "uuid", uuid, "allowed", ok)
	if ok {
		return nil
	}
	return dbus.NewError("org.bluez.Error.Rejected", nil)
}
