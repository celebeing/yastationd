package esphome

import (
	"context"
	"encoding/binary"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The Bluetooth proxy component: advertisements out, and GATT connections that Home Assistant
// drives through the speaker's adapter. The radio side is the BLE interface (bleproxy on a unit).

const (
	msgSubscribeBLEAdvertisements   = 66
	msgBLEDeviceRequest             = 68
	msgBLEDeviceConnection          = 69
	msgGATTGetServices              = 70
	msgGATTGetServicesResponse      = 71
	msgGATTGetServicesDone          = 72
	msgGATTRead                     = 73
	msgGATTReadResponse             = 74
	msgGATTWrite                    = 75
	msgGATTReadDescriptor           = 76
	msgGATTWriteDescriptor          = 77
	msgGATTNotify                   = 78
	msgGATTNotifyData               = 79
	msgSubscribeConnectionsFree     = 80
	msgConnectionsFree              = 81
	msgGATTError                    = 82
	msgGATTWriteResponse            = 83
	msgGATTNotifyResponse           = 84
	msgUnsubscribeBLEAdvertisements = 87
	msgBLERawAdvertisements         = 93
)

// Bluetooth proxy feature flags (aioesphomeapi BluetoothProxyFeature).
const (
	FeaturePassiveScan       = 1 << 0
	FeatureActiveConnections = 1 << 1
	FeatureRemoteCaching     = 1 << 2
	FeatureRawAdvertisements = 1 << 5
)

// Device request types.
const (
	reqDisconnect       = 1
	reqPair             = 2
	reqUnpair           = 3
	reqConnectWithCache = 4
	reqConnectNoCache   = 5
	reqClearCache       = 6
)

// Errors sent back (ESP-IDF's codes, which Home Assistant knows how to word).
const (
	errConnFailEstablish = 0x3e  // ESP_GATT_CONN_FAIL_ESTABLISH
	errGATT              = 0x85  // ESP_GATT_ERROR
	errNotConnected      = 0x103 // ESP_ERR_INVALID_STATE
)

// Advertisement is one raw advertising report (or scan response).
type Advertisement struct {
	Address     uint64 // the MAC as a number, most significant byte first
	RSSI        int32
	AddressType uint32 // 0 public, 1 random
	Data        []byte // the AD structures as received
}

// GATT is a connected device's database as the proxy reports it.
type GATTService struct {
	UUID   string
	Handle uint16
	Chars  []GATTChar
}

type GATTChar struct {
	UUID   string
	Handle uint16 // the value's handle
	Props  uint32 // the characteristic properties byte
	Descs  []GATTDesc
}

type GATTDesc struct {
	UUID   string
	Handle uint16
}

// BLE is the radio side the proxy drives.
type BLE interface {
	// Advertisements calls fn with every batch while subscribed; cancel stops it.
	Advertisements(fn func([]Advertisement)) (cancel func())
	Connect(ctx context.Context, addr uint64, addrType uint32) (mtu uint32, err error)
	Disconnect(addr uint64) error
	// Disconnected is called when a connected device goes away by itself.
	OnDisconnected(fn func(addr uint64))
	Services(addr uint64) ([]GATTService, error)
	Read(addr uint64, handle uint16) ([]byte, error)
	Write(addr uint64, handle uint16, data []byte, response bool) error
	ReadDescriptor(addr uint64, handle uint16) ([]byte, error)
	WriteDescriptor(addr uint64, handle uint16, data []byte) error
	// Notify turns a characteristic's notifications on (data is called with each) or off.
	Notify(addr uint64, handle uint16, enable bool, data func([]byte)) error
}

// Bluetooth serves the proxy over the API.
type Bluetooth struct {
	BLE   BLE
	Limit int // connection slots
	Log   interface {
		Info(string, ...any)
		Warn(string, ...any)
		Debug(string, ...any)
	}

	mu      sync.Mutex
	owner   map[uint64]*Conn // a device's connection belongs to the client that asked
	ads     map[*Conn]func() // advertisement subscriptions
	freeSub map[*Conn]bool   // connection-slot subscriptions
	once    sync.Once
}

func (b *Bluetooth) Types() []uint32 {
	return []uint32{msgSubscribeBLEAdvertisements, msgUnsubscribeBLEAdvertisements, msgBLEDeviceRequest,
		msgGATTGetServices, msgGATTRead, msgGATTWrite, msgGATTReadDescriptor, msgGATTWriteDescriptor,
		msgGATTNotify, msgSubscribeConnectionsFree}
}

// Flags are the features this proxy has.
func (b *Bluetooth) Flags() uint32 {
	return FeaturePassiveScan | FeatureActiveConnections | FeatureRemoteCaching | FeatureRawAdvertisements
}

func (b *Bluetooth) init() {
	b.once.Do(func() {
		b.owner, b.ads, b.freeSub = map[uint64]*Conn{}, map[*Conn]func(){}, map[*Conn]bool{}
		b.BLE.OnDisconnected(func(addr uint64) {
			b.mu.Lock()
			c := b.owner[addr]
			delete(b.owner, addr)
			b.mu.Unlock()
			if c != nil {
				b.Log.Info("bluetooth proxy: device disconnected", "addr", macString(addr))
				b.sendConnection(c, addr, false, 0, 0)
				b.sendFree()
			}
		})
	})
}

func (b *Bluetooth) Handle(c *Conn, typ uint32, m msg) {
	b.init()
	addr := m.Uint(1)
	switch typ {
	case msgSubscribeBLEAdvertisements:
		b.mu.Lock()
		if b.ads[c] == nil {
			b.ads[c] = b.BLE.Advertisements(func(ads []Advertisement) { sendAdvertisements(c, ads) })
		}
		b.mu.Unlock()
	case msgUnsubscribeBLEAdvertisements:
		b.mu.Lock()
		if cancel := b.ads[c]; cancel != nil {
			cancel()
			delete(b.ads, c)
		}
		b.mu.Unlock()
	case msgSubscribeConnectionsFree:
		b.mu.Lock()
		b.freeSub[c] = true
		b.mu.Unlock()
		b.sendFreeTo(c)
	case msgBLEDeviceRequest:
		b.deviceRequest(c, addr, m.Uint(2), uint32(m.Uint(4)))
	case msgGATTGetServices:
		go b.services(c, addr)
	case msgGATTRead:
		h := uint16(m.Uint(2))
		go func() {
			data, err := b.BLE.Read(addr, h)
			b.reply(c, addr, h, err, msgGATTReadResponse, func(e *enc) { e.Bytes(3, data, true) })
		}()
	case msgGATTReadDescriptor:
		h := uint16(m.Uint(2))
		go func() {
			data, err := b.BLE.ReadDescriptor(addr, h)
			b.reply(c, addr, h, err, msgGATTReadResponse, func(e *enc) { e.Bytes(3, data, true) })
		}()
	case msgGATTWrite:
		h, resp, data := uint16(m.Uint(2)), m.Bool(3), append([]byte(nil), m.Bytes(4)...)
		go func() {
			err := b.BLE.Write(addr, h, data, resp)
			if !resp && err == nil {
				return // without a response the client does not wait
			}
			b.reply(c, addr, h, err, msgGATTWriteResponse, nil)
		}()
	case msgGATTWriteDescriptor:
		h, data := uint16(m.Uint(2)), append([]byte(nil), m.Bytes(3)...)
		go func() {
			b.reply(c, addr, h, b.BLE.WriteDescriptor(addr, h, data), msgGATTWriteResponse, nil)
		}()
	case msgGATTNotify:
		h, on := uint16(m.Uint(2)), m.Bool(3)
		go func() {
			err := b.BLE.Notify(addr, h, on, func(data []byte) {
				var e enc
				e.Uint(1, addr)
				e.Uint(2, uint64(h))
				e.Bytes(3, data, true)
				c.Send(msgGATTNotifyData, &e)
			})
			b.reply(c, addr, h, err, msgGATTNotifyResponse, nil)
		}()
	}
}

// reply sends ok's message (address, handle, and what more adds) or a GATT error.
func (b *Bluetooth) reply(c *Conn, addr uint64, h uint16, err error, ok uint32, more func(*enc)) {
	var e enc
	e.Uint(1, addr)
	e.Uint(2, uint64(h))
	if err != nil {
		b.Log.Debug("bluetooth proxy: gatt", "addr", macString(addr), "handle", h, "err", err)
		e.Int32(3, errGATT)
		c.Send(msgGATTError, &e)
		return
	}
	if more != nil {
		more(&e)
	}
	c.Send(ok, &e)
}

func (b *Bluetooth) deviceRequest(c *Conn, addr, kind uint64, addrType uint32) {
	switch kind {
	case reqConnectWithCache, reqConnectNoCache:
		b.mu.Lock()
		if _, busy := b.owner[addr]; busy {
			b.mu.Unlock()
			b.sendConnection(c, addr, false, 0, errConnFailEstablish)
			return
		}
		if len(b.owner) >= b.Limit {
			b.mu.Unlock()
			b.Log.Warn("bluetooth proxy: no free connection slot", "addr", macString(addr))
			b.sendConnection(c, addr, false, 0, errConnFailEstablish)
			return
		}
		b.owner[addr] = c
		b.mu.Unlock()
		b.sendFree()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			mtu, err := b.BLE.Connect(ctx, addr, addrType)
			if err != nil {
				b.Log.Info("bluetooth proxy: connect failed", "addr", macString(addr), "err", err)
				b.mu.Lock()
				if b.owner[addr] == c {
					delete(b.owner, addr)
				}
				b.mu.Unlock()
				b.sendConnection(c, addr, false, 0, errConnFailEstablish)
				b.sendFree()
				return
			}
			b.Log.Info("bluetooth proxy: connected", "addr", macString(addr), "mtu", mtu)
			b.sendConnection(c, addr, true, mtu, 0)
		}()
	case reqDisconnect:
		go func() {
			b.BLE.Disconnect(addr)
			b.mu.Lock()
			_, had := b.owner[addr]
			delete(b.owner, addr)
			b.mu.Unlock()
			b.sendConnection(c, addr, false, 0, 0)
			if had {
				b.sendFree()
			}
		}()
	case reqPair, reqUnpair, reqClearCache:
		// Not served (no PAIRING / CACHE_CLEARING feature); Home Assistant does not ask.
		b.Log.Debug("bluetooth proxy: request not served", "addr", macString(addr), "type", kind)
	}
}

