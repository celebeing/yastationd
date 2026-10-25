package app

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/intercom"
)

// The intercom (docs/intercom.md): 1-to-1 calls to another speaker. Signalling rides MQTT
// (<prefix>/<id>/intercom/sig, signed with intercom_secret), the peer list is every speaker's
// retained <prefix>/<id>/intercom/info, media is UDP between the two. Here: wiring the call
// state machine to the mic (the VQE output), a low-latency playout, the ring, HA and voice.

const (
	icPlayRate     = 48000 // the playout runs at dmix's rate (see intercom.Upsampler3)
	icUplinkQueue  = 5     // 20 ms frames (~100 ms): older uplink audio is dropped
	icAssistDuck   = 0.3   // the far end's level while an Assist session or a reply runs in a call
	icCallTargetNA = "—"   // the target select's only option while no other speaker is known
	icToneGap      = 1500 * time.Millisecond
)

type icState struct {
	m    *intercom.Manager
	name string

	stateMu sync.Mutex // onIntercomState, one at a time and in order
	applied time.Time  // Since of the last status applied

	mu     sync.Mutex
	peers  map[string]intercom.Peer // by device id
	target string                   // the HA select's choice (device id)
	guard  *intercom.Guard          // of the current call
	stop   func()                   // ends the current call's audio

	toneStop  chan struct{} // the repeating ring tone
	wokeAt    time.Time     // «Алиса» during a call (mu): opens the command window
	wasUp     bool          // the last applied state was in_call (stateMu)
	callPulse display.RenderFunc[display.RingFrame]
}

type icInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
	V    int    `json:"v"`
}

func (a *App) newIntercom() *icState {
	c := a.cfg
	if !c.Intercom {
		return nil
	}
	switch {
	case c.IntercomSecret == "":
		a.log.Warn("intercom: off, intercom_secret is not set")
		return nil
	case c.MQTTHost == "":
		a.log.Warn("intercom: off, it needs the MQTT broker for signalling")
		return nil
	}
	ic := &icState{name: c.IntercomName, peers: map[string]intercom.Peer{},
		callPulse: display.Pulse(display.Blue, 2500*time.Millisecond)}
	if ic.name == "" {
		ic.name = c.DeviceName
	}
	a.ic = ic // intercomOptions reads the name
	ic.m = intercom.NewManager(intercomOptions(a, func(to string, payload []byte) {
		if a.mqtt != nil {
			a.mqtt.PublishEvent(a.topics.IntercomSig(to), string(payload))
		}
	}))
	return ic
}

func intercomOptions(a *App, send func(to string, payload []byte)) intercom.Options {
	c := a.cfg
	return intercom.Options{
		ID: c.DeviceID, Name: a.ic.name, Secret: c.IntercomSecret, Port: c.IntercomPort,
		JitterMs: c.IntercomJitterMs, RingTimeout: c.IntercomRing, MaxDuration: c.IntercomMax,
		AutoAnswer: c.IntercomAutoAnswer,
		LocalIP:    func() string { return localIPTowards(c.MQTTHost, c.MQTTPort) },
		Send:       send,
		Policy:     a.intercomPolicy,
		OnChange:   a.onIntercomState,
		Log:        a.log,
	}
}

