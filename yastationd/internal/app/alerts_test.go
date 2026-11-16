package app

import (
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/mqtt"
)

func newAlertsApp(t *testing.T) *App {
	a := newMomentApp(t)
	a.ring = &hw.Ring{} // the alert moments are rings
	a.topics = mqtt.NewTopics("yastation", "homeassistant", "t1")
	c := a.cfg()
	c.MQTTHost, c.AlertsTopic, c.AlertsSpeak = "broker", "yastation/alerts", "severity=critical"
	return a
}

func note(status string, alerts ...string) []byte {
	return []byte(`{"status":"` + status + `","alerts":[` + strings.Join(alerts, ",") + `]}`)
}

func alert(status, fp, name, severity string) string {
	return `{"status":"` + status + `","fingerprint":"` + fp + `","labels":{"alertname":"` + name + `","severity":"` + severity + `"}}`
}

func (a *App) alertsOnCount() int {
	a.alerts.mu.Lock()
	defer a.alerts.mu.Unlock()
	return len(a.alerts.on)
}

func TestAlertsLifecycle(t *testing.T) {
	a := newAlertsApp(t)
	a.cmdAlertsMode([]byte("loud"))
	a.onAlerts(note("firing", alert("firing", "f1", "Диск полон", "critical")))
	if !a.ringStack.Has("alert") || !a.ringStack.Has("alert-fire") || a.alertsOnCount() != 1 {
		t.Fatalf("firing: active %v, fire %v, on %d", a.ringStack.Has("alert"), a.ringStack.Has("alert-fire"), a.alertsOnCount())
	}
	if s := a.states.snapshot()["say"].value; !strings.Contains(s, `"text":"Диск полон"`) {
		t.Errorf("a critical alert in the loud mode was not said: %q", s)
	}
	if !a.alertSpeechSoon() || a.alertSpeechSoon() {
		t.Error("the announcement right after should skip the chime, once")
	}
	// a repeat: no new reaction
	a.endShow("alert-fire")
	a.onAlerts(note("firing", alert("firing", "f1", "Диск полон", "critical")))
	if a.ringStack.Has("alert-fire") {
		t.Error("a repeat fired the moment again")
	}
	// with alerts_repeat it does
	a.cfg().AlertsRepeat = true
	a.onAlerts(note("firing", alert("firing", "f1", "Диск полон", "critical")))
	if !a.ringStack.Has("alert-fire") {
		t.Error("alerts_repeat: a repeat did not fire the moment")
	}
	a.cfg().AlertsRepeat = false
	// a warning is not said (alerts_speak = severity=critical)
	a.states.set("say", "")
	a.onAlerts(note("firing", alert("firing", "f2", "CPU", "warning")))
	if s := a.states.snapshot()["say"].value; s != "" {
		t.Errorf("a warning was said: %q", s)
	}
	// resolves: the active ring stays until the last one goes
	a.onAlerts(note("resolved", alert("resolved", "f1", "Диск полон", "critical")))
	if !a.ringStack.Has("alert") || a.alertsOnCount() != 1 {
		t.Fatal("one resolve ended the active ring with another alert on")
	}
	a.onAlerts(note("resolved", alert("resolved", "f2", "CPU", "warning")))
	time.Sleep(20 * time.Millisecond)
	if a.ringStack.Has("alert") || a.alertsOnCount() != 0 {
		t.Fatal("the last resolve left the active ring")
	}
	// a resolve of an alert never seen: nothing
	a.endShow("alert-fire")
	a.onAlerts(note("resolved", alert("resolved", "f9", "Old", "")))
	if a.ringStack.Has("alert-fire") {
		t.Error("an unknown resolve showed the resolved moment")
	}
}

