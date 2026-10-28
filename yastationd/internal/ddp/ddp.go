// Package ddp receives LED frames over DDP (Distributed Display Protocol, UDP 4048), the
// protocol LedFx, WLED and xLights speak to "a strip of N pixels at this address"
// (docs/ambilight-stream.md). It takes RGB and RGBW 8-bit data for the default output (id 1),
// assembles a frame from one or more packets, and hands it over when the sender pushes it.
package ddp

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"strconv"
)

// DefaultPort is DDP's UDP port.
const DefaultPort = 4048

// Header flags (byte 0) and the data type field (byte 2).
const (
	flagVersionMask = 0xc0
	flagVersion1    = 0x40
	flagTimecode    = 0x10
	flagQuery       = 0x02
	flagPush        = 0x01

	typeRGBW = 3 // TTT bits 5..3 of the data type
)

// Packet is one parsed DDP packet.
type Packet struct {
	Push     bool
	Channels int // 3 (RGB) or 4 (RGBW)
	ID       byte
	Offset   int // in bytes
	Data     []byte
}

// Parse reads a DDP v1 packet; ok is false for anything else (or a query, which has no data).
func Parse(b []byte) (p Packet, ok bool) {
	if len(b) < 10 || b[0]&flagVersionMask != flagVersion1 || b[0]&flagQuery != 0 {
		return p, false
	}
	hdr := 10
	if b[0]&flagTimecode != 0 {
		hdr = 14
	}
	n := int(binary.BigEndian.Uint16(b[8:10]))
	if len(b) < hdr+n {
		return p, false
	}
	p.Push = b[0]&flagPush != 0
	p.Channels = 3
	if (b[2]>>3)&7 == typeRGBW {
		p.Channels = 4
	}
	p.ID = b[3]
	p.Offset = int(binary.BigEndian.Uint32(b[4:8]))
	p.Data = b[hdr : hdr+n]
	return p, true
}

// Frame is a received frame: Channels bytes per pixel, R, G, B (, W).
type Frame struct {
	Channels int
	Data     []byte
}

// Assembler collects packets into frames of up to pixels pixels.
type Assembler struct {
	pixels int
	buf    []byte
	ch     int
}

// NewAssembler makes an assembler for a strip of pixels pixels.
func NewAssembler(pixels int) *Assembler { return &Assembler{pixels: pixels} }

// Add takes one packet and returns a complete frame when the sender pushed it (or, for senders
// that never set push, when the packet reaches the end of our pixels).
func (a *Assembler) Add(p Packet) (Frame, bool) {
	if p.ID != 1 && p.ID != 0 { // 1 = the default output; others are control/config/status
		return Frame{}, false
	}
	if p.Channels != a.ch || a.buf == nil {
		a.ch = p.Channels
		a.buf = make([]byte, a.pixels*a.ch)
	}
	end := p.Offset + len(p.Data)
	if p.Offset < len(a.buf) {
		copy(a.buf[p.Offset:], p.Data)
	}
	if !p.Push && end < len(a.buf) {
		return Frame{}, false
	}
	return Frame{Channels: a.ch, Data: append([]byte(nil), a.buf...)}, true
}

// Listen receives DDP on addr (":4048") until ctx ends, calling onFrame for each complete frame.
func Listen(ctx context.Context, addr string, pixels int, log *slog.Logger, onFrame func(Frame)) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	log.Info("ddp: listening", "addr", addr, "pixels", pixels)
	asm := NewAssembler(pixels)
	buf := make([]byte, 1500)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if p, ok := Parse(buf[:n]); ok {
			if f, ok := asm.Add(p); ok {
				onFrame(f)
			}
		}
	}
}

// Addr is the listen address for port.
func Addr(port int) string { return ":" + strconv.Itoa(port) }
