package app

import "time"

// bluetoothFadeSteps makes the independent BlueALSA softvol restore in one second.
const bluetoothFadeSteps = 10

const wakeMuteBridge = 2 * time.Second

func nextBluetoothFade(current, target, maximum int) int {
	step := max(1, maximum/bluetoothFadeSteps)
	if current < target {
		return min(current+step, target)
	}
	return max(current-step, target)
}

func (a *App) bluetoothDuckLevel() int {
	a.bluetoothDuckMu.Lock()
	defer a.bluetoothDuckMu.Unlock()
	return a.bluetoothDuckRaw
}

func (a *App) setBluetoothDuck(level int) bool {
	g := a.prof.Audio
	a.bluetoothDuckMu.Lock()
	defer a.bluetoothDuckMu.Unlock()
	if a.bluetoothDuckRaw == level {
		return true
	}
	if err := a.mixer.Set(g.BluetoothVolume, level); err != nil {
		a.log.Warn("set Bluetooth duck", "control", g.BluetoothVolume, "err", err)
		return false
	}
	a.bluetoothDuckRaw = level
	return true
}

// duckBluetoothNow stops the music before voice ID or HA can receive its echo. The bridge keeps it
// muted until the normal assistantBusy lifecycle has taken over.
func (a *App) duckBluetoothNow() {
	a.bluetoothWakeUntil.Store(time.Now().Add(wakeMuteBridge).UnixNano())
	if a.bluetoothVolumeReady.Load() {
		a.setBluetoothDuck(0)
	}
}

// bluetoothDuckLoop keeps Bluetooth muted while the assistant owns the speaker and fades it back
// after duckHold. Master remains untouched, so volume_max still caps final output.
func (a *App) bluetoothDuckLoop(stop <-chan struct{}) {
	g := a.prof.Audio
	if g.BluetoothVolume == "" || g.BluetoothVolumeDefault == 0 {
		return
	}
	t := time.NewTicker(duckTick)
	defer t.Stop()
	defer func() {
		if a.bluetoothVolumeReady.Load() {
			a.setBluetoothDuck(g.BluetoothVolumeDefault)
		}
	}()
	var last time.Time
	for {
		select {
		case <-stop:
			return
		case now := <-t.C:
			if !a.bluetoothVolumeReady.Load() {
				continue
			}
			if a.assistantBusy() {
				last = now
			}
			target := g.BluetoothVolumeDefault
			if now.UnixNano() < a.bluetoothWakeUntil.Load() || (!last.IsZero() && now.Sub(last) < duckHold) {
				target = 0
			}
			current := a.bluetoothDuckLevel()
			if current != target {
				a.setBluetoothDuck(nextBluetoothFade(current, target, g.BluetoothVolumeDefault))
			}
		}
	}
}
