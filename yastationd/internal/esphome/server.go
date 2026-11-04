// Package esphome serves the ESPHome native API (docs/esphome-api.md): the protocol Home
// Assistant's ESPHome integration speaks to its devices. The speaker serves only what it uses it
// for (a Bluetooth proxy); the server itself is generic, and a feature plugs in as a Component.
// Plaintext framing; no entities.
package esphome

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Message type ids (api.proto, option (id)).
const (
	msgHelloRequest        = 1
	msgHelloResponse       = 2
	msgDisconnectRequest   = 5
	msgDisconnectResponse  = 6
	msgPingRequest         = 7
	msgPingResponse        = 8
	msgDeviceInfoRequest   = 9
	msgDeviceInfoResponse  = 10
	msgListEntitiesRequest = 11
	msgListEntitiesDone    = 19
	msgSubscribeStates     = 20
	msgSubscribeLogs       = 28
	msgSubscribeHAServices = 34
	msgGetTimeResponse     = 37
	msgSubscribeHAStates   = 38
)

// The API version the server speaks: 1.13 (below 1.15, so clients take the capabilities from
// DeviceInfoResponse; 1.12+ for the short UUIDs in GATT services).
const (
	apiMajor = 1
	apiMinor = 13
)

// DeviceInfo is what DeviceInfoResponse says about the speaker. Home Assistant keys the device
// by MAC (the Wi-Fi one), and checks Name against the name it stored when adding it.
type DeviceInfo struct {
	Name           string // a hostname-like id (device_id)
	FriendlyName   string
	MAC            string // "AC:BC:32:89:0E:A9"
	BluetoothMAC   string
	Model          string
	Manufacturer   string
	ProjectName    string // "<manufacturer>.<model>": HA shows these two halves
	ProjectVersion string
	ESPHomeVersion string // the ESPHome release whose API this implements
	ServerInfo     string
	BluetoothFlags uint32 // bluetooth_proxy_feature_flags; 0 = no proxy
}

// Component is a feature served over the API: it names the message types it takes, handles
// them, and lets go of a connection's state when the connection ends.
type Component interface {
	Types() []uint32
	Handle(c *Conn, typ uint32, m msg)
	Closed(c *Conn)
}

// Server accepts API clients.
type Server struct {
	Addr       string
	Info       func() DeviceInfo
	Components []Component
	Log        *slog.Logger

	mu    sync.Mutex
	conns map[*Conn]bool
}

// Conn is one client connection, after its Hello.
type Conn struct {
	s       *Server
	nc      net.Conn
	wmu     sync.Mutex
	w       *bufio.Writer
	Client  string // client_info from Hello
	Minor   uint32 // the client's API minor version
	closing chan struct{}
	once    sync.Once
}

// readIdle: a client that sends nothing this long is gone (Home Assistant pings every 20 s).
const readIdle = 90 * time.Second

// Run serves until ctx ends.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return err
	}
	s.Log.Info("esphome api: listening", "addr", s.Addr)
	go func() {
		<-ctx.Done()
		ln.Close()
		s.mu.Lock()
		for c := range s.conns {
			c.close()
		}
		s.mu.Unlock()
	}()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			s.Log.Warn("esphome api: accept", "err", err)
			time.Sleep(time.Second)
			continue
		}
		go s.serve(nc)
	}
}

