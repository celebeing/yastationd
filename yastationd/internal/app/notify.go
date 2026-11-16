package app

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/alerts"
	"github.com/celebeing/yastationd/yastationd/internal/display"
)

// Notifications (docs/alerts.md): one-off events with a tone — good, info, bad — and no state:
// nothing stays on, nothing resolves. They come from HA (this speaker's Notification entity, or
// notify_topic for every speaker) and from Grafana (a rule labelled kind=notification; a resolve
// with a "resolved" annotation). The Notifications mode, apart from the alerts', decides: off
// ignores them, quiet fires notify_<tone> without its sounds, loud plays it and has HA say the text.

const notifyTextSlot = "notify-text"

var notifyTones = []string{"good", "info", "bad"}

// notice is one notification.
type notice struct {
	Text  string `json:"text"`
	Tone  string `json:"tone"`
	Speak *bool  `json:"speak"` // nil = say it (in the loud mode)
}

// notifyMode is the persisted mode ("" in an old state file = quiet).
func (a *App) notifyMode() string {
	switch m := a.store.get().NotifyMode; m {
	case alertsOff, alertsLoud:
		return m
	}
	return alertsQuiet
}

func (a *App) notifySubscribe() {
	if t := a.cfg().NotifyTopic; t != "" {
		a.mqtt.Subscribe(t, a.cmdNotify)
	}
}

// cmdNotify takes a notification: JSON (a notice; "message" is taken for "text") or plain text,
// an info.
func (a *App) cmdNotify(payload []byte) {
	var n notice
	s := strings.TrimSpace(string(payload))
	if strings.HasPrefix(s, "{") {
		var m struct {
			notice
			Message string `json:"message"`
		}
		if err := json.Unmarshal(payload, &m); err != nil {
			a.log.Warn("notify: bad JSON", "err", err)
			return
		}
		n = m.notice
		if n.Text == "" {
			n.Text = m.Message
		}
	} else {
		n.Text = s
	}
	a.notify(n)
}

// notify shows (and in the loud mode says) a notification.
func (a *App) notify(n notice) {
	mode := a.notifyMode()
	if mode == alertsOff {
		return
	}
	n.Text = strings.TrimSpace(n.Text)
	n.Tone = strings.ToLower(strings.TrimSpace(n.Tone))
	if !slices.Contains(notifyTones, n.Tone) {
		n.Tone = "info"
	}
	a.log.Info("notification", "tone", n.Tone, "text", n.Text, "mode", mode)
	a.fireAs("notify_"+n.Tone, mode != alertsLoud)
	if n.Text != "" && a.cfg().NotifyShowText && a.front != nil {
		secs := a.store.get().TextSecs
		a.matrixStack.Push(notifyTextSlot, display.PrioScenario, time.Duration(secs)*time.Second,
			a.front.Marquee(n.Text, a.marqueeStep(), a.brightness))
	}
	if mode == alertsLoud && (n.Speak == nil || *n.Speak) {
		a.sayAloud(n.Text)
	}
}

// alertNotice is a Grafana alert labelled kind=notification: its firing is the event (once, not
// on Grafana's repeats); its resolve only forgets it.
func (a *App) alertNotice(al alerts.Alert) {
	st := &a.alerts
	st.mu.Lock()
	if st.notices == nil {
		st.notices = map[string]bool{}
	}
	seen := st.notices[al.Fingerprint]
	if al.Firing() {
		st.notices[al.Fingerprint] = true
	} else {
		delete(st.notices, al.Fingerprint)
	}
	st.mu.Unlock()
	if !al.Firing() || seen {
		return
	}
	speak := !al.Quiet()
	a.notify(notice{Text: al.Speech(), Tone: al.Tone(), Speak: &speak})
}

// cmdNotifyMode: the HA select (and the web app's option).
func (a *App) cmdNotifyMode(payload []byte) {
	m := strings.TrimSpace(string(payload))
	if !slices.Contains(alertsModes, m) {
		a.log.Warn("unknown notifications mode", "mode", m)
		a.publish(a.topics.State("notify_mode"), a.notifyMode())
		return
	}
	a.store.update(func(s *persistState) { s.NotifyMode = m })
	if m == alertsOff {
		a.endShow("notify")
		a.matrixStack.Remove(notifyTextSlot)
	}
	a.publish(a.topics.State("notify_mode"), m)
}
