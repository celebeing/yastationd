package app

import (
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

// Voice-stage ring indications share one "voice" layer, so stages replace each other. The TTLs
// are safety nets: a pipeline that dies without a terminal event can't leave the ring stuck.
const (
	stageTTL    = 15 * time.Second
	errorTTL    = 2 * time.Second
	followDelay = 300 * time.Millisecond
)

// sndQueueLen bounds buffered audio chunks per stream. HA sends 2 KiB chunks faster than
// real time; 8192 of them is ~6 min of 22050 Hz mono — far beyond any announcement.
const sndQueueLen = 8192

// satellite is the Wyoming satellite role (playback only for now: HA's announce and TTS
// replies). The read loop must never block on aplay: HA drops a peer that misses a ping for
// 5 s, so audio chunks go through a queue drained by a writer goroutine.
type satellite struct {
	a *App

	mu     sync.Mutex
	cur    *sndStream
	main   *wyoming.Conn // HA's satellite connection (sent run-satellite, not paused)
	listen *voiceSession // push-to-talk / wake session in progress
	follow bool          // the reply being played asked for a follow-up (dialog mode)

	awaitUntil time.Time // HA is working on a reply until then (see setAwaiting)
}

type sndStream struct {
	owner *wyoming.Conn // the connection that started it; only it can end it
	rate  int
	lvl   hw.LevelMeter // what HA sent (diagnostics: silence from HA vs a playback problem)
	q     chan []byte
	done  <-chan struct{} // aplay exited
}

func (s *satellite) OnConnect(c *wyoming.Conn) {}

// OnDisconnect ends c's stream without "played" (nobody to tell); HA's short describe-only
// connections own no stream, so their closing never cuts an announcement.
func (s *satellite) OnDisconnect(c *wyoming.Conn) {
	s.endListeningOn(c, "HA disconnected")
	s.mu.Lock()
	if s.main == c {
		s.main = nil
	}
	s.mu.Unlock()
	s.endStream(c, false)
}

func (s *satellite) OnEvent(c *wyoming.Conn, ev wyoming.Event) {
	a := s.a
	switch ev.Type {
	case wyoming.TypeDescribe:
		c.Send(wyoming.Info(wyoming.SatelliteInfo{
			Name:        a.cfg.DeviceName,
			Description: "yastationd on a de-Yandexed Station Mini 3",
			Version:     version,
		}))
	case wyoming.TypePing:
		pong := wyoming.Event{Type: wyoming.TypePong}
		if t := ev.String("text"); t != "" {
			pong.Data = map[string]any{"text": t}
		}
		c.Send(pong)
	case wyoming.TypeRunSatellite:
		a.log.Info("wyoming", "event", ev.Type, "peer", c.RemoteAddr())
		s.mu.Lock()
		s.main = c
		s.mu.Unlock()
	case wyoming.TypePauseSatellite:
		// HA muted the satellite (its mute switch) or is going away; nothing streams from us
		// yet (no mic until Phase 2), so just drop any voice-stage indication.
		a.log.Info("wyoming", "event", ev.Type, "peer", c.RemoteAddr())
		s.endListeningOn(c, "satellite paused")
		s.setAwaiting(false)
		s.mu.Lock()
		if s.main == c {
			s.main = nil
		}
		s.mu.Unlock()
		a.ringStack.Remove("voice")
	case wyoming.TypeDetection, wyoming.TypeTranscribe, wyoming.TypeVoiceStarted:
		if ev.Type != wyoming.TypeDetection {
			s.sttStarted(c)
		}
		s.voiceStage("listening")
	case wyoming.TypeVoiceStopped, wyoming.TypeTranscript, wyoming.TypeSynthesize:
		a.log.Info("wyoming", "event", ev.Type, "text", ev.String("text"))
		if ev.Type == wyoming.TypeTranscript {
			s.endListeningOn(c, "transcript")
			s.setAwaiting(true)
		}
		if ev.Type == wyoming.TypeSynthesize {
			// HA doesn't forward continue_conversation to Wyoming satellites; emulate it from the
			// reply text, then act once that reply has played (endStream).
			f := wantsFollowUp(a.store.get().FollowUp, ev.String("text"))
			s.mu.Lock()
			s.follow = f
			s.mu.Unlock()
		}
		s.voiceStage("thinking")
	case wyoming.TypeError:
		s.endListeningOn(c, "pipeline error")
		s.setAwaiting(false)
		if ev.String("code") == "stt-no-text-recognized" { // silence (e.g. an unused follow-up): no alarm
			a.log.Info("wyoming: nothing heard")
			a.ringStack.Remove("voice")
			break
		}
		a.log.Warn("wyoming: pipeline error", "code", ev.String("code"), "text", ev.String("text"))
		a.ringStack.Push("voice", display.PrioVoice, errorTTL, effect("error"))
	case wyoming.TypeTimerStarted, wyoming.TypeTimerUpdated, wyoming.TypeTimerCancelled, wyoming.TypeTimerFinished:
		a.onTimerEvent(ev)
	case wyoming.TypeAudioStart:
		s.startStream(c, ev.Int("rate", 22050), ev.Int("width", 2), ev.Int("channels", 1))
	case wyoming.TypeAudioChunk:
		s.mu.Lock()
		st := s.cur
		s.mu.Unlock()
		if st == nil || st.owner != c { // audio without audio-start: start from the chunk's format
			st = s.startStream(c, ev.Int("rate", 22050), ev.Int("width", 2), ev.Int("channels", 1))
		}
		if st != nil && len(ev.Payload) > 0 {
			st.lvl.Add(ev.Payload)
			select {
			case st.q <- ev.Payload:
			default:
				a.log.Warn("wyoming: audio queue full, dropping chunk")
			}
		}
	case wyoming.TypeAudioStop:
		s.endStream(c, true)
	default:
		a.log.Debug("wyoming event", "type", ev.Type, "data", ev.Data)
	}
}

// startStream begins c's stream; a new stream supersedes any current one (the player
// interrupts it), whoever owns it.
func (s *satellite) startStream(c *wyoming.Conn, rate, width, channels int) *sndStream {
	a := s.a
	s.mu.Lock()
	if prev := s.cur; prev != nil {
		s.cur = nil
		close(prev.q)
	}
	s.mu.Unlock()
	if width != 2 {
		a.log.Warn("wyoming: unsupported sample width", "width", width)
		return nil
	}
	s.setAwaiting(false) // the reply (or an announcement) is here
	a.unmute()
	w, done, err := a.player.Stream(rate, channels)
	if err != nil {
		a.log.Warn("wyoming: start aplay", "err", err)
		return nil
	}
	st := &sndStream{owner: c, rate: rate, q: make(chan []byte, sndQueueLen), done: done}
	go func() {
		defer w.Close()
		for b := range st.q {
			if _, err := w.Write(b); err != nil {
				for range st.q { // aplay is gone (interrupted); drain so nobody blocks
				}
				return
			}
		}
	}()
	s.mu.Lock()
	s.cur = st
	s.mu.Unlock()
	a.ringStack.Push("voice", display.PrioVoice, 0, effect("speaking"))
	a.log.Info("wyoming: audio start", "rate", rate, "channels", channels, "peer", c.RemoteAddr())
	return st
}

// endStream closes the current stream's queue if c owns it. With played set (a real
// audio-stop), it reports "played" once aplay has drained, which ends HA's announce wait early.
func (s *satellite) endStream(c *wyoming.Conn, played bool) {
	s.mu.Lock()
	st := s.cur
	if st == nil || st.owner != c {
		s.mu.Unlock()
		return
	}
	s.cur = nil
	s.mu.Unlock()
	close(st.q)
	ls := st.lvl.Stats()
	s.a.log.Info("wyoming: audio stop", "played", played,
		"secs", float64(int(ls.Seconds*float64(hw.MicOutRate)/float64(st.rate)*10))/10,
		"peak_dbfs", ls.PeakDBFS, "loud_rms_dbfs", ls.LoudDBFS)
	go func() {
		select {
		case <-st.done:
		case <-time.After(10 * time.Minute):
		}
		s.mu.Lock()
		idle := s.cur == nil
		s.mu.Unlock()
		if idle {
			s.a.ringStack.Remove("voice")
		}
		if played {
			c.Send(wyoming.Event{Type: wyoming.TypePlayed})
		}
		s.mu.Lock()
		follow := played && idle && s.follow
		s.follow = false
		s.mu.Unlock()
		if follow {
			time.Sleep(followDelay) // let the room's echo of the reply die down
			s.startListening(srcFollow)
		}
	}()
}

// voiceStage shows a pipeline stage on the ring unless audio is already playing (speaking wins).
func (s *satellite) voiceStage(name string) {
	s.mu.Lock()
	playing := s.cur != nil
	s.mu.Unlock()
	if !playing {
		s.a.ringStack.Push("voice", display.PrioVoice, stageTTL, effect(name))
	}
}

// Dialog follow-up modes (persisted; MQTT select follow_up).
var followModes = []string{"off", "question", "always"}

// wantsFollowUp: "question" listens again when the reply ends with a question mark (what HA's
// own continue_conversation amounts to for LLM agents), "always" after every spoken reply.
func wantsFollowUp(mode, reply string) bool {
	reply = strings.TrimSpace(reply)
	switch mode {
	case "always":
		return reply != ""
	case "question":
		return strings.HasSuffix(strings.TrimRight(reply, " \"»)'"), "?")
	}
	return false
}
