package esphome

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeBLE struct {
	mu     sync.Mutex
	adFn   func([]Advertisement)
	notify func([]byte)
	disc   func(uint64)
	wrote  []byte
}

func (f *fakeBLE) Advertisements(fn func([]Advertisement)) func() {
	f.mu.Lock()
	f.adFn = fn
	f.mu.Unlock()
	return func() {}
}
func (f *fakeBLE) Connect(context.Context, uint64, uint32) (uint32, error) { return 247, nil }
func (f *fakeBLE) Disconnect(uint64) error                                 { return nil }
func (f *fakeBLE) OnDisconnected(fn func(uint64))                          { f.disc = fn }
func (f *fakeBLE) Services(uint64) ([]GATTService, error) {
	return []GATTService{{UUID: "00001828-0000-1000-8000-00805f9b34fb", Handle: 12, Chars: []GATTChar{
		{UUID: "00002add-0000-1000-8000-00805f9b34fb", Handle: 14, Props: 0x14,
			Descs: []GATTDesc{{UUID: "00002902-0000-1000-8000-00805f9b34fb", Handle: 15}}},
		{UUID: "6e400002-b5a3-f393-e0a9-e50e24dcca9e", Handle: 17, Props: 0x08},
	}}}, nil
}
func (f *fakeBLE) Read(uint64, uint16) ([]byte, error) { return []byte{1, 2}, nil }
func (f *fakeBLE) Write(_ uint64, _ uint16, d []byte, _ bool) error {
	f.mu.Lock()
	f.wrote = d
	f.mu.Unlock()
	return nil
}
func (f *fakeBLE) ReadDescriptor(uint64, uint16) ([]byte, error) { return []byte{0, 0}, nil }
func (f *fakeBLE) WriteDescriptor(uint64, uint16, []byte) error  { return nil }
func (f *fakeBLE) Notify(_ uint64, _ uint16, _ bool, fn func([]byte)) error {
	f.mu.Lock()
	f.notify = fn
	f.mu.Unlock()
	return nil
}

type client struct {
	t  *testing.T
	nc net.Conn
	r  *bufio.Reader
}

func (c *client) send(typ uint32, e *enc) {
	f := []byte{0}
	f = binary.AppendUvarint(f, uint64(len(e.b)))
	f = binary.AppendUvarint(f, uint64(typ))
	f = append(f, e.b...)
	if _, err := c.nc.Write(f); err != nil {
		c.t.Fatal(err)
	}
}

// want reads frames until one of type typ (others are skipped) and returns it.
func (c *client) want(typ uint32) msg {
	c.t.Helper()
	c.nc.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		got, body, err := readFrame(c.r)
		if err != nil {
			c.t.Fatalf("waiting for %d: %v", typ, err)
		}
		if got == typ {
			m, err := parse(body)
			if err != nil {
				c.t.Fatal(err)
			}
			return m
		}
	}
}

