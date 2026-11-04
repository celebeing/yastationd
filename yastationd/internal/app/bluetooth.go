package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/bluetooth"
	"github.com/celebeing/yastationd/yastationd/internal/display"
)

const bluetoothSlot = "bluetooth-pair"

// currentVolumePercent: the speaker's volume now (100 when the mixer can't be read).
func (a *App) currentVolumePercent() int {
	if v, err := a.mixer.Get(a.prof.Audio.Volume); err == nil {
		return a.percentFromRaw(v)
	}
	return 100
}

// onPhoneVolume: a linked phone moved its slider; the speaker follows, capped by volume_max.
func (a *App) onPhoneVolume(pct int) {
	if abs(a.currentVolumePercent()-pct) <= 1 {
		return // our own report coming back
	}
	a.log.Info("bluetooth: the phone set the volume", "pct", pct)
	a.setMaster(a.rawFromPercent(pct), 0, true)
}

// reportBluetoothVolume moves a linked phone's slider to the speaker's volume.
func (a *App) reportBluetoothVolume(pct int) {
	if b, ok := a.bluetoothControl().(*bluetooth.Manager); ok && b != nil && a.cfg().BluetoothVolume == "linked" {
		go b.SetPhoneVolume(pct)
	}
}

type bluetoothControl interface {
	View() bluetooth.View
	Pair() error
	Confirm() error
	Cancel()
	Disconnect(string) error
	Forget(string) error
	Close()
}

func (a *App) startBluetooth(ctx context.Context) {
	if !a.prof.Audio.Bluetooth {
		return
	}
	if !a.cfg().Bluetooth {
		if err := bluetooth.PowerOff(); err != nil {
			a.log.Warn("bluetooth: switch the radio off", "err", err)
		} else {
			a.log.Info("bluetooth: off in the settings; radio off")
		}
		return
	}
	b, err := bluetooth.Open(ctx, a.cfg().DeviceName, a.log, a.bluetoothChanged)
	if err != nil {
		a.log.Warn("bluetooth unavailable", "err", err)
		return
	}
	a.log.Info("bluetooth: attached; hidden, not pairable")
	b.OnPlayer(func(p bluetooth.Player) { // the source registers in Run; until then nothing plays
		for _, s := range a.mediaSources() {
			if bs, ok := s.(*btSource); ok {
				bs.onPlayer(p)
			}
		}
	})
	b.OnConnection(func(name string, on bool) {
		if on {
			a.hook("bluetooth_connected")
		} else {
			a.hook("bluetooth_disconnected")
		}
	})
	if a.cfg().BluetoothVolume == "linked" {
		b.LinkVolume(a.currentVolumePercent, a.onPhoneVolume)
	}
	a.btMu.Lock()
	a.bt = b
	a.btMu.Unlock()
}
func (a *App) bluetoothControl() bluetoothControl {
	a.btMu.RLock()
	defer a.btMu.RUnlock()
	return a.bt
}
func (a *App) bluetoothChanged(v bluetooth.View) {
	if a.front == nil {
		return
	}
	if v.Code == "" {
		a.matrixStack.Remove(bluetoothSlot)
		return
	}
	a.matrixStack.Push(bluetoothSlot, display.PrioSetup, 0, func(now time.Time, _ display.FrontFrame) (display.FrontFrame, bool) {
		if a.screen != nil {
			return a.front.Text(v.Code[:3]+" "+v.Code[3:], a.brightness()), false
		}
		if now.UnixMilli()/1000%2 == 0 {
			return a.front.Text(v.Code[:3], a.brightness()), false
		}
		return a.front.Text(v.Code[3:], a.brightness()), false
	})
}

func (a *App) apiBluetooth(w http.ResponseWriter, r *http.Request) {
	b := a.bluetoothControl()
	if b == nil {
		if a.prof.Audio.Bluetooth && r.Method == http.MethodGet && strings.Trim(r.URL.Path, "/") == "api/bluetooth" {
			writeJSON(w, http.StatusOK, bluetooth.View{Off: !a.cfg().Bluetooth, Devices: []bluetooth.Device{}})
			return
		}
		writeErr(w, http.StatusNotFound, "Bluetooth is unavailable on this speaker")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/bluetooth"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET")
			return
		}
		writeJSON(w, http.StatusOK, b.View())
		return
	}
	switch {
	case len(parts) == 1 && parts[0] == "pair" && r.Method == http.MethodPost:
		if err := b.Pair(); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, b.View())
	case len(parts) == 1 && parts[0] == "pair" && r.Method == http.MethodDelete:
		b.Cancel()
		writeJSON(w, http.StatusOK, b.View())
	case len(parts) == 2 && parts[0] == "pair" && parts[1] == "confirm" && r.Method == http.MethodPost:
		if err := b.Confirm(); err != nil {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, b.View())
	case len(parts) == 3 && parts[0] == "devices" && parts[2] == "disconnect" && r.Method == http.MethodPost:
		if err := b.Disconnect(parts[1]); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, b.View())
	case len(parts) == 2 && parts[0] == "devices" && r.Method == http.MethodDelete:
		if err := b.Forget(parts[1]); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, b.View())
	default:
		writeErr(w, http.StatusMethodNotAllowed, "GET, POST or DELETE")
	}
}
