package wifisetup

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"
)

// A minimal DHCP server for the access point's clients (the firmware has no udhcpd): one /24,
// leases by MAC, and the speaker as router and DNS so phones find the captive portal.

const (
	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpDecline  = 4
	dhcpAck      = 5
	dhcpNak      = 6
	dhcpRelease  = 7
	dhcpInform   = 8

	optMask      = 1
	optRouter    = 3
	optDNS       = 6
	optReqIP     = 50
	optLease     = 51
	optMsgType   = 53
	optServerID  = 54
	optCaptive   = 114 // RFC 8910 captive-portal URI
	optEnd       = 255
	dhcpLease    = time.Hour
	dhcpPoolFrom = 10
	dhcpPoolTo   = 250
)

var dhcpMagic = []byte{99, 130, 83, 99}

type dhcpServer struct {
	iface  string
	ip     net.IP // ours, 4 bytes; the pool is ip's /24
	portal string
	log    *slog.Logger

	mu     sync.Mutex
	leases map[string]byte // MAC -> last octet
}

func newDHCPServer(iface string, ip net.IP, portal string, log *slog.Logger) *dhcpServer {
	return &dhcpServer{iface: iface, ip: ip.To4(), portal: portal, log: log, leases: map[string]byte{}}
}

// listenUDP binds addr on iface only, with broadcast allowed.
func listenUDP(ctx context.Context, iface, addr string) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); serr != nil {
				return
			}
			serr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, iface)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	return lc.ListenPacket(ctx, "udp4", addr)
}

func (s *dhcpServer) serve(ctx context.Context) error {
	pc, err := listenUDP(ctx, s.iface, "0.0.0.0:67")
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); pc.Close() }()
	buf := make([]byte, 1500)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		reply, to := s.handle(buf[:n])
		if reply == nil {
			continue
		}
		if _, err := pc.WriteTo(reply, to); err != nil {
			s.log.Warn("wifi setup: dhcp reply", "err", err)
		}
	}
}

type dhcpPacket struct {
	xid    []byte
	flags  []byte
	ciaddr net.IP
	chaddr []byte // 16 bytes
	mac    net.HardwareAddr
	opts   map[byte][]byte
}

func parseDHCP(b []byte) (*dhcpPacket, error) {
	if len(b) < 240 || b[0] != 1 || !bytes.Equal(b[236:240], dhcpMagic) {
		return nil, errors.New("not a DHCP request")
	}
	hlen := int(b[2])
	if hlen > 16 {
		return nil, errors.New("bad hlen")
	}
	p := &dhcpPacket{xid: b[4:8], flags: b[10:12], ciaddr: net.IP(b[12:16]), chaddr: b[28:44],
		mac: net.HardwareAddr(b[28 : 28+hlen]), opts: map[byte][]byte{}}
	for o := b[240:]; len(o) > 0; {
		code := o[0]
		if code == 0 {
			o = o[1:]
			continue
		}
		if code == optEnd || len(o) < 2 || len(o) < 2+int(o[1]) {
			break
		}
		p.opts[code] = o[2 : 2+int(o[1])]
		o = o[2+int(o[1]):]
	}
	return p, nil
}

// handle answers one request; it returns nil when nothing is to be sent.
func (s *dhcpServer) handle(b []byte) ([]byte, net.Addr) {
	p, err := parseDHCP(b)
	if err != nil || len(p.opts[optMsgType]) != 1 {
		return nil, nil
	}
	mac := p.mac.String()
	switch p.opts[optMsgType][0] {
	case dhcpDiscover:
		ip := s.lease(mac)
		if ip == nil {
			s.log.Warn("wifi setup: dhcp pool exhausted")
			return nil, nil
		}
		return s.reply(p, dhcpOffer, ip), s.dest(p)
	case dhcpRequest:
		if sid := p.opts[optServerID]; sid != nil && !net.IP(sid).Equal(s.ip) {
			return nil, nil // the client chose another server
		}
		want := net.IP(p.opts[optReqIP])
		if want == nil {
			want = p.ciaddr
		}
		ip := s.lease(mac)
		if ip == nil || !want.Equal(ip) {
			return s.reply(p, dhcpNak, nil), s.dest(p)
		}
		s.log.Info("wifi setup: client joined", "mac", mac, "ip", ip.String(),
			"host", string(p.opts[12]))
		return s.reply(p, dhcpAck, ip), s.dest(p)
	case dhcpInform:
		return s.reply(p, dhcpAck, nil), s.dest(p)
	case dhcpRelease, dhcpDecline:
		// Keep the address for the MAC: it will very likely come back.
	}
	return nil, nil
}

// lease returns the MAC's address, assigning the next free one first.
func (s *dhcpServer) lease(mac string) net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.leases[mac]; ok {
		return s.addr(o)
	}
	used := map[byte]bool{s.ip[3]: true}
	for _, o := range s.leases {
		used[o] = true
	}
	for o := byte(dhcpPoolFrom); o <= dhcpPoolTo; o++ {
		if !used[o] {
			s.leases[mac] = o
			return s.addr(o)
		}
	}
	return nil
}

func (s *dhcpServer) addr(o byte) net.IP {
	return net.IPv4(s.ip[0], s.ip[1], s.ip[2], o).To4()
}

// dest: the client has no address yet, so replies are broadcast unless it is renewing one.
func (s *dhcpServer) dest(p *dhcpPacket) net.Addr {
	if !p.ciaddr.Equal(net.IPv4zero) {
		return &net.UDPAddr{IP: p.ciaddr, Port: 68}
	}
	return &net.UDPAddr{IP: net.IPv4bcast, Port: 68}
}

func (s *dhcpServer) reply(p *dhcpPacket, typ byte, yiaddr net.IP) []byte {
	b := make([]byte, 240, 320)
	b[0], b[1], b[2] = 2, 1, 6
	copy(b[4:8], p.xid)
	copy(b[10:12], p.flags)
	if typ == dhcpAck && yiaddr == nil { // INFORM: echo the client's own address
		copy(b[12:16], p.ciaddr.To4())
	}
	if yiaddr != nil {
		copy(b[16:20], yiaddr)
	}
	copy(b[20:24], s.ip)
	copy(b[28:44], p.chaddr)
	copy(b[236:240], dhcpMagic)
	opt := func(code byte, v []byte) { b = append(append(b, code, byte(len(v))), v...) }
	opt(optMsgType, []byte{typ})
	opt(optServerID, s.ip)
	if typ != dhcpNak {
		lease := make([]byte, 4)
		binary.BigEndian.PutUint32(lease, uint32(dhcpLease/time.Second))
		if yiaddr != nil {
			opt(optLease, lease)
		}
		opt(optMask, []byte{255, 255, 255, 0})
		opt(optRouter, s.ip)
		opt(optDNS, s.ip)
		if s.portal != "" {
			opt(optCaptive, []byte(s.portal))
		}
	}
	b = append(b, optEnd)
	for len(b) < 300 { // BOOTP minimum; some clients drop shorter replies
		b = append(b, 0)
	}
	return b
}
