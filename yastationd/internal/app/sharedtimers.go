package app

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/voiceid"
)

// Shared timers (docs/timers.md): a timer a known voice sets on one speaker also counts down and
// rings on that person's own speakers (their places in the People album). HA keeps the timer on
// the speaker that was asked (the owner); the owner tells the others over MQTT, a retained message
// per timer, and any of them stopping the ringing stops it everywhere.

// sharedTimer is the message (yastation/timers/<owner>/<id>, retained; empty = gone).
type sharedTimer struct {
	Owner    string   `json:"owner"`
	ID       string   `json:"id"`
	Person   string   `json:"person,omitempty"` // the voice ID person
	Name     string   `json:"name,omitempty"`
	Places   []string `json:"places"`
	State    string   `json:"state"` // running, paused, ringing, done
	Deadline int64    `json:"deadline_ms,omitempty"`
	Left     int64    `json:"left_ms,omitempty"` // when paused
	By       string   `json:"by,omitempty"`      // the speaker that sent this state
}

const (
	timerRunning = "running"
	timerPaused  = "paused"
	timerRinging = "ringing"
	timerDone    = "done"
)

// recentVoice: a voice verdict this recent is the request's (the early one comes before HA
// starts the timer, the final one about when it does).
const recentVoice = 30 * time.Second

type heardVoice struct {
	v  voiceid.Verdict
	at time.Time
}

// noteVerdict keeps the speaker's latest voice verdict (who asked, for a timer HA starts next).
func (a *App) noteVerdict(v voiceid.Verdict) {
	a.lastVoice.Store(&heardVoice{v: v, at: time.Now()})
}

