package app

import (
	"os"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
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
	if a.cfg.WyomingPort > 0 {
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
				if state == lifeBooting && now.Sub(start) >= a.cfg.BootConnectTimeout {
					state = lifeBootFailed
					a.log.Warn("status: no connection to HA yet", "after", a.cfg.BootConnectTimeout)
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

// showLife puts the boot-phase layers on the ring and the front display.
func (a *App) showLife(s life) {
	a.lifeState.Store(int32(s))
	switch s {
	case lifeBooting, lifeBootFailed:
		c := display.Blue
		if s == lifeBootFailed {
			c = display.Red
		}
		a.ringStack.Push(lifeSlot, display.PrioDegraded, 0, display.Pulse(c, 2*time.Second))
		if clip, ok := a.frontAnim("booting"); ok {
			a.matrixStack.Push(lifeSlot, display.PrioDegraded, 0, clip.Play(true, false, a.brightness))
			return
		}
		// No animation: a blank front (the time is unknown anyway).
		a.matrixStack.Push(lifeSlot, display.PrioDegraded, 0, func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
			return display.FrontFrame{}, false
		})
	case lifeReady:
		a.unexpress(lifeSlot)
		a.greetOncePerBoot()
	}
}

// showDegraded: a dim red pulse on the ring while something is missing after ready.
func (a *App) showDegraded(on bool) {
	if on {
		a.log.Warn("status: degraded", "health", a.hmon.State().String(), "ha_link", a.connected())
		a.ringStack.Push("degraded", display.PrioDegraded, 0, display.Pulse(display.Red.Scale(40), 3*time.Second))
	} else {
		a.log.Info("status: back to normal")
		a.ringStack.Remove("degraded")
	}
}

// bootMarker plays the greeting once per device boot, not on every daemon restart. The stock
// "boot" clip is not used: it is timed to a boot jingle and runs ~10 s.
const bootMarker = "/tmp/yastationd.boot-shown"

func (a *App) greetOncePerBoot() {
	marker := a.cfg.Path(bootMarker)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	os.WriteFile(marker, nil, 0o644)
	a.moment(lifeSlot, "startup", 0)
}

// voiceUnavailable: a voice attempt with no HA to talk to — a bright red pulse and the stock
// error phrase.
func (a *App) voiceUnavailable() {
	a.ringStack.Push("voice", display.PrioError, 2*time.Second, display.Pulse(display.Red, 700*time.Millisecond))
	snd := a.prof.Audio.ErrorSound
	if snd == "" {
		return
	}
	if _, err := os.Stat(a.cfg.Path(snd)); err != nil {
		return
	}
	a.announce(a.cfg.Path(snd))
}
