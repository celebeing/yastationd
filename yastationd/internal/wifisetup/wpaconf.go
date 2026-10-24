// Package wifisetup is the Wi-Fi setup mode: the speaker stops its Wi-Fi client, becomes an
// access point and serves a page for editing the networks saved in wpa_supplicant.conf
// (docs/wifi-setup.md).
package wifisetup

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Conf is a wpa_supplicant.conf: the lines outside network blocks (globals, comments) kept
// verbatim, and the network blocks with their fields in file order. Fields we don't know are
// written back unchanged, so a network the page doesn't touch stays as it was.
type Conf struct {
	Head     []string
	Networks []*Network
}

// Network is one network={...} block. A field with key "#" is a comment line. A block read from
// the file and not changed since is written back byte for byte (raw).
type Network struct {
	Fields []Field
	raw    []string
}

type Field struct {
	Key, Value string // Value raw, as in the file (quotes included)
}

func ParseConf(data []byte) (*Conf, error) {
	c := &Conf{}
	var cur *Network
	for i, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if cur == nil {
			if strings.HasPrefix(t, "network={") {
				cur = &Network{raw: []string{strings.TrimRight(line, "\r")}}
				continue
			}
			c.Head = append(c.Head, strings.TrimRight(line, "\r"))
			continue
		}
		cur.raw = append(cur.raw, strings.TrimRight(line, "\r"))
		switch {
		case t == "}":
			c.Networks = append(c.Networks, cur)
			cur = nil
		case t == "":
		case strings.HasPrefix(t, "#"):
			cur.Fields = append(cur.Fields, Field{"#", t})
		default:
			k, v, ok := strings.Cut(t, "=")
			if !ok {
				return nil, fmt.Errorf("line %d: expected key=value in a network block", i+1)
			}
			cur.Fields = append(cur.Fields, Field{strings.TrimSpace(k), strings.TrimSpace(v)})
		}
	}
	if cur != nil {
		return nil, errors.New("unterminated network block")
	}
	for len(c.Head) > 0 && strings.TrimSpace(c.Head[len(c.Head)-1]) == "" {
		c.Head = c.Head[:len(c.Head)-1]
	}
	return c, nil
}

