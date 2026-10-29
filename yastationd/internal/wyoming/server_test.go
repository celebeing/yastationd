package wyoming

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

type serverEvents struct {
	connected    chan *Conn
	events       chan Event
	disconnected chan *Conn
}

func (h *serverEvents) OnConnect(c *Conn)        { h.connected <- c }
func (h *serverEvents) OnEvent(_ *Conn, e Event) { h.events <- e }
func (h *serverEvents) OnDisconnect(c *Conn)     { h.disconnected <- c }

func TestServerDeliversEventsAndClosesPeers(t *testing.T) {
	h := &serverEvents{make(chan *Conn, 2), make(chan Event, 2), make(chan *Conn, 2)}
	s := &Server{Handler: h, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	serve := make(chan error, 1)
	go func() { serve <- s.Serve("127.0.0.1:0") }()

	var addr string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if s.ln != nil {
			addr = s.ln.Addr().String()
		}
		s.mu.Unlock()
		if addr != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if addr == "" {
		t.Fatal("server did not start listening")
	}

	peer, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := Write(peer, Event{Type: TypePing}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.connected:
	case <-time.After(time.Second):
		t.Fatal("missing connect callback")
	}
	select {
	case got := <-h.events:
		if got.Type != TypePing {
			t.Fatalf("event type = %q, want %q", got.Type, TypePing)
		}
	case <-time.After(time.Second):
		t.Fatal("missing event callback")
	}

	s.Close()
	select {
	case <-h.disconnected:
	case <-time.After(time.Second):
		t.Fatal("missing disconnect callback")
	}
	select {
	case err := <-serve:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after Close")
	}
}

func TestConnSendSerializesConcurrentEvents(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	c := &Conn{nc: left}

	const sends = 8
	var wg sync.WaitGroup
	for range sends {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Send(Event{Type: TypePing}); err != nil {
				t.Errorf("Send: %v", err)
			}
		}()
	}
	got := make(chan error, 1)
	go func() {
		r := bufio.NewReader(right)
		for range sends {
			ev, err := Read(r)
			if err != nil || ev.Type != TypePing {
				if err == nil {
					err = fmt.Errorf("event type = %q, want %q", ev.Type, TypePing)
				}
				got <- err
				return
			}
		}
		got <- nil
	}()
	wg.Wait()
	if err := <-got; err != nil {
		t.Fatalf("concurrent events were not readable: %v", err)
	}
}
