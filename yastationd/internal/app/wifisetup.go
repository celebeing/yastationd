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

// Wi-Fi setup mode from the keys (docs/wifi-setup.md): volume-down + voice + mute held together
// for setupHold enter it, and the same again leaves it. While it lasts, the ring runs a yellow
// comet and the front display shows wide-open eyes with a dot ("O  O°"), above everything else.

const setupHold = 5 * time.Second

var setupCombo = [...]string{"volume_down", "voice", "mute_key"}

// keyCombo tracks the combo's keys across input devices (each device has its own reader).
type keyCombo struct {
	mu    sync.Mutex
	held  map[string]bool
	timer *time.Timer // armed while all keys are held; nil once any is released
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
		Reboot: func() {
			a.log.Info("wifi setup: rebooting on request from the page")
			exec.Command("reboot").Run()
		},
	})
}

// setupKey records a combo key going down or up. It reports true when this press completed the
// combo: the key's own action is then skipped.
func (a *App) setupKey(action string, down bool) bool {
	if a.setup == nil {
		return false
	}
	inCombo := false
	for _, k := range setupCombo {
		inCombo = inCombo || k == action
	}
	if !inCombo {
		return false
	}
	k := &a.combo
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.held == nil {
		k.held = map[string]bool{}
	}
	k.held[action] = down
	for _, n := range setupCombo {
		if !k.held[n] {
			if k.timer != nil {
				k.timer.Stop()
				k.timer = nil
			}
			return false
		}
	}
	if k.timer != nil || !down {
		return down
	}
	a.log.Info("wifi setup: key combo held", "hold", setupHold.String())
	// The voice key in the combo may have started a session: drop it (the clean cancel).
	if a.sat.inDialog() {
		a.sat.dropConn("wifi setup key combo")
		a.unexpress("voice")
	}
	k.timer = time.AfterFunc(setupHold, a.toggleSetup)
	return true
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
	a.matrixStack.Push("wifisetup", display.PrioSetup, 0,
		func(time.Time, display.MatrixFrame) (display.MatrixFrame, bool) {
			f := display.Text("O  O", a.brightness())
			f.Degree = true
			return f, false
		})
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
