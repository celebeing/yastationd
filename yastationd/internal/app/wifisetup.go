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
	c := a.cfg
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

// setupKey watches the voice key: held down with the mic muted, it arms the setup toggle;
// released (or the mic unmuted) before setupHold, it disarms it. It never swallows the key: a
// voice press while muted only shows the mute indication anyway.
func (a *App) setupKey(action string, down bool) bool {
	if a.setup == nil || action != "voice" {
		return false
	}
	k := &a.combo
	k.mu.Lock()
	defer k.mu.Unlock()
	if !down || !a.micMuted() {
		if k.timer != nil {
			k.timer.Stop()
			k.timer = nil
		}
		return false
	}
	if k.timer != nil {
		return false
	}
	a.log.Info("wifi setup: voice key held while muted", "hold", setupHold.String())
	k.timer = time.AfterFunc(setupHold, func() {
		k.mu.Lock()
		armed := k.timer != nil
		k.timer = nil
		k.mu.Unlock()
		if armed && a.micMuted() {
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
		a.express("voice", "error", errorTTL)
	}
}

// showSetup puts the setup-mode indication on, or takes it off.
func (a *App) showSetup(on bool) {
	if !on {
		a.ringStack.Remove("wifisetup")
		a.matrixStack.Remove("wifisetup")
		return
	}
	a.ringStack.Push("wifisetup", display.PrioSetup, 0, display.Comet(display.Yellow, 1200*time.Millisecond))
	switch c, ok := a.frontAnim("setup"); {
	case ok:
		a.matrixStack.Push("wifisetup", display.PrioSetup, 0, c.Play(true, false, a.brightness))
	case a.front != nil:
		a.matrixStack.Push("wifisetup", display.PrioSetup, 0,
			func(time.Time, display.FrontFrame) (display.FrontFrame, bool) {
				return a.front.SetupMode(a.brightness()), false
			})
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
