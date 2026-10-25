package intercom

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
)

// State of the one call a speaker can be in.
type State string

const (
	Idle    State = "idle"
	Calling State = "calling" // our invite is out
	Ringing State = "ringing" // an invite is waiting for our answer
	InCall  State = "in_call"
)

// Status is what the daemon shows and publishes.
type Status struct {
	State    State
	Peer     string // device id
	PeerName string // its room name
	Incoming bool
	Since    time.Time
	Ended    string // why the last call ended (set when State goes back to Idle)
}

// Options configure a Manager.
type Options struct {
	ID     string // this speaker's device id
	Name   string // its room name, sent with invites
	Secret string
	Port   int // UDP media port
	// Jitter buffer target.
	JitterMs     int
	RingTimeout  time.Duration // unanswered invite
	MaxDuration  time.Duration // hard cap of a call (0 = none)
	MediaTimeout time.Duration // no packet from the peer for this long ends the call
	AutoAnswer   bool

	// LocalIP is our address as the peer reaches it.
	LocalIP func() string
	// Send hands a signed message to the transport, addressed to device id `to`.
	Send func(to string, payload []byte)
	// Policy decides on an incoming call: "" lets it ring, anything else is the reject reason.
	// It runs under the Manager's lock: it must not call back into the Manager.
	Policy func(from string) string
	// OnChange gets every status change (called without the Manager's lock held).
	OnChange func(Status)
	Log      *slog.Logger
	Now      func() time.Time
}

// Manager runs the signalling state machine and owns the media of the current call.
type Manager struct {
	o Options
	v *verifier

	mu sync.Mutex
	st Status
	c  *call
}

type call struct {
	id               string
	peer, peerName   string
	incoming         bool
	callerNonce      string
	calleeNonce      string
	mySSRC, peerSSRC uint32
	peerAddr         string
	peerPort         int
	conn             *net.UDPConn
	sess             *session
	timer            *time.Timer // ring timeout, then the max duration
	stopWatch        chan struct{}
}

var (
	ErrBusy     = errors.New("intercom: a call is already on")
	ErrNoCall   = errors.New("intercom: no call to answer")
	ErrSelfCall = errors.New("intercom: can't call ourselves")
)

func NewManager(o Options) *Manager {
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RingTimeout <= 0 {
		o.RingTimeout = 30 * time.Second
	}
	if o.MediaTimeout <= 0 {
		o.MediaTimeout = 5 * time.Second
	}
	if o.Policy == nil {
		o.Policy = func(string) string { return "" }
	}
	return &Manager{o: o, v: newVerifier(o.Secret), st: Status{State: Idle}}
}

// Status is the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// Call invites peer `to` (a device id); toName is only for the status.
func (m *Manager) Call(to, toName string) error {
	if to == m.o.ID {
		return ErrSelfCall
	}
	m.mu.Lock()
	if m.st.State != Idle {
		m.mu.Unlock()
		return ErrBusy
	}
	conn, err := listenUDP(m.o.Port)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	c := &call{id: randHex(8), peer: to, peerName: toName, mySSRC: randSSRC(0), conn: conn}
	inv := Msg{T: MsgInvite, Call: c.id, Addr: m.o.LocalIP(), Port: m.o.Port, SSRC: c.mySSRC}
	c.callerNonce = m.sendLocked(c, &inv)
	m.c = c
	c.timer = time.AfterFunc(m.o.RingTimeout, func() { m.timeout(c) })
	st := m.setLocked(Status{State: Calling, Peer: to, PeerName: toName})
	m.mu.Unlock()
	m.o.Log.Info("intercom: calling", "to", to)
	m.notify(st)
	return nil
}

// Answer takes the ringing call.
func (m *Manager) Answer() error {
	m.mu.Lock()
	if m.st.State != Ringing {
		m.mu.Unlock()
		return ErrNoCall
	}
	st, err := m.answerLocked()
	m.mu.Unlock()
	m.notify(st)
	return err
}

// answerLocked accepts m.c (ringing); m.mu held.
func (m *Manager) answerLocked() (Status, error) {
	c := m.c
	conn, err := listenUDP(m.o.Port)
	if err != nil {
		return m.endLocked("media error: "+err.Error(), MsgReject, ReasonOff), err
	}
	c.conn = conn
	c.mySSRC = randSSRC(c.peerSSRC)
	acc := Msg{T: MsgAccept, Call: c.id, Addr: m.o.LocalIP(), Port: m.o.Port, SSRC: c.mySSRC}
	c.calleeNonce = m.sendLocked(c, &acc)
	return m.startMediaLocked()
}

