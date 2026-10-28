package wifisetup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Setup mode, step by step (docs/wifi-setup.md):
//   enter: scan with the running wpa_supplicant, remember its command line, stop it, start our
//          hostapd (the firmware's wpa_supplicant has no AP mode), put the speaker's address on the
//          interface, serve DHCP, DNS and the page;
//   leave: stop all that, start wpa_supplicant again with the remembered command line, ask
//          dhcpcd to rebind.
// Nothing about the access point is ever written to persistent storage: the state lives in
// runDir (/tmp), so a reboot always comes back as a plain Wi-Fi client. Only the page's edits to
// wpa_supplicant.conf persist.

// Options configure the setup mode (config keys wifi_setup_*, docs/wifi-setup.md).
type Options struct {
	HostapdBin string
	SSID       string
	PSK        string // "" = open network
	Channel    int
	Address    string // the speaker's address and prefix on the access point, e.g. 10.42.0.1/24
	WPAConf    string // "" = from the running wpa_supplicant's -c
	RunDir     string
	DeviceName string
	Log        *slog.Logger
	// OnExit is called after setup mode ended for any reason (the page, a failure, Leave).
	OnExit func()
	// Reboot restarts the speaker (the page's "reboot" button).
	Reboot func()
	// ConfFile is yastationd.conf for the home settings ("" = no settings section); Restart is
	// called after leaving setup mode when they were changed, so the daemon loads them.
	ConfFile string
	Restart  func()
	// SharedHTTP: the daemon's own HTTP server already listens on port 80 on every address; the
	// page is then served through it (Portal) instead of a listener of its own.
	SharedHTTP bool
}

// Mode is the Wi-Fi setup mode: off until Enter, then an access point with the setup page.
type Mode struct {
	o Options

	mu      sync.Mutex
	active  bool
	cancel  context.CancelFunc
	wpa     wpaProc
	iface   string
	conf    string
	hostapd *exec.Cmd
	scanned []ScanResult
	exited  chan struct{} // closed when hostapd exits

	settingsChanged bool // under confMu

	portal atomic.Pointer[portalRoute] // the page while active (SharedHTTP)
}

type portalRoute struct {
	ip net.IP
	h  http.Handler
}

// Portal is the setup page's handler and the access point's address while setup mode is on
// (nil otherwise): the daemon's server hands it the requests that arrive on that address.
func (m *Mode) Portal() (http.Handler, net.IP) {
	if p := m.portal.Load(); p != nil {
		return p.h, p.ip
	}
	return nil, nil
}

// New makes the (inactive) setup mode, filling in defaults for an empty RunDir, Channel and
// Address.
func New(o Options) *Mode {
	if o.RunDir == "" {
		o.RunDir = "/tmp/yastationd-wifisetup"
	}
	if o.Channel == 0 {
		o.Channel = 6
	}
	if o.Address == "" {
		o.Address = "10.42.0.1/24"
	}
	return &Mode{o: o}
}

// Active reports whether setup mode is on.
func (m *Mode) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

func (m *Mode) markerPath() string { return filepath.Join(m.o.RunDir, "wpa_supplicant.argv") }
func (m *Mode) pidPath() string    { return filepath.Join(m.o.RunDir, "hostapd.pid") }

