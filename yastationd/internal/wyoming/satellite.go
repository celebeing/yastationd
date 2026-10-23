package wyoming

// Event types used by the satellite role.
const (
	TypeDescribe       = "describe"
	TypeInfo           = "info"
	TypePing           = "ping"
	TypePong           = "pong"
	TypeRunSatellite   = "run-satellite"
	TypePauseSatellite = "pause-satellite"
	TypeAudioStart     = "audio-start"
	TypeAudioChunk     = "audio-chunk"
	TypeAudioStop      = "audio-stop"
	TypePlayed         = "played"
	TypeRunPipeline    = "run-pipeline"

	// Pipeline progress HA forwards to the satellite.
	TypeDetect       = "detect"
	TypeDetection    = "detection"
	TypeTranscribe   = "transcribe"
	TypeVoiceStarted = "voice-started"
	TypeVoiceStopped = "voice-stopped"
	TypeTranscript   = "transcript"
	TypeSynthesize   = "synthesize"
	TypeError        = "error"

	// Voice timers (HA-owned, forwarded to the satellite).
	TypeTimerStarted   = "timer-started"
	TypeTimerUpdated   = "timer-updated"
	TypeTimerCancelled = "timer-cancelled"
	TypeTimerFinished  = "timer-finished"
)

// SatelliteInfo describes us in the info event.
type SatelliteInfo struct {
	Name        string
	Description string
	Version     string
	Area        string
}

// Info builds the reply to describe. HA treats any peer with an installed satellite as a
// satellite (homeassistant/components/wyoming/data.py); no asr/tts/wake services are offered.
// "snd" advertises the output format we prefer; HA's announce path sends 22050 Hz mono s16 anyway.
func Info(s SatelliteInfo) Event {
	attribution := map[string]any{"name": "yastation_cfw", "url": ""}
	sat := map[string]any{
		"name":                  s.Name,
		"attribution":           attribution,
		"installed":             true,
		"description":           s.Description,
		"version":               s.Version,
		"has_vad":               false,
		"active_wake_words":     []string{},
		"max_active_wake_words": 0,
		"supports_trigger":      false,
	}
	if s.Area != "" {
		sat["area"] = s.Area
	}
	snd := map[string]any{
		"name":        "aplay",
		"attribution": attribution,
		"installed":   true,
		"description": "ALSA default (dmix) on the Station's speaker",
		"version":     s.Version,
		"snd_format":  map[string]any{"rate": 22050, "width": 2, "channels": 1},
	}
	return Event{Type: TypeInfo, Data: map[string]any{
		"asr": []any{}, "tts": []any{}, "handle": []any{}, "intent": []any{}, "wake": []any{},
		"satellite": sat,
		"snd":       []any{snd},
	}}
}
