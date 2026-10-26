package wifisetup

import (
	"errors"
	"os"
	"os/exec"
	"strings"
)

// The saved networks seen from a running speaker (the web UI, docs/web-ui.md): listed without
// their passwords, and added to — never changed or removed — while the client connection is up.
// The file is written, wpa_supplicant is not asked to reload it: a new network is used at the
// next reconnect or reboot, so the connection in use is never touched. Changing and removing
// networks stays in setup mode, where losing the client connection is expected.

// NetView is a saved network without its secret.
type NetView = netView

// clientConf is the running wpa_supplicant's config file (or the configured one).
func (m *Mode) clientConf() (string, error) {
	if m.o.WPAConf != "" {
		return m.o.WPAConf, nil
	}
	wpa, ok := findWPA("/proc")
	if !ok || wpa.conf == "" {
		return "", errors.New("no running wpa_supplicant")
	}
	return wpa.conf, nil
}

// SavedNetworks lists the saved networks and the SSID connected now ("" if none or unknown).
func (m *Mode) SavedNetworks() (nets []NetView, current string, err error) {
	path, err := m.clientConf()
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	c, err := ParseConf(data)
	if err != nil {
		return nil, "", err
	}
	for i, n := range c.Networks {
		nets = append(nets, netView{ID: i, SSID: n.SSID(), Security: n.Security(), Priority: n.Priority(),
			Disabled: n.Disabled(), Hidden: n.Hidden()})
	}
	if out, err := exec.Command("wpa_cli", "status").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(l, "ssid="); ok {
				current = v
			}
		}
	}
	return nets, current, nil
}

// ErrNetworkExists: adding never replaces a saved network.
var ErrNetworkExists = errors.New("a network with this name is already saved (change it in setup mode)")

// AddNetwork adds a network to the running client's config (the old file kept as .before-web).
func (m *Mode) AddNetwork(ssid, psk string, hidden bool, priority int) error {
	path, err := m.clientConf()
	if err != nil {
		return err
	}
	confMu.Lock()
	defer confMu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c, err := ParseConf(data)
	if err != nil {
		return err
	}
	if c.Index(ssid) >= 0 {
		return ErrNetworkExists
	}
	n, err := NewNetwork(ssid, psk, hidden, priority)
	if err != nil {
		return err
	}
	c.Networks = append(c.Networks, n)
	if err := WriteConfAtomic(path+".before-web", data); err != nil {
		return err
	}
	if err := WriteConfAtomic(path, c.Format()); err != nil {
		return err
	}
	m.o.Log.Info("wifi: network added from the web UI", "open", psk == "", "hidden", hidden)
	return nil
}
