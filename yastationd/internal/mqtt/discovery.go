package mqtt

import "encoding/json"

// Device is the HA device block shared by every entity (so they group under one device card).
type Device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Manufacturer string   `json:"manufacturer"`
	SWVersion    string   `json:"sw_version,omitempty"`
}

// Entity is a discovery config. Only the fields used by the MVP entities are modelled; the map
// in Extra carries anything component-specific (colour modes, options, …).
type Entity struct {
	Component string // light, select, text, number, sensor, binary_sensor, media_player, event, button
	Object    string
	Config    map[string]any
}

type builder struct {
	t   Topics
	dev Device
	// RingEffects is the ring light's effect_list; Animations the ring_animation select's
	// options. HasAmbilight adds the independently controlled rear RGBW light.
	RingEffects  []string
	Animations   []string
	FollowModes  []string
	HasAmbilight bool
	// Expressions are the Expression select's options; HasFaces adds the Display faces switch
	// (a front display that takes expression clips).
	Expressions []string
	HasFaces    bool
}

func NewBuilder(t Topics, dev Device) *builder { return &builder{t: t, dev: dev} }

// build fills the common fields (unique_id, availability, device) and merges the entity's own.
func (b *builder) build(e Entity) (topic string, payload []byte) {
	cfg := map[string]any{
		"unique_id":          b.t.node() + "_" + e.Object,
		"availability_topic": b.t.Availability(),
		"device":             b.dev,
	}
	for k, v := range e.Config {
		cfg[k] = v
	}
	payload, _ = json.Marshal(cfg)
	return b.t.Discovery(e.Component, e.Object), payload
}