// Enter starts setup mode. On any failure the Wi-Fi client is restored before it returns.
func (m *Mode) Enter() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return nil
	}
	log := m.o.Log
	ip, _, err := net.ParseCIDR(m.o.Address)
	if err != nil || ip.To4() == nil {
		return fmt.Errorf("setup address %q: want an IPv4 CIDR", m.o.Address)
	}
	if _, err := os.Stat(m.o.HostapdBin); err != nil {
		return fmt.Errorf("hostapd: %w", err)
	}
	wpa, ok := findWPA("/proc")
	if !ok || wpa.iface == "" {
		return errors.New("no running wpa_supplicant to take the interface from")
	}
	m.wpa, m.iface = wpa, wpa.iface
	m.conf = m.o.WPAConf
	if m.conf == "" {
		m.conf = wpa.conf
	}
	if err := os.MkdirAll(m.o.RunDir, 0o700); err != nil {
		return err
	}
	// A new session keeps its own before-setup copies.
	os.Remove(m.markerPath() + ".backed-up")
	os.Remove(m.markerPath() + ".settings-backed-up")

	log.Info("wifi setup: scanning before the switch", "iface", m.iface)
	m.scanned = scan(m.iface, 10*time.Second) // a full 2.4 + 5 GHz scan takes ~7 s
	log.Info("wifi setup: scan done", "networks", len(m.scanned))

	if err := os.WriteFile(m.markerPath(), []byte(strings.Join(wpa.argv, "\x00")), 0o600); err != nil {
		return err
	}
	if err := stopPid(wpa.pid, 5*time.Second); err != nil {
		m.restoreWPA()
		return fmt.Errorf("stop wpa_supplicant: %w", err)
	}
	if err := m.startHostapd(); err != nil {
		m.restoreWPA()
		return err
	}
	run("ip", "addr", "add", m.o.Address, "dev", m.iface)

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel, m.active = cancel, true
	portal := "http://" + ip.String() + "/"
	go m.keep(ctx, "dhcp", newDHCPServer(m.iface, ip, portal, log).serve)
	go m.keep(ctx, "dns", func(ctx context.Context) error { return serveDNS(ctx, ip) })
	go m.keep(ctx, "http", func(ctx context.Context) error { return m.servePortal(ctx, ip) })
	go m.watchHostapd(ctx)
	log.Info("wifi setup: access point up", "ssid", m.o.SSID, "open", m.o.PSK == "",
		"address", m.o.Address, "conf", m.conf)
	return nil
}

// Leave ends setup mode and gives the interface back to wpa_supplicant.
func (m *Mode) Leave() {
	m.mu.Lock()
	if !m.active {
		m.mu.Unlock()
		return
	}
	m.active = false
	m.cancel()
	m.stopHostapd()
	run("ip", "addr", "del", m.o.Address, "dev", m.iface)
	m.restoreWPA()
	m.mu.Unlock()
	m.o.Log.Info("wifi setup: left, Wi-Fi client restored")
	if m.o.OnExit != nil {
		m.o.OnExit()
	}
	confMu.Lock()
	changed := m.settingsChanged
	m.settingsChanged = false
	confMu.Unlock()
	if changed && m.o.Restart != nil {
		m.o.Log.Info("wifi setup: settings changed, restarting to load them")
		m.o.Restart()
	}
}

// Recover cleans up after a daemon that died in setup mode: our hostapd still holding the
// interface, wpa_supplicant not running. Call once at startup.
func (m *Mode) Recover() {
	argv, err := os.ReadFile(m.markerPath())
	stray := m.findHostapd("/proc")
	if err != nil && len(stray) == 0 {
		return
	}
	m.o.Log.Warn("wifi setup: the previous run ended in setup mode, restoring the Wi-Fi client",
		"stray_hostapd", len(stray))
	for _, pid := range stray {
		stopPid(pid, 3*time.Second)
	}
	os.Remove(m.pidPath())
	if err != nil {
		return
	}
	m.wpa = wpaProc{argv: strings.Split(string(argv), "\x00")}
	m.iface = optArg(m.wpa.argv, "-i")
	if m.iface != "" {
		run("ip", "addr", "del", m.o.Address, "dev", m.iface)
	}
	if _, running := findWPA("/proc"); running {
		os.Remove(m.markerPath())
		return
	}
	m.restoreWPA()
}

// restoreWPA starts wpa_supplicant with the remembered command line (it daemonizes itself, -B)
// and nudges dhcpcd. Called with the interface free.
func (m *Mode) restoreWPA() {
	log := m.o.Log
	if len(m.wpa.argv) == 0 {
		return
	}
	cmd := exec.Command(m.wpa.argv[0], m.wpa.argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Error("wifi setup: restart wpa_supplicant", "err", err, "out", strings.TrimSpace(string(out)))
		return
	}
	os.Remove(m.markerPath())
	if m.iface != "" {
		go func() {
			time.Sleep(10 * time.Second) // association first; dhcpcd also reacts to the carrier on its own
			run("dhcpcd", "-n", m.iface)
		}()
	}
}

