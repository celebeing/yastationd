package ddp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func packet(flags, typ, id byte, off uint32, data []byte) []byte {
	b := []byte{flags, 1, typ, id, byte(off >> 24), byte(off >> 16), byte(off >> 8), byte(off), byte(len(data) >> 8), byte(len(data))}
	return append(b, data...)
}

func TestParseRGBAndRGBW(t *testing.T) {
	p, ok := Parse(packet(0x41, 0x0b, 1, 0, []byte{1, 2, 3}))
	if !ok || !p.Push || p.Channels != 3 || p.ID != 1 || string(p.Data) != "\x01\x02\x03" {
		t.Fatalf("rgb: %+v %v", p, ok)
	}
	p, ok = Parse(packet(0x40, 0x1b, 1, 8, []byte{1, 2, 3, 4}))
	if !ok || p.Push || p.Channels != 4 || p.Offset != 8 {
		t.Fatalf("rgbw: %+v %v", p, ok)
	}
	// With a timecode the data starts 4 bytes later.
	b := packet(0x51, 0x0b, 1, 0, nil)
	b[9] = 3
	b = append(b, 0, 0, 0, 0, 7, 8, 9)
	if p, ok = Parse(b); !ok || string(p.Data) != "\x07\x08\x09" {
		t.Fatalf("timecode: %+v %v", p, ok)
	}
	if _, ok := Parse([]byte{0x80, 0, 0, 1, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("version 2 accepted")
	}
	if _, ok := Parse(packet(0x43, 0x0b, 1, 0, nil)); ok {
		t.Fatal("a query accepted as data")
	}
	if _, ok := Parse(packet(0x41, 0x0b, 1, 0, []byte{1, 2, 3})[:11]); ok {
		t.Fatal("short packet accepted")
	}
}

func TestAssembler(t *testing.T) {
	a := NewAssembler(2)
	// Split over two packets, pushed by the second.
	if _, ok := a.Add(Packet{Channels: 3, ID: 1, Offset: 0, Data: []byte{1, 2, 3}}); ok {
		t.Fatal("frame before push / end")
	}
	f, ok := a.Add(Packet{Push: true, Channels: 3, ID: 1, Offset: 3, Data: []byte{4, 5, 6}})
	if !ok || f.Channels != 3 || string(f.Data) != "\x01\x02\x03\x04\x05\x06" {
		t.Fatalf("frame %+v %v", f, ok)
	}
	// No push, but the data reaches the end: shown.
	if _, ok := a.Add(Packet{Channels: 3, ID: 1, Data: []byte{9, 9, 9, 9, 9, 9}}); !ok {
		t.Fatal("full frame without push not shown")
	}
	// More pixels than ours: cut; another id: ignored.
	if f, ok := a.Add(Packet{Push: true, Channels: 4, ID: 1, Data: make([]byte, 40)}); !ok || len(f.Data) != 8 {
		t.Fatalf("long frame: %d bytes", len(f.Data))
	}
	if _, ok := a.Add(Packet{Push: true, Channels: 3, ID: 246, Data: []byte("{}")}); ok {
		t.Fatal("control packet taken as a frame")
	}
}

func TestListen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan Frame, 1)
	// A free port: listen, note it, release it for Listen.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := pc.LocalAddr().String()
	pc.Close()
	go Listen(ctx, addr, 1, slog.New(slog.NewTextHandler(io.Discard, nil)), func(f Frame) { got <- f })
	time.Sleep(50 * time.Millisecond)
	c, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.Write(packet(0x41, 0x0b, 1, 0, []byte{10, 20, 30}))
	select {
	case f := <-got:
		if string(f.Data) != "\x0a\x14\x1e" {
			t.Fatalf("frame % x", f.Data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
}
