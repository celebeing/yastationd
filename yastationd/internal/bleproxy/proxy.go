package bleproxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/celebeing/yastationd/yastationd/internal/esphome"
)

const (
	bluez     = "org.bluez"
	props     = "org.freedesktop.DBus.Properties"
	devIface  = "org.bluez.Device1"
	svcIface  = "org.bluez.GattService1"
	chrIface  = "org.bluez.GattCharacteristic1"
	descIface = "org.bluez.GattDescriptor1"
	cccdUUID  = "00002902-0000-1000-8000-00805f9b34fb"
)

// Proxy implements esphome.BLE on bluetoothd. It holds its own system-bus connection, so its
// discovery session is its own (bluetoothd stops it when the connection goes).
type Proxy struct {
	Adapter string // "/org/bluez/hci0"
	Index   uint16 // the controller's HCI index (0 for hci0)
	// Paused, when set, is asked every few seconds; true stops the scan (connections stay),
	// e.g. while a phone streams audio over the same radio.
	Paused func() bool
	Log    *slog.Logger

	conn   *dbus.Conn
	mu     sync.Mutex
	subs   map[int]func([]esphome.Advertisement)
	nextID int
	batch  []esphome.Advertisement
	devs   map[uint64]*device
	onDisc func(uint64)
}

// device is a connected device and its GATT database.
type device struct {
	path     dbus.ObjectPath
	services []esphome.GATTService
	chars    map[uint16]dbus.ObjectPath // value handle -> characteristic
	descs    map[uint16]dbus.ObjectPath // handle -> descriptor
	cccd     map[uint16]bool            // descriptor handles that are a CCCD
	notify   map[dbus.ObjectPath]func([]byte)
}

// batchEvery and batchMax: advertisements go out in batches, like ESPHome's (16 at most).
const (
	batchEvery = 100 * time.Millisecond
	batchMax   = 16
)

// Run starts the scan and the monitor and serves until ctx ends.
func (p *Proxy) Run(ctx context.Context) error {
	conn, err := dbus.SystemBusPrivate()
	if err != nil {
		return err
	}
	if err := conn.Auth(nil); err != nil {
		conn.Close()
		return err
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return err
	}
	p.mu.Lock()
	p.conn = conn
	p.subs, p.devs = map[int]func([]esphome.Advertisement){}, map[uint64]*device{}
	p.mu.Unlock()
	defer conn.Close()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		if err := monitor(p.Index, p.add, stop); err != nil {
			p.Log.Warn("bluetooth proxy: no raw advertisements", "err", err)
		}
	}()
	go p.signals(ctx)
	t := time.NewTicker(batchEvery)
	defer t.Stop()
	scan := time.NewTicker(5 * time.Second)
	defer scan.Stop()
	p.scan(true)
	for {
		select {
		case <-ctx.Done():
			p.disconnectAll()
			return nil
		case <-t.C:
			p.flush()
		case <-scan.C:
			p.scan(false)
		}
	}
}

// scan keeps the discovery running (bluetoothd restarted, or another client stopped it) unless
// paused; first logs what it does.
func (p *Proxy) scan(first bool) {
	ad := p.conn.Object(bluez, dbus.ObjectPath(p.Adapter))
	var v dbus.Variant
	discovering := ad.Call(props+".Get", 0, "org.bluez.Adapter1", "Discovering").Store(&v) == nil && v.Value() == true
	paused := p.Paused != nil && p.Paused()
	switch {
	case paused && discovering:
		ad.Call("org.bluez.Adapter1.StopDiscovery", 0)
		p.Log.Info("bluetooth proxy: scan paused")
	case !paused && !discovering:
		filter := map[string]dbus.Variant{"Transport": dbus.MakeVariant("le"), "DuplicateData": dbus.MakeVariant(true)}
		if err := ad.Call("org.bluez.Adapter1.SetDiscoveryFilter", 0, filter).Err; err != nil {
			p.Log.Warn("bluetooth proxy: discovery filter", "err", err)
		}
		if err := ad.Call("org.bluez.Adapter1.StartDiscovery", 0).Err; err != nil {
			p.Log.Warn("bluetooth proxy: start the scan", "err", err)
			return
		}
		if first {
			p.Log.Info("bluetooth proxy: scanning")
		} else {
			p.Log.Info("bluetooth proxy: scan (re)started")
		}
	}
}

