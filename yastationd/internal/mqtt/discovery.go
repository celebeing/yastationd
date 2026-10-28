package mqtt

import (
	"encoding/json"
	"sync"
)

// Device is the HA device block shared by every entity (so they group under one device card).
type Device struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Manufacturer string   `json:"manufacturer"`
	SWVersion    string   `json:"sw_version,omitempty"`
}

// Entity is a discovery config: its component and object, and in Config the entity's own
// fields (name, topics, options, …); build adds the common ones.
type Entity struct {
	Component string // light, select, text, number, sensor, binary_sensor, media_player, event, button
	Object    string
	Config    map[string]any
}

type builder struct {
	t   Topics
	dev Device
	// RingEffects is the ring light's effect_list. HasAmbilight adds the independently
	// controlled rear RGBW light.
	RingEffects  []string
	FollowModes  []string
	HasAmbilight bool
	// HasAmbilightFx adds the switch and brightness of effects streamed to the ambilight (DDP).
	HasAmbilightFx bool
	AmbilightFx    []string // the music effect select's options
	// HasFaces adds the Display faces switch (a front display that takes expression clips).
	HasFaces bool
	// DisplayModes are the Display select's options (nil = clock, off, message).
	DisplayModes []string
	// HARingLight adds the ring as an HA light (ha_ring_light); without it automations drive the
	// ring through its MQTT topics only.
	HARingLight bool
	// Intercom adds the call entities; their target select lists the other speakers, which
	// change at runtime (SetIntercomPeers, then Publish again).
	Intercom bool
	// RoomSound adds the room sound level sensors; SoundSPL: their levels are calibrated dB SPL,
	// not dBFS.
	RoomSound bool
	SoundSPL  bool

	mu            sync.Mutex
	intercomPeers []string
}

// SetIntercomPeers sets the intercom target select's options (the other speakers' names).
func (b *builder) SetIntercomPeers(names []string) {
	b.mu.Lock()
	b.intercomPeers = append([]string(nil), names...)
	b.mu.Unlock()
}

// NewBuilder makes the discovery builder for device dev.
func NewBuilder(t Topics, dev Device) *builder { return &builder{t: t, dev: dev} }

func (b *builder) displayModes() []string {
	if len(b.DisplayModes) == 0 {
		return []string{"clock", "off", "message"}
	}
	return b.DisplayModes
}

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

