package app

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// Input: one reader per board input device (found by kernel name); the board profile maps key
// and switch codes to actions, so the handlers below are model-independent.

const (
	circularGestureGap   = 750 * time.Millisecond
	panelGestureDebounce = 2 * time.Second
)

type inputDeviceState struct {
	circular         map[uint16]*hw.CircularAxis
	absFailed        map[uint16]bool
	lastPanelGesture time.Time
	// Volume ring: one HA event per gesture (a turn is 20 steps), a new one after a pause or a
	// change of direction.
	lastRingStep time.Time
	lastRingDir  int
}

func newInputDeviceState() *inputDeviceState {
	return &inputDeviceState{
		circular:  make(map[uint16]*hw.CircularAxis),
		absFailed: make(map[uint16]bool),
	}
}

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
		state := newInputDeviceState()
		err = hw.ReadEvents(f, func(ev hw.InputEvent) { a.onInputEvent(f, hasSwitch, state, ev) })
		f.Close()
		a.log.Warn("input reader ended", "device", name, "err", err)
		if !sleepOrStop(stop, 1*time.Second) {
			return
		}
	}
}

func (a *App) onInputEvent(f *os.File, hasSwitch bool, state *inputDeviceState, ev hw.InputEvent) {
	switch ev.Type {
	case hw.EvSw:
		if a.prof.Input.Switches[int(ev.Code)] == "mic_mute" {
			a.setMute(ev.Value != 0)
		}
	case hw.EvAbs:
		a.onAbsoluteInput(f, state, ev)
	case hw.EvKey:
		action := a.prof.Input.Keys[int(ev.Code)]
		if ev.Value == 0 || ev.Value == 1 { // down/up, not autorepeat
			if a.setupKey(action, ev.Value == 1) {
				return
			}
		}
		if ev.Value != 1 { // key down only
			return
		}
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
			a.volumeBeep()
			a.publishButton("volume_up")
		case "volume_down":
			a.nudgeVolume(-a.cfg.VolumeStep)
			a.volumeBeep()
			a.publishButton("volume_down")
		case "voice":
			a.publishButton("voice_command")
			if a.cfg.WyomingPort > 0 {
				a.sat.startListening(srcKey)
			}
		case "panel_gesture":
			now := time.Now()
			if now.Sub(state.lastPanelGesture) >= panelGestureDebounce {
				state.lastPanelGesture = now
				a.publishButton("panel_gesture")
			}
		}
	}
}

func (a *App) onAbsoluteInput(f *os.File, state *inputDeviceState, ev hw.InputEvent) {
	if a.prof.Input.Absolute[int(ev.Code)] != "volume_ring" || state.absFailed[ev.Code] {
		return
	}
	axis := state.circular[ev.Code]
	if axis == nil {
		info := hw.AbsInfo{}
		source := "kernel"
		if configured, ok := a.prof.Input.AbsoluteRange[int(ev.Code)]; ok {
			info.Minimum, info.Maximum = configured.Minimum, configured.Maximum
			source = "profile"
		} else {
			var err error
			info, err = hw.AbsoluteInfo(f, ev.Code)
			if err != nil {
				state.absFailed[ev.Code] = true
				a.log.Warn("read absolute input range", "code", ev.Code, "err", err)
				return
			}
		}
		axis = hw.NewCircularAxis(info, 20, circularGestureGap)
		a.log.Info("absolute input ready", "code", ev.Code, "min", info.Minimum,
			"max", info.Maximum, "source", source)
		state.circular[ev.Code] = axis
	}
	steps := axis.Move(ev.Value, time.Now())
	a.log.Debug("circular input", "code", ev.Code, "value", ev.Value, "steps", steps)
	if steps == 0 || a.stopAlarm() {
		return
	}
	a.nudgeVolume(steps * a.cfg.VolumeStep)
	a.volumeBeep()
	dir, now := 1, time.Now()
	if steps < 0 {
		dir = -1
	}
	if dir != state.lastRingDir || now.Sub(state.lastRingStep) > circularGestureGap {
		if dir > 0 {
			a.publishButton("volume_up")
		} else {
			a.publishButton("volume_down")
		}
	}
	state.lastRingStep, state.lastRingDir = now, dir
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

// nudgeVolume moves Master by n steps of the board's volume curve (5 % each) and shows the local
// volume bubble.
func (a *App) nudgeVolume(n int) {
	cur, err := a.mixer.Get(a.prof.Audio.Volume)
	if err != nil {
		a.log.Warn("volume get", "err", err)
		return
	}
	a.applyVolume(a.prof.Audio.StepVolume(cur, n))
}

// volumeBeep ticks the stock volume sound at the new level, so the level is heard as it is set.
// Keys only: a voice «громче» comes while something plays, and HA's slider is not at the speaker.
// Not over music either: the music's own level is the feedback, and a tick on top of it sounds off.
func (a *App) volumeBeep() {
	snd := a.prof.Audio.VolumeSound
	if !a.cfg.VolumeBeep || snd == "" || a.music.streaming.Load() {
		return
	}
	path := a.cfg.Path(snd)
	if _, err := os.Stat(path); err != nil { // not decoded onto this unit (make sounds)
		return
	}
	a.unmute()
	if err := a.player.Blip(path); err != nil {
		a.log.Warn("volume beep", "err", err)
	}
}

// announceChime plays the board's announce sound and returns how long it lasts (0 = none).
func (a *App) announceChime() time.Duration {
	snd := a.prof.Audio.AnnounceSound
	if !a.cfg.AnnounceChime || snd == "" {
		return 0
	}
	path := a.cfg.Path(snd)
	d, err := wavDuration(path)
	if err != nil { // not decoded onto this unit (make sounds)
		return 0
	}
	if err := a.player.Blip(path); err != nil {
		a.log.Warn("announce chime", "err", err)
		return 0
	}
	return d
}

// wavDuration estimates a PCM wav's length from its byte rate and file size (ffmpeg's few extra
// header bytes add microseconds).
func wavDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := make([]byte, 44)
	if _, err := io.ReadFull(f, h); err != nil {
		return 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	byteRate := binary.LittleEndian.Uint32(h[28:32])
	if string(h[:4]) != "RIFF" || byteRate == 0 {
		return 0, fmt.Errorf("%s: not a PCM wav", path)
	}
	return time.Duration(fi.Size()-44) * time.Second / time.Duration(byteRate), nil
}

func (a *App) setVolumePercent(pct int) {
	a.applyVolume(a.prof.Audio.RawFromPercent(pct))
}

func (a *App) applyVolume(v int) {
	if err := a.mixer.Set(a.prof.Audio.Volume, v); err != nil {
		a.log.Warn("volume set", "err", err)
		return
	}
	a.store.update(func(s *persistState) { s.Volume = v })
	pct := a.prof.Audio.PercentFromRaw(v)
	a.reportMusicVolume(pct)
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
