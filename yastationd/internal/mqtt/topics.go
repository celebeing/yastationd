package mqtt

import (
	"fmt"
	"strings"
)

// Topics centralises every topic string. prefix is the state/command root (default "yastation"),
// disc is the HA discovery prefix (default "homeassistant"), id is the device id.
type Topics struct {
	prefix, disc, id string
}

func NewTopics(prefix, disc, id string) Topics {
	return Topics{prefix: prefix, disc: disc, id: id}
}

func (t Topics) node() string { return "yastation_" + t.id }

func (t Topics) Availability() string { return fmt.Sprintf("%s/%s/availability", t.prefix, t.id) }

// Discovery is the HA MQTT-Discovery config topic for one object.
func (t Topics) Discovery(component, obj string) string {
	return fmt.Sprintf("%s/%s/%s/%s/config", t.disc, component, t.node(), obj)
}

// State and Command build per-object topics under the device root.
func (t Topics) State(obj string) string   { return fmt.Sprintf("%s/%s/%s/state", t.prefix, t.id, obj) }
func (t Topics) Command(obj string) string { return fmt.Sprintf("%s/%s/%s/set", t.prefix, t.id, obj) }

// Intercom (docs/intercom.md): this speaker's retained info, every speaker's (the peer list),
// and the signalling inbox of speaker id.
func (t Topics) IntercomInfo() string         { return fmt.Sprintf("%s/%s/intercom/info", t.prefix, t.id) }
func (t Topics) IntercomInfoAll() string      { return fmt.Sprintf("%s/+/intercom/info", t.prefix) }
func (t Topics) IntercomSig(id string) string { return fmt.Sprintf("%s/%s/intercom/sig", t.prefix, id) }

// IntercomCall is the shared topic HA's "call a room" script publishes to; the speaker that just
// heard the request takes it (docs/ha-intercom-setup.md).
func (t Topics) IntercomCall() string { return fmt.Sprintf("%s/intercom/call", t.prefix) }

// IntercomInfoID is the device id in an IntercomInfoAll topic ("" if it isn't one).
func (t Topics) IntercomInfoID(topic string) string {
	rest, ok := strings.CutPrefix(topic, t.prefix+"/")
	if !ok {
		return ""
	}
	id, ok := strings.CutSuffix(rest, "/intercom/info")
	if !ok || strings.Contains(id, "/") {
		return ""
	}
	return id
}

// WebInfo is this speaker's retained web UI card; WebInfoAll every speaker's (discovery for the
// app, docs/web-ui.md).
func (t Topics) WebInfo() string    { return fmt.Sprintf("%s/%s/web/info", t.prefix, t.id) }
func (t Topics) WebInfoAll() string { return fmt.Sprintf("%s/+/web/info", t.prefix) }

// ObjectOf is the object of a state or command topic of this device ("" when it is neither).
func (t Topics) ObjectOf(topic string) string {
	rest, ok := strings.CutPrefix(topic, t.prefix+"/"+t.id+"/")
	if !ok {
		return ""
	}
	for _, suf := range []string{"/state", "/set"} {
		if obj, ok := strings.CutSuffix(rest, suf); ok && !strings.Contains(obj, "/") {
			return obj
		}
	}
	return ""
}

// Arbitration is the shared topic where speakers that heard a wake word post their claims.
func (t Topics) Arbitration() string { return fmt.Sprintf("%s/arbitration", t.prefix) }

// DisplayCmd is the one-shot overlay command topic (JSON {surface,op,args,ttl_ms,priority}).
func (t Topics) DisplayCmd() string { return fmt.Sprintf("%s/%s/display/cmd", t.prefix, t.id) }

// StateObject is the object of one of this device's state topics ("" when topic is not one).
func (t Topics) StateObject(topic string) string {
	rest, ok := strings.CutPrefix(topic, t.prefix+"/"+t.id+"/")
	if !ok {
		return ""
	}
	obj, ok := strings.CutSuffix(rest, "/state")
	if !ok || strings.Contains(obj, "/") {
		return ""
	}
	return obj
}

// Shared (docs/settings.md, "Sync"): a value every speaker takes, one retained topic per item —
// kind "setting" (a config key), "option" (an HA config entity) or "moment/<display kind>"
// (a moment, shared by the speakers with that kind of front display). An empty payload: the
// item is not shared.
func (t Topics) Shared(kind, item string) string {
	return fmt.Sprintf("%s/shared/%s/%s", t.prefix, kind, item)
}
func (t Topics) SharedAll(kind string) string { return fmt.Sprintf("%s/shared/%s/+", t.prefix, kind) }

// SharedItem is the item of a Shared(kind, …) topic ("" if it isn't one).
func (t Topics) SharedItem(kind, topic string) string {
	item, ok := strings.CutPrefix(topic, fmt.Sprintf("%s/shared/%s/", t.prefix, kind))
	if !ok || strings.Contains(item, "/") {
		return ""
	}
	return item
}