// A Home Assistant-like client: hello, device info, no entities, advertisements, a connection,
// its services, a read, a write, a notification.
func TestBluetoothProxySession(t *testing.T) {
	ble := &fakeBLE{}
	bt := &Bluetooth{BLE: ble, Limit: 3, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	s := &Server{Addr: addr, Components: []Component{bt}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Info: func() DeviceInfo {
			return DeviceInfo{Name: "station3", FriendlyName: "Station", MAC: "02:00:00:00:00:01", BluetoothMAC: "02:00:00:00:00:02",
				ESPHomeVersion: "2026.10.0", ProjectName: "Yandex.Station 3", ProjectVersion: "v0", BluetoothFlags: bt.Flags()}
		}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	var nc net.Conn
	for i := 0; i < 50; i++ {
		if nc, err = net.Dial("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	c := &client{t: t, nc: nc, r: bufio.NewReader(nc)}
	var hello enc
	hello.String(1, "test")
	hello.Uint(2, 1)
	hello.Uint(3, 16)
	c.send(msgHelloRequest, &hello)
	if h := c.want(msgHelloResponse); h.Uint(1) != 1 || h.String(4) != "station3" {
		t.Fatalf("hello: %v", h)
	}
	c.send(msgDeviceInfoRequest, &enc{})
	di := c.want(msgDeviceInfoResponse)
	if di.String(3) != "02:00:00:00:00:01" || di.Uint(15) != uint64(bt.Flags()) || di.String(18) != "02:00:00:00:00:02" {
		t.Fatalf("device info: mac %q flags %d bt %q", di.String(3), di.Uint(15), di.String(18))
	}
	c.send(msgListEntitiesRequest, &enc{})
	c.want(msgListEntitiesDone)

	var sub enc
	sub.Uint(1, 1)
	c.send(msgSubscribeBLEAdvertisements, &sub)
	c.send(msgPingRequest, &enc{}) // the subscription is in place once the ping is answered
	c.want(msgPingResponse)
	ble.mu.Lock()
	ble.adFn([]Advertisement{{Address: 0x0200000000A1, RSSI: -60, AddressType: 0, Data: []byte{2, 1, 6}}})
	ble.mu.Unlock()
	ads, _ := decode(c.want(msgBLERawAdvertisements).Bytes(1))
	a := msg(ads)
	if a.Uint(1) != 0x0200000000A1 || int32(uint32(a.Uint(2))>>1)^-int32(uint32(a.Uint(2))&1) != -60 || len(a.Bytes(4)) != 3 {
		t.Fatalf("advertisement: %v", a)
	}

	c.send(msgSubscribeConnectionsFree, &enc{})
	if f := c.want(msgConnectionsFree); f.Uint(1) != 3 || f.Uint(2) != 3 {
		t.Fatalf("free: %v", f)
	}
	var conn enc
	conn.Uint(1, 0x0200000000A1)
	conn.Uint(2, reqConnectWithCache)
	conn.Bool(3, true)
	c.send(msgBLEDeviceRequest, &conn)
	if r := c.want(msgBLEDeviceConnection); !r.Bool(2) || r.Uint(3) != 247 {
		t.Fatalf("connection: %v", r)
	}
	var gs enc
	gs.Uint(1, 0x0200000000A1)
	c.send(msgGATTGetServices, &gs)
	svc, _ := parse(c.want(msgGATTGetServicesResponse).Bytes(2))
	if svc.Uint(4) != 0x1828 || svc.Uint(2) != 12 {
		t.Fatalf("service: short uuid %x handle %d", svc.Uint(4), svc.Uint(2))
	}
	var chars []msg
	for _, f := range svc {
		if f.num == 3 {
			m, _ := parse(f.bytes)
			chars = append(chars, m)
		}
	}
	if len(chars) != 2 || chars[0].Uint(5) != 0x2add || len(chars[1].Bytes(1)) == 0 {
		t.Fatalf("characteristics: %v", chars)
	}
	c.want(msgGATTGetServicesDone)

	var rd enc
	rd.Uint(1, 0x0200000000A1)
	rd.Uint(2, 14)
	c.send(msgGATTRead, &rd)
	if r := c.want(msgGATTReadResponse); string(r.Bytes(3)) != "\x01\x02" {
		t.Fatalf("read: %v", r)
	}
	var wr enc
	wr.Uint(1, 0x0200000000A1)
	wr.Uint(2, 17)
	wr.Bool(3, true)
	wr.Bytes(4, []byte("hi"), true)
	c.send(msgGATTWrite, &wr)
	c.want(msgGATTWriteResponse)
	var nt enc
	nt.Uint(1, 0x0200000000A1)
	nt.Uint(2, 14)
	nt.Bool(3, true)
	c.send(msgGATTNotify, &nt)
	c.want(msgGATTNotifyResponse)
	ble.mu.Lock()
	ble.notify([]byte{9})
	ble.mu.Unlock()
	if n := c.want(msgGATTNotifyData); n.Uint(2) != 14 || string(n.Bytes(3)) != "\x09" {
		t.Fatalf("notify: %v", n)
	}
	ble.disc(0x0200000000A1)
	if r := c.want(msgBLEDeviceConnection); r.Bool(2) {
		t.Fatal("a disconnection was reported connected")
	}
}

func TestMDNSAnswer(t *testing.T) {
	m := &MDNS{Name: "station3", Port: 6053, TXT: map[string]string{"mac": "020000000001", "friendly_name": "Station"}}
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[4:], 1)
	q = append(q, name(mdnsService)...)
	q = binary.BigEndian.AppendUint16(q, typePTR)
	q = binary.BigEndian.AppendUint16(q, 1)
	if !m.asked(q, "station3."+mdnsService, "station3.local.") {
		t.Fatal("the service query was not ours")
	}
	r := m.response("station3."+mdnsService, "station3.local.", net.IPv4(192, 0, 2, 7))
	if binary.BigEndian.Uint16(r[6:]) != 4 {
		t.Fatal("answers")
	}
	if n, end := readName(r, 12); n != mdnsService || end < 0 {
		t.Fatalf("first answer's owner %q", n)
	}
}

// A client that only listens is pinged, and stays while it answers the pings.
func TestSilentClientIsPingedNotDropped(t *testing.T) {
	old, oldWait := readIdle, pingWait
	readIdle, pingWait = 200*time.Millisecond, 200*time.Millisecond
	defer func() { readIdle, pingWait = old, oldWait }()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	s := &Server{Addr: addr, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Info: func() DeviceInfo { return DeviceInfo{Name: "x"} }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	var nc net.Conn
	var err error
	for i := 0; i < 50; i++ {
		if nc, err = net.Dial("tcp", addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	c := &client{t: t, nc: nc, r: bufio.NewReader(nc)}
	c.send(msgHelloRequest, &enc{})
	c.want(msgHelloResponse)
	for i := 0; i < 3; i++ { // three rounds, each longer than readIdle + pingWait without the answer
		c.want(msgPingRequest)
		c.send(msgPingResponse, &enc{})
	}
	c.send(msgDeviceInfoRequest, &enc{})
	c.want(msgDeviceInfoResponse) // still connected
}