func TestAlertsModesAndFilter(t *testing.T) {
	a := newAlertsApp(t)
	if a.alertsMode() != "quiet" {
		t.Fatalf("default mode %q", a.alertsMode())
	}
	// quiet: shown, never said
	a.onAlerts(note("firing", alert("firing", "f1", "Диск полон", "critical")))
	if !a.ringStack.Has("alert") || a.states.snapshot()["say"].value != "" {
		t.Fatal("quiet: not shown, or said")
	}
	// off: forgets what is on and ignores the rest
	a.cmdAlertsMode([]byte("off"))
	time.Sleep(20 * time.Millisecond)
	if a.ringStack.Has("alert") || a.alertsOnCount() != 0 {
		t.Fatal("off kept the active ring")
	}
	a.onAlerts(note("firing", alert("firing", "f2", "CPU", "warning")))
	if a.alertsOnCount() != 0 {
		t.Fatal("off took an alert")
	}
	a.cmdAlertsMode([]byte("loud"))
	// the speaker's filter
	a.cfg().AlertsMatch = "severity=critical"
	a.onAlerts(note("firing", alert("firing", "f3", "CPU", "warning")))
	if a.alertsOnCount() != 0 {
		t.Fatal("alerts_match let a warning through")
	}
	// acknowledge: the ring goes, the alert stays on
	a.onAlerts(note("firing", alert("firing", "f4", "Диск полон", "critical")))
	a.alertsAck()
	time.Sleep(20 * time.Millisecond)
	if a.ringStack.Has("alert") || a.alertsOnCount() != 1 {
		t.Fatal("ack: the ring stayed, or the alert was dropped")
	}
	// an unknown mode is refused
	a.cmdAlertsMode([]byte("shout"))
	if a.alertsMode() != "loud" {
		t.Fatal("an unknown mode was taken")
	}
	// garbage is ignored
	a.onAlerts([]byte("nope"))
}

func TestWithoutSound(t *testing.T) {
	a := newMomentApp(t)
	in := []scenarioStep{{Ring: "alert_info", Sound: "notification_sound", Ms: 2000}, {Ring: "alert", Phrase: "x", Hold: true}}
	out := a.withoutSound(in)
	if out[0].Sound != "" || out[0].Ms != 2000 || out[1].Phrase != "" || !out[1].Hold || in[0].Sound == "" {
		t.Fatalf("%+v (in %+v)", out, in)
	}
}

func TestNotifications(t *testing.T) {
	a := newAlertsApp(t)
	said := func() string { return a.states.snapshot()["say"].value }
	// quiet (the default): shown, not said
	a.cmdNotify([]byte(`{"text":"Стиралка достирала","tone":"good"}`))
	if !a.ringStack.Has("notify") || said() != "" {
		t.Fatalf("quiet: shown %v, said %q", a.ringStack.Has("notify"), said())
	}
	// loud: said; plain text is an info; speak:false stays silent
	a.cmdNotifyMode([]byte("loud"))
	a.cmdNotify([]byte("Курьер у двери"))
	if !strings.Contains(said(), "Курьер у двери") {
		t.Errorf("loud plain text not said: %q", said())
	}
	a.states.set("say", "")
	a.cmdNotify([]byte(`{"text":"тихо","tone":"bad","speak":false}`))
	if said() != "" {
		t.Errorf("speak:false was said: %q", said())
	}
	// off: nothing
	a.cmdNotifyMode([]byte("off"))
	a.endShow("notify")
	a.cmdNotify([]byte("ничего"))
	if a.ringStack.Has("notify") {
		t.Error("off showed a notification")
	}
	a.cmdNotifyMode([]byte("loud"))

	// Grafana: kind=notification is an event — once, no active state, its resolve shows nothing
	n := `{"status":"firing","fingerprint":"n1","labels":{"alertname":"Бэкап не прошёл","kind":"notification","tone":"bad"}}`
	a.states.set("say", "")
	a.onAlerts(note("firing", n))
	if !strings.Contains(said(), "Бэкап не прошёл") || a.alertsOnCount() != 0 || a.ringStack.Has("alert") {
		t.Fatalf("kind=notification: said %q, on %d, active %v", said(), a.alertsOnCount(), a.ringStack.Has("alert"))
	}
	a.states.set("say", "")
	a.onAlerts(note("firing", n)) // Grafana's repeat
	if said() != "" {
		t.Error("a repeat of a notification was said again")
	}
	a.endShow("notify")
	a.onAlerts(note("resolved", strings.Replace(n, `"status":"firing"`, `"status":"resolved"`, 1)))
	if a.ringStack.Has("notify") {
		t.Error("a notification's resolve showed something")
	}
	// an alert's resolve with a "resolved" annotation is good news, even with the alerts off
	a.cmdAlertsMode([]byte("off"))
	a.states.set("say", "")
	a.onAlerts(note("resolved", `{"status":"resolved","fingerprint":"l1","labels":{"alertname":"Основной канал лёг"},"annotations":{"resolved":"Основной интернет снова жив"}}`))
	if !strings.Contains(said(), "Основной интернет снова жив") {
		t.Errorf("the resolved annotation was not said: %q", said())
	}
}