func (b *Bluetooth) services(c *Conn, addr uint64) {
	svcs, err := b.BLE.Services(addr)
	if err != nil {
		var e enc
		e.Uint(1, addr)
		e.Int32(3, errNotConnected)
		c.Send(msgGATTError, &e)
		return
	}
	short := c.Minor >= 12
	for _, s := range svcs { // one service per message keeps each frame small
		var e enc
		e.Uint(1, addr)
		var se enc
		putUUID(&se, s.UUID, 1, 4, short)
		se.Uint(2, uint64(s.Handle))
		for _, ch := range s.Chars {
			var ce enc
			putUUID(&ce, ch.UUID, 1, 5, short)
			ce.Uint(2, uint64(ch.Handle))
			ce.Uint(3, uint64(ch.Props))
			for _, d := range ch.Descs {
				var de enc
				putUUID(&de, d.UUID, 1, 3, short)
				de.Uint(2, uint64(d.Handle))
				ce.Msg(4, &de)
			}
			se.Msg(3, &ce)
		}
		e.Msg(2, &se)
		c.Send(msgGATTGetServicesResponse, &e)
	}
	var e enc
	e.Uint(1, addr)
	c.Send(msgGATTGetServicesDone, &e)
}

// putUUID writes a UUID: a 16/32-bit one of the Bluetooth base as short_uuid (field shortField)
// when the client takes it, else the 128 bits as two uint64s, high first (field longField).
func putUUID(e *enc, uuid string, longField, shortField int, short bool) {
	hexs := strings.ReplaceAll(strings.ToLower(uuid), "-", "")
	if len(hexs) != 32 {
		return
	}
	if short && strings.HasSuffix(hexs, "00001000800000805f9b34fb") {
		v, err := strconv.ParseUint(hexs[:8], 16, 32)
		if err == nil && v != 0 {
			e.Uint(shortField, v)
			return
		}
	}
	hi, err1 := strconv.ParseUint(hexs[:16], 16, 64)
	lo, err2 := strconv.ParseUint(hexs[16:], 16, 64)
	if err1 != nil || err2 != nil {
		return
	}
	// repeated uint64, packed (proto3's default for scalars)
	var p []byte
	p = binary.AppendUvarint(p, hi)
	p = binary.AppendUvarint(p, lo)
	e.Bytes(longField, p, true)
}

