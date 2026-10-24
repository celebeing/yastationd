package app

import (
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

// Push-to-talk: the voice key starts an Assist pipeline at STT on HA's satellite connection and
// streams the mic until HA has a transcript (its VAD decides when we stopped talking), an error
// arrives, the key is pressed again, or maxListen passes. A wake word will start the same session.

const (
	maxListen = 30 * time.Second
	// HA sends `transcribe` when its STT stage starts. Without it within sttStartWait, HA didn't
	// really start the run (seen on-device: after a run was cancelled mid-LLM, every later
	// run-pipeline "finished" at once) — end the session instead of streaming into nothing.
	sttStartWait = 4 * time.Second
	// After the transcript, HA is working on the answer (LLM, tools). A wake now would make HA
	// cancel that run and lose the reply, so wakes are ignored until audio starts, an error
	// arrives, or this safety timeout passes (a dead HA must not lock the speaker out).
	awaitReplyMax = 20 * time.Second
)

type voiceSession struct {
	conn    *wyoming.Conn
	unsub   func()
	once    sync.Once
	done    chan struct{}
	started chan struct{} // closed when HA signals its STT started
	sttOnce sync.Once
	lvl     hw.LevelMeter // what we sent, to judge mic gain
}

// Session sources.
const (
	srcKey    = "key"    // the voice key (push-to-talk)
	srcWake   = "wake"   // the wake word
	srcFollow = "follow" // dialog follow-up after a reply
)

// startListening begins a session. A key press during a session ends it instead; a wake or
// follow-up during one is ignored.
func (s *satellite) startListening(src string) {
	a := s.a
	s.mu.Lock()
	if v := s.listen; v != nil {
		s.mu.Unlock()
		if src == srcKey {
			s.stopListening(v, "key pressed again")
		}
		return
	}
	c := s.main
	s.mu.Unlock()

	if a.micMuted() {
		a.express("voice", "mute", errorTTL)
		return
	}
	if c == nil {
		a.log.Warn("voice: no HA satellite connection")
		a.express("voice", "error", errorTTL)
		return
	}
	v := &voiceSession{conn: c, done: make(chan struct{}), started: make(chan struct{})}
	s.mu.Lock()
	s.listen = v
	s.mu.Unlock()

	a.log.Info("voice: listening", "src", src)
	a.express("voice", "listening", stageTTL)
	if src == srcWake { // lets HA show which wake word started the run
		c.Send(wyoming.Event{Type: wyoming.TypeDetection, Data: map[string]any{"name": "alisa"}})
	}
	c.Send(wyoming.Event{Type: wyoming.TypeRunPipeline, Data: map[string]any{
		"start_stage": "asr", "end_stage": "tts", "restart_on_end": false,
	}})
	c.Send(wyoming.Event{Type: wyoming.TypeAudioStart, Data: map[string]any{
		"rate": hw.MicOutRate, "width": 2, "channels": 1, "timestamp": 0,
	}})
	start := time.Now()
	v.unsub = a.wakeAudio(func(pcm []byte) {
		v.lvl.Add(pcm)
		c.Send(wyoming.Event{Type: wyoming.TypeAudioChunk, Data: map[string]any{
			"rate": hw.MicOutRate, "width": 2, "channels": 1,
			"timestamp": time.Since(start).Milliseconds(),
		}, Payload: pcm})
	})
	go func() {
		select {
		case <-v.done:
			return
		case <-v.started:
		case <-time.After(sttStartWait):
			s.stopListening(v, "HA did not start STT")
			a.express("voice", "error", errorTTL)
			return
		}
		select {
		case <-v.done:
		case <-time.After(maxListen):
			s.stopListening(v, "timeout")
		}
	}()
}

// sttStarted marks c's session as acknowledged by HA (transcribe / voice-started).
func (s *satellite) sttStarted(c *wyoming.Conn) {
	s.mu.Lock()
	v := s.listen
	s.mu.Unlock()
	if v != nil && v.conn == c {
		v.sttOnce.Do(func() { close(v.started) })
	}
}

// Awaiting-reply window: from the transcript until the reply audio starts (or an error, or
// awaitReplyMax). Wakes in it are ignored.
func (s *satellite) setAwaiting(on bool) {
	s.mu.Lock()
	if on {
		s.awaitUntil = time.Now().Add(awaitReplyMax)
	} else {
		s.awaitUntil = time.Time{}
	}
	s.mu.Unlock()
}

func (s *satellite) awaitingReply() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.awaitUntil)
}

// inDialog: a voice session, a reply being computed, or HA audio streaming to us (including
// while the jitter buffer waits, when nothing plays yet).
func (s *satellite) inDialog() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen != nil || s.cur != nil || time.Now().Before(s.awaitUntil)
}

// stopListening ends v (idempotent): mic off, audio-stop to HA.
func (s *satellite) stopListening(v *voiceSession, why string) {
	v.once.Do(func() {
		s.mu.Lock()
		if s.listen == v {
			s.listen = nil
		}
		s.mu.Unlock()
		v.unsub()
		v.conn.Send(wyoming.Event{Type: wyoming.TypeAudioStop})
		close(v.done)
		st := v.lvl.Stats()
		s.a.log.Info("voice: stopped listening", "why", why, "secs", st.Seconds,
			"peak_dbfs", st.PeakDBFS, "loud_rms_dbfs", st.LoudDBFS, "floor_rms_dbfs", st.FloorDBFS)
	})
}

// endListeningOn stops the session if it runs on c (transcript/error/disconnect from HA).
func (s *satellite) endListeningOn(c *wyoming.Conn, why string) {
	s.mu.Lock()
	v := s.listen
	s.mu.Unlock()
	if v != nil && v.conn == c {
		s.stopListening(v, why)
	}
}
