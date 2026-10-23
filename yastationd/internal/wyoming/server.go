package wyoming

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Handler receives the events of every connection. Each connection's events are delivered in
// order from its own goroutine; reply through the Conn.
type Handler interface {
	OnConnect(c *Conn)
	OnEvent(c *Conn, ev Event)
	// OnDisconnect is called once when c goes away.
	OnDisconnect(c *Conn)
}

// Conn is one peer connection. Send is safe for concurrent use.
type Conn struct {
	nc net.Conn
	mu sync.Mutex
}

func (c *Conn) Send(ev Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Write(c.nc, ev)
}

func (c *Conn) RemoteAddr() string { return c.nc.RemoteAddr().String() }

func (c *Conn) Close() error { return c.nc.Close() }

// Server accepts Wyoming peers. Connections are concurrent: besides its long-lived satellite
// connection, HA opens a short describe-only connection every ~30 s, so kicking the old peer on
// connect would sever the satellite (and any announcement) twice a minute. Stale peers are
// reaped by TCP keepalive (on by default for accepted Go conns).
type Server struct {
	Handler Handler
	Log     *slog.Logger

	mu    sync.Mutex
	ln    net.Listener
	conns map[*Conn]struct{}
}

// Serve listens on addr until Close. It returns after the listener fails or is closed.
func (s *Server) Serve(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.Log.Info("wyoming listening", "addr", addr)
	for {
		nc, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handle(nc)
	}
}

func (s *Server) handle(nc net.Conn) {
	c := &Conn{nc: nc}
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[*Conn]struct{}{}
	}
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	s.Log.Debug("wyoming peer connected", "peer", c.RemoteAddr())
	s.Handler.OnConnect(c)

	r := bufio.NewReaderSize(nc, 64<<10)
	for {
		ev, err := Read(r)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.Log.Info("wyoming peer read", "peer", c.RemoteAddr(), "err", err)
			}
			break
		}
		s.Handler.OnEvent(c, ev)
	}
	nc.Close()
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	s.Log.Debug("wyoming peer disconnected", "peer", c.RemoteAddr())
	s.Handler.OnDisconnect(c)
}

// Close stops accepting and drops all peers.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		s.ln.Close()
	}
	for c := range s.conns {
		c.Close()
	}
}
