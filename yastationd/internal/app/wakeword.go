package app

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

// Wake word («Алиса», vendor spotter + VQE in a child process) and the local command reflexes.

// newSpotter builds the spotter if enabled and its binary is present; nil otherwise.
func (a *App) newSpotter() *wake.Spotter {
	c := a.cfg()
	if !c.WakeEnabled {
		return nil
	}
	if _, err := os.Stat(c.Path(c.SpotterBin)); err != nil {
		a.log.Warn("wake word disabled: spotter binary missing", "path", c.SpotterBin)
		return nil
	}
	w := a.prof.Wake
	if w.VQEPreset == "" || w.Model == "" || len(w.Mics) == 0 {
		a.log.Warn("wake word disabled: board profile has no wake.vqe_preset/model/mics", "board", a.prof.Name)
		return nil
	}
	args := []string{"serve", "-P", w.VQEPreset, "-N", strconv.Itoa(a.prof.Mic.Channels), "-M", joinInts(w.Mics)}
	if len(w.Refs) > 0 { // multi-ref preset (orion's 7 speaker refs), in VQE order
		args = append(args, "-Q", joinInts(w.Refs))
	} else {
		args = append(args, "-R", strconv.Itoa(w.Ref))
	}
	// The VQE's models (the call mode's neural noise reducer, docs/intercom.md): ours under
	// <cfw>/vendor/vqe_models when a model is there (`make vqe-models`: a bergamot whose firmware
	// lacks it, pickle), else the firmware's own (orion 3.21 ships noise_reducer/model.nnet, the
	// name its library loads). A directory without the model would leave the VQE without it.
	models := c.VQEModels
	if models == "" {
		if d := a.prof.Platform.CFWRoot + "/vendor/vqe_models"; fileExists(c.Path(d + "/noise_reducer/model.nnet")) {
			models = d
		}
	}
	if models != "" {
		args = append(args, "-m", models)
	}
	args = append(args, "-a", w.Model)
	if w.CommandModel != "" {
		args = append(args, "-k", w.CommandModel)
	}
	return &wake.Spotter{
		Bin:     c.Path(c.SpotterBin),
		Args:    args,
		Env:     []string{"LD_LIBRARY_PATH=" + w.Libs},
		Log:     a.log,
		OnEvent: a.onWakeEvent,
	}
}

func joinInts(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ",")
}

func (a *App) onWakeEvent(ev wake.Event) {
	switch ev.Kind {
	case "WAKE":
		if a.cfg().WakeCapture {
			a.captureWake(ev)
		}
		switch {
		case a.micMuted():
			a.log.Info("wake: ignored, mic muted")
		case a.icRinging() || a.icInCall():
			// During a call «Алиса» only opens a short window for a command (docs/intercom.md):
			// no Assist session, nothing leaves the speaker.
			a.log.Info("wake: intercom command window", "phrase", ev.Phrase)
			a.icWoke()
		case a.speaking():
			// No verified AEC yet: our own playback (e.g. TTS saying «Алиса») must not wake us.
			a.log.Info("wake: ignored while playing", "phrase", ev.Phrase)
		case a.sat.awaitingReply():
			// A run-pipeline now would wedge HA's satellite; see interruptForWake.
			if a.cfg().WakeWhileWaiting == "interrupt" {
				a.log.Info("wake: interrupting the pending reply", "phrase", ev.Phrase)
				a.publishButton("wake_word")
				a.sat.interruptForWake(ev)
			} else {
				a.log.Info("wake: ignored, HA is still answering the previous request")
				a.express("busy", "busy", 0)
			}
		default:
			a.log.Info("wake: detected", "phrase", ev.Phrase, "at", ev.At)
			if a.arbitrationOn() {
				a.arbitrateWake(ev) // the loudest of the speakers that heard it answers
				return
			}
			a.startWake(ev)
		}
	case "CMD":
		a.onCommand(ev.Phrase)
	default:
		a.log.Debug("wake: near miss", "kind", ev.Kind, "phrase", ev.Phrase)
		if ev.Kind == "SUBHIT:WAKE" {
			a.lastSubhit.Store(&ev)
		}
	}
}

