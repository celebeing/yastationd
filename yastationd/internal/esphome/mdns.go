package esphome

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"strings"
	"time"
)

// MDNS announces the API as an ESPHome device: _esphomelib._tcp, which is how Home Assistant
// discovers a new one and finds it again at a new address. A minimal responder: it answers
// questions about its own names (PTR, SRV, TXT, A) and announces itself at start and every
// few minutes; it caches nothing and probes nothing (the name is the unique device_id).
type MDNS struct {
	Name  string            // the instance and host name (device_id)
	Port  int               // the API's port
	TXT   map[string]string // friendly_name, mac, version, ...
	Iface string            // the interface whose IPv4 the A record gives ("" = the first up one)
	Log   *slog.Logger
}

const (
	mdnsService = "_esphomelib._tcp.local."
	mdnsTTL     = 120
	typeA       = 1
	typePTR     = 12
	typeTXT     = 16
	typeSRV     = 33
	typeANY     = 255
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Run answers until ctx ends.
func (m *MDNS) Run(ctx context.Context) {
	var ifi *net.Interface
	if m.Iface != "" {
		ifi, _ = net.InterfaceByName(m.Iface)
	}
	pc, err := net.ListenMulticastUDP("udp4", ifi, mdnsGroup)
	if err != nil {
		m.Log.Warn("mdns: listen", "err", err)
		return
	}
	go func() {
		<-ctx.Done()
		pc.Close()
	}()
	instance := m.Name + "." + mdnsService
	host := m.Name + ".local."
	announce := func() {
		if ip := m.ip(); ip != nil {
			pc.WriteToUDP(m.response(instance, host, ip), mdnsGroup)
		}
	}
	go func() {
		announce()
		time.Sleep(time.Second)
		announce()
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				announce()
			}
		}
	}()
	m.Log.Info("mdns: announcing", "service", instance)
	buf := make([]byte, 9000)
	for {
		n, src, err := pc.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if !m.asked(buf[:n], instance, host) {
			continue
		}
		ip := m.ip()
		if ip == nil {
			continue
		}
		resp := m.response(instance, host, ip)
		if src.Port != 5353 { // a one-shot (legacy) query wants a unicast answer
			pc.WriteToUDP(resp, src)
		} else {
			pc.WriteToUDP(resp, mdnsGroup)
		}
	}
}

func (m *MDNS) ip() net.IP {
	ifaces, _ := net.Interfaces()
	for _, ifi := range ifaces {
		if m.Iface != "" && ifi.Name != m.Iface {
			continue
		}
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip4 := ipn.IP.To4(); ip4 != nil && !ip4.IsLinkLocalUnicast() {
					return ip4
				}
			}
		}
	}
	return nil
}

// asked: the packet is a query with a question about one of our names.
func (m *MDNS) asked(p []byte, instance, host string) bool {
	if len(p) < 12 || p[2]&0x80 != 0 { // a response, not a query
		return false
	}
	qd := int(binary.BigEndian.Uint16(p[4:]))
	off := 12
	for i := 0; i < qd; i++ {
		name, n := readName(p, off)
		if n < 0 || n+4 > len(p) {
			return false
		}
		off = n + 4
		qt := binary.BigEndian.Uint16(p[n:])
		name = strings.ToLower(name)
		switch {
		case name == mdnsService && (qt == typePTR || qt == typeANY):
			return true
		case name == strings.ToLower(instance) && (qt == typeSRV || qt == typeTXT || qt == typeANY):
			return true
		case name == strings.ToLower(host) && (qt == typeA || qt == typeANY):
			return true
		}
	}
	return false
}

// response is an authoritative answer with all four records (they are small, and a browser
// wants them all anyway).
func (m *MDNS) response(instance, host string, ip net.IP) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[2:], 0x8400) // response, authoritative
	binary.BigEndian.PutUint16(b[6:], 4)      // answers
	b = rr(b, mdnsService, typePTR, false, name(instance))
	b = rr(b, instance, typeSRV, true, srv(m.Port, host))
	b = rr(b, instance, typeTXT, true, txt(m.TXT))
	b = rr(b, host, typeA, true, ip.To4())
	return b
}

func rr(b []byte, owner string, typ uint16, unique bool, data []byte) []byte {
	b = append(b, name(owner)...)
	class := uint16(1)
	if unique {
		class |= 0x8000 // cache flush
	}
	b = binary.BigEndian.AppendUint16(b, typ)
	b = binary.BigEndian.AppendUint16(b, class)
	b = binary.BigEndian.AppendUint32(b, mdnsTTL)
	b = binary.BigEndian.AppendUint16(b, uint16(len(data)))
	return append(b, data...)
}

func name(s string) []byte {
	var b []byte
	for _, l := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

func srv(port int, host string) []byte {
	b := []byte{0, 0, 0, 0} // priority, weight
	b = binary.BigEndian.AppendUint16(b, uint16(port))
	return append(b, name(host)...)
}

func txt(kv map[string]string) []byte {
	var b []byte
	for _, k := range sortedKeys(kv) {
		s := k + "=" + kv[k]
		if len(s) > 255 {
			s = s[:255]
		}
		b = append(b, byte(len(s)))
		b = append(b, s...)
	}
	return b
}

func sortedKeys(kv map[string]string) []string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// readName reads a (possibly compressed) name at off; it returns the name and the offset after
// it in the packet (-1 on a malformed name).
func readName(p []byte, off int) (string, int) {
	var parts []string
	end := -1
	for hops := 0; hops < 20; hops++ {
		if off >= len(p) {
			return "", -1
		}
		l := int(p[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(parts, ".") + ".", end
		case l&0xc0 == 0xc0:
			if off+1 >= len(p) {
				return "", -1
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(p[off:]) & 0x3fff)
		default:
			if off+1+l > len(p) {
				return "", -1
			}
			parts = append(parts, string(p[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", -1
}
