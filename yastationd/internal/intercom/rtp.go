// Package intercom is a 1-to-1 voice call between two speakers on the LAN (docs/intercom.md):
// signalling messages signed with a shared secret (carried over MQTT by the caller of this
// package), and RTP-shaped UDP media — L16, 16 kHz mono, 20 ms frames, sealed with AES-128-GCM —
// with a jitter buffer that also absorbs the drift between the two speakers' audio clocks.
// Nothing here touches audio devices: the daemon feeds the uplink and pulls the downlink.
package intercom

import (
	"encoding/binary"
	"errors"
	"time"
)

const (
	Rate         = 16000
	FrameSamples = Rate / 50 // 20 ms
	FrameBytes   = FrameSamples * 2
	FrameDur     = 20 * time.Millisecond

	rtpVersion = 2
	ptL16      = 96 // dynamic payload type: L16/16000/1
	headerLen  = 12
	tagLen     = 16
)

// header is the fixed RFC 3550 header (no CSRCs, no extension, no padding).
type header struct {
	marker bool
	seq    uint16
	ts     uint32
	ssrc   uint32
}

func (h header) put(b []byte) {
	b[0] = rtpVersion << 6
	b[1] = ptL16
	if h.marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:], h.seq)
	binary.BigEndian.PutUint32(b[4:], h.ts)
	binary.BigEndian.PutUint32(b[8:], h.ssrc)
}

var errPacket = errors.New("intercom: not one of our packets")

func parseHeader(b []byte) (header, error) {
	if len(b) < headerLen+tagLen || b[0] != rtpVersion<<6 || b[1]&0x7f != ptL16 {
		return header{}, errPacket
	}
	return header{
		marker: b[1]&0x80 != 0,
		seq:    binary.BigEndian.Uint16(b[2:]),
		ts:     binary.BigEndian.Uint32(b[4:]),
		ssrc:   binary.BigEndian.Uint32(b[8:]),
	}, nil
}

// putL16 writes samples as network-order (big-endian) L16, as RTP wants.
func putL16(b []byte, s []int16) {
	for i, v := range s {
		binary.BigEndian.PutUint16(b[2*i:], uint16(v))
	}
}

func getL16(b []byte) []int16 {
	s := make([]int16, len(b)/2)
	for i := range s {
		s[i] = int16(binary.BigEndian.Uint16(b[2*i:]))
	}
	return s
}

// firstIndex is the extended index given to the first packet of a stream: far enough from zero
// that unwrapping a packet from just before it can't underflow.
func firstIndex(seq uint16) uint64 { return 1<<32 | uint64(seq) }

// unwrap extends a 16-bit sequence number to the index nearest the highest one seen so far
// (RFC 3550 A.1 in spirit: a jump of more than half the space is a wrap, either way).
func unwrap(highest uint64, seq uint16) uint64 {
	idx := highest&^0xffff | uint64(seq)
	switch {
	case idx+0x8000 < highest:
		idx += 1 << 16
	case idx > highest+0x8000:
		idx -= 1 << 16
	}
	return idx
}
