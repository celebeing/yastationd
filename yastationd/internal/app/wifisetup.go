package app

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
)

// Wi-Fi setup mode from the keys (docs/wifi-setup.md): with the mic muted, the voice key held for
// setupHold enters it, and the same again leaves it. The same on every board — orion has no
// volume keys, and its mute is a switch that cannot be held. While it lasts, the ring runs a yellow
// comet and the front display shows wide-open eyes with a dot ("O  O°"), above everything else.

const setupHold = 5 * time.Second

// keyCombo tracks the hold of the voice key while muted.
type keyCombo struct {
	mu    sync.Mutex
	timer *time.Timer // armed while the voice key is held with the mic muted
	busy  atomic.Bool // entering or leaving
}

func (a *App) newWifiSetup() *wifisetup.Mode {
	c := a.cfg()
	if !c.WifiSetup {
		return nil
	}
	ssid := c.WifiSetupSSID
	if ssid == "" {
		ssid = "yastation-" + c.DeviceID
	}
	wpaConf := ""
	if c.WPAConf != "" {
		wpaConf = c.Path(c.WPAConf)
	}
	return wifisetup.New(wifisetup.Options{
		HostapdBin: c.Path(c.HostapdBin),
		SSID:       ssid,
		PSK:        c.WifiSetupPSK,
		Channel:    c.WifiSetupChannel,
		Address:    c.WifiSetupAddress,
		WPAConf:    wpaConf,
		DeviceName: c.DeviceName,
		Log:        a.log,
		OnExit:     func() { a.showSetup(false) },
		ConfFile:   c.File,
		SharedHTTP: c.HTTPAddr != "",
		Restart: func() {
			if a.quit != nil {
				a.quit() // Run returns, the daemon exits 0 and the supervisor starts it again in 1 s
			}
		},
		Reboot: func() {
			a.log.Info("wifi setup: rebooting on request from the page")
			exec.Command("reboot").Run()
		},
	})
}

// setupKey watches the voice key: pressed with the mic muted, it arms the setup toggle; released
// before setupHold, it disarms it. Only the press needs the mute: orion's mute flips back by
// itself when it is held (on 2 s later, off; on-device 2026-10-26), and people hold it through
// the combo. It never swallows the key: a voice press while muted only shows the mute indication.
func (a *App) setupKey(action string, down bool) bool {
	if a.setup == nil || action != "voice" {
		return false
	}
	k := &a.combo
	k.mu.Lock()
	defer k.mu.Unlock()
	if !down {
		if k.timer != nil {
			k.timer.Stop()
			k.timer = nil
			a.ringStack.Remove("setuphold")
			a.log.Info("wifi setup: hold cancelled: the voice key was released")
		}
		return false
	}
	if k.timer != nil || !a.micMuted() {
		return false
	}
	a.log.Info("wifi setup: voice key held while muted", "hold", setupHold.String())
	// Show the hold: the ring fills with yellow over setupHold, so the user knows to keep holding.
	start := time.Now()
	a.ringStack.Push("setuphold", display.PrioSetup, setupHold+time.Second,
		func(now time.Time, below display.RingFrame) (display.RingFrame, bool) {
			f := make(display.RingFrame, len(below))
			lit := int(float64(len(f)) * float64(now.Sub(start)) / float64(setupHold))
			for i := 0; i < len(f) && i < lit; i++ {
				f[i] = display.Yellow
			}
			return f, false
		})
	k.timer = time.AfterFunc(setupHold, func() {
		k.mu.Lock()
		armed := k.timer != nil
		k.timer = nil
		k.mu.Unlock()
		a.ringStack.Remove("setuphold")
		if armed {
			a.log.Info("wifi setup: hold complete")
			a.toggleSetup()
		}
	})
	return false
}

func (a *App) toggleSetup() {
	if !a.combo.busy.CompareAndSwap(false, true) {
		return
	}
	defer a.combo.busy.Store(false)
	if a.setup.Active() {
		a.log.Info("wifi setup: leaving")
		a.setup.Leave()
		return
	}
	if a.ic != nil {
		a.ic.m.Hangup("wifi setup")
	}
	a.showSetup(true)
	if err := a.setup.Enter(); err != nil {
		a.log.Error("wifi setup: could not start", "err", err)
		a.showSetup(false)
		a.hook("wifi_setup_failed")
	}
}

// showSetup fires wifi_setup (held while setup mode lasts), or ends it.
func (a *App) showSetup(on bool) {
	if on {
		a.hook("wifi_setup")
	} else {
		a.endHook("wifi_setup")
	}
}

// setupSignalLoop: SIGUSR1 toggles setup mode like the key combo, for testing over SSH
// (`kill -USR1 <yastationd pid>`; the SSH session drops with the Wi-Fi client).
func (a *App) setupSignalLoop(stop <-chan struct{}) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	defer signal.Stop(ch)
	for {
		select {
		case <-stop:
			return
		case <-ch:
			a.log.Info("wifi setup: SIGUSR1")
			go a.toggleSetup()
		}
	}
}
