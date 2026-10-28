package app

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/version"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

// Voice-stage ring indications share one "voice" layer, so stages replace each other. The TTLs
// are safety nets: a pipeline that dies without a terminal event can't leave the ring stuck.
const (
	dialogEndWink = 1500 * time.Millisecond
	stageTTL      = 15 * time.Second
	errorTTL      = 2 * time.Second
	followDelay   = 300 * time.Millisecond
)

// defaultTTSRate is the sample rate assumed when HA's audio events carry none (Piper's).
const defaultTTSRate = 22050

// sndQueueLen bounds buffered audio chunks per stream. HA sends 2 KiB chunks faster than
// real time; 8192 of them is ~6 min of 22050 Hz mono — far beyond any announcement.
const sndQueueLen = 8192

// Jitter buffer. HA normally sends TTS faster than real time, but over a congested Wi-Fi link (or
// from a TTS engine that streams while it synthesizes) chunks can stall for seconds. Writing each
// chunk as it comes then starves aplay every few tens of ms, and the reply stutters. Instead the
// pump holds audio back until prebufferDur is queued (or the stream ends, or rebufferWait passes),
// and goes back to holding when the audio written so far has run out, so a stall becomes one pause.
const (
	prebufferDur = 500 * time.Millisecond
	rebufferWait = 2 * time.Second
)

// satellite is the Wyoming satellite role: HA's announcements and TTS replies here, the mic
// sessions (push-to-talk, wake word) in voice.go. The read loop must never block on aplay: HA
// drops a peer that misses a ping for 5 s, so audio chunks go through a queue drained by a writer
// goroutine.
type satellite struct {
	a *App

	mu     sync.Mutex
	cur    *sndStream
	main   *wyoming.Conn // HA's satellite connection (sent run-satellite, not paused)
	listen *voiceSession // push-to-talk / wake session in progress
	follow bool          // the reply being played asked for a follow-up (dialog mode)

	awaitUntil time.Time // a reply is pending until then (see setAwaiting); zero = none
	awaitGen   int       // bumps on every setAwaiting, so stale timers do nothing
	held       *heldWake // a wake word waiting for HA to reconnect

	lastHeard atomic.Int64 // unix nanos of the last transcript HA sent us
}

type sndStream struct {
	owner    *wyoming.Conn // the connection that started it; only it can end it
	rate     int
	channels int
	lvl      hw.LevelMeter // what HA sent (diagnostics: silence from HA vs a playback problem)
	q        chan []byte
	done     <-chan struct{} // aplay exited

	// Delivery diagnostics (read loop only): wall time of the stream, the longest gap between
	// chunks, and how often the pump had to wait for audio after it started playing.
	start, last time.Time
	maxGap      time.Duration
	rebuffers   atomic.Int32

	holdUntil time.Time // the pump starts no earlier (the announce chime is playing)
	announce  bool      // an announcement, not a reply in a dialog
}

func (s *satellite) OnConnect(c *wyoming.Conn) {}

// OnDisconnect ends c's stream without "played" (nobody to tell); HA's short describe-only
// connections own no stream, so their closing never cuts an announcement.
func (s *satellite) OnDisconnect(c *wyoming.Conn) {
	s.endListeningOn(c, "HA disconnected")
	s.mu.Lock()
	wasMain := s.main == c
	if wasMain {
		s.main = nil
	}
	held := s.held != nil
	s.mu.Unlock()
	s.endStream(c, false)
	// HA went away (e.g. its ping timeout) with a reply pending: that reply will never come. Our
	// own drop (cancel, interrupt) has already cleared the flag.
	if wasMain && !held && s.awaitingReply() {
		s.setAwaiting(false)
		s.a.express("voice", "error", errorTTL)
	}
}

