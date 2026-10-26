// Package timesync keeps the system clock right on firmwares with no working NTP daemon (fw 0.6.x
// ships no chronyd, and its vendor loop polls public pools our egress lock blocks). It is a
// minimal SNTP client that steps the clock. When chronyd/ntpd runs, it only checks the daemon's
// work: there's no RTC battery, so the clock boots stale (bergamot: two days behind), and chronyd
// was seen slewing such an offset for ~11 minutes before stepping, with the clock showing the old
// time and no cue. So a daemon's clock counts as synced only once an SNTP query agrees with it
// (a big disagreement is stepped right away), or, with no server answering, when the kernel's
// STA_UNSYNC flag (kept by the daemon) is clear.
//
// Servers are tried in order: the configured ones, those in /etc/chrony.conf (the install
// writes the LAN server there), then the default gateway — home routers often answer NTP.
// DHCP option 42 is deliberately not used: few routers send it and dhcpcd here doesn't ask.
package timesync

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	syncEvery  = 15 * time.Minute
	retryMin   = 30 * time.Second
	retryMax   = 10 * time.Minute
	daemonPoll = 10 * time.Minute
	// Under a daemon, step only for an error this large: it's a stale boot clock, not drift.
	daemonMaxSkew = 5 * time.Second
	queryWait     = 3 * time.Second
	maxSkew       = 200 * time.Millisecond // smaller offsets are left alone (we step, never slew)
)

// ntpEpoch is 1900-01-01 in Unix seconds.
const ntpEpoch = 2208988800

type Syncer struct {
	Servers    []string // configured servers, tried first
	ChronyConf string   // e.g. /etc/chrony.conf; "" = skip
	Root       string   // prefix for /proc paths (tests)
	Log        *slog.Logger
	OnChange   func(synced bool)

	synced       atomic.Bool
	query        func(server string) (time.Duration, error)
	step         func(off time.Duration) error
	kernelSynced func() bool
	verified     bool // under a daemon: a server has confirmed the clock this run
}

// Synced reports whether the clock was set from a server (or a system daemon owns it).
func (s *Syncer) Synced() bool { return s.synced.Load() }

func (s *Syncer) setSynced(v bool) {
	if s.synced.Swap(v) != v && s.OnChange != nil {
		s.OnChange(v)
	}
}

func (s *Syncer) Run(stop <-chan struct{}) {
	if s.query == nil {
		s.query = func(server string) (time.Duration, error) { return Query(server, queryWait) }
	}
	if s.step == nil {
		s.step = stepClock
	}
	if s.kernelSynced == nil {
		s.kernelSynced = kernelClockSynced
	}
	retry := retryMin
	daemonLogged := false
	for {
		var wait time.Duration
		if d := s.timeDaemon(); d != "" {
			if !daemonLogged {
				s.Log.Info("time sync left to the system daemon (checking it)", "daemon", d)
				daemonLogged = true
			}
			wait = s.checkDaemon()
		} else if s.syncOnce() {
			daemonLogged = false
			s.verified = false
			retry, wait = retryMin, syncEvery
		} else {
			wait = retry
			retry = min(retry*2, retryMax)
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// checkDaemon verifies a daemon-owned clock (see the package doc) and returns the next wait.
func (s *Syncer) checkDaemon() time.Duration {
	srv, off, ok := s.queryFirst()
	if !ok {
		synced := s.verified || s.kernelSynced()
		s.setSynced(synced)
		if s.verified {
			return daemonPoll
		}
		return retryMin // not confirmed yet: retry soon (e.g. the network was still coming up)
	}
	if off > daemonMaxSkew || off < -daemonMaxSkew {
		s.Log.Warn("clock far off while the daemon owns it — stepping", "server", srv, "offset", off.Round(time.Millisecond))
		if err := s.step(off); err != nil {
			s.Log.Warn("time sync: set clock", "server", srv, "err", err)
			s.setSynced(false)
			return retryMin
		}
	} else if !s.verified {
		s.Log.Info("clock confirmed", "server", srv, "offset", off.Round(time.Millisecond))
	}
	s.verified = true
	s.setSynced(true)
	return daemonPoll
}

// queryFirst asks the candidates in order; the first answer wins.
func (s *Syncer) queryFirst() (srv string, off time.Duration, ok bool) {
	cands := s.candidates()
	if len(cands) == 0 {
		s.Log.Warn("time sync: no NTP server candidates")
		return "", 0, false
	}
	var errs []string
	for _, c := range cands {
		o, err := s.query(c)
		if err == nil {
			return c, o, true
		}
		errs = append(errs, c+": "+err.Error())
	}
	s.Log.Warn("time sync failed", "tried", strings.Join(errs, "; "))
	return "", 0, false
}

// syncOnce queries the candidates in order and steps the clock from the first that answers.
func (s *Syncer) syncOnce() bool {
	srv, off, ok := s.queryFirst()
	if !ok {
		return false
	}
	if off > maxSkew || off < -maxSkew {
		if err := s.step(off); err != nil {
			s.Log.Warn("time sync: set clock", "server", srv, "err", err)
			return false
		}
		s.Log.Info("clock stepped", "server", srv, "offset", off.Round(time.Millisecond))
	} else if !s.Synced() {
		s.Log.Info("clock in sync", "server", srv, "offset", off.Round(time.Millisecond))
	}
	s.setSynced(true)
	return true
}

func (s *Syncer) candidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, h := range s.Servers {
		add(h)
	}
	if s.ChronyConf != "" {
		for _, h := range chronyServers(s.ChronyConf) {
			add(h)
		}
	}
	add(defaultGateway(filepath.Join(s.Root, "/proc/net/route")))
	return out
}

// timeDaemon returns the name of a running NTP daemon, or "".
func (s *Syncer) timeDaemon() string {
	comms, _ := filepath.Glob(filepath.Join(s.Root, "/proc/[0-9]*/comm"))
	for _, p := range comms {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		switch c := strings.TrimSpace(string(b)); c {
		case "chronyd", "ntpd":
			return c
		}
	}
	return ""
}

// chronyServers lists the hosts of `server`/`pool`/`peer` lines.
func chronyServers(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 && (fs[0] == "server" || fs[0] == "pool" || fs[0] == "peer") {
			out = append(out, fs[1])
		}
	}
	return out
}

// defaultGateway reads the IPv4 default route's gateway from /proc/net/route.
func defaultGateway(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		// Iface Destination Gateway Flags ...; hex fields are little-endian.
		if len(fs) < 4 || fs[1] != "00000000" {
			continue
		}
		flags, err1 := strconv.ParseUint(fs[3], 16, 32)
		gw, err2 := strconv.ParseUint(fs[2], 16, 32)
		if err1 != nil || err2 != nil || flags&0x2 == 0 || gw == 0 {
			continue
		}
		ip := make(net.IP, 4)
		binary.LittleEndian.PutUint32(ip, uint32(gw))
		return ip.String()
	}
	return ""
}

