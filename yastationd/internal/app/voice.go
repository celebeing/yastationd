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
	// After the transcript HA works on the answer (LLM, tools) and has no timeout of its own: it
	// waits as long as we answer its pings (docs/voice-slow-replies.md). So the reply's deadline
	// is ours (Config.ReplyTimeout); past slowReplyAfter the "thinking" indication turns into the
	// slower "waiting" one, so a long wait still looks alive.
	slowReplyAfter = 10 * time.Second
	// A run-pipeline while HA still runs one wedges HA's satellite (its stale run-end resets the
	// new run's state: every later run hangs in STT). The only clean cancel is dropping the
	// connection: HA cancels the run at once and reconnects after ~3 s. A wake that interrupts a
	// pending reply buffers the speech meanwhile, for up to reconnectWait.
	reconnectWait = 8 * time.Second
	// Wedge watchdog: we heard speech, but HA sent no voice-started this long into the session.
	wedgeWait = 8 * time.Second
	// "Speech heard": the session's loud level this far above its floor.
	wedgeSpeechDB = 12
)

type voiceSession struct {
	conn      *wyoming.Conn
	unsub     func()
	once      sync.Once
	done      chan struct{}
	started   chan struct{} // closed when HA signals its STT started
	sttOnce   sync.Once
	speech    chan struct{} // closed when HA's VAD heard speech (voice-started)
	speechOne sync.Once
	lvl       hw.LevelMeter // what we sent, to judge mic gain
}

// heldWake is a wake word that interrupted a pending reply: its speech is buffered while the
// connection is dropped and HA reconnects, then streamed ahead of the live audio.
type heldWake struct {
	mu    sync.Mutex
	buf   [][]byte
	sink  func([]byte) // set once the new session runs: forwards live audio
	unsub func()
	timer *time.Timer
}

func (h *heldWake) add(pcm []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sink != nil {
		h.sink(pcm)
		return
	}
	if len(h.buf) < 1000 { // ~30 s of 32 ms chunks: far beyond reconnectWait
		h.buf = append(h.buf, append([]byte(nil), pcm...))
	}
}

// attach sends what was buffered through sink, then forwards live audio to it.
func (h *heldWake) attach(sink func([]byte)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, b := range h.buf {
		sink(b)
	}
	h.buf, h.sink = nil, sink
}

// Session sources.
const (
	srcKey    = "key"    // the voice key (push-to-talk)
	srcWake   = "wake"   // the wake word
	srcFollow = "follow" // dialog follow-up after a reply
)

// startListening begins a session. A key press during a session ends it instead; a wake or
// follow-up during one is ignored.
func (s *satellite) startListening(src string) { s.startListeningWith(src, nil) }

// startListeningWith starts a session; with held it streams held's buffered speech first and
// then held's live audio (a wake that interrupted a pending reply).
func (s *satellite) startListeningWith(src string, held *heldWake) {
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
		a.voiceUnavailable()
		return
	}
	v := &voiceSession{conn: c, done: make(chan struct{}), started: make(chan struct{}), speech: make(chan struct{})}
	s.mu.Lock()
	s.listen = v
	s.mu.Unlock()
	s.setAwaiting(false) // a new session supersedes a reply still pending

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
	send := func(pcm []byte) {
		v.lvl.Add(pcm)
		c.Send(wyoming.Event{Type: wyoming.TypeAudioChunk, Data: map[string]any{
			"rate": hw.MicOutRate, "width": 2, "channels": 1,
			"timestamp": time.Since(start).Milliseconds(),
		}, Payload: pcm})
	}
	if held != nil {
		held.attach(send)
		v.unsub = held.unsub
	} else {
		v.unsub = a.wakeAudio(send)
	}
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
			return
		case <-v.speech:
		case <-time.After(wedgeWait):
			if st := v.lvl.Stats(); st.LoudDBFS-st.FloorDBFS > wedgeSpeechDB {
				// HA took the run but ignores the audio: its satellite is wedged. Only a new
				// connection clears it.
				s.stopListening(v, "HA ignores our speech: satellite looks wedged")
				s.dropConn("wedged satellite")
				a.express("voice", "error", errorTTL)
				return
			}
		}
		select {
		case <-v.done:
		case <-time.After(maxListen - wedgeWait):
			s.stopListening(v, "timeout")
		}
	}()
}