func (m *Mode) startHostapd() error {
	conf := filepath.Join(m.o.RunDir, "hostapd.conf")
	if err := os.WriteFile(conf, []byte(hostapdConf(m.iface, m.o.SSID, m.o.PSK, m.o.Channel)), 0o600); err != nil {
		return err
	}
	cmd := exec.Command(m.o.HostapdBin, conf)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start hostapd: %w", err)
	}
	// hostapd writes -P only when it daemonizes; ours runs in the foreground.
	os.WriteFile(m.pidPath(), []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	m.hostapd, m.exited = cmd, make(chan struct{})
	up := make(chan bool, 1)
	go func(exited chan struct{}) {
		sc := bufio.NewScanner(out)
		sent := false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.Contains(line, "AP-ENABLED"):
				if !sent {
					up <- true
					sent = true
				}
			case strings.Contains(line, "AP-STA-"), strings.Contains(line, "AP-DISABLED"):
				m.o.Log.Info("wifi setup: hostapd", "event", strings.TrimSpace(line))
			case strings.Contains(strings.ToLower(line), "fail"), strings.Contains(line, "rror"):
				m.o.Log.Warn("wifi setup: hostapd", "msg", strings.TrimSpace(line))
			}
		}
		cmd.Wait()
		if !sent {
			up <- false
		}
		close(exited)
	}(m.exited)
	select {
	case ok := <-up:
		if ok {
			return nil
		}
		return errors.New("hostapd exited before the access point came up")
	case <-time.After(10 * time.Second):
		m.stopHostapd()
		return errors.New("hostapd: no AP-ENABLED in 10 s")
	}
}

func (m *Mode) stopHostapd() {
	if m.hostapd == nil || m.hostapd.Process == nil {
		return
	}
	m.hostapd.Process.Signal(syscall.SIGTERM)
	select {
	case <-m.exited:
	case <-time.After(3 * time.Second):
		m.hostapd.Process.Kill()
		<-m.exited
	}
	m.hostapd = nil
	os.Remove(m.pidPath())
}

// watchHostapd ends setup mode if hostapd dies under us.
func (m *Mode) watchHostapd(ctx context.Context) {
	m.mu.Lock()
	exited := m.exited
	m.mu.Unlock()
	select {
	case <-ctx.Done():
	case <-exited:
		if ctx.Err() == nil {
			m.o.Log.Error("wifi setup: hostapd exited, leaving setup mode")
			m.Leave()
		}
	}
}

// keep runs one server for the mode's lifetime, restarting it after an error.
func (m *Mode) keep(ctx context.Context, name string, serve func(context.Context) error) {
	for ctx.Err() == nil {
		if err := serve(ctx); err != nil && ctx.Err() == nil {
			m.o.Log.Warn("wifi setup: server failed", "server", name, "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(2 * time.Second):
		}
	}
}

func hostapdConf(iface, ssid, psk string, channel int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "interface=%s\ndriver=nl80211\nssid=%s\nutf8_ssid=1\nhw_mode=g\nchannel=%d\n",
		iface, ssid, channel)
	if psk != "" {
		fmt.Fprintf(&b, "wpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nwpa_passphrase=%s\n", psk)
	}
	return b.String()
}

// stopPid sends SIGTERM and waits for the process to go.
func stopPid(pid int, wait time.Duration) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); {
		if syscall.Kill(pid, 0) != nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(200 * time.Millisecond)
	return nil
}

// findHostapd lists processes running our hostapd binary.
func (m *Mode) findHostapd(procRoot string) []int {
	var pids []int
	ents, _ := os.ReadDir(procRoot)
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err == nil && strings.SplitN(string(b), "\x00", 2)[0] == m.o.HostapdBin {
			pids = append(pids, pid)
		}
	}
	return pids
}

func run(name string, args ...string) {
	exec.Command(name, args...).Run()
}