// localIPTowards is our source address on the route to host (the LAN address peers reach us
// on); no packet is sent.
func localIPTowards(host string, port int) string {
	conn, err := net.Dial("udp4", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// intercomPolicy decides on an incoming call (under the Manager's lock: no calls back into it).
func (a *App) intercomPolicy(from string) string {
	switch {
	case a.setup != nil && a.setup.Active():
		return intercom.ReasonBusy
	case a.micMuted():
		return intercom.ReasonOff
	}
	return ""
}

// icActive: a call is ringing, being placed or up.
func (a *App) icActive() bool { return a.ic != nil && a.ic.m.Status().State != intercom.Idle }

func (a *App) icInCall() bool { return a.ic != nil && a.ic.m.Status().State == intercom.InCall }

// ---- MQTT ----

func (a *App) intercomSubscribe() {
	t := a.topics
	if a.ic == nil {
		a.mqtt.Publish(t.IntercomInfo(), "") // clear a stale entry from when it was on
		return
	}
	info, _ := json.Marshal(icInfo{ID: a.cfg.DeviceID, Name: a.ic.name, Port: a.cfg.IntercomPort, V: 1})
	a.mqtt.Publish(t.IntercomInfo(), string(info))
	a.mqtt.Subscribe(t.IntercomSig(a.cfg.DeviceID), a.ic.m.Handle)
	a.mqtt.SubscribeTopic(t.IntercomInfoAll(), a.onIntercomInfo)
	a.mqtt.Subscribe(t.Command("intercom"), a.cmdIntercom)
	a.mqtt.Subscribe(t.IntercomCall(), a.cmdIntercomCall)
	a.mqtt.Subscribe(t.Command("intercom_target"), a.cmdIntercomTarget)
	a.mqtt.Subscribe(t.Command("intercom_call"), func([]byte) { a.icCallOrAnswer("", "HA") })
	a.mqtt.Subscribe(t.Command("intercom_hangup"), func([]byte) { a.ic.m.Hangup("HA") })
	a.publishIntercom(a.ic.m.Status())
}

// onIntercomInfo tracks the other speakers: a retained info per speaker, an empty one when it
// turns the intercom off (or a test peer leaves).
func (a *App) onIntercomInfo(topic string, payload []byte) {
	id := a.topics.IntercomInfoID(topic)
	if id == "" || id == a.cfg.DeviceID {
		return
	}
	var in icInfo
	if len(payload) > 0 && (json.Unmarshal(payload, &in) != nil || in.ID != id || in.V != 1) {
		return
	}
	ic := a.ic
	ic.mu.Lock()
	changed := false
	if p, ok := ic.peers[id]; len(payload) == 0 && ok {
		delete(ic.peers, id)
		if ic.target == id {
			ic.target = ""
		}
		changed = true
	} else if len(payload) > 0 && (!ok || p.Name != in.Name) {
		ic.peers[id] = intercom.Peer{ID: id, Name: in.Name}
		changed = true
	}
	ic.mu.Unlock()
	if changed {
		a.log.Info("intercom: peer", "id", id, "name", in.Name, "gone", len(payload) == 0)
		a.publishIntercomDiscovery()
	}
}

// icPeers lists the other speakers, by name.
func (a *App) icPeers() []intercom.Peer {
	a.ic.mu.Lock()
	defer a.ic.mu.Unlock()
	ps := make([]intercom.Peer, 0, len(a.ic.peers))
	for _, p := range a.ic.peers {
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps
}

func (a *App) icPeerNames() []string {
	var names []string
	for _, p := range a.icPeers() {
		names = append(names, p.Name)
	}
	return names
}

// icFindPeer resolves a device id or a room name.
func (a *App) icFindPeer(s string) (intercom.Peer, bool) {
	for _, p := range a.icPeers() {
		if p.ID == s || strings.EqualFold(p.Name, s) {
			return p, true
		}
	}
	return intercom.Peer{}, false
}

func (a *App) publishIntercomDiscovery() {
	if a.mqtt == nil {
		return
	}
	a.builder.SetIntercomPeers(a.icPeerNames())
	a.builder.Publish(a.mqtt)
	peers := a.icPeers()
	a.ic.mu.Lock()
	if _, ok := a.ic.peers[a.ic.target]; !ok && len(peers) > 0 {
		a.ic.target = peers[0].ID // a target from the start: the call button works at once
	}
	target := a.ic.peers[a.ic.target].Name
	a.ic.mu.Unlock()
	if target == "" {
		target = icCallTargetNA
	}
	a.mqtt.Publish(a.topics.State("intercom_target"), target)
}

// cmdIntercom: {"action":"call","to":"<device id or room name>"}, {"action":"answer"},
// {"action":"hangup"} — for scripts and automations.
func (a *App) cmdIntercom(payload []byte) {
	var c struct{ Action, To string }
	if err := json.Unmarshal(payload, &c); err != nil {
		a.log.Warn("intercom: bad command", "err", err)
		return
	}
	switch c.Action {
	case "call":
		a.icCallOrAnswer(c.To, "HA")
	case "answer":
		if err := a.ic.m.Answer(); err != nil {
			a.log.Info("intercom: answer", "err", err)
		}
	case "hangup":
		a.ic.m.Hangup("HA")
	default:
		a.log.Warn("intercom: unknown action", "action", c.Action)
	}
}

func (a *App) cmdIntercomTarget(payload []byte) {
	p, ok := a.icFindPeer(string(payload))
	if !ok {
		return
	}
	a.ic.mu.Lock()
	a.ic.target = p.ID
	a.ic.mu.Unlock()
	a.mqtt.Publish(a.topics.State("intercom_target"), p.Name)
}

// icCallOrAnswer answers a ringing call, or calls `to` (a device id or name; "" = the HA
// select's target).
func (a *App) icCallOrAnswer(to, src string) {
	if a.ic.m.Status().State == intercom.Ringing {
		if err := a.ic.m.Answer(); err != nil {
			a.log.Info("intercom: answer", "err", err)
		}
		return
	}
	if to == "" {
		a.ic.mu.Lock()
		to = a.ic.target
		a.ic.mu.Unlock()
	}
	p, ok := a.icFindPeer(to)
	if !ok {
		a.log.Info("intercom: unknown callee", "to", to, "src", src)
		a.express("voice", "error", errorTTL)
		return
	}
	a.icCall(p, src)
}

func (a *App) icCall(p intercom.Peer, src string) {
	if err := a.ic.m.Call(p.ID, p.Name); err != nil {
		a.log.Info("intercom: call", "to", p.ID, "src", src, "err", err)
		a.express("voice", "error", errorTTL)
	}
}

func (a *App) publishIntercom(st intercom.Status) {
	if a.mqtt == nil {
		return
	}
	a.mqtt.Publish(a.topics.State("intercom"), string(st.State))
	attr, _ := json.Marshal(map[string]any{
		"peer": st.Peer, "peer_name": st.PeerName, "incoming": st.Incoming,
		"since": st.Since.Format(time.RFC3339), "ended": st.Ended,
	})
	a.mqtt.Publish(a.topics.State("intercom_attr"), string(attr))
}

// ---- states: the ring, the audio ----

func (a *App) onIntercomState(st intercom.Status) {
	// The Manager reports from several goroutines (MQTT, timers, the media watchdog); a status
	// overtaken by a newer one is dropped, so a late "in call" can't start audio after "idle".
	a.ic.stateMu.Lock()
	defer a.ic.stateMu.Unlock()
	if st.Since.Before(a.ic.applied) {
		return
	}
	a.ic.applied = st.Since
	a.log.Info("intercom: state", "state", st.State, "peer", st.Peer, "ended", st.Ended)
	a.publishIntercom(st)
	a.icStopTone()
	if st.State != intercom.Idle && a.stopVideo("intercom") {
		a.publishDisplayMode()
	}
	switch st.State {
	case intercom.Calling:
		a.ringStack.Push("intercom", display.PrioCall, 0, display.Comet(display.Green, 1200*time.Millisecond))
		a.icFront(a.icClip("call_outgoing"))
		a.icTone("calls_ringing_outgoing")
	case intercom.Ringing:
		blink := display.Blink(display.Green, time.Second)
		a.ringStack.Push("intercom", display.PrioCall, 0, func(now time.Time, below display.RingFrame) (display.RingFrame, bool) {
			if a.icWindow() { // «Алиса» heard: show that it listens for the command
				return below, false
			}
			return blink(now, below)
		})
		a.icFront(a.icClip("call_incoming"))
		if !a.icTone("calls_ringing_incoming") {
			a.icTone("") // the announce chime, repeated
		}
	case intercom.InCall:
		a.ringStack.Push("intercom", display.PrioCall, 0, a.icInCallRing)
		a.icFront(a.icInCallFront(time.Now()))
		if !a.icSound("calls_connected") {
			a.announceChime()
		}
		a.icStartAudio()
	case intercom.Idle:
		wasUp := a.ic.wasUp
		a.icStopAudio()
		a.ringStack.Remove("intercom")
		a.matrixStack.Remove("intercom")
		switch {
		case strings.HasPrefix(st.Ended, "rejected"), st.Ended == "no answer", strings.HasPrefix(st.Ended, "media error"):
			if !a.icSound("calls_busy") {
				a.icSound("calls_ended")
			}
			a.express("voice", "error", errorTTL)
		case st.Ended == "missed":
		default:
			if wasUp {
				a.icSound("calls_ended")
			}
			a.ringStack.Push("intercom-end", display.PrioCall, 600*time.Millisecond, display.Flash(display.White, 500*time.Millisecond))
		}
	}
	a.ic.wasUp = st.State == intercom.InCall
}

// The call's front-display layer sits above the expressions (an Assist session in the call shows
// its stages on the ring) and below the local reflexes, so the volume bubble stays readable.
const prioCallFront = display.PrioExpression + 2

func (a *App) icFront(r display.RenderFunc[display.FrontFrame]) {
	if r == nil {
		a.matrixStack.Remove("intercom")
		return
	}
	a.matrixStack.Push("intercom", prioCallFront, 0, r)
}

// icClip is the front display's calling animation ("call_incoming" / "call_outgoing": the stock
// "ringing" clip on seg7, the screen's call patterns on a pixel screen), looped; nil without one.
func (a *App) icClip(name string) display.RenderFunc[display.FrontFrame] {
	if !a.frontClips() {
		return nil
	}
	c, ok := a.front.Anim(name)
	if !ok {
		return nil
	}
	return c.Play(true, false, a.brightness)
}

// icInCallFront shows the call's duration (m:ss).
func (a *App) icInCallFront(start time.Time) display.RenderFunc[display.FrontFrame] {
	if a.front == nil {
		return nil
	}
	return func(now time.Time, _ display.FrontFrame) (display.FrontFrame, bool) {
		d := now.Sub(start)
		m, sec := int(d.Minutes()), int(d.Seconds())%60
		if m > 99 {
			m = 99
		}
		return a.front.Text(fmt.Sprintf("%2d:%02d", m, sec), a.brightness()), false
	}
}

// icInCallRing: a slow blue breath while the mic streams to the other room (blue is no other
// state's colour) — except under an Assist session in the call, whose stages show instead.
func (a *App) icInCallRing(now time.Time, below display.RingFrame) (display.RingFrame, bool) {
	if a.sat.inDialog() || a.icWindow() {
		return below, false
	}
	return a.ic.callPulse(now, below)
}

// icSoundPath is a decoded vendor sound (make sounds) under <cfw>/sounds; "" when not there.
func (a *App) icSoundPath(name string) string {
	p := a.cfg.Path(a.prof.Platform.CFWRoot + "/sounds/" + name + ".wav")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// icSound plays a call sound beside everything else (a blip); false when it isn't there.
func (a *App) icSound(name string) bool {
	p := a.icSoundPath(name)
	if p == "" {
		return false
	}
	a.unmute()
	if err := a.player.Blip(p); err != nil {
		a.log.Warn("intercom: sound", "name", name, "err", err)
		return false
	}
	return true
}

// icTone repeats a ringing sound (name "" = the announce chime) until icStopTone; false when it
// isn't there.
func (a *App) icTone(name string) bool {
	path := a.prof.Audio.AnnounceSound
	if name != "" {
		path = a.icSoundPath(name)
	} else if path != "" {
		path = a.cfg.Path(path)
	}
	if path == "" {
		return false
	}
	d, err := wavDuration(path)
	if err != nil {
		return false
	}
	stop := make(chan struct{})
	a.ic.mu.Lock()
	a.ic.toneStop = stop
	a.ic.mu.Unlock()
	go func() {
		for {
			a.unmute()
			a.player.Blip(path)
			select {
			case <-stop:
				return
			case <-time.After(d + icToneGap):
			}
		}
	}()
	return true
}

func (a *App) icStopTone() {
	a.ic.mu.Lock()
	stop := a.ic.toneStop
	a.ic.toneStop = nil
	a.ic.mu.Unlock()
	if stop != nil {
		close(stop)
		a.player.StopBlip() // the ringtone playing right now, too (it lasts ~12 s)
	}
}

// icListenIfRinging opens a session for the answer (also after HA's announcement ends).
func (a *App) icListenIfRinging() {
	if a.ic == nil || a.ic.m.Status().State != intercom.Ringing || a.sat.inDialog() || a.playing.Load() {
		return
	}
	if a.cfg.WyomingPort > 0 {
		a.sat.startListening(srcFollow)
	}
}

func (a *App) icStartAudio() {
	ic := a.ic
	// The uplink: the VQE in its call mode (neural NR + AGC on vqe_gain; vqe_0, the wake word's
	// input, stays as it is) when this unit has it, else vqe_0 through our own gain and gate.
	// The switch takes effect when spotter-host reports it (the first one loads the model), so
	// both streams are taken and each frame comes from the one that fits the mode right now.
	callMode := a.spot != nil && a.spot.Running() && a.spot.CallMode(true)
	g := &intercom.Guard{UpGainDB: a.cfg.IntercomGainDB}
	out, err := hw.StartLiveOut(a.prof.Audio.Playback, icPlayRate)
	if err != nil {
		a.log.Error("intercom: playout", "err", err)
		ic.m.Hangup("playout failed")
		return
	}
	a.unmute()
	quit := make(chan struct{})
	type upFrame struct {
		f         []int16
		processed bool // the VQE call mode's audio
	}
	frames := make(chan upFrame, icUplinkQueue)
	push := func(f upFrame) { // the capture goroutines: never block
		select {
		case frames <- f:
		default:
			select { // drop the oldest: latency over completeness
			case <-frames:
			default:
			}
			frames <- f
		}
	}
	var fr, frCall intercom.Framer
	inCallMode := func() bool { return callMode && a.spot.InCallMode() }
	feed := func(b []byte) {
		if inCallMode() {
			return
		}
		fr.Push(b, func(f []int16) { push(upFrame{f: f}) })
	}
	feedCall := func(b []byte) {
		if !inCallMode() {
			return
		}
		frCall.Push(b, func(f []int16) { push(upFrame{f: f, processed: true}) })
	}
	// Testing (intercom_record_dir): the uplink before the guard (S16LE 16 kHz mono), and the raw
	// mic-array capture as arecord gives it (to replay it through the VQE in other modes).
	var rec, recMics *os.File
	unsubMics := func() {}
	if d := a.cfg.IntercomRecordDir; d != "" {
		stamp := time.Now().UTC().Format("20060102-150405")
		if rec, err = os.Create(filepath.Join(d, "intercom-up-"+stamp+".raw")); err != nil {
			a.log.Warn("intercom: record", "err", err)
		}
		if recMics, err = os.Create(filepath.Join(d, "intercom-mics-"+stamp+".raw")); err != nil {
			a.log.Warn("intercom: record", "err", err)
		} else {
			mics := make(chan []byte, 64)
			unsubRaw := a.mic.SubscribeRaw(func(b []byte) {
				select {
				case mics <- append([]byte(nil), b...):
				default:
				}
			})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for b := range mics {
					recMics.Write(b)
				}
				recMics.Close()
			}()
			unsubMics = func() { unsubRaw(); close(mics); <-done }
		}
		a.log.Info("intercom: recording", "dir", d, "stamp", stamp)
	}
	var unsub func()
	switch {
	case a.spot != nil && a.spot.Running():
		unsubVQE := a.spot.SubscribeVQE(0, feed) // echo-cancelled against our playback
		unsubCall := func() {}
		if callMode {
			unsubCall = a.spot.SubscribeCall(feedCall)
		} else {
			a.log.Info("intercom: no VQE call mode (an older spotter-host), uplink from vqe_0")
		}
		unsub = func() { unsubVQE(); unsubCall() }
		go func() { // the mode this call ended up with, for the log
			time.Sleep(3 * time.Second)
			a.log.Info("intercom: uplink source", "vqe_call_mode", inCallMode())
		}()
	default:
		a.log.Warn("intercom: no spotter-host, so no echo cancellation: expect echo")
		unsub = a.mic.Subscribe(feed)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // uplink
		defer wg.Done()
		report := time.NewTicker(5 * time.Second)
		defer report.Stop()
		logAGC := func(msg string) {
			s := g.Uplink()
			a.log.Info(msg, "floor_dbfs", s.FloorDB, "open_frames", s.OpenFrames, "frames", s.Frames)
		}
		for {
			select {
			case <-report.C:
				logAGC("intercom: uplink")
			case <-quit:
				logAGC("intercom: uplink at the end")
				if rec != nil {
					rec.Close()
				}
				return
			case uf := <-frames:
				f := uf.f
				g.Processed = uf.processed            // Guard.Up runs only here
				if a.micMuted() || a.sat.inDialog() { // the hardware mute; or talking to Alice
					clear(f)
				}
				if rec != nil {
					b := make([]byte, 2*len(f))
					for i, v := range f {
						b[2*i], b[2*i+1] = byte(v), byte(v>>8)
					}
					rec.Write(b)
				}
				g.Up(f, time.Now())
				ic.m.Uplink(f)
			}
		}
	}()
	go func() { // playout, paced by aplay's blocking write
		defer wg.Done()
		var up intercom.Upsampler3
		for {
			select {
			case <-quit:
				return
			default:
			}
			f := ic.m.Downlink()
			if f == nil {
				return
			}
			g.Far(f, time.Now())
			level := 1.0
			if a.sat.inDialog() || a.playing.Load() {
				level = icAssistDuck
			}
			g.Down(f, level)
			if _, err := out.Write(up.Bytes(f)); err != nil {
				select {
				case <-quit: // closed under us: the call is over
					return
				default:
				}
				a.log.Warn("intercom: playout ended", "err", err)
				go ic.m.Hangup("playout failed")
				return
			}
		}
	}()
	ic.mu.Lock()
	ic.guard = g
	ic.stop = func() {
		unsub()
		if a.spot != nil {
			a.spot.CallMode(false) // back to ASR, whatever the switch on did
		}
		unsubMics()
		close(quit)
		out.Close() // unblocks a pending write
		wg.Wait()
	}
	ic.mu.Unlock()
}

func (a *App) icStopAudio() {
	ic := a.ic
	ic.mu.Lock()
	stop := ic.stop
	ic.stop, ic.guard = nil, nil
	ic.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// ---- voice and keys ----

// icVoiceKey: the voice key answers a ringing call and ends any other; false = not ours.
func (a *App) icVoiceKey() bool {
	if a.ic == nil {
		return false
	}
	switch a.ic.m.Status().State {
	case intercom.Ringing:
		a.ic.m.Answer()
		return true
	case intercom.Calling, intercom.InCall:
		a.ic.m.Hangup("voice key")
		return true
	}
	return false
}

// icHeardWindow: how recently this speaker must have sent HA a request to take a call command
// on the shared topic (the LLM and the script take a few seconds).
const icHeardWindow = 60 * time.Second

// cmdIntercomCall: HA's "call a room" script ({"to":"<device id or room name>"} on the shared
// topic). Every speaker gets it; the one that sent HA the request just now places the call —
// the script doesn't know which satellite asked.
func (a *App) cmdIntercomCall(payload []byte) {
	var c struct{ To string }
	if err := json.Unmarshal(payload, &c); err != nil || c.To == "" {
		a.log.Warn("intercom: bad call request", "payload", string(payload))
		return
	}
	heard := a.sat.lastHeard.Load()
	if heard == 0 || time.Since(time.Unix(0, heard)) > icHeardWindow {
		return // someone else's request
	}
	a.sat.lastHeard.Store(0) // one call per request
	a.log.Info("intercom: call requested by HA", "to", c.To)
	a.icCallOrAnswer(c.To, "HA voice")
}

// icRinging: an incoming call is waiting for an answer.
func (a *App) icRinging() bool { return a.ic != nil && a.ic.m.Status().State == intercom.Ringing }

// icWoke opens the command window after «Алиса» during a call (see icCommand).
func (a *App) icWoke() {
	a.ic.mu.Lock()
	a.ic.wokeAt = time.Now()
	a.ic.mu.Unlock()
	a.express("voice", "listening", icWakeCommandWindow)
}

// icWindow: «Алиса» was heard during this call just now.
func (a *App) icWindow() bool {
	a.ic.mu.Lock()
	defer a.ic.mu.Unlock()
	return time.Since(a.ic.wokeAt) < icWakeCommandWindow
}

const icWakeCommandWindow = 4 * time.Second

// icCommand: voice control of a call, all on the speaker's own spotters (the vendor command
// model has no «да»/«нет»; docs/intercom.md, "Voice control"). false = not the intercom's.
//   - ringing: «продолжи / играй» answer, «стоп / хватит / пауза» decline — with «Алиса» before
//     them or without;
//   - in a call, only right after «Алиса»: «стоп / хватит / пауза» hang up, «громче / тише»
//     set the volume. Without the wake word they are ignored (they come up in conversation).
//     No guard against the far end's voice: the AEC takes it below the room's floor on the
//     spotters' input (measured), and a guard on "the far end just spoke" blocked our own
//     «Алиса» whenever the other room talked.
func (a *App) icCommand(phrase string) bool {
	if a.ic == nil {
		return false
	}
	p := strings.ToLower(phrase)
	st := a.ic.m.Status().State
	if st == intercom.Ringing || st == intercom.InCall {
		a.log.Info("intercom: command heard", "phrase", phrase, "state", st, "window", a.icWindow())
	}
	switch st {
	case intercom.Ringing:
		switch {
		case intercom.IsAnswerCommand(p):
			a.log.Info("intercom: answered by voice", "phrase", phrase)
			a.unexpress("voice")
			a.ic.m.Answer()
		case intercom.IsDeclineCommand(p):
			a.log.Info("intercom: declined by voice", "phrase", phrase)
			a.unexpress("voice")
			a.ic.m.Hangup("declined by voice")
		}
		return true
	case intercom.InCall:
		if !a.icWindow() {
			a.log.Debug("intercom: command without «Алиса», ignored", "phrase", phrase)
			return true
		}
		switch {
		case p == "громче":
			a.nudgeVolume(+2 * a.cfg.VolumeStep)
		case p == "тише":
			a.nudgeVolume(-2 * a.cfg.VolumeStep)
		case intercom.IsDeclineCommand(p):
			a.log.Info("intercom: hung up by voice", "phrase", phrase)
			a.ic.m.Hangup("by voice")
		default:
			return true
		}
		a.ic.mu.Lock()
		a.ic.wokeAt = time.Time{} // one command per «Алиса»
		a.ic.mu.Unlock()
		a.unexpress("voice")
		return true
	}
	return false
}