// Entities returns every entity the speaker offers (HA's device card and the web app; see
// WebOnly). Topics are wired to Topics so the app only needs the object names to publish state
// and subscribe to commands.
func (b *builder) Entities() []Entity {
	t := b.t
	entities := []Entity{
		{"select", "display_mode", map[string]any{
			"name":          "Display",
			"state_topic":   t.State("display_mode"),
			"command_topic": t.Command("display_mode"),
			"options":       b.displayModes(),
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
		{"number", "animation_duration", map[string]any{
			"name":          "Animation duration",
			"state_topic":   t.State("animation_duration"),
			"command_topic": t.Command("animation_duration"),
			"min":           1, "max": 600, "step": 1, "mode": "box",
			"unit_of_measurement": "s",
			"entity_category":     "config",
			"icon":                "mdi:timer-outline",
		}},
		{"sensor", "version", map[string]any{
			"name":                  "Version",
			"state_topic":           t.State("version"),
			"json_attributes_topic": t.State("version_info"),
			"entity_category":       "diagnostic",
			"icon":                  "mdi:tag-outline",
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
			"name":        "Mic mute",
			"state_topic": t.State("mic_mute"),
			"payload_on":  "ON",
			"payload_off": "OFF",
			"icon":        "mdi:microphone-off",
		}},
		{"sensor", "lux", map[string]any{
			"name":                  "Illuminance",
			"state_topic":           t.State("lux"),
			"json_attributes_topic": t.State("lux_attr"),
			"device_class":          "illuminance",
			"unit_of_measurement":   "lx",
			"state_class":           "measurement",
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
	if b.HARingLight {
		entities = append([]Entity{{"light", "ring", map[string]any{
			"name":                  "Ring",
			"schema":                "json",
			"state_topic":           t.State("ring"),
			"command_topic":         t.Command("ring"),
			"brightness":            true,
			"supported_color_modes": []string{"rgb"},
			"effect":                len(b.RingEffects) > 0,
			"effect_list":           b.RingEffects,
			"icon":                  "mdi:led-strip-variant",
		}}}, entities...)
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
	if b.Intercom {
		b.mu.Lock()
		peers := b.intercomPeers
		b.mu.Unlock()
		if len(peers) == 0 {
			peers = []string{"—"} // HA wants at least one option
		}
		entities = append(entities,
			Entity{"sensor", "intercom", map[string]any{
				"name":                  "Intercom",
				"state_topic":           t.State("intercom"),
				"json_attributes_topic": t.State("intercom_attr"),
				"icon":                  "mdi:phone-in-talk",
			}},
			Entity{"select", "intercom_target", map[string]any{
				"name":          "Intercom target",
				"state_topic":   t.State("intercom_target"),
				"command_topic": t.Command("intercom_target"),
				"options":       peers,
				"icon":          "mdi:account-voice",
			}},
			Entity{"button", "intercom_call", map[string]any{
				"name":          "Intercom call",
				"command_topic": t.Command("intercom_call"),
				"icon":          "mdi:phone",
			}},
			Entity{"button", "intercom_hangup", map[string]any{
				"name":          "Intercom hang up",
				"command_topic": t.Command("intercom_hangup"),
				"icon":          "mdi:phone-hangup",
			}})
	}
	if b.RoomSound {
		level := "dBFS"
		if b.SoundSPL {
			level = "dB"
		}
		for _, s := range []struct{ obj, name, unit, icon string }{
			{"sound_floor", "Sound floor", level, "mdi:volume-low"},
			{"sound_level", "Sound level", level, "mdi:waveform"},
			{"sound_activity", "Sound activity", "%", "mdi:account-voice"},
		} {
			cfg := map[string]any{
				"name":                s.name,
				"state_topic":         t.State(s.obj),
				"unit_of_measurement": s.unit,
				"state_class":         "measurement",
				"icon":                s.icon,
			}
			if s.unit == "dB" {
				cfg["device_class"] = "sound_pressure"
			}
			entities = append(entities, Entity{"sensor", s.obj, cfg})
		}
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
	if b.HasAmbilightFx {
		entities = append(entities,
			Entity{"switch", "ambilight_fx", map[string]any{
				"name":          "Ambilight music effects",
				"state_topic":   t.State("ambilight_fx"),
				"command_topic": t.Command("ambilight_fx"),
				"payload_on":    "ON",
				"payload_off":   "OFF",
				"icon":          "mdi:led-strip-variant",
			}},
			Entity{"select", "ambilight_fx_effect", map[string]any{
				"name":          "Ambilight music effect",
				"state_topic":   t.State("ambilight_fx_effect"),
				"command_topic": t.Command("ambilight_fx_effect"),
				"options":       b.AmbilightFx,
				"icon":          "mdi:music-circle-outline",
			}},
			Entity{"number", "ambilight_fx_brightness", map[string]any{
				"name":          "Ambilight music effects brightness",
				"state_topic":   t.State("ambilight_fx_brightness"),
				"command_topic": t.Command("ambilight_fx_brightness"),
				"min":           1, "max": 255, "step": 1, "mode": "slider",
				"entity_category": "config",
				"icon":            "mdi:brightness-6",
			}})
	}
	return entities
}

// WebOnly: entities the speaker's web app shows (they are its settings) that HA's device card
// does not get. The web app lists them with the rest (Entities), Publish keeps them out of HA.
var WebOnly = map[string]bool{"text_duration": true, "animation_duration": true, "display_faces": true, "follow_up": true}

// retired: entities HA got from earlier versions; Publish clears them so HA drops them. Their
// MQTT command topics stay for automations (ring_animation/set, expression/set).
var retired = []Entity{{Component: "select", Object: "ring_animation"}, {Component: "select", Object: "expression"}}

// Publish sends every entity's discovery config (retained), and an empty one for each entity HA
// should not have (web-only, retired, the ring light when off), which removes it from HA.
func (b *builder) Publish(c *Client) {
	gone := append([]Entity(nil), retired...)
	if !b.HARingLight {
		gone = append(gone, Entity{Component: "light", Object: "ring"})
	}
	for _, e := range b.Entities() {
		if WebOnly[e.Object] {
			gone = append(gone, e)
			continue
		}
		topic, payload := b.build(e)
		c.Publish(topic, string(payload))
	}
	for _, e := range gone {
		c.Publish(b.t.Discovery(e.Component, e.Object), "")
	}
}