func (c *Conf) Format() []byte {
	var b strings.Builder
	for _, l := range c.Head {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, n := range c.Networks {
		if n.raw != nil {
			b.WriteString("\n" + strings.Join(n.raw, "\n") + "\n")
			continue
		}
		b.WriteString("\nnetwork={\n")
		for _, f := range n.Fields {
			if f.Key == "#" {
				b.WriteString("\t" + f.Value + "\n")
				continue
			}
			b.WriteString("\t" + f.Key + "=" + f.Value + "\n")
		}
		b.WriteString("}\n")
	}
	return []byte(b.String())
}

func (n *Network) Get(k string) (string, bool) {
	for _, f := range n.Fields {
		if f.Key == k {
			return f.Value, true
		}
	}
	return "", false
}

func (n *Network) Set(k, v string) {
	n.raw = nil
	for i, f := range n.Fields {
		if f.Key == k {
			n.Fields[i].Value = v
			return
		}
	}
	n.Fields = append(n.Fields, Field{k, v})
}

func (n *Network) Del(k string) {
	n.raw = nil
	out := n.Fields[:0]
	for _, f := range n.Fields {
		if f.Key != k {
			out = append(out, f)
		}
	}
	n.Fields = out
}

// SSID decodes the ssid field: "quoted", P"printf-escaped" or hex.
func (n *Network) SSID() string {
	v, _ := n.Get("ssid")
	s, err := decodeString(v)
	if err != nil {
		return v
	}
	return s
}

// Security is a short label: "open", "psk", or the key_mgmt value (EAP and the like).
func (n *Network) Security() string {
	km, ok := n.Get("key_mgmt")
	_, psk := n.Get("psk")
	_, sae := n.Get("sae_password")
	switch {
	case psk || sae:
		return "psk"
	case ok && km == "NONE":
		if _, wep := n.Get("wep_key0"); wep {
			return "wep"
		}
		return "open"
	case ok:
		return km
	}
	return "psk"
}

func (n *Network) Priority() int {
	v, _ := n.Get("priority")
	p, _ := strconv.Atoi(v)
	return p
}

func (n *Network) Disabled() bool {
	v, _ := n.Get("disabled")
	return v == "1"
}

func (n *Network) Hidden() bool {
	v, _ := n.Get("scan_ssid")
	return v == "1"
}

// Index returns the position of the network named ssid, or -1.
func (c *Conf) Index(ssid string) int {
	for i, n := range c.Networks {
		if n.SSID() == ssid {
			return i
		}
	}
	return -1
}

// SetPSK makes n a WPA-PSK network with passphrase psk, or an open one for psk "".
func (n *Network) SetPSK(psk string) error {
	if psk == "" {
		n.Del("psk")
		n.Del("sae_password")
		n.Set("key_mgmt", "NONE")
		return nil
	}
	v, err := encodePSK(psk)
	if err != nil {
		return err
	}
	n.Del("sae_password")
	n.Set("psk", v)
	if km, ok := n.Get("key_mgmt"); !ok || km == "NONE" {
		n.Set("key_mgmt", "WPA-PSK")
	}
	return nil
}

// NewNetwork builds a block for ssid; psk "" = open, hidden = probe for it by name.
func NewNetwork(ssid, psk string, hidden bool, priority int) (*Network, error) {
	if l := len(ssid); l == 0 || l > 32 {
		return nil, errors.New("SSID must be 1 to 32 bytes")
	}
	n := &Network{}
	n.Set("ssid", encodeSSID(ssid))
	if hidden {
		n.Set("scan_ssid", "1")
	}
	if err := n.SetPSK(psk); err != nil {
		return nil, err
	}
	if priority != 0 {
		n.Set("priority", strconv.Itoa(priority))
	}
	return n, nil
}

// encodeSSID quotes printable ASCII and hex-encodes anything else (wpa_supplicant's own choice).
func encodeSSID(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return hex.EncodeToString([]byte(s))
		}
	}
	return `"` + s + `"`
}

// encodePSK: an 8..63 character passphrase is quoted (wpa_supplicant takes everything between the
// first and the last quote, so quotes and # inside are fine); 64 hex digits are a raw key.
func encodePSK(p string) (string, error) {
	if len(p) == 64 {
		if _, err := hex.DecodeString(p); err == nil {
			return strings.ToLower(p), nil
		}
	}
	if len(p) < 8 || len(p) > 63 {
		return "", errors.New("password must be 8 to 63 characters (or 64 hex digits)")
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] > 0x7e {
			return "", errors.New("password must be printable ASCII")
		}
	}
	return `"` + p + `"`, nil
}

func decodeString(v string) (string, error) {
	switch {
	case strings.HasPrefix(v, `"`):
		end := strings.LastIndexByte(v, '"')
		if end < 1 {
			return "", errors.New("unterminated string")
		}
		return v[1:end], nil
	case strings.HasPrefix(v, `P"`):
		end := strings.LastIndexByte(v, '"')
		if end < 2 {
			return "", errors.New("unterminated string")
		}
		return unescape(v[2:end]), nil
	}
	b, err := hex.DecodeString(v)
	return string(b), err
}

// unescape undoes wpa_supplicant's printf_encode (\\ \" \e \n \r \t \xNN).
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'e':
			b.WriteByte(0x1b)
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 2
					continue
				}
			}
			b.WriteByte('x')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// WriteConfAtomic replaces path with data: a temp file in the same directory, fsync, rename, so a
// power cut leaves either the old or the new file, never a torn one.
func WriteConfAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".wpa_supplicant.conf.")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