func (p *Proxy) add(a esphome.Advertisement) {
	p.mu.Lock()
	if len(p.subs) == 0 {
		p.batch = p.batch[:0]
		p.mu.Unlock()
		return
	}
	p.batch = append(p.batch, a)
	full := len(p.batch) >= batchMax
	p.mu.Unlock()
	if full {
		p.flush()
	}
}

func (p *Proxy) flush() {
	p.mu.Lock()
	if len(p.batch) == 0 {
		p.mu.Unlock()
		return
	}
	b := p.batch
	p.batch = nil
	subs := make([]func([]esphome.Advertisement), 0, len(p.subs))
	for _, fn := range p.subs {
		subs = append(subs, fn)
	}
	p.mu.Unlock()
	for _, fn := range subs {
		fn(b)
	}
}

// Advertisements implements esphome.BLE.
func (p *Proxy) Advertisements(fn func([]esphome.Advertisement)) func() {
	p.mu.Lock()
	if p.subs == nil {
		p.subs = map[int]func([]esphome.Advertisement){}
	}
	id := p.nextID
	p.nextID++
	p.subs[id] = fn
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.subs, id)
		p.mu.Unlock()
	}
}

// OnDisconnected implements esphome.BLE.
func (p *Proxy) OnDisconnected(fn func(uint64)) {
	p.mu.Lock()
	p.onDisc = fn
	p.mu.Unlock()
}

var errNoBus = errors.New("bluetooth proxy: not running")

