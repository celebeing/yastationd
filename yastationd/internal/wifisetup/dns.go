package wifisetup

import (
	"context"
	"encoding/binary"
	"net"
)

// A catch-all DNS server: every A query is answered with the speaker's address, so a phone's
// connectivity check lands on the page and the phone offers to open it (captive portal). Other
// query types get an empty answer. It binds the speaker's address only (unbound keeps 127.0.0.1:53), not the device, so it also
// answers the speaker itself, whose own traffic to that address goes over lo.
func serveDNS(ctx context.Context, ip net.IP) error {
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp4", net.JoinHostPort(ip.String(), "53"))
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); pc.Close() }()
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if r := dnsAnswer(buf[:n], ip.To4()); r != nil {
			pc.WriteTo(r, from)
		}
	}
}

// dnsAnswer builds the reply to query q (nil = drop it).
func dnsAnswer(q []byte, ip net.IP) []byte {
	if len(q) < 12 || q[2]&0x80 != 0 || binary.BigEndian.Uint16(q[4:6]) != 1 {
		return nil // not a single-question query
	}
	end := 12
	for end < len(q) && q[end] != 0 {
		if q[end]&0xc0 != 0 {
			return nil // no compression in a question
		}
		end += int(q[end]) + 1
	}
	end += 5 // zero label + type + class
	if end > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[end-4 : end-2])
	r := make([]byte, end, end+16)
	copy(r, q[:end])
	r[2] = 0x84 | q[2]&0x79 // QR, AA; keep opcode and RD
	r[3] = 0                // no recursion, NOERROR
	binary.BigEndian.PutUint16(r[6:8], 0)
	binary.BigEndian.PutUint16(r[8:10], 0)
	binary.BigEndian.PutUint16(r[10:12], 0)
	if qtype == 1 { // A
		binary.BigEndian.PutUint16(r[6:8], 1)
		r = append(r, 0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 10, 0, 4)
		r = append(r, ip...)
	}
	return r
}