// Hangup ends whatever is going on: cancels our invite, declines a ringing one, ends a call.
func (m *Manager) Hangup(why string) {
	m.mu.Lock()
	var st Status
	switch m.st.State {
	case Idle:
		m.mu.Unlock()
		return
	case Ringing:
		st = m.endLocked(why, MsgReject, ReasonDeclined)
	default:
		st = m.endLocked(why, MsgBye, "")
	}
	m.mu.Unlock()
	m.notify(st)
}

// Handle takes a message from the transport (addressed to us).
func (m *Manager) Handle(payload []byte) {
	msg, err := m.v.parse(payload, m.o.Now())
	if err != nil {
		m.o.Log.Warn("intercom: message refused", "err", err, "from", msg.From)
		return
	}
	if msg.To != m.o.ID || msg.From == m.o.ID {
		return
	}
	m.mu.Lock()
	st, changed := m.handleLocked(msg)
	m.mu.Unlock()
	if changed {
		m.notify(st)
	}
}

func (m *Manager) handleLocked(msg Msg) (Status, bool) {
	c := m.c
	ours := c != nil && msg.Call == c.id && msg.From == c.peer
	switch msg.T {
	case MsgInvite:
		switch {
		case m.st.State == Idle:
			return m.ringLocked(msg)
		case m.st.State == Calling && c.peer == msg.From:
			// Both called each other at once: the invite from the lower id wins.
			if msg.From < m.o.ID {
				c.timer.Stop()
				c.conn.Close()
				m.c = nil
				st := m.setLocked(Status{State: Idle, Ended: "crossed calls"})
				if rst, ok := m.ringLocked(msg); ok {
					st = rst
				}
				if m.st.State == Ringing { // we wanted this call anyway
					st, _ = m.answerLocked()
				}
				return st, true
			}
			return Status{}, false // ours wins; they yield
		default:
			m.reply(msg, MsgReject, ReasonBusy)
			return Status{}, false
		}
	case MsgAccept:
		if !ours || m.st.State != Calling {
			return Status{}, false
		}
		c.calleeNonce, c.peerAddr, c.peerPort, c.peerSSRC = msg.Nonce, msg.Addr, msg.Port, msg.SSRC
		st, _ := m.startMediaLocked()
		return st, true
	case MsgReject:
		if !ours || m.st.State != Calling {
			return Status{}, false
		}
		return m.endLocked("rejected: "+msg.Reason, "", ""), true
	case MsgBye:
		if !ours {
			return Status{}, false
		}
		why := "hung up by the peer"
		if m.st.State == Ringing {
			why = "missed"
		}
		return m.endLocked(why, "", ""), true
	}
	return Status{}, false
}

// ringLocked turns an invite into a ringing call (or rejects it per policy).
func (m *Manager) ringLocked(msg Msg) (Status, bool) {
	if msg.Addr == "" || msg.Port <= 0 || msg.SSRC == 0 {
		return Status{}, false
	}
	if why := m.o.Policy(msg.From); why != "" {
		m.reply(msg, MsgReject, why)
		return Status{}, false
	}
	c := &call{id: msg.Call, peer: msg.From, peerName: msg.Name, incoming: true,
		callerNonce: msg.Nonce, peerAddr: msg.Addr, peerPort: msg.Port, peerSSRC: msg.SSRC}
	m.c = c
	// A little longer than the caller waits: its bye normally ends the ringing first.
	c.timer = time.AfterFunc(m.o.RingTimeout+5*time.Second, func() { m.timeout(c) })
	st := m.setLocked(Status{State: Ringing, Peer: c.peer, PeerName: c.peerName, Incoming: true})
	m.o.Log.Info("intercom: incoming call", "from", c.peer, "name", c.peerName)
	if m.o.AutoAnswer {
		st, _ = m.answerLocked()
	}
	return st, true
}