// Query asks server (host or host:port) for the time and returns the local clock's offset.
func Query(server string, timeout time.Duration) (time.Duration, error) {
	if _, _, err := net.SplitHostPort(server); err != nil {
		server = net.JoinHostPort(server, "123")
	}
	conn, err := net.DialTimeout("udp", server, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	req := make([]byte, 48)
	req[0] = 0x23 // LI 0, version 4, mode 3 (client)
	t1 := time.Now().Round(0)
	putTimestamp(req[40:], t1)
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	resp := make([]byte, 128)
	n, err := conn.Read(resp)
	t4 := time.Now().Round(0)
	if err != nil {
		return 0, err
	}
	if n < 48 {
		return 0, fmt.Errorf("short reply (%d bytes)", n)
	}
	switch {
	case resp[0]&0x7 != 4:
		return 0, errors.New("not a server reply")
	case resp[0]>>6 == 3 || resp[1] == 0 || resp[1] > 15:
		return 0, fmt.Errorf("server unsynchronized (stratum %d)", resp[1])
	case string(resp[24:32]) != string(req[40:48]):
		return 0, errors.New("reply does not match the request")
	}
	t2, t3 := timestamp(resp[32:40]), timestamp(resp[40:48])
	return (t2.Sub(t1) + t3.Sub(t4)) / 2, nil
}

func putTimestamp(b []byte, t time.Time) {
	binary.BigEndian.PutUint32(b, uint32(t.Unix()+ntpEpoch))
	binary.BigEndian.PutUint32(b[4:], uint32((uint64(t.Nanosecond())<<32)/1e9))
}

func timestamp(b []byte) time.Time {
	sec := int64(binary.BigEndian.Uint32(b)) - ntpEpoch
	frac := uint64(binary.BigEndian.Uint32(b[4:]))
	return time.Unix(sec, int64((frac*1e9)>>32))
}

// kernelClockSynced: the kernel's view, maintained by an NTP daemon (STA_UNSYNC clear and the
// clock state not TIME_ERROR).
func kernelClockSynced() bool {
	const staUnsync, timeError = 0x0040, 5
	var tx syscall.Timex
	state, err := syscall.Adjtimex(&tx)
	return err == nil && state != timeError && tx.Status&staUnsync == 0
}

func stepClock(off time.Duration) error {
	tv := syscall.NsecToTimeval(time.Now().Add(off).UnixNano())
	return syscall.Settimeofday(&tv)
}

// DefaultGateway is the IPv4 default gateway from a /proc/net/route file ("" = none).
func DefaultGateway(routeFile string) string { return defaultGateway(routeFile) }
