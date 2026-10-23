package app

import (
	"os"
	"strconv"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Input: one reader per board input device (found by kernel name); the board profile maps key
// and switch codes to actions, so the handlers below are model-independent.

// inputLoop reads one evdev device, re-resolving and reopening it with backoff if it fails.
func (a *App) inputLoop(stop <-chan struct{}, name string) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		path, hasSwitch, err := hw.FindInputInfo(a.cfg.SysRoot, name)
		var f *os.File
		if err == nil {
			f, err = hw.OpenInput(path)
		}
		if err != nil {
			a.log.Warn("open input", "device", name, "err", err)
			if !sleepOrStop(stop, 3*time.Second) {
				return
			}
			continue
		}
		if hasSwitch {
			a.readSwitches(f) // publish the initial latch state
		}
		err = hw.ReadEvents(f, func(ev hw.InputEvent) { a.onInputEvent(f, hasSwitch, ev) })
		f.Close()
		a.log.Warn("input reader ended", "device", name, "err", err)
		if !sleepOrStop(stop, 1*time.Second) {
			return
		}
	}
}

func (a *App) onInputEvent(f *os.File, hasSwitch bool, ev hw.InputEvent) {
	switch ev.Type {
	case hw.EvSw:
		if a.prof.Input.Switches[int(ev.Code)] == "mic_mute" {
			a.setMute(ev.Value != 0)
		}
	case hw.EvKey:
		if ev.Value != 1 { // key down only
			return
		}
		action := a.prof.Input.Keys[int(ev.Code)]
		if action == "mute_key" {
			if hasSwitch {
				a.readSwitches(f)
			}
			return
		}
		if action != "" && a.stopAlarm() { // any key silences a ringing timer, and does nothing else
			return
		}
		switch action {
		case "volume_up":
			a.nudgeVolume(+a.cfg.VolumeStep)
			a.publishButton("volume_up")
		case "volume_down":
			a.nudgeVolume(-a.cfg.VolumeStep)
			a.publishButton("volume_down")
		case "voice":
			a.publishButton("voice_command")
			if a.cfg.WyomingPort > 0 {
				a.sat.startListening(srcKey)
			}
		}
	}
}

// readSwitches reads the latched state of the profile's switches (EVIOCGSW) on f.
func (a *App) readSwitches(f *os.File) {
	for code, action := range a.prof.Input.Switches {
		if action != "mic_mute" {
			continue
		}
		on, err := hw.SwitchState(f, code)
		if err != nil {
			a.log.Debug("read switch", "code", code, "err", err)
			continue
		}
		a.setMute(on)
	}
}

func (a *App) micMuted() bool { return a.muted.Load() }

func (a *App) setMute(muted bool) {
	a.muted.Store(muted)
	if muted {
		a.ringStack.Push("mute", display.PrioLocal, 0, effect("mute"))
	} else {
		a.ringStack.Remove("mute")
	}
	a.publishMicMuteValue(muted)
}

func (a *App) publishMicMute() { a.publishMicMuteValue(a.muted.Load()) }

func (a *App) publishMicMuteValue(muted bool) {
	if a.mqtt != nil {
		a.mqtt.Publish(a.topics.State("mic_mute"), onOff(muted))
	}
}

func (a *App) publishButton(kind string) {
	if a.mqtt != nil {
		a.mqtt.PublishEvent(a.topics.State("button"), `{"event_type":"`+kind+`"}`)
	}
}

// ---- volume ----

// nudgeVolume changes Master by delta (in Master units) and shows the local volume bubble.
func (a *App) nudgeVolume(delta int) {
	cur, err := a.mixer.Get(a.prof.Audio.Volume)
	if err != nil {
		a.log.Warn("volume get", "err", err)
		return
	}
	a.applyVolume(clamp(cur+delta, 0, a.prof.Audio.VolumeMax))
}

func (a *App) setVolumePercent(pct int) {
	a.applyVolume(rawFromPercent(pct, a.prof.Audio.VolumeMax))
}

func (a *App) applyVolume(v int) {
	if err := a.mixer.Set(a.prof.Audio.Volume, v); err != nil {
		a.log.Warn("volume set", "err", err)
		return
	}
	a.store.update(func(s *persistState) { s.Volume = v })
	pct := percentFromRaw(v, a.prof.Audio.VolumeMax)
	if a.mqtt != nil {
		a.mqtt.Publish(a.topics.State("volume"), strconv.Itoa(pct))
	}
	// Local reflex: the percentage on the matrix for a moment. The "ring" is ~12 mm across, too
	// small to read as a level bar, so it stays for colour/animation only.
	a.matrixStack.Push("volbubble", display.PrioLocal, volumeBubbleTTL,
		func(time.Time, display.MatrixFrame) (display.MatrixFrame, bool) {
			return display.Number(pct, a.brightness()), false
		})
}

// unmute applies the configured unmute sequence (before any playback).
func (a *App) unmute() {
	for _, line := range a.prof.Audio.Unmute {
		if err := a.mixer.CSet(line); err != nil {
			a.log.Warn("unmute step failed", "line", line, "err", err)
		}
	}
}

// announce unmutes, then plays a local file.
func (a *App) announce(path string) {
	a.unmute()
	if err := a.player.Play(path); err != nil {
		a.log.Warn("play", "path", path, "err", err)
	}
}

func sleepOrStop(stop <-chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return false
	case <-time.After(d):
		return true
	}
}