// speechStarted marks HA's VAD hearing speech in c's session (voice-started).
func (s *satellite) speechStarted(c *wyoming.Conn) {
	s.mu.Lock()
	v := s.listen
	s.mu.Unlock()
	if v != nil && v.conn == c {
		v.speechOne.Do(func() { close(v.speech) })
	}
}

// dropConn closes HA's satellite connection: HA cancels the run in flight and reconnects
// within ~3 s (OnDisconnect clears our side).
func (s *satellite) dropConn(why string) {
	s.mu.Lock()
	c := s.main
	s.mu.Unlock()
	if c != nil {
		s.a.log.Info("wyoming: dropping HA's connection", "why", why)
		c.Close()
	}
}

// cancelReply gives up on the pending reply (timeout or «стоп»): drop the connection, which is
// the only clean cancel. Whatever HA already started (a tool call, an automation) still runs.
func (s *satellite) cancelReply(why string) {
	// The flag, not the clock: the deadline timer fires exactly at awaitUntil, where
	// awaitingReply() already reads false.
	s.mu.Lock()
	pending := !s.awaitUntil.IsZero()
	s.mu.Unlock()
	if !pending {
		return
	}
	s.a.log.Info("voice: giving up on the reply", "why", why)
	s.setAwaiting(false)
	s.dropConn("reply cancelled: " + why)
}

// interruptForWake: a wake word while a reply is pending. Buffer the speech, drop the
// connection (a new run on it would wedge HA's satellite), and start the session as soon as HA
// is back (OnEvent run-satellite calls resumeHeldWake).
func (s *satellite) interruptForWake() {
	a := s.a
	h := &heldWake{}
	h.unsub = a.wakeAudio(h.add)
	s.mu.Lock()
	if s.held != nil {
		s.mu.Unlock()
		h.unsub()
		return
	}
	s.held = h
	s.mu.Unlock()
	h.timer = time.AfterFunc(reconnectWait, func() {
		s.mu.Lock()
		mine := s.held == h
		if mine {
			s.held = nil
		}
		s.mu.Unlock()
		if mine {
			h.unsub()
			a.log.Warn("voice: HA did not come back after the interrupt")
			a.express("voice", "error", errorTTL)
		}
	})
	a.express("voice", "listening", stageTTL)
	s.setAwaiting(false)
	s.dropConn("wake word interrupts a pending reply")
}

// resumeHeldWake starts the interrupted session on HA's new connection.
func (s *satellite) resumeHeldWake() {
	s.mu.Lock()
	h := s.held
	s.held = nil
	s.mu.Unlock()
	if h == nil {
		return
	}
	h.timer.Stop()
	s.a.log.Info("voice: HA is back, starting the interrupting session")
	s.startListeningWith(srcWake, h)
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

// A reply is pending from the transcript until HA's audio starts, an error, a new session, a
// pause or a disconnect, or our own deadline (Config.ReplyTimeout), which cancels it.
func (s *satellite) setAwaiting(on bool) {
	s.mu.Lock()
	s.awaitGen++
	gen := s.awaitGen
	if !on {
		s.awaitUntil = time.Time{}
		s.mu.Unlock()
		return
	}
	timeout := s.a.cfg.ReplyTimeout
	s.awaitUntil = time.Now().Add(timeout)
	s.mu.Unlock()
	still := func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.awaitGen == gen && !s.awaitUntil.IsZero()
	}
	time.AfterFunc(slowReplyAfter, func() {
		if still() {
			s.a.express("voice", "waiting", timeout)
		}
	})
	time.AfterFunc(timeout, func() {
		if still() {
			s.cancelReply("no reply in " + timeout.String())
			s.a.express("voice", "sad", errorTTL)
		}
	})
}

func (s *satellite) awaitingReply() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.awaitUntil.IsZero() && time.Now().Before(s.awaitUntil)
}

// inDialog: a voice session, a reply being computed, or HA audio streaming to us (including
// while the jitter buffer waits, when nothing plays yet).
func (s *satellite) inDialog() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listen != nil || s.cur != nil || s.held != nil ||
		(!s.awaitUntil.IsZero() && time.Now().Before(s.awaitUntil))
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
