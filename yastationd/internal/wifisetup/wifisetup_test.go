package wifisetup

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleConf = `ctrl_interface=/var/run/wpa_supplicant
ap_scan=1
update_config=1

network={
	ssid="home"
	psk="secret123"
	key_mgmt=WPA-PSK
	priority=5
}

network={
	ssid=d0b4d0b0d187d0b0
	bssid=02:00:00:00:00:01
	key_mgmt=NONE
	# a comment
}
`

func TestConfRoundTrip(t *testing.T) {
	c, err := ParseConf([]byte(sampleConf))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Head) != 3 || len(c.Networks) != 2 {
		t.Fatalf("head %d networks %d", len(c.Head), len(c.Networks))
	}
	if s := c.Networks[0].SSID(); s != "home" {
		t.Errorf("ssid %q", s)
	}
	if s := c.Networks[1].SSID(); s != "дача" {
		t.Errorf("hex ssid %q", s)
	}
	if c.Networks[0].Security() != "psk" || c.Networks[1].Security() != "open" || c.Networks[0].Priority() != 5 {
		t.Errorf("security/priority wrong")
	}
	again, err := ParseConf(c.Format())
	if err != nil {
		t.Fatal(err)
	}
	if string(again.Format()) != string(c.Format()) {
		t.Errorf("format not stable:\n%s\n---\n%s", c.Format(), again.Format())
	}
	if !strings.Contains(string(c.Format()), "bssid=02:00:00:00:00:01") || !strings.Contains(string(c.Format()), "# a comment") {
		t.Errorf("unknown fields or comments lost:\n%s", c.Format())
	}
}

func TestConfErrors(t *testing.T) {
	if _, err := ParseConf([]byte("network={\n\tssid=\"x\"\n")); err == nil {
		t.Error("unterminated block accepted")
	}
}

func TestNewNetworkAndSetPSK(t *testing.T) {
	n, err := NewNetwork("Кафе \"x\"", "", true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := n.Get("ssid"); strings.HasPrefix(v, `"`) {
		t.Errorf("non-ASCII SSID not hex-encoded: %s", v)
	}
	if n.SSID() != "Кафе \"x\"" || n.Security() != "open" || !n.Hidden() {
		t.Errorf("got %q %s %v", n.SSID(), n.Security(), n.Hidden())
	}
	if err := n.SetPSK("pa\"ss#word"); err != nil {
		t.Fatal(err)
	}
	if v, _ := n.Get("psk"); v != `"pa"ss#word"` {
		t.Errorf("psk %s", v)
	}
	if v, _ := n.Get("key_mgmt"); v != "WPA-PSK" {
		t.Errorf("key_mgmt %s", v)
	}
	for _, bad := range []string{"short", strings.Repeat("x", 64), "пароль123"} {
		if err := n.SetPSK(bad); err == nil {
			t.Errorf("psk %q accepted", bad)
		}
	}
	if err := n.SetPSK(strings.Repeat("aB", 32)); err != nil {
		t.Errorf("64 hex digits rejected: %v", err)
	}
	if _, err := NewNetwork(strings.Repeat("x", 33), "", false, 0); err == nil {
		t.Error("33-byte SSID accepted")
	}
}

func TestWriteConfAtomic(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wpa_supplicant.conf")
	os.WriteFile(p, []byte("old"), 0o600)
	if err := WriteConfAtomic(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Errorf("got %q", b)
	}
	ents, _ := os.ReadDir(filepath.Dir(p))
	if len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}
}

