package mqtt

import "testing"

func TestAmbilightDiscoveryIsConditionalRGBW(t *testing.T) {
	builder := NewBuilder(NewTopics("yastation", "homeassistant", "test"), Device{})
	for _, entity := range builder.Entities() {
		if entity.Object == "ambilight" {
			t.Fatal("ambilight entity published for a board without the strip")
		}
	}

	builder.HasAmbilight = true
	for _, entity := range builder.Entities() {
		if entity.Object != "ambilight" {
			continue
		}
		if entity.Component != "light" || entity.Config["schema"] != "json" ||
			entity.Config["state_topic"] != "yastation/test/ambilight/state" ||
			entity.Config["command_topic"] != "yastation/test/ambilight/set" {
			t.Fatalf("ambilight discovery = %+v", entity)
		}
		modes, ok := entity.Config["supported_color_modes"].([]string)
		if !ok || len(modes) != 1 || modes[0] != "rgbw" {
			t.Fatalf("ambilight color modes = %#v", entity.Config["supported_color_modes"])
		}
		return
	}
	t.Fatal("ambilight entity missing")
}

func TestButtonDiscoveryIncludesPanelGesture(t *testing.T) {
	builder := NewBuilder(NewTopics("yastation", "homeassistant", "test"), Device{})
	for _, entity := range builder.Entities() {
		if entity.Object != "button" {
			continue
		}
		types, ok := entity.Config["event_types"].([]string)
		if !ok {
			t.Fatalf("button event types = %#v", entity.Config["event_types"])
		}
		for _, eventType := range types {
			if eventType == "panel_gesture" {
				return
			}
		}
		t.Fatalf("panel_gesture missing from %v", types)
	}
	t.Fatal("button event entity missing")
}

func TestIntercomDiscovery(t *testing.T) {
	b := NewBuilder(NewTopics("yastation", "homeassistant", "test"), Device{})
	for _, e := range b.Entities() {
		if e.Object == "intercom_target" {
			t.Fatal("intercom entities without the intercom")
		}
	}
	b.Intercom = true
	options := func() []string {
		for _, e := range b.Entities() {
			if e.Object == "intercom_target" {
				return e.Config["options"].([]string)
			}
		}
		t.Fatal("no intercom target select")
		return nil
	}
	if o := options(); len(o) != 1 {
		t.Fatalf("empty peer list must still give HA one option: %v", o)
	}
	b.SetIntercomPeers([]string{"Кабинет", "Спальня"})
	if o := options(); len(o) != 2 || o[1] != "Спальня" {
		t.Fatalf("options %v", o)
	}
	seen := map[string]bool{}
	for _, e := range b.Entities() {
		seen[e.Component+"/"+e.Object] = true
	}
	for _, want := range []string{"sensor/intercom", "button/intercom_call", "button/intercom_hangup"} {
		if !seen[want] {
			t.Errorf("missing %s", want)
		}
	}
}

func TestIntercomInfoID(t *testing.T) {
	tp := NewTopics("yastation", "homeassistant", "me")
	for topic, want := range map[string]string{
		"yastation/mini3/intercom/info": "mini3",
		"yastation/a/b/intercom/info":   "",
		"other/mini3/intercom/info":     "",
		"yastation/mini3/intercom/sig":  "",
		tp.IntercomInfo():               "me",
	} {
		if got := tp.IntercomInfoID(topic); got != want {
			t.Errorf("%s: %q, want %q", topic, got, want)
		}
	}
}

func TestEntitiesForHA(t *testing.T) {
	b := NewBuilder(NewTopics("yastation", "homeassistant", "t1"), Device{Name: "t"})
	b.HasFaces = true
	has := func(obj string) bool {
		for _, e := range b.Entities() {
			if e.Object == obj {
				return true
			}
		}
		return false
	}
	if has("ring") || has("ring_animation") || has("expression") {
		t.Fatal("the ring light is opt-in; the animation and expression selects are gone")
	}
	for obj := range WebOnly {
		if !has(obj) {
			t.Fatalf("web-only %s missing from the entities the web app lists", obj)
		}
	}
	b.HARingLight = true
	if !has("ring") {
		t.Fatal("ha_ring_light on: no ring light")
	}
}
