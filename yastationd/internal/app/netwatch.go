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

// The network watchdog: a speaker whose Wi-Fi stopped passing data stays off the LAN until
// something resets it — seen on both Mini 3 (the Wi-Fi chip's firmware hangs: "fw recovery not
// finish" in the kernel log, yastationd/README.md item 7). It climbs the ladder the firmware's own
// WifiWatcher climbs (docs/storage.md, "The network watchdog"): with the default gateway
// unreachable for net_wifi_reconfigure_min it has wpa_supplicant reload its configuration; after
// net_wifi_reset_min it resets the Wi-Fi controller with the board's platform.wifi_reset (the
// firmware's yandex-wifireset: driver out, SDIO power cycled, back in), again every
// net_wifi_reset_every_min (net_wifi_recovery = false: neither); after cfg.NetWatchdog it logs the kernel's last lines, puts the waiting log
// lines on flash and reboots. Each watchdog reboot doubles the next wait (up to netWatchMax) so
// that a router outage does not reboot every speaker every few minutes; an hour of good network
// resets it. The count lives in <cfw>/netwatch.count and is measured in uptime, not wall time: a
// mini's clock is stale after a reboot until the network is back.

const (
	netWatchEvery = 30 * time.Second
	netWatchMax   = 4 * time.Hour
	netWatchReset = time.Hour
	netWatchCount = "netwatch.count"

	netWifiCmdTimeout = time.Minute
)

type netAction int

const (
	netNothing         netAction = iota
	netDown                      // just went down: log it
	netReconfigure               // wpa_cli reconfigure
	netWifiResetAction           // reset the Wi-Fi controller
	netReboot
)

type netWatch struct {
	limit        time.Duration // the outage that triggers a reboot now
	downSince    time.Time
	upSince      time.Time
	count        int
	reconfigured bool      // this outage
	resetAt      time.Time // the last controller reset in this outage

	// The recovery rungs (config): 0 = that rung off.
	reconfigureAfter time.Duration
	resetAfter       time.Duration // 0 also without a reset command on the board
	resetEvery       time.Duration
}

// netWatchLimit is the outage allowed after n watchdog reboots in a row.
func netWatchLimit(base time.Duration, n int) time.Duration {
	l := base
	for i := 0; i < n && l < netWatchMax; i++ {
		l *= 2
	}
	return min(l, max(netWatchMax, base))
}

// step takes one observation and says what to do: the ladder, in order, one rung per check.
func (w *netWatch) step(now time.Time, up bool, uptime time.Duration) netAction {
	if up {
		w.downSince, w.reconfigured, w.resetAt = time.Time{}, false, time.Time{}
		if w.upSince.IsZero() {
			w.upSince = now
		}
		return netNothing
	}
	w.upSince = time.Time{}
	if w.downSince.IsZero() {
		w.downSince = now
		return netDown
	}
	down := now.Sub(w.downSince)
	switch {
	case down >= w.limit && uptime >= w.limit:
		return netReboot
	case w.resetAfter > 0 && down >= w.resetAfter &&
		(w.resetAt.IsZero() || (w.resetEvery > 0 && now.Sub(w.resetAt) >= w.resetEvery)):
		w.resetAt = now
		return netWifiResetAction
	case w.reconfigureAfter > 0 && !w.reconfigured && down >= w.reconfigureAfter:
		w.reconfigured = true
		return netReconfigure
	}
	return netNothing
}

func (a *App) netWatchLoop(stop <-chan struct{}) {
	if a.cfg.NetWatchdog <= 0 {
		return
	}
	countFile := a.cfg.Path(a.prof.Platform.CFWRoot + "/" + netWatchCount)
	w := netWatch{}
	if c := a.cfg; c.NetWifiRecovery {
		w.reconfigureAfter, w.resetEvery = c.NetWifiReconfigure, c.NetWifiResetEvery
		if a.prof.Platform.WifiReset != "" {
			w.resetAfter = c.NetWifiReset
		}
	}
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
		wasDown := w.downSince
		up := a.networkUp()
		if up && !wasDown.IsZero() {
			a.log.Info("network: gateway reachable again", "after", now.Sub(wasDown).Round(time.Second))
		}
		if up && w.count > 0 && !w.upSince.IsZero() && now.Sub(w.upSince) >= netWatchReset {
			w.count = 0
			w.limit = a.cfg.NetWatchdog
			os.Remove(countFile)
		}
		switch w.step(now, up, uptime()) {
		case netDown:
			a.log.Warn("network: gateway unreachable", "wifi_reset_after", w.resetAfter, "reboot_after", w.limit)
		case netReconfigure:
			a.log.Warn("network: still down, reloading the Wi-Fi configuration")
			a.netCommand("wpa_cli", "-i", "wlan0", "reconfigure")
		case netWifiResetAction:
			a.log.Warn("network: still down, resetting the Wi-Fi controller", "cmd", a.prof.Platform.WifiReset,
				"down", now.Sub(w.downSince).Round(time.Second))
			a.logKernelTail(15)
			a.netCommand(a.prof.Platform.WifiReset)
		case netReboot:
			a.log.Warn("network: down too long, rebooting", "down", now.Sub(w.downSince).Round(time.Second), "watchdog_reboots", w.count)
			a.logKernelTail(40)
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
}

// netCommand runs a recovery command with a timeout and logs how it went.
func (a *App) netCommand(name string, args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), netWifiCmdTimeout)
	defer cancel()
	start := time.Now()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	a.log.Info("network: recovery command", "cmd", name, "err", err, "took", time.Since(start).Round(time.Millisecond),
		"out", strings.TrimSpace(lastLines(string(out), 3)))
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(l[max(len(l)-n, 0):], " | ")
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

// logKernelTail puts the kernel's last n lines in the log (the Wi-Fi driver's complaints).
func (a *App) logKernelTail(n int) {
	out, err := exec.Command("dmesg").Output()
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for _, l := range lines[max(len(lines)-n, 0):] {
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