func TestParseScanResults(t *testing.T) {
	out := "bssid / frequency / signal level / flags / ssid\n" +
		"aa:bb:cc:dd:ee:01\t2437\t-70\t[WPA2-PSK-CCMP][ESS]\thome\n" +
		"aa:bb:cc:dd:ee:02\t5500\t-50\t[WPA2-PSK-CCMP][ESS]\thome\n" +
		"aa:bb:cc:dd:ee:03\t2412\t-60\t[ESS]\t\\xd0\\xb4\\\"q\\\"\n" +
		"aa:bb:cc:dd:ee:04\t2412\t-40\t[ESS]\t\n"
	r := parseScanResults([]byte(out))
	if len(r) != 2 {
		t.Fatalf("%+v", r)
	}
	if r[0].SSID != "home" || r[0].Signal != -50 || r[0].Freq != 5500 || r[0].Security != "psk" {
		t.Errorf("%+v", r[0])
	}
	if r[1].SSID != "д\"q\"" || r[1].Security != "open" {
		t.Errorf("%+v", r[1])
	}
}

func TestFindWPAArgs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "1"), 0o755)
	os.WriteFile(filepath.Join(root, "1", "cmdline"), []byte("/sbin/init\x00"), 0o644)
	os.MkdirAll(filepath.Join(root, "1339"), 0o755)
	os.WriteFile(filepath.Join(root, "1339", "cmdline"),
		[]byte("/usr/sbin/wpa_supplicant\x00-B\x00-Dnl80211\x00-iwlan0\x00-c/data/wpa.conf\x00-P\x00/var/run/w.pid\x00"), 0o644)
	p, ok := findWPA(root)
	if !ok || p.pid != 1339 || p.iface != "wlan0" || p.conf != "/data/wpa.conf" || len(p.argv) != 7 {
		t.Fatalf("%+v %v", p, ok)
	}
	if v := optArg([]string{"x", "-i", "wlan1"}, "-i"); v != "wlan1" {
		t.Errorf("separate arg: %q", v)
	}
}

func dhcpRequestPacket(typ byte, mac net.HardwareAddr, opts ...[]byte) []byte {
	b := make([]byte, 240)
	b[0], b[1], b[2] = 1, 1, 6
	copy(b[4:8], []byte{1, 2, 3, 4})
	copy(b[28:], mac)
	copy(b[236:], dhcpMagic)
	b = append(b, optMsgType, 1, typ)
	for _, o := range opts {
		b = append(b, o...)
	}
	return append(b, optEnd)
}

func replyOpts(t *testing.T, b []byte) (net.IP, map[byte][]byte) {
	t.Helper()
	if len(b) < 300 || b[0] != 2 {
		t.Fatalf("bad reply len %d", len(b))
	}
	opts := map[byte][]byte{}
	for o := b[240:]; len(o) > 1 && o[0] != optEnd; o = o[2+int(o[1]):] {
		opts[o[0]] = o[2 : 2+int(o[1])]
	}
	return net.IP(b[16:20]), opts
}

func TestDHCPFlow(t *testing.T) {
	ip := net.IPv4(10, 42, 0, 1)
	s := newDHCPServer("wlan0", ip, "http://10.42.0.1/", slog.New(slog.NewTextHandler(io.Discard, nil)))
	mac := net.HardwareAddr{2, 0, 0, 0, 0, 9}

	offer, to := s.handle(dhcpRequestPacket(dhcpDiscover, mac))
	yi, opts := replyOpts(t, offer)
	if !yi.Equal(net.IPv4(10, 42, 0, 10)) || opts[optMsgType][0] != dhcpOffer {
		t.Fatalf("offer %v %v", yi, opts[optMsgType])
	}
	if to.String() != "255.255.255.255:68" {
		t.Errorf("offer sent to %s", to)
	}
	if !net.IP(opts[optRouter]).Equal(ip) || !net.IP(opts[optDNS]).Equal(ip) || string(opts[optCaptive]) != "http://10.42.0.1/" {
		t.Errorf("options %v", opts)
	}
	if binary.BigEndian.Uint32(opts[optLease]) != 3600 {
		t.Errorf("lease %v", opts[optLease])
	}

	ack, _ := s.handle(dhcpRequestPacket(dhcpRequest, mac, []byte{optReqIP, 4, 10, 42, 0, 10}, []byte{optServerID, 4, 10, 42, 0, 1}))
	yi, opts = replyOpts(t, ack)
	if opts[optMsgType][0] != dhcpAck || !yi.Equal(net.IPv4(10, 42, 0, 10)) {
		t.Fatalf("ack %v %v", yi, opts[optMsgType])
	}
	nak, _ := s.handle(dhcpRequestPacket(dhcpRequest, mac, []byte{optReqIP, 4, 10, 42, 0, 77}))
	if _, opts = replyOpts(t, nak); opts[optMsgType][0] != dhcpNak {
		t.Errorf("wrong address not NAKed")
	}
	if r, _ := s.handle(dhcpRequestPacket(dhcpRequest, mac, []byte{optServerID, 4, 192, 0, 2, 1})); r != nil {
		t.Errorf("answered a request for another server")
	}
	other, _ := s.handle(dhcpRequestPacket(dhcpDiscover, net.HardwareAddr{2, 0, 0, 0, 0, 8}))
	if yi, _ = replyOpts(t, other); !yi.Equal(net.IPv4(10, 42, 0, 11)) {
		t.Errorf("second client got %v", yi)
	}
}