func (b *Bluetooth) sendConnection(c *Conn, addr uint64, connected bool, mtu uint32, errCode int32) {
	var e enc
	e.Uint(1, addr)
	e.Bool(2, connected)
	e.Uint(3, uint64(mtu))
	e.Int32(4, errCode)
	c.Send(msgBLEDeviceConnection, &e)
}

func (b *Bluetooth) sendFree() {
	b.mu.Lock()
	var subs []*Conn
	for c := range b.freeSub {
		subs = append(subs, c)
	}
	b.mu.Unlock()
	for _, c := range subs {
		b.sendFreeTo(c)
	}
}

func (b *Bluetooth) sendFreeTo(c *Conn) {
	b.mu.Lock()
	var e enc
	e.UintForce(1, uint64(max(b.Limit-len(b.owner), 0)))
	e.UintForce(2, uint64(b.Limit))
	var p []byte
	for addr := range b.owner {
		p = binary.AppendUvarint(p, addr)
	}
	b.mu.Unlock()
	if len(p) > 0 {
		e.Bytes(3, p, true)
	}
	c.Send(msgConnectionsFree, &e)
}

// Closed: the client is gone; its subscriptions end and its devices are disconnected.
func (b *Bluetooth) Closed(c *Conn) {
	b.init()
	b.mu.Lock()
	if cancel := b.ads[c]; cancel != nil {
		cancel()
	}
	delete(b.ads, c)
	delete(b.freeSub, c)
	var mine []uint64
	for addr, owner := range b.owner {
		if owner == c {
			mine = append(mine, addr)
			delete(b.owner, addr)
		}
	}
	b.mu.Unlock()
	for _, addr := range mine {
		b.BLE.Disconnect(addr)
	}
	if len(mine) > 0 {
		b.sendFree()
	}
}

// sendAdvertisements sends a batch as BluetoothLERawAdvertisementsResponse.
func sendAdvertisements(c *Conn, ads []Advertisement) {
	var e enc
	for _, a := range ads {
		var ae enc
		ae.UintForce(1, a.Address)
		ae.Sint32(2, a.RSSI)
		ae.Uint(3, uint64(a.AddressType))
		ae.Bytes(4, a.Data, true)
		e.Msg(1, &ae)
	}
	c.Send(msgBLERawAdvertisements, &e)
}

// macString formats an address for the log.
func macString(addr uint64) string {
	var b [6]byte
	for i := 0; i < 6; i++ {
		b[i] = byte(addr >> (8 * (5 - i)))
	}
	parts := make([]string, 6)
	for i, x := range b {
		parts[i] = strings.ToUpper(strconv.FormatUint(uint64(x)|0x100, 16)[1:])
	}
	return strings.Join(parts, ":")
}