// requester: the known person who spoke to this speaker within recentVoice, waiting up to wait
// for a verdict that is still on its way.
func (a *App) requester(wait time.Duration) (voiceid.Verdict, bool) {
	end := time.Now().Add(wait)
	for {
		if h := a.lastVoice.Load(); h != nil && time.Since(h.at) < recentVoice {
			if h.v.State == voiceid.Known {
				return h.v, true
			}
		}
		if time.Now().After(end) {
			return voiceid.Verdict{}, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// personPlaces: the speakers that are person id's own (the People album), without this one.
func (a *App) personPlaces(id string) []string {
	if a.vid == nil {
		return nil
	}
	var out []string
	for _, p := range a.vid.household() {
		if p.ID == id {
			for _, d := range p.Places {
				if d != a.cfg().DeviceID {
					out = append(out, d)
				}
			}
		}
	}
	return out
}

// shareTimer: an own timer's change (HA's timer event); started ones are shared when a known voice
// asked and that person has places.
func (a *App) shareTimer(id, state string, deadline time.Time, left time.Duration) {
	if !a.cfg().TimerFollow || a.mqtt == nil {
		return
	}
	t := &a.timers
	t.mu.Lock()
	st := t.owned[id]
	t.mu.Unlock()
	if st == nil {
		if state != timerRunning {
			return
		}
		go func() { // the verdict may still be on its way
			v, ok := a.requester(4 * time.Second)
			if !ok {
				return
			}
			places := a.personPlaces(v.ID)
			if len(places) == 0 {
				return
			}
			st := &sharedTimer{Owner: a.cfg().DeviceID, ID: id, Person: v.ID, Name: v.Name, Places: places}
			t.mu.Lock()
			if t.owned == nil {
				t.owned = map[string]*sharedTimer{}
			}
			if _, still := t.m[id]; !still { // cancelled or done meanwhile
				t.mu.Unlock()
				return
			}
			t.owned[id] = st
			t.mu.Unlock()
			a.log.Info("timer: shared", "id", id, "person", v.Name, "places", places)
			a.publishTimer(st, timerRunning, deadline, left)
		}()
		return
	}
	a.publishTimer(st, state, deadline, left)
	if state == timerDone {
		t.mu.Lock()
		delete(t.owned, id)
		t.mu.Unlock()
	}
}

func (a *App) publishTimer(st *sharedTimer, state string, deadline time.Time, left time.Duration) {
	m := *st
	m.State, m.By = state, a.cfg().DeviceID
	if !deadline.IsZero() {
		m.Deadline = deadline.UnixMilli()
	}
	m.Left = left.Milliseconds()
	b, _ := json.Marshal(m)
	topic := a.topics.Timer(st.Owner, st.ID)
	a.mqtt.Publish(topic, string(b))
	if state == timerDone && st.Owner == a.cfg().DeviceID {
		a.mqtt.Publish(topic, "") // gone: no retained timer left for a speaker that boots later
	}
}

// mirrorKey is a mirrored timer's key in timers.m (an own timer's is HA's id).
func mirrorKey(owner, id string) string { return "@" + owner + "/" + id }

// onSharedTimer: another speaker's shared timer (or a stop for one of ours).
func (a *App) onSharedTimer(_ string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	var m sharedTimer
	if json.Unmarshal(payload, &m) != nil || m.ID == "" {
		return
	}
	me := a.cfg().DeviceID
	if m.By == me {
		return // our own message coming back
	}
	t := &a.timers
	if m.Owner == me { // a stop from one of the places: our alarm too
		if m.State == timerDone {
			t.mu.Lock()
			_, ringing := t.ringing[m.ID]
			delete(t.owned, m.ID)
			t.mu.Unlock()
			if ringing {
				a.log.Info("timer: stopped on another speaker", "id", m.ID, "by", m.By)
				a.stopAlarmFrom(m.By)
			}
			if a.mqtt != nil {
				a.mqtt.Publish(a.topics.Timer(me, m.ID), "") // ours to clear
			}
		}
		return
	}
	if !slices.Contains(m.Places, me) {
		return
	}
	key := mirrorKey(m.Owner, m.ID)
	now := time.Now()
	t.mu.Lock()
	if t.m == nil {
		t.m = map[string]*voiceTimer{}
	}
	_, ringing := t.ringing[key]
	vt := t.m[key]
	switch m.State {
	case timerRunning:
		deadline := time.UnixMilli(m.Deadline)
		if deadline.Before(now.Add(-time.Minute)) { // long over: a stale retained message
			t.mu.Unlock()
			return
		}
		if vt == nil {
			a.log.Info("timer: following", "owner", m.Owner, "id", m.ID, "person", m.Name, "left", time.Until(deadline).Round(time.Second))
		}
		vt = &voiceTimer{deadline: deadline, active: true, shared: &m}
		t.m[key] = vt
		t.mu.Unlock()
		a.armMirror(key, vt)
	case timerPaused:
		t.m[key] = &voiceTimer{left: time.Duration(m.Left) * time.Millisecond, shared: &m}
		t.mu.Unlock()
	case timerRinging:
		if vt != nil {
			delete(t.m, key)
		}
		t.mu.Unlock()
		if vt != nil && !ringing { // the owner rang before our own deadline did
			a.ringShared(key, &m)
		}
	case timerDone:
		delete(t.m, key)
		t.mu.Unlock()
		if ringing {
			a.log.Info("timer: stopped on another speaker", "owner", m.Owner, "by", m.By)
			a.stopAlarmFrom(m.By)
		}
	default:
		t.mu.Unlock()
	}
	a.refreshTimerDisplay()
}

// armMirror rings a mirrored timer at its deadline, unless it changed meanwhile.
func (a *App) armMirror(key string, vt *voiceTimer) {
	time.AfterFunc(time.Until(vt.deadline), func() {
		t := &a.timers
		t.mu.Lock()
		if t.m[key] != vt { // updated, paused, cancelled, or rung already
			t.mu.Unlock()
			return
		}
		delete(t.m, key)
		t.mu.Unlock()
		a.refreshTimerDisplay()
		a.ringShared(key, vt.shared)
	})
}

func (a *App) ringShared(key string, m *sharedTimer) {
	a.log.Info("timer finished", "id", key, "owner", m.Owner, "person", m.Name)
	t := &a.timers
	t.mu.Lock()
	if t.ringing == nil {
		t.ringing = map[string]*sharedTimer{}
	}
	t.ringing[key] = m
	t.mu.Unlock()
	a.startAlarm()
}

// alarmStopped: the ringing ended here by a stop (key, voice, HA's command); the shared timers it
// rang for stop on every speaker. by != "": the stop came from that speaker (nothing to send).
func (a *App) alarmStopped(by string) {
	t := &a.timers
	t.mu.Lock()
	ringing := t.ringing
	t.ringing = nil
	t.mu.Unlock()
	if by != "" || a.mqtt == nil {
		return
	}
	me := a.cfg().DeviceID
	for _, m := range ringing {
		a.publishTimer(m, timerDone, time.Time{}, 0)
		if m.Owner == me {
			t.mu.Lock()
			delete(t.owned, m.ID)
			t.mu.Unlock()
		}
	}
}

// stopAlarmFrom silences the alarm for a stop another speaker sent.
func (a *App) stopAlarmFrom(by string) { a.stopAlarmBy(by) }