// Entities returns the MVP entity set. Topics are wired to Topics so the app only needs the
// object names to publish state and subscribe to commands.
func (b *builder) Entities() []Entity {
	t := b.t
	entities := []Entity{
		{"light", "ring", map[string]any{
			"name":                  "Ring",
			"schema":                "json",
			"state_topic":           t.State("ring"),
			"command_topic":         t.Command("ring"),
			"brightness":            true,
			"supported_color_modes": []string{"rgb"},
			"effect":                len(b.RingEffects) > 0,
			"effect_list":           b.RingEffects,
			"icon":                  "mdi:led-strip-variant",
		}},
		{"select", "display_mode", map[string]any{
			"name":          "Display",
			"state_topic":   t.State("display_mode"),
			"command_topic": t.Command("display_mode"),
			"options":       []string{"clock", "off", "message"},
			"icon":          "mdi:clock-digital",
		}},
		{"text", "display_message", map[string]any{
			"name":          "Message",
			"state_topic":   t.State("display_message"),
			"command_topic": t.Command("display_message"),
			"max":           64,
			"icon":          "mdi:format-text",
		}},
		{"number", "volume", map[string]any{
			"name":          "Volume",
			"state_topic":   t.State("volume"),
			"command_topic": t.Command("volume"),
			"min":           0, "max": 100, "step": 1,
			"icon": "mdi:volume-high",
		}},
		{"notify", "show_text", map[string]any{
			"name":          "Show text",
			"command_topic": t.Command("show_text"),
			"icon":          "mdi:message-text-clock",
		}},
		{"switch", "auto_brightness", map[string]any{
			"name":            "Auto brightness",
			"state_topic":     t.State("auto_brightness"),
			"command_topic":   t.Command("auto_brightness"),
			"payload_on":      "ON",
			"payload_off":     "OFF",
			"entity_category": "config",
			"icon":            "mdi:brightness-auto",
		}},
		{"number", "clock_brightness", map[string]any{
			"name":          "Clock brightness",
			"state_topic":   t.State("clock_brightness"),
			"command_topic": t.Command("clock_brightness"),
			"min":           1, "max": 255, "step": 1,
			"entity_category": "config",
			"icon":            "mdi:brightness-6",
		}},
		{"number", "text_duration", map[string]any{
			"name":          "Text duration",
			"state_topic":   t.State("text_duration"),
			"command_topic": t.Command("text_duration"),
			"min":           1, "max": 600, "step": 1, "mode": "box",
			"unit_of_measurement": "s",
			"entity_category":     "config",
			"icon":                "mdi:timer-outline",
		}},
		{"select", "ring_animation", map[string]any{
			"name":          "Ring animation",
			"state_topic":   t.State("ring_animation"),
			"command_topic": t.Command("ring_animation"),
			"options":       b.Animations,
			"icon":          "mdi:animation-play",
		}},
		{"number", "animation_duration", map[string]any{
			"name":          "Animation duration",
			"state_topic":   t.State("animation_duration"),
			"command_topic": t.Command("animation_duration"),
			"min":           1, "max": 600, "step": 1, "mode": "box",
			"unit_of_measurement": "s",
			"entity_category":     "config",
			"icon":                "mdi:timer-outline",
		}},
		{"select", "expression", map[string]any{
			"name":                  "Expression",
			"state_topic":           t.State("expression"),
			"command_topic":         t.Command("expression"),
			"options":               b.Expressions,
			"json_attributes_topic": t.State("display_caps"),
			"icon":                  "mdi:emoticon-outline",
		}},
		{"select", "follow_up", map[string]any{
			"name":            "Dialog follow-up",
			"state_topic":     t.State("follow_up"),
			"command_topic":   t.Command("follow_up"),
			"options":         b.FollowModes,
			"entity_category": "config",
			"icon":            "mdi:chat-processing",
		}},
		// No media_player: HA has no MQTT media_player platform. Audio from HA goes through the
		// Wyoming satellite (assist_satellite.announce); the raw yastation/<id>/speaker/set topic
		// still plays local files for mqtt.publish automations.
		{"binary_sensor", "mic_mute", map[string]any{
			"name":         "Mic mute",
			"state_topic":  t.State("mic_mute"),
			"payload_on":   "ON",
			"payload_off":  "OFF",
			"device_class": "problem",
			"icon":         "mdi:microphone-off",
		}},
		{"sensor", "lux", map[string]any{
			"name":                "Illuminance",
			"state_topic":         t.State("lux"),
			"device_class":        "illuminance",
			"unit_of_measurement": "lx",
			"state_class":         "measurement",
		}},
		{"sensor", "supply_voltage", map[string]any{
			"name":                "Supply voltage",
			"state_topic":         t.State("supply_voltage"),
			"device_class":        "voltage",
			"unit_of_measurement": "V",
			"state_class":         "measurement",
			"entity_category":     "diagnostic",
		}},
		{"binary_sensor", "power_problem", map[string]any{
			"name":            "Power supply problem",
			"state_topic":     t.State("power_problem"),
			"payload_on":      "ON",
			"payload_off":     "OFF",
			"device_class":    "problem",
			"entity_category": "diagnostic",
			"icon":            "mdi:power-plug-off",
		}},
		{"sensor", "health", map[string]any{
			"name":            "Health",
			"state_topic":     t.State("health"),
			"entity_category": "diagnostic",
			"icon":            "mdi:heart-pulse",
		}},
		{"binary_sensor", "pipeline_ok", map[string]any{
			"name":            "Pipeline available",
			"state_topic":     t.State("pipeline_ok"),
			"payload_on":      "ON",
			"payload_off":     "OFF",
			"device_class":    "connectivity",
			"entity_category": "diagnostic",
		}},
		{"sensor", "uptime", map[string]any{
			"name":                "Uptime",
			"state_topic":         t.State("uptime"),
			"unit_of_measurement": "s",
			"state_class":         "total_increasing",
			"entity_category":     "diagnostic",
		}},
		{"event", "button", map[string]any{
			"name":        "Button",
			"state_topic": t.State("button"),
			"event_types": []string{"volume_up", "volume_down", "voice_command", "panel_gesture", "wake_word"},
			"icon":        "mdi:gesture-tap-button",
		}},
	}
	if b.HasFaces {
		entities = append(entities, Entity{"switch", "display_faces", map[string]any{
			"name":            "Display faces",
			"state_topic":     t.State("display_faces"),
			"command_topic":   t.Command("display_faces"),
			"payload_on":      "ON",
			"payload_off":     "OFF",
			"entity_category": "config",
			"icon":            "mdi:emoticon-happy-outline",
		}})
	}
	if b.HasAmbilight {
		entities = append(entities, Entity{"light", "ambilight", map[string]any{
			"name":                  "Ambilight",
			"schema":                "json",
			"state_topic":           t.State("ambilight"),
			"command_topic":         t.Command("ambilight"),
			"brightness":            true,
			"supported_color_modes": []string{"rgbw"},
			"icon":                  "mdi:led-strip-variant",
		}})
	}
	return entities
}

// Publish sends every entity's discovery config (retained).
func (b *builder) Publish(c *Client) {
	for _, e := range b.Entities() {
		topic, payload := b.build(e)
		c.Publish(topic, string(payload))
	}
}
