package mqtt

import "fmt"

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

// DisplayCmd is the one-shot overlay command topic (JSON {surface,op,args,ttl_ms,priority}).
func (t Topics) DisplayCmd() string { return fmt.Sprintf("%s/%s/display/cmd", t.prefix, t.id) }
