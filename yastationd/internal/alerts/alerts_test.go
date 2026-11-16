package alerts

import "testing"

// Grafana's MQTT contact point, default JSON (trimmed).
const sample = `{"receiver":"speakers","status":"firing","orgId":1,"alerts":[
 {"status":"firing","labels":{"alertname":"Диск почти полон","severity":"Critical","host":"nas"},
  "annotations":{"summary":"/data 95%"},"fingerprint":"c6eadffa33fcdf37"},
 {"status":"resolved","labels":{"alertname":"CPU","severity":"warning"},
  "annotations":{"speech":"Процессор остыл"},"fingerprint":""}]}`

func TestParse(t *testing.T) {
	n, err := Parse([]byte(sample))
	if err != nil || len(n.Alerts) != 2 {
		t.Fatal(n, err)
	}
	a, b := n.Alerts[0], n.Alerts[1]
	if !a.Firing() || a.Title() != "Диск почти полон" || a.Speech() != "Диск почти полон" || a.Severity() != "critical" {
		t.Errorf("first: %+v", a)
	}
	if b.Firing() || b.Speech() != "Процессор остыл" || b.Fingerprint == "" {
		t.Errorf("second (a fingerprint from its labels, the speech annotation): %+v", b)
	}
	if _, err := Parse([]byte("not json")); err == nil {
		t.Error("garbage parsed")
	}
}

func TestMatchers(t *testing.T) {
	labels := map[string]string{"alertname": "CPU", "severity": "critical", "team": "home"}
	for _, tc := range []struct {
		m    string
		want bool
	}{
		{"", true},
		{"severity=critical", true},
		{"severity=warning", false},
		{"severity!=warning, team=home", true},
		{"team=~home|lab", true},
		{"team=~ho", false}, // anchored, as in Alertmanager
		{"room!~.+", true},  // a missing label is ""
		{`team="home"`, true},
	} {
		ms, err := ParseMatchers(tc.m)
		if err != nil {
			t.Fatalf("%q: %v", tc.m, err)
		}
		if got := ms.Match(labels); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.m, got, tc.want)
		}
	}
	for _, bad := range []string{"severity", "=x", "a=~(", "x~y"} {
		if _, err := ParseMatchers(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestNotificationLabels(t *testing.T) {
	a := Alert{Labels: map[string]string{"kind": "Notification", "tone": "Good", "speak": "no"},
		Annotations: map[string]string{"resolved": " снова жив "}}
	if !a.Notification() || a.Tone() != "good" || !a.Quiet() || a.ResolvedText() != "снова жив" {
		t.Fatalf("%+v", a)
	}
	if (Alert{}).Notification() || (Alert{}).Quiet() {
		t.Fatal("an alert without labels is a notification, or quiet")
	}
}