// startWake starts the session for a wake word ev; startWakeWith with the speech held meanwhile.
func (a *App) startWake(ev wake.Event) {
	h := &heldWake{}
	h.unsub = a.wakeAudio(&ev, h.add)
	a.startWakeWith(h)
}

func (a *App) startWakeWith(held *heldWake) {
	a.publishButton("wake_word")
	a.sat.startListeningWith(srcWake, held)
}

// speaking: our speaker is busy (local playback, a Wyoming stream from HA, or the alarm).
func (a *App) speaking() bool {
	if a.playing.Load() {
		return true
	}
	a.timers.mu.Lock()
	ringing := a.timers.alarm != nil
	a.timers.mu.Unlock()
	return ringing
}

// onCommand handles a command-spotter phrase. Commands are armed only while we're making
// sound — that's when "стоп"/"громче" are meant for the speaker, and it keeps false accepts
// from household talk out of the rest of the day.
func (a *App) onCommand(phrase string) {
	if a.icCommand(phrase) {
		return
	}
	if isStop(phrase) && a.sat.abortListening("«"+phrase+"»") {
		a.log.Info("command: session cancelled", "phrase", phrase)
		a.express("voice", "blink", 0)
		return
	}
	if !a.speaking() {
		// While a reply is pending, «стоп» gives up on it (the request, not the speaker).
		if a.sat.awaitingReply() && isStop(phrase) {
			a.log.Info("command: cancel the pending reply", "phrase", phrase)
			a.sat.cancelReply("«" + phrase + "»")
			a.express("voice", "blink", 0)
			return
		}
		a.log.Debug("command ignored (not playing)", "phrase", phrase)
		return
	}
	a.log.Info("command", "phrase", phrase)
	switch p := strings.ToLower(phrase); {
	case isStop(p) || p == "пауза":
		a.sat.mu.Lock()
		a.sat.follow = false // "стоп" during a question means: don't ask me
		a.sat.mu.Unlock()
		a.stopAlarm()
		a.player.Stop()
	case p == "громче":
		a.nudgeVolume(+2 * a.cfg().VolumeStep)
	case p == "тише":
		a.nudgeVolume(-2 * a.cfg().VolumeStep)
	default:
		a.log.Info("command not mapped", "phrase", phrase)
	}
}

func isStop(phrase string) bool {
	switch strings.ToLower(phrase) {
	case "стоп", "хватит", "отмена", "выключись":
		return true
	}
	return false
}

// defaultPrerollMs: preroll_ms when neither the config nor the board profile sets one.
const defaultPrerollMs = 300

// prerollLead: the config's preroll_ms, else the board's (wake.preroll_ms), else the default.
func (a *App) prerollLead() time.Duration {
	ms := a.cfg().PrerollMs
	if ms <= 0 {
		ms = a.prof.Wake.PrerollMs
	}
	if ms <= 0 {
		ms = defaultPrerollMs
	}
	return time.Duration(ms) * time.Millisecond
}

// WakeWordLead: with wake_word_in_stream, how far before the WAKE point the audio for HA starts.
// The spotter fires 0.55-0.7 s after the word starts (up to 1.3 s for a drawn-out one,
// docs/wake-word-boundary.md).
const wakeWordLead = 1300 * time.Millisecond

// wakeAudio picks the session audio source: VQE output when the spotter runs, else the raw mic
// channel. For a wake word (ev) the VQE audio starts at the wake word's position less the lead
// (preroll_ms, or wakeWordLead with the wake word kept); otherwise preroll_ms before now.
func (a *App) wakeAudio(ev *wake.Event, fn func([]byte)) (unsubscribe func()) {
	if a.spot != nil && a.spot.Running() {
		c := a.cfg()
		lead := a.prerollLead()
		if ev == nil {
			return a.spot.SubscribeVQE(lead, fn)
		}
		if c.WakeWordInStream {
			lead = wakeWordLead
		}
		unsub, _ := a.spot.SubscribeVQEAt(*ev, lead, fn)
		return unsub
	}
	return a.mic.Subscribe(fn)
}