func TestDNSAnswer(t *testing.T) {
	q := []byte{0xab, 0xcd, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0,
		7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	r := dnsAnswer(q, net.IPv4(10, 42, 0, 1).To4())
	if r == nil || r[0] != 0xab || r[2]&0x80 == 0 || r[2]&1 == 0 || binary.BigEndian.Uint16(r[6:8]) != 1 {
		t.Fatalf("reply %x", r)
	}
	if !net.IP(r[len(r)-4:]).Equal(net.IPv4(10, 42, 0, 1)) {
		t.Errorf("answer %v", r[len(r)-4:])
	}
	aaaa := append([]byte(nil), q...)
	aaaa[len(aaaa)-3] = 28
	if r := dnsAnswer(aaaa, net.IPv4(10, 42, 0, 1).To4()); r == nil || binary.BigEndian.Uint16(r[6:8]) != 0 {
		t.Errorf("AAAA should be an empty answer: %x", r)
	}
	if dnsAnswer(q[:10], nil) != nil {
		t.Error("short query answered")
	}
}

func TestHostapdConf(t *testing.T) {
	c := hostapdConf("wlan0", "yastation-x", "", 6)
	if !strings.Contains(c, "ssid=yastation-x\n") || strings.Contains(c, "wpa=") {
		t.Errorf("open conf:\n%s", c)
	}
	if c := hostapdConf("wlan0", "x", "password1", 11); !strings.Contains(c, "wpa_passphrase=password1") || !strings.Contains(c, "channel=11") {
		t.Errorf("psk conf:\n%s", c)
	}
}

func TestUntouchedNetworksKeepTheirBytes(t *testing.T) {
	orig := "ctrl_interface=/var/run/wpa_supplicant\nupdate_config=1\n\nnetwork={\n    ssid=\"a\"\n    psk=\"aaaaaaaa\"\n}\n\nnetwork={\n\tssid=\"b\"\n\tkey_mgmt=NONE\n}\n"
	c, err := ParseConf([]byte(orig))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(c.Format()); got != orig {
		t.Fatalf("unchanged file rewritten:\n%q\n%q", got, orig)
	}
	n, _ := NewNetwork("c", "", false, 0)
	c.Networks = append(c.Networks, n)
	c.Networks[1].Set("priority", "2")
	got := string(c.Format())
	if !strings.Contains(got, "network={\n    ssid=\"a\"\n    psk=\"aaaaaaaa\"\n}") {
		t.Errorf("untouched block changed:\n%s", got)
	}
	if !strings.Contains(got, "\tssid=\"b\"\n\tkey_mgmt=NONE\n\tpriority=2\n") {
		t.Errorf("edited block wrong:\n%s", got)
	}
}