func (s *Server) serve(nc net.Conn) {
	if tc, ok := nc.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
		tc.SetKeepAlive(true)
	}
	c := &Conn{s: s, nc: nc, w: bufio.NewWriter(nc), closing: make(chan struct{})}
	r := bufio.NewReader(nc)
	defer c.close()
	// The first frame must be Hello; anything else, or a frame that can't be read, closes the
	// connection without a word (the protocol's rule).
	nc.SetReadDeadline(time.Now().Add(30 * time.Second))
	typ, body, err := readFrame(r)
	if err != nil || typ != msgHelloRequest {
		return
	}
	hello, _ := parse(body)
	c.Client, c.Minor = hello.String(1), uint32(hello.Uint(3))
	info := s.Info()
	var e enc
	e.UintForce(1, apiMajor)
	e.UintForce(2, apiMinor)
	e.String(3, info.ServerInfo)
	e.String(4, info.Name)
	if c.Send(msgHelloResponse, &e) != nil {
		return
	}
	s.Log.Info("esphome api: client", "remote", nc.RemoteAddr().String(), "client", c.Client,
		"api", fmt.Sprintf("1.%d", c.Minor))
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[*Conn]bool{}
	}
	s.conns[c] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		for _, comp := range s.Components {
			comp.Closed(c)
		}
		s.Log.Info("esphome api: client gone", "remote", nc.RemoteAddr().String())
	}()
	handlers := map[uint32]Component{}
	for _, comp := range s.Components {
		for _, t := range comp.Types() {
			handlers[t] = comp
		}
	}
	for {
		nc.SetReadDeadline(time.Now().Add(readIdle))
		typ, body, err := readFrame(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.Log.Debug("esphome api: read", "err", err)
			}
			return
		}
		m, err := parse(body)
		if err != nil {
			s.Log.Warn("esphome api: bad message", "type", typ, "err", err)
			return
		}
		switch typ {
		case msgPingRequest:
			c.Send(msgPingResponse, &enc{})
		case msgPingResponse:
		case msgDisconnectRequest:
			c.Send(msgDisconnectResponse, &enc{})
			return
		case msgDisconnectResponse:
			return
		case msgDeviceInfoRequest:
			c.Send(msgDeviceInfoResponse, deviceInfo(s.Info()))
		case msgListEntitiesRequest:
			c.Send(msgListEntitiesDone, &enc{}) // no entities: MQTT has them
		case msgSubscribeStates, msgSubscribeLogs, msgSubscribeHAServices, msgSubscribeHAStates, msgGetTimeResponse:
			// accepted, nothing to send
		default:
			if comp := handlers[typ]; comp != nil {
				comp.Handle(c, typ, m)
			} else {
				s.Log.Debug("esphome api: message not served", "type", typ)
			}
		}
	}
}

func deviceInfo(i DeviceInfo) *enc {
	var e enc
	e.String(2, i.Name)
	e.String(3, i.MAC)
	e.String(4, i.ESPHomeVersion)
	e.String(6, i.Model)
	e.String(8, i.ProjectName)
	e.String(9, i.ProjectVersion)
	e.Uint(15, uint64(i.BluetoothFlags))
	e.String(12, i.Manufacturer)
	e.String(13, i.FriendlyName)
	if i.BluetoothFlags != 0 {
		e.String(18, i.BluetoothMAC)
	}
	return &e
}

// Send writes one message (safe from any goroutine).
func (c *Conn) Send(typ uint32, e *enc) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	select {
	case <-c.closing:
		return net.ErrClosed
	default:
	}
	frame := make([]byte, 0, len(e.b)+12)
	frame = append(frame, 0)
	frame = binary.AppendUvarint(frame, uint64(len(e.b)))
	frame = binary.AppendUvarint(frame, uint64(typ))
	frame = append(frame, e.b...)
	c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := c.w.Write(frame); err != nil {
		go c.close()
		return err
	}
	if err := c.w.Flush(); err != nil {
		go c.close()
		return err
	}
	return nil
}

// Done is closed when the connection ends.
func (c *Conn) Done() <-chan struct{} { return c.closing }

func (c *Conn) close() {
	c.once.Do(func() {
		close(c.closing)
		c.nc.Close()
	})
}

// maxFrame bounds a frame from a client (the largest the API sends is a GATT write of an MTU).
const maxFrame = 64 << 10

// readFrame reads one plaintext frame: 0x00, varint length, varint type, the message.
func readFrame(r *bufio.Reader) (uint32, []byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if b != 0 {
		return 0, nil, fmt.Errorf("esphome api: preamble %#x (an encrypted client? encryption is not served)", b)
	}
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, err
	}
	typ, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, nil, err
	}
	if n > maxFrame {
		return 0, nil, fmt.Errorf("esphome api: frame of %d bytes", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return uint32(typ), body, nil
}
