package app

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/timesync"
)

// The network watchdog: a speaker whose Wi-Fi stopped passing data (still associated, the
// rates at the floor, no traffic) stays off the LAN until it is power-cycled — seen twice on one
// Mini 3 (yastationd/README.md, item 7). With the default gateway unreachable for
// cfg.NetWatchdog the speaker logs the kernel's last lines, puts the waiting log lines on flash
// and reboots. Each watchdog reboot doubles the next wait (up to netWatchMax) so that a router
// outage does not reboot every speaker every few minutes; an hour of good network resets it.
// The count lives in <cfw>/netwatch.count and is measured in uptime, not wall time: a mini's
// clock is stale after a reboot until the network is back.

const (
	netWatchEvery = 30 * time.Second
	netWatchMax   = 4 * time.Hour
	netWatchReset = time.Hour
	netWatchCount = "netwatch.count"
)

type netWatch struct {
	limit     time.Duration // the outage that triggers a reboot now
	downSince time.Time
	upSince   time.Time
	count     int
}

// netWatchLimit is the outage allowed after n watchdog reboots in a row.
func netWatchLimit(base time.Duration, n int) time.Duration {
	l := base
	for i := 0; i < n && l < netWatchMax; i++ {
		l *= 2
	}
	return min(l, max(netWatchMax, base))
}

func (a *App) netWatchLoop(stop <-chan struct{}) {
	if a.cfg.NetWatchdog <= 0 {
		return
	}
	countFile := a.cfg.Path(a.prof.Platform.CFWRoot + "/" + netWatchCount)
	w := netWatch{}
	if b, err := os.ReadFile(countFile); err == nil {
		w.count, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	}
	w.limit = netWatchLimit(a.cfg.NetWatchdog, w.count)
	t := time.NewTicker(netWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		now := time.Now()
		if a.setup != nil && a.setup.Active() { // the Wi-Fi is an access point on purpose
			w.downSince = time.Time{}
			continue
		}
		if a.networkUp() {
			if !w.downSince.IsZero() {
				a.log.Info("network: gateway reachable again", "after", now.Sub(w.downSince).Round(time.Second))
				w.downSince = time.Time{}
			}
			if w.upSince.IsZero() {
				w.upSince = now
			}
			if w.count > 0 && now.Sub(w.upSince) >= netWatchReset {
				w.count = 0
				w.limit = a.cfg.NetWatchdog
				os.Remove(countFile)
			}
			continue
		}
		w.upSince = time.Time{}
		if w.downSince.IsZero() {
			w.downSince = now
			a.log.Warn("network: gateway unreachable", "reboot_after", w.limit)
			continue
		}
		if now.Sub(w.downSince) < w.limit || uptime() < w.limit {
			continue
		}
		a.log.Warn("network: down too long, rebooting", "down", now.Sub(w.downSince).Round(time.Second), "watchdog_reboots", w.count)
		a.logKernelTail()
		os.WriteFile(countFile, []byte(strconv.Itoa(w.count+1)), 0o644)
		if a.ship != nil {
			a.ship.Flush()
		}
		exec.Command("sync").Run()
		if err := exec.Command("reboot").Run(); err != nil {
			a.log.Warn("network: reboot failed", "err", err)
		}
		return
	}
}

// networkUp: the broker is connected, or the default gateway answers a ping.
func (a *App) networkUp() bool {
	if a.mqtt != nil && a.mqtt.Connected() {
		return true
	}
	gw := timesync.DefaultGateway(a.cfg.Path("/proc/net/route"))
	if gw == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", gw).Run() == nil
}

// logKernelTail puts the kernel's last lines in the log (the Wi-Fi driver's complaints).
func (a *App) logKernelTail() {
	out, err := exec.Command("dmesg").Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for _, l := range lines[max(len(lines)-40, 0):] {
		a.log.Warn("network: dmesg", "line", l)
	}
}

// uptime since boot (the watchdog never reboots a speaker younger than its limit).
func uptime() time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}