// Connect implements esphome.BLE: the device must have been seen by the scan (bluetoothd keeps
// a device object only then).
func (p *Proxy) Connect(ctx context.Context, addr uint64, _ uint32) (uint32, error) {
	if p.conn == nil {
		return 0, errNoBus
	}
	path := dbus.ObjectPath(macPath(p.Adapter, addr))
	obj := p.conn.Object(bluez, path)
	var v dbus.Variant
	for i := 0; obj.Call(props+".Get", 0, devIface, "Address").Store(&v) != nil; i++ {
		if i >= 50 {
			return 0, fmt.Errorf("device not seen by the scan")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := obj.CallWithContext(ctx, devIface+".Connect", 0).Err; err != nil {
		return 0, err
	}
	for {
		if obj.Call(props+".Get", 0, devIface, "ServicesResolved").Store(&v) == nil && v.Value() == true {
			break
		}
		select {
		case <-ctx.Done():
			obj.Call(devIface+".Disconnect", 0)
			return 0, fmt.Errorf("services not resolved: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	d, mtu, err := p.database(path)
	if err != nil {
		obj.Call(devIface+".Disconnect", 0)
		return 0, err
	}
	p.mu.Lock()
	p.devs[addr] = d
	p.mu.Unlock()
	return mtu, nil
}

// database reads a connected device's GATT objects. BlueZ names them by handle
// (service000c/char000d/desc000f, the characteristic by its declaration); the value's handle,
// which ESPHome reports and Home Assistant uses, is the next one.
func (p *Proxy) database(path dbus.ObjectPath) (*device, uint32, error) {
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := p.conn.Object(bluez, "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&objects); err != nil {
		return nil, 0, err
	}
	d := &device{path: path, chars: map[uint16]dbus.ObjectPath{}, descs: map[uint16]dbus.ObjectPath{},
		cccd: map[uint16]bool{}, notify: map[dbus.ObjectPath]func([]byte){}}
	prefix := string(path) + "/"
	svcIdx := map[string]int{}
	type chr struct {
		svc  string
		path dbus.ObjectPath
		c    esphome.GATTChar
	}
	var chrs []chr
	var descs []struct {
		chr string
		d   esphome.GATTDesc
	}
	mtu := uint32(23)
	paths := make([]string, 0, len(objects))
	for op := range objects {
		if strings.HasPrefix(string(op), prefix) {
			paths = append(paths, string(op))
		}
	}
	sort.Strings(paths)
	for _, op := range paths {
		ifaces := objects[dbus.ObjectPath(op)]
		last := op[strings.LastIndex(op, "/")+1:]
		h := handleOf(last)
		switch {
		case ifaces[svcIface] != nil:
			svcIdx[op] = len(d.services)
			d.services = append(d.services, esphome.GATTService{UUID: str(ifaces[svcIface]["UUID"]), Handle: h})
		case ifaces[chrIface] != nil:
			c := ifaces[chrIface]
			value := h + 1
			d.chars[value] = dbus.ObjectPath(op)
			if m, ok := c["MTU"].Value().(uint16); ok && uint32(m) > mtu {
				mtu = uint32(m)
			}
			chrs = append(chrs, chr{svc: op[:strings.LastIndex(op, "/")], path: dbus.ObjectPath(op),
				c: esphome.GATTChar{UUID: str(c["UUID"]), Handle: value, Props: flags(c["Flags"])}})
		case ifaces[descIface] != nil:
			u := str(ifaces[descIface]["UUID"])
			d.descs[h] = dbus.ObjectPath(op)
			if strings.EqualFold(u, cccdUUID) {
				d.cccd[h] = true
			}
			descs = append(descs, struct {
				chr string
				d   esphome.GATTDesc
			}{op[:strings.LastIndex(op, "/")], esphome.GATTDesc{UUID: u, Handle: h}})
		}
	}
	chrIdx := map[string]int{}
	for _, c := range chrs {
		i, ok := svcIdx[c.svc]
		if !ok {
			continue
		}
		chrIdx[string(c.path)] = len(d.services[i].Chars)
		d.services[i].Chars = append(d.services[i].Chars, c.c)
	}
	for _, x := range descs {
		ci, ok := chrIdx[x.chr]
		if !ok {
			continue
		}
		si := svcIdx[x.chr[:strings.LastIndex(x.chr, "/")]]
		d.services[si].Chars[ci].Descs = append(d.services[si].Chars[ci].Descs, x.d)
	}
	if len(d.services) == 0 {
		return nil, 0, fmt.Errorf("no GATT services")
	}
	return d, mtu, nil
}

// handleOf reads the handle out of a BlueZ GATT object's name (service000c, char000d, desc000f:
// the last four characters, hex).
func handleOf(last string) uint16 {
	if len(last) < 4 {
		return 0
	}
	v, _ := strconv.ParseUint(last[len(last)-4:], 16, 16)
	return uint16(v)
}

func str(v dbus.Variant) string {
	s, _ := v.Value().(string)
	return s
}

// flags turns BlueZ's characteristic flags into the properties byte.
func flags(v dbus.Variant) uint32 {
	names, _ := v.Value().([]string)
	bits := map[string]uint32{"broadcast": 0x01, "read": 0x02, "write-without-response": 0x04, "write": 0x08,
		"notify": 0x10, "indicate": 0x20, "authenticated-signed-writes": 0x40, "extended-properties": 0x80}
	var out uint32
	for _, n := range names {
		out |= bits[n]
	}
	return out
}

func (p *Proxy) dev(addr uint64) (*device, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.devs[addr]
	if d == nil {
		return nil, fmt.Errorf("not connected")
	}
	return d, nil
}

// Disconnect implements esphome.BLE.
func (p *Proxy) Disconnect(addr uint64) error {
	p.mu.Lock()
	d := p.devs[addr]
	delete(p.devs, addr)
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return errNoBus
	}
	path := dbus.ObjectPath(macPath(p.Adapter, addr))
	if d != nil {
		path = d.path
	}
	return conn.Object(bluez, path).Call(devIface+".Disconnect", 0).Err
}

func (p *Proxy) disconnectAll() {
	p.mu.Lock()
	var addrs []uint64
	for a := range p.devs {
		addrs = append(addrs, a)
	}
	p.mu.Unlock()
	for _, a := range addrs {
		p.Disconnect(a)
	}
}

// Services implements esphome.BLE.
func (p *Proxy) Services(addr uint64) ([]esphome.GATTService, error) {
	d, err := p.dev(addr)
	if err != nil {
		return nil, err
	}
	return d.services, nil
}

func (p *Proxy) char(addr uint64, h uint16) (dbus.BusObject, error) {
	d, err := p.dev(addr)
	if err != nil {
		return nil, err
	}
	path, ok := d.chars[h]
	if !ok {
		return nil, fmt.Errorf("no characteristic at handle %d", h)
	}
	return p.conn.Object(bluez, path), nil
}

func (p *Proxy) desc(addr uint64, h uint16) (dbus.BusObject, bool, error) {
	d, err := p.dev(addr)
	if err != nil {
		return nil, false, err
	}
	path, ok := d.descs[h]
	if !ok {
		return nil, false, fmt.Errorf("no descriptor at handle %d", h)
	}
	return p.conn.Object(bluez, path), d.cccd[h], nil
}

// Read implements esphome.BLE.
func (p *Proxy) Read(addr uint64, h uint16) ([]byte, error) {
	o, err := p.char(addr, h)
	if err != nil {
		return nil, err
	}
	var v []byte
	err = o.Call(chrIface+".ReadValue", 0, map[string]dbus.Variant{}).Store(&v)
	return v, err
}

// Write implements esphome.BLE.
func (p *Proxy) Write(addr uint64, h uint16, data []byte, response bool) error {
	o, err := p.char(addr, h)
	if err != nil {
		return err
	}
	kind := "command"
	if response {
		kind = "request"
	}
	return o.Call(chrIface+".WriteValue", 0, data, map[string]dbus.Variant{"type": dbus.MakeVariant(kind)}).Err
}

// ReadDescriptor implements esphome.BLE.
func (p *Proxy) ReadDescriptor(addr uint64, h uint16) ([]byte, error) {
	o, _, err := p.desc(addr, h)
	if err != nil {
		return nil, err
	}
	var v []byte
	err = o.Call(descIface+".ReadValue", 0, map[string]dbus.Variant{}).Store(&v)
	return v, err
}

// WriteDescriptor implements esphome.BLE. bluetoothd owns the CCCDs (it writes them for
// StartNotify and refuses a client's write), so Home Assistant's own CCCD write after a notify
// request is taken as done.
func (p *Proxy) WriteDescriptor(addr uint64, h uint16, data []byte) error {
	o, isCCCD, err := p.desc(addr, h)
	if err != nil {
		return err
	}
	if isCCCD {
		return nil
	}
	return o.Call(descIface+".WriteValue", 0, data, map[string]dbus.Variant{}).Err
}

// Notify implements esphome.BLE.
func (p *Proxy) Notify(addr uint64, h uint16, enable bool, fn func([]byte)) error {
	d, err := p.dev(addr)
	if err != nil {
		return err
	}
	path, ok := d.chars[h]
	if !ok {
		return fmt.Errorf("no characteristic at handle %d", h)
	}
	o := p.conn.Object(bluez, path)
	if !enable {
		p.mu.Lock()
		delete(d.notify, path)
		p.mu.Unlock()
		return o.Call(chrIface+".StopNotify", 0).Err
	}
	p.mu.Lock()
	d.notify[path] = fn
	p.mu.Unlock()
	if err := o.Call(chrIface+".StartNotify", 0).Err; err != nil {
		p.mu.Lock()
		delete(d.notify, path)
		p.mu.Unlock()
		return err
	}
	return nil
}

// signals follows the connected devices: a disconnection, and notified values.
func (p *Proxy) signals(ctx context.Context) {
	ch := make(chan *dbus.Signal, 64)
	p.conn.Signal(ch)
	defer p.conn.RemoveSignal(ch)
	p.conn.AddMatchSignal(dbus.WithMatchSender(bluez), dbus.WithMatchInterface(props), dbus.WithMatchMember("PropertiesChanged"))
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-ch:
			if s == nil || len(s.Body) < 2 {
				continue
			}
			iface, _ := s.Body[0].(string)
			changed, _ := s.Body[1].(map[string]dbus.Variant)
			switch iface {
			case chrIface:
				v, ok := changed["Value"]
				if !ok {
					continue
				}
				data, _ := v.Value().([]byte)
				p.mu.Lock()
				var fn func([]byte)
				for _, d := range p.devs {
					if strings.HasPrefix(string(s.Path), string(d.path)+"/") {
						fn = d.notify[s.Path]
						break
					}
				}
				p.mu.Unlock()
				if fn != nil {
					fn(data)
				}
			case devIface:
				c, ok := changed["Connected"]
				if !ok || c.Value() != false {
					continue
				}
				p.mu.Lock()
				var gone uint64
				found := false
				for a, d := range p.devs {
					if d.path == s.Path {
						gone, found = a, true
						delete(p.devs, a)
						break
					}
				}
				fn := p.onDisc
				p.mu.Unlock()
				if found && fn != nil {
					fn(gone)
				}
			}
		}
	}
}

// Address is the adapter's own Bluetooth address ("" until running).
func (p *Proxy) Address() string {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return ""
	}
	var v dbus.Variant
	if conn.Object(bluez, dbus.ObjectPath(p.Adapter)).Call(props+".Get", 0, "org.bluez.Adapter1", "Address").Store(&v) != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}
