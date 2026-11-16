// Package alerts reads Grafana's alert notifications (its MQTT contact point's default JSON, the
// Alertmanager webhook shape) and the label matchers a speaker filters them with
// (docs/alerts.md).
package alerts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Notification is one message of the contact point: a group of alerts, each firing or resolved.
type Notification struct {
	Status string  `json:"status"`
	Alerts []Alert `json:"alerts"`
}

// Alert is one alert of a notification.
type Alert struct {
	Status      string            `json:"status"` // "firing" or "resolved"
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Fingerprint string            `json:"fingerprint"`
}

// Parse reads a notification. Alerts without a fingerprint get one from their labels, so a
// sender that leaves it out still pairs a resolve with its firing.
func Parse(payload []byte) (Notification, error) {
	var n Notification
	if err := json.Unmarshal(payload, &n); err != nil {
		return n, err
	}
	for i := range n.Alerts {
		if n.Alerts[i].Fingerprint == "" {
			n.Alerts[i].Fingerprint = labelKey(n.Alerts[i].Labels)
		}
	}
	return n, nil
}

func labelKey(l map[string]string) string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the same labels give the same key
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + l[k] + ",")
	}
	return b.String()
}

// Firing: the alert is on.
func (a Alert) Firing() bool { return a.Status == "firing" }

// Title is the alert's name: Grafana puts the rule's title in the alertname label.
func (a Alert) Title() string { return strings.TrimSpace(a.Labels["alertname"]) }

// Speech is what a speaker says for the alert: the rule's "speech" annotation when it has one,
// else its title.
func (a Alert) Speech() string {
	if s := strings.TrimSpace(a.Annotations["speech"]); s != "" {
		return s
	}
	return a.Title()
}

// Notification: the rule is labelled kind=notification — an event, not a state (docs/alerts.md).
func (a Alert) Notification() bool {
	return strings.EqualFold(strings.TrimSpace(a.Labels["kind"]), "notification")
}

// Tone is a notification's tone label (good, info, bad; "" when none).
func (a Alert) Tone() string { return strings.ToLower(strings.TrimSpace(a.Labels["tone"])) }

// Quiet: the rule asks not to be said aloud (a label or an annotation speak=no).
func (a Alert) Quiet() bool {
	for _, v := range []string{a.Labels["speak"], a.Annotations["speak"]} {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "no", "false", "0":
			return true
		}
	}
	return false
}

// ResolvedText is the rule's "resolved" annotation: the good news its resolve brings.
func (a Alert) ResolvedText() string { return strings.TrimSpace(a.Annotations["resolved"]) }

// Severity is the severity label, lower case ("" when the rule has none).
func (a Alert) Severity() string { return strings.ToLower(strings.TrimSpace(a.Labels["severity"])) }

// Matcher is one label condition: label=value, label!=value, label=~regex, label!~regex (a regex
// must match the whole value, as in Alertmanager). A missing label reads as "".
type Matcher struct {
	Label, Op, Value string
	re               *regexp.Regexp
}

// Matchers is a list of conditions, all of which must hold; an empty list takes every alert.
type Matchers []Matcher

// ParseMatchers reads comma-separated matchers ("severity=critical, team=~home|lab").
func ParseMatchers(s string) (Matchers, error) {
	var out Matchers
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m, err := parseMatcher(part)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func parseMatcher(s string) (Matcher, error) {
	for _, op := range []string{"=~", "!~", "!=", "="} {
		i := strings.Index(s, op)
		if i <= 0 {
			continue
		}
		m := Matcher{Label: strings.TrimSpace(s[:i]), Op: op, Value: strings.Trim(strings.TrimSpace(s[i+len(op):]), `"`)}
		if m.Label == "" {
			break
		}
		if op == "=~" || op == "!~" {
			re, err := regexp.Compile("^(?:" + m.Value + ")$")
			if err != nil {
				return Matcher{}, fmt.Errorf("%s: %w", s, err)
			}
			m.re = re
		}
		return m, nil
	}
	return Matcher{}, fmt.Errorf("%q: want label=value, label!=value, label=~regex or label!~regex", s)
}

// Match: every matcher holds for labels.
func (ms Matchers) Match(labels map[string]string) bool {
	for _, m := range ms {
		v := labels[m.Label]
		var ok bool
		switch m.Op {
		case "=":
			ok = v == m.Value
		case "!=":
			ok = v != m.Value
		case "=~":
			ok = m.re.MatchString(v)
		case "!~":
			ok = !m.re.MatchString(v)
		}
		if !ok {
			return false
		}
	}
	return true
}
