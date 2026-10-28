package app

import (
	"os"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/health"
)

// Lifecycle and status indication (docs/status-indication.md):
//
//	booting     from the start until the first connection: blue pulse, the sleep clip, no clock
//	bootFailed  no first connection within boot_connect_timeout: red pulse, the sleep clip
//	ready       the first connection made: eyes -> wink once per device boot, then the base layer
//	            (the clock shows only a blinking colon until the time is synced)
//	degraded    something went away after ready: a dim red pulse on the ring; a voice attempt
//	            without HA gets a bright red pulse and the stock error phrase
//
// "Connected" = the MQTT broker is up (when one is configured) and HA holds the Wyoming
// satellite connection (when the satellite is on): the speaker is usable from HA and by voice.

type life int

const (
	lifeBooting life = iota
	lifeBootFailed
	lifeReady
)

const (
	statusTick = 500 * time.Millisecond
	// Something missing this long after ready counts as degraded: HA reconnects within ~3 s after
	// a drop of ours, and the probe state may lag one probe interval behind a reconnect.
	degradeGrace = 10 * time.Second
	lifeSlot     = "life"
)

// connected: the broker (if any) and HA's satellite connection (if the satellite is on).
func (a *App) connected() bool {
	if a.mqtt != nil && !a.hmon.BrokerConnected() {
		return false
	}
	if a.cfg().WyomingPort > 0 {
		a.sat.mu.Lock()
		up := a.sat.main != nil
		a.sat.mu.Unlock()
		if !up {
			return false
		}
	}
	return true
}

// statusLoop runs the lifecycle and keeps the status layers in step with it.
func (a *App) statusLoop(stop <-chan struct{}) {
	t := time.NewTicker(statusTick)
	defer t.Stop()
	state := lifeBooting
	start := time.Now()
	a.showLife(state)
	var badSince time.Time
	degraded := false
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			up := a.connected()
			switch state {
			case lifeBooting, lifeBootFailed:
				if up {
					state = lifeReady
					a.log.Info("status: connected", "after", now.Sub(start).Round(100*time.Millisecond))
					a.showLife(state)
					a.hmon.Recheck(2 * time.Second) // its last probe may predate the broker
					continue
				}
				if state == lifeBooting && now.Sub(start) >= a.cfg().BootConnectTimeout {
					state = lifeBootFailed
					a.log.Warn("status: no connection to HA yet", "after", a.cfg().BootConnectTimeout)
					a.showLife(state)
				}
			case lifeReady:
				if up && a.hmon.State() == health.Online {
					badSince = time.Time{}
				} else if badSince.IsZero() {
					badSince = now
				}
				d := !badSince.IsZero() && now.Sub(badSince) >= degradeGrace
				if d != degraded {
					degraded = d
					a.showDegraded(d)
				}
			}
		}
	}
}

// showLife fires the boot phase's moment (booting, boot_failed: held until ready).
func (a *App) showLife(s life) {
	a.lifeState.Store(int32(s))
	switch s {
	case lifeBooting:
		a.hook("booting")
	case lifeBootFailed:
		a.hook("boot_failed")
	case lifeReady:
		a.endShow(lifeSlot)
		a.greetOncePerBoot()
	}
}

// showDegraded fires ha_lost (held; by default a dim red pulse) while something is missing after
// ready, and ha_back when it is back.
func (a *App) showDegraded(on bool) {
	if on {
		a.log.Warn("status: degraded", "health", a.hmon.State().String(), "ha_link", a.connected())
		a.hook("ha_lost")
	} else {
		a.log.Info("status: back to normal")
		a.endHook("ha_lost")
		a.hook("ha_back")
	}
}

// bootMarker plays the greeting once per device boot, not on every daemon restart. The stock
// "boot" clip is not used: it is timed to a boot jingle and runs ~10 s.
const bootMarker = "/tmp/yastationd.boot-shown"

func (a *App) greetOncePerBoot() {
	if a.firstThisBoot(bootMarker) {
		a.moment(lifeSlot, "startup", 0)
	}
}

// firstThisBoot reports whether marker (a file under /tmp, which survives daemon respawns but not
// a reboot) was missing, and creates it: true once per device boot.
func (a *App) firstThisBoot(marker string) bool {
	p := a.cfg().Path(marker)
	if _, err := os.Stat(p); err == nil {
		return false
	}
	os.WriteFile(p, nil, 0o644)
	return true
}

// voiceUnavailable: a voice attempt with no HA to talk to (by default a bright red pulse and the
// stock error phrase).
func (a *App) voiceUnavailable() {
	a.hook("voice_unavailable")
}

// rtcSaneAfter: a clock before this is an RTC that lost its time (a pcf8563 that lost power
// boots the system at its reset date, or the kernel leaves 1970).
var rtcSaneAfter = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// rtcTimeTrusted: the board's battery-backed RTC set the clock at boot and it looks real.
func (a *App) rtcTimeTrusted(now time.Time) bool {
	return a.prof.Platform.RTCBacked && now.After(rtcSaneAfter)
}