// startMediaLocked opens the session once both nonces and endpoints are known.
func (m *Manager) startMediaLocked() (Status, error) {
	c := m.c
	key := callKey(m.o.Secret, c.id, c.callerNonce, c.calleeNonce)
	sess, err := newSession(c.conn, c.peerAddr, c.peerPort, key, c.mySSRC, c.peerSSRC, m.o.JitterMs)
	if err != nil {
		return m.endLocked("media error: "+err.Error(), MsgBye, ""), err
	}
	c.sess = sess
	c.timer.Stop()
	if m.o.MaxDuration > 0 {
		c.timer = time.AfterFunc(m.o.MaxDuration, func() { m.expire(c, "maximum duration") })
	}
	c.stopWatch = make(chan struct{})
	go m.watch(c)
	m.o.Log.Info("intercom: call up", "peer", c.peer, "addr", c.peerAddr)
	return m.setLocked(Status{State: InCall, Peer: c.peer, PeerName: c.peerName, Incoming: c.incoming}), nil
}

// watch ends the call when the peer's media stops (a lost bye, a peer that died).
func (m *Manager) watch(c *call) {
	t := time.NewTicker(time.Second / 2)
	defer t.Stop()
	for {
		select {
		case <-c.stopWatch:
			return
		case <-t.C:
			if time.Since(c.sess.heard()) > m.o.MediaTimeout {
				m.expire(c, "no audio from the peer")
				return
			}
		}
	}
}

func (m *Manager) timeout(c *call) {
	m.mu.Lock()
	if m.c != c {
		m.mu.Unlock()
		return
	}
	var st Status
	if m.st.State == Calling {
		st = m.endLocked("no answer", MsgBye, "")
	} else {
		st = m.endLocked("missed", "", "")
	}
	m.mu.Unlock()
	m.notify(st)
}

func (m *Manager) expire(c *call, why string) {
	m.mu.Lock()
	if m.c != c {
		m.mu.Unlock()
		return
	}
	st := m.endLocked(why, MsgBye, "")
	m.mu.Unlock()
	m.notify(st)
}

// endLocked tears the call down, optionally telling the peer (t = bye or reject).
func (m *Manager) endLocked(why, t, reason string) Status {
	c := m.c
	if c != nil {
		if t != "" {
			out := Msg{T: t, Call: c.id, Reason: reason}
			m.sendLocked(c, &out)
		}
		if c.timer != nil {
			c.timer.Stop()
		}
		if c.stopWatch != nil {
			close(c.stopWatch)
		}
		if c.sess != nil {
			js := c.sess.jb.Stats()
			m.o.Log.Info("intercom: call ended", "why", why, "secs", int(time.Since(c.sess.start).Seconds()),
				"sent", c.sess.sent.Load(), "played", js.Frames, "missing", js.Missing, "late", js.Late,
				"overflow", js.Overflow, "drift_drop", js.Dropped, "drift_insert", js.Inserted,
				"rebuffers", js.Rebuffers, "jitter_ms", js.TargetMs, "bad_packets", c.sess.bad.Load())
			c.sess.close()
		} else {
			m.o.Log.Info("intercom: call ended", "why", why)
			if c.conn != nil {
				c.conn.Close()
			}
		}
	}
	m.c = nil
	return m.setLocked(Status{State: Idle, Ended: why})
}

func (m *Manager) setLocked(st Status) Status {
	st.Since = m.o.Now()
	m.st = st
	return st
}

func (m *Manager) notify(st Status) {
	if st.State != "" && m.o.OnChange != nil {
		m.o.OnChange(st)
	}
}

// sendLocked signs msg for c's peer and sends it; it returns the nonce used.
func (m *Manager) sendLocked(c *call, msg *Msg) string {
	msg.From, msg.To, msg.Name = m.o.ID, c.peer, m.o.Name
	msg.Sign(m.o.Secret, m.o.Now())
	m.o.Send(c.peer, msg.Marshal())
	return msg.Nonce
}

// reply answers a message outside any call of ours (busy, policy).
func (m *Manager) reply(to Msg, t, reason string) {
	out := Msg{T: t, Call: to.Call, From: m.o.ID, To: to.From, Name: m.o.Name, Reason: reason}
	out.Sign(m.o.Secret, m.o.Now())
	m.o.Send(to.From, out.Marshal())
}

// Uplink sends one 20 ms frame of our audio (dropped when no call is up).
func (m *Manager) Uplink(frame []int16) {
	m.mu.Lock()
	var s *session
	if m.c != nil {
		s = m.c.sess
	}
	m.mu.Unlock()
	if s != nil {
		if err := s.send(frame); err != nil {
			m.o.Log.Debug("intercom: send", "err", err)
		}
	}
}

// Downlink returns the next frame to play, or nil when no call is up.
func (m *Manager) Downlink() []int16 {
	m.mu.Lock()
	var s *session
	if m.c != nil {
		s = m.c.sess
	}
	m.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.jb.Pop()
}
