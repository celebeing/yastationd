package app

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/alerts"
)

// Grafana alerts (docs/alerts.md): Grafana's MQTT contact point publishes its notifications to
// alerts_topic; each speaker takes the alerts alerts_match lets through and reacts by its Alerts
// mode — off (ignores them), quiet (the moments without their sounds), loud (the moments, and the
// alerts alerts_speak picks said aloud through HA). A new alert fires alert_firing (by severity),
// alert_active is held while one is on, a resolve fires alert_resolved. What is on lives in memory
// only: after a restart the speaker learns of an alert from its next repeat, without a reaction.

const (
	alertsOff   = "off"
	alertsQuiet = "quiet"
	alertsLoud  = "loud"
	// alertSayHold: an HA announcement this soon after the speaker asked HA to say an alert is
	// that alert: it plays without the announce chime (the alert's own moment came first).
	alertSayHold = 30 * time.Second
)

var alertsModes = []string{alertsOff, alertsQuiet, alertsLoud}

// alertOn is an alert that is on here.
type alertOn struct {
	title, severity string
	since           time.Time
}

type alertState struct {
	mu     sync.Mutex
	on     map[string]alertOn // by fingerprint
	acked  bool               // alert_active ended by hand until a new alert fires
	sayAt  time.Time          // when the speaker last asked HA to say an alert
}

// alertsOn: the speaker takes alerts at all (a topic is set and there is a broker).
func (a *App) alertsOn() bool { return a.cfg().AlertsTopic != "" && a.cfg().MQTTHost != "" }

// alertsMode is the persisted mode ("" in an old state file = quiet).
func (a *App) alertsMode() string {
	switch m := a.store.get().AlertsMode; m {
	case alertsOff, alertsLoud:
		return m
	}
	return alertsQuiet
}

func (a *App) alertsSubscribe() {
	if !a.alertsOn() {
		return
	}
	a.mqtt.Subscribe(a.cfg().AlertsTopic, a.onAlerts)
}

// onAlerts handles one notification.
func (a *App) onAlerts(payload []byte) {
	n, err := alerts.Parse(payload)
	if err != nil {
		a.log.Warn("alerts: not a Grafana notification", "err", err)
		return
	}
	mode := a.alertsMode()
	if mode == alertsOff {
		return
	}
	cfg := a.cfg()
	take, _ := alerts.ParseMatchers(cfg.AlertsMatch) // checked when the config was read
	speak, _ := alerts.ParseMatchers(cfg.AlertsSpeak)
	for _, al := range n.Alerts {
		if !take.Match(al.Labels) {
			continue
		}
		if al.Firing() {
			a.alertFiring(al, mode, cfg.AlertsRepeat, speak)
		} else {
			a.alertResolved(al, mode)
		}
	}
	a.publishAlerts()
}

func (a *App) alertFiring(al alerts.Alert, mode string, repeat bool, speak alerts.Matchers) {
	st := &a.alerts
	st.mu.Lock()
	if st.on == nil {
		st.on = map[string]alertOn{}
	}
	_, known := st.on[al.Fingerprint]
	st.on[al.Fingerprint] = alertOn{title: al.Title(), severity: al.Severity(), since: time.Now()}
	if known && !repeat {
		st.mu.Unlock()
		return // a repeat of an alert already on: no new reaction
	}
	st.acked = false
	st.mu.Unlock()
	a.log.Info("alert: firing", "title", al.Title(), "severity", al.Severity(), "repeat", known, "mode", mode)
	name := "alert_firing"
	switch al.Severity() {
	case "critical", "warning":
		name += "_" + al.Severity()
	}
	a.fireAs(name, mode != alertsLoud)
	a.fireAs("alert_active", true)
	if mode == alertsLoud && speak.Match(al.Labels) {
		a.alertSay(al.Speech())
	}
}

func (a *App) alertResolved(al alerts.Alert, mode string) {
	st := &a.alerts
	st.mu.Lock()
	_, known := st.on[al.Fingerprint]
	delete(st.on, al.Fingerprint)
	left := len(st.on)
	st.mu.Unlock()
	if !known {
		return // resolved before this speaker saw it fire (a restart, a filter change): nothing to undo
	}
	a.log.Info("alert: resolved", "title", al.Title(), "left", left)
	a.fireAs("alert_resolved", mode != alertsLoud)
	if left == 0 {
		a.endHook("alert_active")
	}
}

// alertSay asks HA to say text on this speaker: an event of the alert_say entity, which the HA
// automation of docs/guides/grafana-alerts.md turns into assist_satellite.announce.
func (a *App) alertSay(text string) {
	if text == "" {
		return
	}
	a.alerts.mu.Lock()
	a.alerts.sayAt = time.Now()
	a.alerts.mu.Unlock()
	b, _ := json.Marshal(map[string]string{"event_type": "say", "text": text})
	a.publishEvent(a.topics.State("alert_say"), string(b))
}

// alertSpeechSoon: an announcement now is the alert the speaker just asked HA to say (it plays
// without the announce chime). Taken once.
func (a *App) alertSpeechSoon() bool {
	a.alerts.mu.Lock()
	defer a.alerts.mu.Unlock()
	if !a.alerts.sayAt.IsZero() && time.Since(a.alerts.sayAt) < alertSayHold {
		a.alerts.sayAt = time.Time{}
		return true
	}
	return false
}

// alertsAck ends alert_active here until a new alert fires (the alerts stay on: their resolves
// still show).
func (a *App) alertsAck() {
	a.alerts.mu.Lock()
	a.alerts.acked = true
	n := len(a.alerts.on)
	a.alerts.mu.Unlock()
	a.log.Info("alerts: acknowledged", "on", n)
	a.endHook("alert_active")
	a.publishAlerts()
}

// cmdAlertsMode: the HA select (and the web app's option). Off forgets what is on.
func (a *App) cmdAlertsMode(payload []byte) {
	m := strings.TrimSpace(string(payload))
	if !slices.Contains(alertsModes, m) {
		a.log.Warn("unknown alerts mode", "mode", m)
		a.publish(a.topics.State("alerts_mode"), a.alertsMode())
		return
	}
	a.store.update(func(s *persistState) { s.AlertsMode = m })
	if m == alertsOff {
		a.alerts.mu.Lock()
		a.alerts.on = nil
		a.alerts.mu.Unlock()
		a.endHook("alert_active")
		a.endShow("alert-fire")
	}
	a.publish(a.topics.State("alerts_mode"), m)
	a.publishAlerts()
}

// publishAlerts: the count of alerts on here, their titles as an attribute.
func (a *App) publishAlerts() {
	if !a.alertsOn() {
		return
	}
	a.alerts.mu.Lock()
	type item struct {
		Title    string `json:"title"`
		Severity string `json:"severity,omitempty"`
		Since    string `json:"since"`
	}
	list := make([]item, 0, len(a.alerts.on))
	for _, o := range a.alerts.on {
		list = append(list, item{o.title, o.severity, o.since.Format(time.RFC3339)})
	}
	acked := a.alerts.acked
	a.alerts.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Since < list[j].Since })
	b, _ := json.Marshal(map[string]any{"alerts": list, "acknowledged": acked})
	a.publish(a.topics.State("alerts_active"), strconv.Itoa(len(list)))
	a.publish(a.topics.State("alerts_attr"), string(b))
}
