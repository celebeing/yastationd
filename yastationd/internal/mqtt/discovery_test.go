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