func (s *satellite) OnEvent(c *wyoming.Conn, ev wyoming.Event) {
	a := s.a
	switch ev.Type {
	case wyoming.TypeDescribe:
		c.Send(wyoming.Info(wyoming.SatelliteInfo{
			Name:        a.cfg().DeviceName,
			Description: satelliteDescription(a.prof.Model),
			Version:     version.Version,
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
		s.resumeHeldWake()
	case wyoming.TypePauseSatellite:
		// HA muted the satellite (its mute switch) or is going away: end a mic session on this
		// connection, forget a pending reply and drop any voice-stage indication.
		a.log.Info("wyoming", "event", ev.Type, "peer", c.RemoteAddr())
		s.endListeningOn(c, "satellite paused")
		s.setAwaiting(false)
		s.mu.Lock()
		if s.main == c {
			s.main = nil
		}
		s.mu.Unlock()
		a.unexpress("voice")
	case wyoming.TypeDetection, wyoming.TypeTranscribe, wyoming.TypeVoiceStarted:
		if ev.Type != wyoming.TypeDetection {
			s.sttStarted(c)
		}
		if ev.Type == wyoming.TypeVoiceStarted {
			s.speechStarted(c)
		}
		s.voiceStage("listening")
	case wyoming.TypeVoiceStopped, wyoming.TypeTranscript, wyoming.TypeSynthesize:
		a.log.Info("wyoming", "event", ev.Type, "text", ev.String("text"))
		if ev.Type == wyoming.TypeTranscript {
			s.endListeningOn(c, "transcript")
			s.setAwaiting(true)
			s.lastHeard.Store(time.Now().UnixNano()) // whose request an HA intercom call is (cmdIntercomCall)
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
			a.unexpress("voice")
			break
		}
		a.log.Warn("wyoming: pipeline error", "code", ev.String("code"), "text", ev.String("text"))
		a.express("voice", "error", errorTTL)
	case wyoming.TypeTimerStarted, wyoming.TypeTimerUpdated, wyoming.TypeTimerCancelled, wyoming.TypeTimerFinished:
		a.onTimerEvent(ev)
	case wyoming.TypeAudioStart:
		s.startStream(c, ev.Int("rate", defaultTTSRate), ev.Int("width", 2), ev.Int("channels", 1))
	case wyoming.TypeAudioChunk:
		s.mu.Lock()
		st := s.cur
		s.mu.Unlock()
		if st == nil || st.owner != c { // audio without audio-start: start from the chunk's format
			st = s.startStream(c, ev.Int("rate", defaultTTSRate), ev.Int("width", 2), ev.Int("channels", 1))
		}
		if st != nil && len(ev.Payload) > 0 {
			now := time.Now()
			if !st.last.IsZero() && now.Sub(st.last) > st.maxGap {
				st.maxGap = now.Sub(st.last)
			}
			st.last = now
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
	// Audio from HA with no dialog going on is an announcement. Wyoming carries no preannounce
	// sound, so we chime first and the pump holds the stream until the chime has played.
	s.mu.Lock()
	announce := s.listen == nil && s.awaitUntil.IsZero()
	s.mu.Unlock()
	s.setAwaiting(false) // the reply (or an announcement) is here
	a.unmute()
	w, done, err := a.player.Stream(rate, channels)
	if err != nil {
		a.log.Warn("wyoming: start aplay", "err", err)
		return nil
	}
	st := &sndStream{owner: c, rate: rate, channels: channels, q: make(chan []byte, sndQueueLen),
		done: done, start: time.Now()}
	if announce {
		st.announce = true
		st.holdUntil = st.start.Add(a.announceChime())
	}
	s.mu.Lock()
	s.cur = st
	s.mu.Unlock()
	go s.pump(st, w)
	a.log.Info("wyoming: audio start", "rate", rate, "channels", channels, "peer", c.RemoteAddr())
	return st
}

// pump feeds st's queue to aplay through the jitter buffer, and keeps the ring honest: green
// pulse while audio actually plays, the "thinking" comet while it waits for more, nothing once
// aplay is gone (a «стоп», or a newer stream) even if HA is still sending.
func (s *satellite) pump(st *sndStream, w io.WriteCloser) {
	defer w.Close()
	bps := st.rate * st.channels * 2
	if bps <= 0 {
		bps = 2 * defaultTTSRate
	}
	want := int(prebufferDur.Seconds() * float64(bps))
	var (
		pending   [][]byte
		pendingN  int
		buffering = true
		t0        time.Time // when the audio written since the last resume started playing
		written   int       // bytes written since then
	)
	wait := time.NewTimer(rebufferWait) // buffering: give up waiting for `want` bytes
	defer wait.Stop()
	dry := time.NewTimer(time.Hour) // playing: the audio written so far has run out
	dry.Stop()
	defer dry.Stop()
	playedOut := func() time.Duration {
		return time.Duration(written)*time.Second/time.Duration(bps) - time.Since(t0)
	}
	ring := func(name string) {
		s.mu.Lock()
		cur := s.cur == st
		s.mu.Unlock()
		if cur {
			s.a.moment("voice", name, 0)
		}
	}
	gone := func() { // aplay exited under us: drop the rest so the read loop never blocks
		s.mu.Lock()
		cur := s.cur == st
		s.mu.Unlock()
		if cur {
			s.a.unexpress("voice")
		}
		for range st.q {
		}
	}
	resume := func() bool {
		wait.Stop()
		t0, written = time.Now(), 0
		for _, b := range pending {
			if _, err := w.Write(b); err != nil {
				return false
			}
			written += len(b)
		}
		pending, pendingN, buffering = nil, 0, false
		dry.Reset(playedOut())
		ring("speaking")
		return true
	}
	ring("thinking")
	if d := time.Until(st.holdUntil); d > 0 {
		select {
		case <-time.After(d):
		case <-st.done:
			gone()
			return
		}
	}
	for {
		select {
		case b, ok := <-st.q:
			if !ok { // audio-stop (or superseded): play out whatever is held back
				if buffering && pendingN > 0 {
					resume()
				}
				return
			}
			if buffering {
				pending, pendingN = append(pending, b), pendingN+len(b)
				if pendingN >= want && !resume() {
					gone()
					return
				}
				continue
			}
			if _, err := w.Write(b); err != nil {
				gone()
				return
			}
			written += len(b)
			dry.Reset(playedOut())
		case <-dry.C:
			if !buffering && playedOut() <= 0 {
				buffering = true
				st.rebuffers.Add(1)
				wait.Reset(rebufferWait)
				ring("thinking")
			}
		case <-wait.C:
			if buffering && pendingN > 0 && !resume() {
				gone()
				return
			}
			if buffering {
				wait.Reset(rebufferWait)
			}
		case <-st.done:
			gone()
			return
		}
	}
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
		"wall_secs", time.Since(st.start).Round(100*time.Millisecond).Seconds(),
		"max_gap_ms", st.maxGap.Milliseconds(), "rebuffers", st.rebuffers.Load(),
		"peak_dbfs", ls.PeakDBFS, "loud_rms_dbfs", ls.LoudDBFS)
	go func() {
		select {
		case <-st.done:
		case <-time.After(10 * time.Minute):
		}
		s.mu.Lock()
		idle := s.cur == nil
		follow := played && idle && s.follow
		s.follow = false
		s.mu.Unlock()
		switch {
		case !idle:
		case played && !follow && !st.announce:
			s.a.moment("voice", "dialog_end", dialogEndWink) // the dialog is over: a wink (blink), then the clock
		default:
			s.a.unexpress("voice")
		}
		if played {
			c.Send(wyoming.Event{Type: wyoming.TypePlayed})
		}
		if follow {
			time.Sleep(followDelay) // let the room's echo of the reply die down
			s.startListening(srcFollow)
		} else if played && st.announce {
			time.Sleep(followDelay)
			s.a.icListenIfRinging() // HA announced an incoming call («… принять?»)
		}
	}()
}

// satelliteDescription is what HA shows for the satellite: the board's model, when the profile
// names one.
func satelliteDescription(model string) string {
	if model == "" {
		return "yastationd on a de-Yandexed speaker"
	}
	return "yastationd on a de-Yandexed " + model
}

// voiceStage shows a pipeline stage (ring, front display) unless audio is already playing (speaking wins).
func (s *satellite) voiceStage(name string) {
	s.mu.Lock()
	playing := s.cur != nil
	s.mu.Unlock()
	ttl := stageTTL
	if name == "thinking" { // lasts the whole wait: HA has no timeout, ours cancels it
		ttl = s.a.cfg().ReplyTimeout
	}
	if !playing {
		s.a.moment("voice", name, ttl)
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
