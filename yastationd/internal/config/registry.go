package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
	"github.com/celebeing/yastationd/yastationd/internal/web"
)

// Key describes one config key: how it is parsed and printed, and what the settings UI, the
// reload and the sync between speakers may do with it (docs/settings.md).
type Key struct {
	Name  string
	Group string // one of Groups
	Label string // short, for the settings UI
	Doc   string // the explaining paragraph

	Kind    string   // bool, int, float, enum, text, secret, list
	Min     float64  // int, float: the range
	Max     float64  //
	Unit    string   // shown after a number: "s", "ms", "min", "%", "dB", "KB"
	Options []string // enum

	// Runtime: a change applies while the daemon runs (every reader goes through App.cfg());
	// otherwise saving it restarts the daemon (on trial, docs/web-ui.md).
	Runtime bool
	// Sync: the key may be shared by every speaker (docs/settings.md, "Sync"). Never set for
	// what is this speaker's own: its identity, addresses, calibrations, files.
	Sync bool
	// Hidden: set elsewhere in the app (the login, the calibration tab); not in the settings list.
	Hidden bool
	// Advanced: paths, test hooks, hardware overrides — under "Advanced" in the settings.
	Advanced bool
	// Needs: the hardware the key is for ("ambilight", "pixels": a dot-matrix front screen);
	// boards without it don't list the key.
	Needs string

	get func(*Config) []string
	set func(*Config, string) error
}

// Groups are the settings UI's sections, in order, and the page each is on (the web app's
// Configuration tabs). A new key joins a section; a new section names its page here. Keys
// marked Advanced are folded at the bottom of their page.
var Groups = []struct{ ID, Title, Page string }{
	{"display", "Display", "general"},
	{"theme", "Theme", "general"},
	{"ambilight", "Ambilight", "general"},
	{"sound", "Sound", "general"},
	{"video", "Video", "general"},
	{"initial", "A new speaker's first values", "general"},
	{"voice", "Voice", "voice"},
	{"voice_id", "Voice ID", "voice"},
	{"identity", "Identity", "home"},
	{"ha", "Home Assistant", "home"},
	{"web", "Web app", "home"},
	{"time", "Time", "home"},
	{"logs", "Logs", "home"},
	{"music", "Music Assistant", "music"},
	{"intercom", "Intercom", "intercom"},
	{"wifi_setup", "Wi-Fi setup mode", "wifi"},
	{"network", "When the connection is lost", "wifi"},
	{"calibration", "Mic", "calibration"},
}

// Retired are keys earlier versions had: a config file that still sets one keeps working (the
// line is accepted and ignored), so an update never fails on an old config.
var Retired = map[string]string{
	"display_faces": "the faces switch is gone; themes decide what the front display shows (docs/asset-packs.md)",
}

// Keys is every config key, in the settings UI's order within each group.
var Keys []*Key

var keyIndex = map[string]*Key{}

// Lookup returns the key's description, or nil.
func Lookup(name string) *Key { return keyIndex[name] }

// Get prints a key's value in c as config lines (one per entry for a list; none for an empty
// list).
func (k *Key) Get(c *Config) []string { return k.get(c) }

// Default prints the key's default value.
func (k *Key) Default() []string {
	d := Default()
	return k.get(&d)
}

// List reports whether the key repeats (one line per entry).
func (k *Key) List() bool { return k.Kind == "list" }

func reg(k Key, v value) {
	k.Kind, k.Min, k.Max, k.Options, k.get, k.set = v.kind, v.min, v.max, v.options, v.get, v.set
	if k.Unit == "" {
		k.Unit = v.unit
	}
	p := &k
	Keys = append(Keys, p)
	keyIndex[k.Name] = p
}

type value struct {
	kind     string
	min, max float64
	unit     string
	options  []string
	get      func(*Config) []string
	set      func(*Config, string) error
}

func one(s string) []string { return []string{s} }

func boolV(f func(*Config) *bool) value {
	return value{kind: "bool",
		get: func(c *Config) []string { return one(strconv.FormatBool(*f(c))) },
		set: func(c *Config, v string) (err error) { *f(c), err = strconv.ParseBool(v); return }}
}

func intV(lo, hi int, f func(*Config) *int) value {
	return value{kind: "int", min: float64(lo), max: float64(hi),
		get: func(c *Config) []string { return one(strconv.Itoa(*f(c))) },
		set: func(c *Config, v string) (err error) { *f(c), err = atoiRange(v, lo, hi); return }}
}

// durV: a duration written as a whole number of unit (s, ms, min).
func durV(lo, hi int, unit time.Duration, f func(*Config) *time.Duration) value {
	u := map[time.Duration]string{time.Second: "s", time.Millisecond: "ms", time.Minute: "min"}[unit]
	return value{kind: "int", min: float64(lo), max: float64(hi), unit: u,
		get: func(c *Config) []string { return one(strconv.Itoa(int(*f(c) / unit))) },
		set: func(c *Config, v string) error {
			n, err := atoiRange(v, lo, hi)
			*f(c) = time.Duration(n) * unit
			return err
		}}
}

func floatV(lo, hi float64, f func(*Config) *float64) value {
	return value{kind: "float", min: lo, max: hi,
		get: func(c *Config) []string { return one(strconv.FormatFloat(*f(c), 'f', -1, 64)) },
		set: func(c *Config, v string) error {
			x, err := strconv.ParseFloat(v, 64)
			if err == nil && (x < lo || x > hi) {
				err = fmt.Errorf("want %g..%g", lo, hi)
			}
			*f(c) = x
			return err
		}}
}

// textV: free text; check (optional) validates it.
func textV(f func(*Config) *string, check func(string) error) value {
	return value{kind: "text",
		get: func(c *Config) []string { return one(*f(c)) },
		set: func(c *Config, v string) error {
			if check != nil {
				if err := check(v); err != nil {
					return err
				}
			}
			*f(c) = v
			return nil
		}}
}

func secretV(f func(*Config) *string, check func(string) error) value {
	v := textV(f, check)
	v.kind = "secret"
	return v
}

func enumV(opts []string, f func(*Config) *string) value {
	return value{kind: "enum", options: opts,
		get: func(c *Config) []string { return one(*f(c)) },
		set: func(c *Config, v string) error {
			for _, o := range opts {
				if v == o {
					*f(c) = v
					return nil
				}
			}
			return fmt.Errorf("want %s", strings.Join(opts, " or "))
		}}
}

// listV: a repeated key; every line adds an entry.
func listV(f func(*Config) *[]string) value {
	return value{kind: "list",
		get: func(c *Config) []string { return append([]string(nil), *f(c)...) },
		set: func(c *Config, v string) error { *f(c) = append(*f(c), v); return nil }}
}

func init() {
	// --- display ---
	reg(Key{Name: "auto_brightness_min", Group: "display", Label: "Auto brightness: dimmest", Runtime: true, Sync: true,
		Doc: "The dimmest the clock goes under auto brightness, in the dark. Empty or 0 uses the board's dimmest readable level (so shared as 0, each speaker keeps its own). The brightest is the clock brightness above."},
		intV(0, 255, func(c *Config) *int { return &c.AutoBrightnessMin }))
	reg(Key{Name: "text_lines", Group: "display", Label: "Lines of text", Sync: true, Needs: "pixels",
		Doc: "Pixel screens: text in one line, two lines, or chosen by its length (auto)."},
		value{kind: "enum", options: []string{"auto", "1", "2"},
			get: func(c *Config) []string {
				if c.TextLines == 0 {
					return one("auto")
				}
				return one(strconv.Itoa(c.TextLines))
			},
			set: func(c *Config, v string) error {
				switch v {
				case "auto":
					c.TextLines = 0
				case "1", "2":
					c.TextLines = int(v[0] - '0')
				default:
					return fmt.Errorf("want auto, 1 or 2")
				}
				return nil
			}})
	reg(Key{Name: "marquee_speed", Group: "display", Label: "Running text speed", Runtime: true, Sync: true,
		Doc: "How fast running lines move, as a multiple of the default pace."},
		floatV(0.25, 4, func(c *Config) *float64 { return &c.MarqueeSpeed }))
	reg(Key{Name: "auto_brightness_curve", Group: "display", Label: "Auto brightness curve", Runtime: true, Sync: true, Advanced: true,
		Doc: "The shape of auto brightness: light (lux) to a level 0–255 at a few points, lux:level, lux:level, … interpolated between them. The levels are scaled into the range from the dimmest to the clock brightness."},
		value{kind: "text",
			get: func(c *Config) []string { return one(CurveString(c.BrightnessCurve)) },
			set: func(c *Config, v string) (err error) { c.BrightnessCurve, err = parseCurve(v); return }})
	reg(Key{Name: "lux_path", Group: "display", Label: "Light sensor file", Advanced: true,
		Doc: "The light sensor's IIO file; empty finds it from the board profile."}, textV(func(c *Config) *string { return &c.LuxPath }, nil))
	reg(Key{Name: "lux_calibration", Group: "display", Label: "Light sensor calibration", Hidden: true,
		Doc: "Set on the Calibration tab."},
		value{kind: "text",
			get: func(c *Config) []string {
				if c.LuxCalibration == nil {
					return one("")
				}
				return one(luxcal.String(c.LuxCalibration))
			},
			set: func(c *Config, v string) (err error) { c.LuxCalibration, err = luxcal.Parse(v); return }})
	reg(Key{Name: "clips_dir", Group: "display", Label: "Clips", Advanced: true,
		Doc: "Our converted stock clips, searched before the firmware's."}, textV(func(c *Config) *string { return &c.ClipsDir }, nil))
	reg(Key{Name: "scenarios_dir", Group: "display", Label: "Scenarios", Advanced: true,
		Doc: "Your own scenarios (*.json)."}, textV(func(c *Config) *string { return &c.ScenariosDir }, nil))

	// --- ambilight ---
	reg(Key{Name: "ambilight_fx_white", Group: "ambilight", Label: "Ambilight effects: white LED", Runtime: true, Sync: true, Needs: "ambilight",
		Doc: "The white in the ambilight's music effects (the highs of rain, the peak of vu) lights the strip's white LEDs; off, it is mixed from red, green and blue. DDP streams and the HA light are not affected."},
		boolV(func(c *Config) *bool { return &c.AmbilightFxWhite }))
	reg(Key{Name: "ddp_port", Group: "ambilight", Label: "DDP port", Sync: true, Needs: "ambilight", Advanced: true,
		Doc: "Boards with an ambilight: the UDP port for LED frames from LedFx or WLED tools; 0 turns it off."},
		intV(0, 65535, func(c *Config) *int { return &c.DDPPort }))

	// --- sound ---
	reg(Key{Name: "volume_step", Group: "sound", Label: "Volume step", Runtime: true, Sync: true,
		Doc: "Steps of 5 % per volume key press; «громче» and «тише» are two presses."},
		intV(1, 4, func(c *Config) *int { return &c.VolumeStep }))
	reg(Key{Name: "volume_max", Group: "sound", Label: "Maximum volume", Unit: "%", Runtime: true, Sync: true,
		Doc: "The loudest the speaker may go, for the keys, the ring, Home Assistant and music alike. 100 is no cap."},
		intV(5, 100, func(c *Config) *int { return &c.VolumeMax }))
	reg(Key{Name: "volume_beep", Group: "sound", Label: "Volume key tick", Runtime: true, Sync: true,
		Doc: "A tick on each volume key press, the firmware's own sound."},
		boolV(func(c *Config) *bool { return &c.VolumeBeep }))
	reg(Key{Name: "announce_chime", Group: "sound", Label: "Announcement chime", Runtime: true, Sync: true,
		Doc: "A chime before an announcement from Home Assistant."},
		boolV(func(c *Config) *bool { return &c.AnnounceChime }))
	reg(Key{Name: "amixer_card", Group: "sound", Label: "Mixer card", Advanced: true, Doc: "Overrides the board's."},
		overrideV("amixer_card", textV(func(c *Config) *string { return &c.AmixerCard }, nil)))
	reg(Key{Name: "aplay_device", Group: "sound", Label: "Playback device", Advanced: true, Doc: "Overrides the board's."},
		overrideV("aplay_device", textV(func(c *Config) *string { return &c.AplayDevice }, nil)))
	reg(Key{Name: "unmute", Group: "sound", Label: "Unmute controls", Advanced: true,
		Doc: "Replaces the board's list of mixer settings applied at start, one per line."},
		listV(func(c *Config) *[]string { return &c.Unmute }))

	// --- video ---
	reg(Key{Name: "video_frames", Group: "video", Label: "Video frames", Runtime: true, Advanced: true, Needs: "pixels",
		Doc: "The video mode's frames file (made by cmd/videoconv, or picked on the Assets page)."}, textV(func(c *Config) *string { return &c.VideoFrames }, nil))
	reg(Key{Name: "video_audio", Group: "video", Label: "Video sound", Runtime: true, Advanced: true, Needs: "pixels",
		Doc: "The video mode's WAV (picked with the frames on the Assets page)."}, textV(func(c *Config) *string { return &c.VideoAudio }, nil))
	reg(Key{Name: "video_av_offset_ms", Group: "video", Label: "Picture offset", Runtime: true, Needs: "pixels", Advanced: true,
		Doc: "Shifts the picture against the sound; positive shows it later."},
		durV(-2000, 2000, time.Millisecond, func(c *Config) *time.Duration { return &c.VideoAVOffset }))

	// --- a new speaker's first values ---
	reg(Key{Name: "clock_brightness", Group: "initial", Label: "Clock brightness", Advanced: true,
		Doc: "Only the value a new speaker starts with (the fixed level, and the brightest auto brightness goes); afterwards the Clock brightness option rules."},
		intV(0, 255, func(c *Config) *int { return &c.ClockBrightness }))
	reg(Key{Name: "auto_brightness", Group: "initial", Label: "Auto brightness", Advanced: true,
		Doc: "Only the value a new speaker starts with; afterwards the Auto brightness option rules."},
		boolV(func(c *Config) *bool { return &c.AutoBrightness }))
	reg(Key{Name: "theme", Group: "theme", Label: "Theme packs", Runtime: true,
		Doc: "The asset packs in force, one id per line, the top one first (docs/asset-packs.md): a moment, a sound or an animation comes from the first pack that has it, else from the default theme. Your own moments are the pack custom; not listed, it is worn on top (-custom: off). A pack this speaker does not have is skipped. Not shared between speakers for now: packs are installed on each speaker by hand. Theme packs on this speaker, when set, stands in for it here. Behaviour → Packs sets it."},
		listV(func(c *Config) *[]string { return &c.Themes }))
	reg(Key{Name: "theme_here", Group: "theme", Label: "Theme packs on this speaker", Runtime: true, Advanced: true,
		Doc: "This speaker's own theme, in place of Theme packs (from the time Theme packs could be shared): the same list. Empty: the speaker wears Theme packs. Behaviour → Packs edits whichever of the two is in force."},
		listV(func(c *Config) *[]string { return &c.ThemesHere }))
	reg(Key{Name: "ring_brightness", Group: "initial", Label: "Ring brightness", Advanced: true,
		Doc: "Only the value a new speaker starts with; afterwards Home Assistant's light entity rules."},
		intV(0, 255, func(c *Config) *int { return &c.RingBrightness }))
	reg(Key{Name: "ambilight_brightness", Group: "initial", Label: "Ambilight brightness", Advanced: true, Needs: "ambilight",
		Doc: "Boards with a rear strip: the value a new speaker starts with; afterwards Home Assistant's light entity rules."},
		intV(0, 255, func(c *Config) *int { return &c.AmbilightBrightness }))

	// --- voice ---
	reg(Key{Name: "wake_enabled", Group: "voice", Label: "Wake word", Sync: true,
		Doc: "Listen for «Алиса». Off, only the voice key starts a request."},
		boolV(func(c *Config) *bool { return &c.WakeEnabled }))
	reg(Key{Name: "wake_word_in_stream", Group: "voice", Label: "Send the wake word too", Runtime: true, Sync: true,
		Doc: "Off, the audio Home Assistant gets starts just before the point where the wake word was heard (Audio before the wake word), so the transcript is the request alone. On, it starts 1.3 s earlier and carries «Алиса» too: for a speech-to-text or a voice recognition on the HA side that wants the wake word."},
		boolV(func(c *Config) *bool { return &c.WakeWordInStream }))
	reg(Key{Name: "preroll_ms", Group: "voice", Label: "Audio before the wake word", Runtime: true, Advanced: true,
		Doc: "Where the audio for Home Assistant starts, counted back from the point where the wake word was heard (with the wake word left out). That point is near the end of the word, a little after it in a room. Too little and the first word of the request is clipped; too much and the end of the wake word lands in the transcript. Empty or 0: the board's value, measured for its wake word model (not shared between speakers for that reason). For the voice key and follow-ups: the audio from before the session started."},
		value{kind: "int", min: 0, max: 2000, unit: "ms",
			get: func(c *Config) []string { return one(strconv.Itoa(c.PrerollMs)) },
			set: func(c *Config, v string) (err error) { c.PrerollMs, err = atoiRange(v, 0, 2000); return }})
	reg(Key{Name: "reply_timeout", Group: "voice", Label: "Reply deadline", Runtime: true, Sync: true,
		Doc: "How long to wait for Home Assistant's answer before giving up (HA has no timeout of its own)."},
		durV(10, 600, time.Second, func(c *Config) *time.Duration { return &c.ReplyTimeout }))
	reg(Key{Name: "wake_while_waiting", Group: "voice", Label: "Wake word while waiting", Runtime: true, Sync: true,
		Doc: "A wake word while a reply is pending: interrupt starts the new request as soon as Home Assistant is back; ignore blinks busy."},
		enumV([]string{"interrupt", "ignore"}, func(c *Config) *string { return &c.WakeWhileWaiting }))
	reg(Key{Name: "arbitration", Group: "voice", Label: "Nearest speaker answers", Sync: true,
		Doc: "Speakers that hear the same «Алиса» agree that only the loudest answers."},
		boolV(func(c *Config) *bool { return &c.Arbitration }))
	reg(Key{Name: "arbitration_wait_ms", Group: "voice", Label: "Arbitration wait", Runtime: true, Sync: true, Advanced: true,
		Doc: "The longest a quiet claim waits for louder speakers to speak up."},
		durV(100, 3000, time.Millisecond, func(c *Config) *time.Duration { return &c.ArbitrationWait }))
	reg(Key{Name: "wake_capture", Group: "voice", Label: "Save audio around wake words", Runtime: true, Sync: true, Advanced: true,
		Doc: "For tuning the wake word's timing: every wake word saves 3.5 s of the processed mic audio (2 s before the point it was heard, 1.5 s after) with its timing under <cfw>/wakes, the newest 200 kept (about 22 MB). Leave it off in normal use: it records what is said in the room."},
		boolV(func(c *Config) *bool { return &c.WakeCapture }))
	reg(Key{Name: "spotter_bin", Group: "voice", Label: "Spotter binary", Advanced: true,
		Doc: "spotter-host, the voice front-end."}, textV(func(c *Config) *string { return &c.SpotterBin }, nil))
	reg(Key{Name: "mic_device", Group: "voice", Label: "Mic device", Advanced: true, Doc: "Overrides the board's."},
		overrideV("mic_device", textV(func(c *Config) *string { return &c.MicDevice }, nil)))
	reg(Key{Name: "mic_channels", Group: "voice", Label: "Mic channels", Advanced: true, Doc: "Overrides the board's."},
		overrideV("mic_channels", intV(1, 16, func(c *Config) *int { return &c.MicChannels })))
	reg(Key{Name: "mic_rate", Group: "voice", Label: "Mic rate", Advanced: true,
		Doc: "The device rate, decimated to 16 kHz: a multiple of 16000."},
		overrideV("mic_rate", intV(16000, 96000, func(c *Config) *int { return &c.MicRate })))
	reg(Key{Name: "mic_channel", Group: "voice", Label: "Mic channel", Advanced: true, Doc: "Overrides the board's."},
		overrideV("mic_channel", intV(0, 15, func(c *Config) *int { return &c.MicChannel })))
	reg(Key{Name: "mic_pdm_gain", Group: "voice", Label: "Mic PDM gain", Advanced: true, Doc: "Overrides the board's; -1 leaves it."},
		overrideV("mic_pdm_gain", intV(-1, 48, func(c *Config) *int { return &c.MicPDMGain })))
	reg(Key{Name: "spotter_libs", Group: "voice", Label: "Vendor libraries", Advanced: true, Doc: "Overrides the board's."},
		overrideV("spotter_libs", textV(func(c *Config) *string { return &c.SpotterLibs }, nil)))
	reg(Key{Name: "wake_model", Group: "voice", Label: "Wake word model", Advanced: true, Doc: "Overrides the board's."},
		overrideV("wake_model", textV(func(c *Config) *string { return &c.WakeModel }, nil)))
	reg(Key{Name: "command_model", Group: "voice", Label: "Command model", Advanced: true, Doc: "Overrides the board's."},
		overrideV("command_model", textV(func(c *Config) *string { return &c.CommandModel }, nil)))

	// --- voice id ---
	reg(Key{Name: "voice_id", Group: "voice_id", Label: "Recognise voices", Sync: true,
		Doc: "Tells who is talking from the voice, with the speaker's own voiceprint model, on the speaker: no server, no cloud. HA gets the person's name (or guest), the gender and whether the voice is the household's, before it acts on the request. People appear by themselves in the Voices tab as they talk, to be named there; until someone is named every voice is unknown. Needs the firmware's voice models (Mini 3 0.369, Mini 3 Pro, Station 3; other firmware: make voice-models)."},
		boolV(func(c *Config) *bool { return &c.VoiceID }))
	reg(Key{Name: "voice_store", Group: "voice_id", Label: "Keep the voices here",
		Doc: "This speaker keeps the log of requests and the voice groups for the whole household, and hosts the Voices tab; the others send it what they hear. Turn it on for one speaker, a Station 3 if there is one (more flash, a faster CPU). Voiceprints are biometric data: they stay on this speaker's flash and the home's broker."},
		boolV(func(c *Config) *bool { return &c.VoiceStore }))
	reg(Key{Name: "voice_guest_threshold", Group: "voice_id", Label: "Guest threshold", Runtime: true, Sync: true,
		Doc: "How close a voice must be to a person's (cosine of the voiceprints, 0.40 to 0.90) to count as theirs; below it, a guest. Higher: fewer guests taken for the household, more of the household taken for guests. The Voices tab shows what a value would have done to the last requests. 0.66 is the firmware's own."},
		floatV(0.4, 0.9, func(c *Config) *float64 { return &c.GuestThreshold }))
	reg(Key{Name: "voice_guest_margin", Group: "voice_id", Label: "Margin over the runner-up", Runtime: true, Sync: true, Advanced: true,
		Doc: "How much closer the voice must be to the best match than to the second person (0 to 0.2) to be taken for them; otherwise a guest."},
		floatV(0, 0.2, func(c *Config) *float64 { return &c.GuestMargin }))
	reg(Key{Name: "voice_hold_ms", Group: "voice_id", Label: "Check before sending", Runtime: true, Sync: true, Advanced: true,
		Doc: "With guests ignored: how much of the request after «Алиса» the check waits for before the request goes to HA (0 to 2000 ms; 0 judges on the wake word alone). The speech is held meanwhile and sent at once after, so HA hears the end of the request no later than without the check; more speech makes the check surer."},
		durV(0, 2000, time.Millisecond, func(c *Config) *time.Duration { return &c.VoiceHold }))
	reg(Key{Name: "voice_unsure", Group: "voice_id", Label: "When unsure", Runtime: true, Sync: true, Advanced: true,
		Doc: "With guests ignored: what happens when the check can't tell (too little speech, or the voiceprint failed): allow sends the request on, drop ignores it."},
		enumV([]string{"allow", "drop"}, func(c *Config) *string { return &c.VoiceUnsure }))
	reg(Key{Name: "voice_bio_dir", Group: "voice_id", Label: "Voice models", Advanced: true,
		Doc: "Where the voiceprint models are (the firmware's bio_lingware). Empty: <cfw>/vendor/bio_lingware when there (make voice-models), else the firmware's /system/vendor/quasar/bio_lingware."},
		textV(func(c *Config) *string { return &c.VoiceBio }, nil))

	// --- identity ---
	reg(Key{Name: "device_id", Group: "identity", Label: "Device ID",
		Doc: "The speaker's ID in MQTT topics and in Home Assistant's entity IDs. Changing it makes HA see a new device."},
		textV(func(c *Config) *string { return &c.DeviceID }, nonEmpty))
	reg(Key{Name: "device_name", Group: "identity", Label: "Name",
		Doc: "The device's name in Home Assistant and in the web app; also the intercom and Music Assistant name unless those are set."},
		textV(func(c *Config) *string { return &c.DeviceName }, nonEmpty))
	reg(Key{Name: "board", Group: "identity", Label: "Board", Advanced: true,
		Doc: "auto detects the model from the device tree; or a codename (bergamot, pickle, orion)."},
		textV(func(c *Config) *string { return &c.Board }, nil))
	reg(Key{Name: "board_file", Group: "identity", Label: "Board overrides file", Advanced: true,
		Doc: "Single board-profile keys changed without a rebuild."}, textV(func(c *Config) *string { return &c.BoardFile }, nil))

	// --- home assistant ---
	reg(Key{Name: "mqtt_host", Group: "ha", Label: "MQTT broker",
		Doc: "Home Assistant's MQTT broker; empty runs the speaker without one."},
		textV(func(c *Config) *string { return &c.MQTTHost }, nil))
	reg(Key{Name: "mqtt_port", Group: "ha", Label: "MQTT port",
		Doc: "The broker's port."}, intV(1, 65535, func(c *Config) *int { return &c.MQTTPort }))
	reg(Key{Name: "mqtt_user", Group: "ha", Label: "MQTT user", Doc: "Empty for a broker without a login."},
		textV(func(c *Config) *string { return &c.MQTTUser }, nil))
	reg(Key{Name: "mqtt_pass", Group: "ha", Label: "MQTT password", Doc: "The broker's password."},
		secretV(func(c *Config) *string { return &c.MQTTPass }, nil))
	reg(Key{Name: "room_sound", Group: "ha", Label: "Room sound sensors", Sync: true,
		Doc: "Sound floor, level and activity sensors in Home Assistant: mic levels once a minute, never audio. They keep the mic open even with the wake word off."},
		boolV(func(c *Config) *bool { return &c.RoomSound }))
	reg(Key{Name: "ha_ring_light", Group: "ha", Label: "Ring as a light in Home Assistant", Sync: true, Advanced: true,
		Doc: "Show the LED ring as a light entity on the speaker's device in Home Assistant. Off, the ring still answers automations on its MQTT topic (yastation/<id>/ring/set); the web app's LED page plays its animations."},
		boolV(func(c *Config) *bool { return &c.HARingLight }))
	reg(Key{Name: "wyoming_port", Group: "ha", Label: "Wyoming port", Advanced: true,
		Doc: "The port Home Assistant's Wyoming integration connects to; 0 turns the satellite off."},
		intV(0, 65535, func(c *Config) *int { return &c.WyomingPort }))
	reg(Key{Name: "boot_connect_timeout", Group: "ha", Label: "First connection deadline", Sync: true, Advanced: true,
		Doc: "After boot, how long the broker and Home Assistant's link may take before the speaker shows that the first connection failed."},
		durV(1, 600, time.Second, func(c *Config) *time.Duration { return &c.BootConnectTimeout }))
	reg(Key{Name: "topic_prefix", Group: "ha", Label: "Topic prefix", Advanced: true,
		Doc: "State and command topics are <prefix>/<device ID>/…; every speaker that should see the others needs the same one."},
		textV(func(c *Config) *string { return &c.TopicPrefix }, nonEmpty))
	reg(Key{Name: "discovery_prefix", Group: "ha", Label: "Discovery prefix", Advanced: true,
		Doc: "Home Assistant's MQTT discovery prefix."},
		textV(func(c *Config) *string { return &c.DiscoveryPrefix }, nonEmpty))
	reg(Key{Name: "probe", Group: "ha", Label: "Health probes", Sync: true, Advanced: true,
		Doc: "Reachability checks for the health state, one per line: name=host:port@role, role broker, pipeline or lan."},
		listV(func(c *Config) *[]string { return &c.Probes }))

	// --- web app ---
	reg(Key{Name: "http_addr", Group: "web", Label: "Web server address", Advanced: true,
		Doc: "[host]:port of this web app and its API; empty turns it off."},
		textV(func(c *Config) *string { return &c.HTTPAddr }, func(v string) error {
			if v == "" {
				return nil
			}
			_, port, perr := net.SplitHostPort(v)
			n, aerr := strconv.Atoi(port)
			if perr != nil || aerr != nil || n < 1 || n > 65535 {
				return fmt.Errorf("want [host]:port, e.g. :80")
			}
			return nil
		}))
	reg(Key{Name: "web_user", Group: "web", Label: "Login", Hidden: true, Doc: "Set on the Login tab."},
		textV(func(c *Config) *string { return &c.WebUser }, nonEmpty))
	reg(Key{Name: "web_password", Group: "web", Label: "Password", Hidden: true, Doc: "Set on the Login tab."},
		secretV(func(c *Config) *string { return &c.WebPassword }, func(v string) error {
			if !web.ValidHash(v) {
				return fmt.Errorf("want a pbkdf2-sha256$… hash (set it from the web UI)")
			}
			return nil
		}))
	reg(Key{Name: "led_preview", Group: "web", Label: "LED preview", Sync: true,
		Doc: "The LED tab: what the screen, the ring and the ambilight show, live."},
		boolV(func(c *Config) *bool { return &c.LEDPreview }))
	reg(Key{Name: "frametap_addr", Group: "web", Label: "Frame tap address", Advanced: true,
		Doc: "Development: a separate, unauthenticated LED stream for the desk tools (e.g. :8930). Empty turns it off."},
		textV(func(c *Config) *string { return &c.FrameTapAddr }, nil))

	// --- time ---
	reg(Key{Name: "tz", Group: "time", Label: "Time zone", Sync: true,
		Doc: "The clock's time zone, an IANA name such as Europe/Moscow."},
		textV(func(c *Config) *string { return &c.TZ }, func(v string) error {
			_, err := time.LoadLocation(v)
			return err
		}))
	reg(Key{Name: "time_sync", Group: "time", Label: "Clock sync", Sync: true, Advanced: true,
		Doc: "Set the clock over NTP when the firmware runs no time daemon."},
		boolV(func(c *Config) *bool { return &c.TimeSync }))
	reg(Key{Name: "ntp_server", Group: "time", Label: "NTP servers", Sync: true,
		Doc: "Tried first, one per line; then the servers in the chrony config, then the default gateway."},
		listV(func(c *Config) *[]string { return &c.NTPServers }))
	reg(Key{Name: "chrony_conf", Group: "time", Label: "chrony config", Advanced: true,
		Doc: "Where to find more NTP servers."}, textV(func(c *Config) *string { return &c.ChronyConf }, nil))

	// --- logs ---
	reg(Key{Name: "log_remote", Group: "logs", Label: "Syslog server", Sync: true,
		Doc: "host:port of a syslog server on the LAN (TCP). Logs wait on flash while it is unreachable. Empty turns it off."},
		textV(func(c *Config) *string { return &c.LogRemote }, nil))
	reg(Key{Name: "log_spool_kb", Group: "logs", Label: "Log spool size", Unit: "KB", Sync: true, Advanced: true,
		Doc: "The most logs kept on flash while the syslog server is unreachable."},
		intV(64, 65536, func(c *Config) *int { return &c.LogSpoolKB }))
	reg(Key{Name: "log_level", Group: "logs", Label: "Log level", Advanced: true,
		Doc: "How much the daemon logs."}, enumV([]string{"debug", "info", "warn", "error"}, func(c *Config) *string { return &c.LogLevel }))
	reg(Key{Name: "state_file", Group: "logs", Label: "State file", Hidden: true,
		Doc: "Where the speaker keeps what Home Assistant set (ring colour, display mode, volume…)."},
		textV(func(c *Config) *string { return &c.StateFile }, nil))
	reg(Key{Name: "heartbeat_file", Group: "logs", Label: "Heartbeat file", Hidden: true,
		Doc: "Touched while the daemon is alive."}, textV(func(c *Config) *string { return &c.HeartbeatFile }, nil))
	reg(Key{Name: "sysroot", Group: "logs", Label: "Sysroot", Hidden: true,
		Doc: "Testing: a fake root for /sys and /dev."}, textV(func(c *Config) *string { return &c.SysRoot }, nil))

	// --- music assistant ---
	reg(Key{Name: "sendspin", Group: "music", Label: "Music Assistant player", Sync: true,
		Doc: "Play music from Music Assistant (Sendspin)."},
		boolV(func(c *Config) *bool { return &c.Sendspin }))
	reg(Key{Name: "sendspin_name", Group: "music", Label: "Player name",
		Doc: "The name in Music Assistant; empty uses the speaker's name."},
		textV(func(c *Config) *string { return &c.SendspinName }, nil))
	reg(Key{Name: "sendspin_server", Group: "music", Label: "Music Assistant address", Sync: true,
		Doc: "Optional: a ws:// URL the player dials itself, besides waiting for the server."},
		textV(func(c *Config) *string { return &c.SendspinServer }, nil))
	reg(Key{Name: "sendspin_volume", Group: "music", Label: "Music volume", Sync: true,
		Doc: "linked: the player's volume in Music Assistant is the speaker's volume; separate: a music-only level under it."},
		enumV([]string{"linked", "separate"}, func(c *Config) *string { return &c.SendspinVolume }))
	reg(Key{Name: "sendspin_unpaired", Group: "music", Label: "Unpaired servers may play", Sync: true, Advanced: true,
		Doc: "Let a Music Assistant server play before it is paired."},
		boolV(func(c *Config) *bool { return &c.SendspinUnpaired }))
	reg(Key{Name: "music_duck_db", Group: "music", Label: "Music under the assistant", Unit: "dB", Runtime: true, Sync: true,
		Doc: "How much quieter the music gets while the assistant listens, talks or rings."},
		intV(-60, 0, func(c *Config) *int { return &c.MusicDuckDB }))
	reg(Key{Name: "music_title", Group: "music", Label: "Track title", Runtime: true, Sync: true,
		Doc: "A new track's title runs once on the front display."},
		boolV(func(c *Config) *bool { return &c.MusicTitle }))
	reg(Key{Name: "music_clips", Group: "music", Label: "Pause and resume animations", Runtime: true, Sync: true,
		Doc: "Animations on the front display when the music pauses and resumes."},
		boolV(func(c *Config) *bool { return &c.MusicClips }))
	reg(Key{Name: "music_levels", Group: "music", Label: "Spectrum under the clock", Sync: true, Needs: "pixels",
		Doc: "Pixel screens: the music's spectrum under the clock."},
		boolV(func(c *Config) *bool { return &c.MusicLevels }))
	reg(Key{Name: "sendspin_port", Group: "music", Label: "Player port", Advanced: true,
		Doc: "The player listens here for Music Assistant (ws://<speaker>:<port>/sendspin)."},
		intV(1, 65535, func(c *Config) *int { return &c.SendspinPort }))
	reg(Key{Name: "sendspin_bin", Group: "music", Label: "Player binary", Advanced: true,
		Doc: "The sendspin-host binary."}, textV(func(c *Config) *string { return &c.SendspinBin }, nil))
	reg(Key{Name: "sendspin_state", Group: "music", Label: "Player identity file", Advanced: true,
		Doc: "The player's stable ID and pairing records."}, textV(func(c *Config) *string { return &c.SendspinState }, nil))

	// --- intercom ---
	reg(Key{Name: "intercom", Group: "intercom", Label: "Intercom", Sync: true,
		Doc: "Calls between speakers on the LAN. Needs the same secret on every speaker and the MQTT broker."},
		boolV(func(c *Config) *bool { return &c.Intercom }))
	reg(Key{Name: "intercom_secret", Group: "intercom", Label: "Intercom secret", Sync: true,
		Doc: "At least 16 characters, the same on every speaker: it encrypts the calls."},
		secretV(func(c *Config) *string { return &c.IntercomSecret }, func(v string) error {
			if len(v) < 16 {
				return fmt.Errorf("want at least 16 characters")
			}
			return nil
		}))
	reg(Key{Name: "intercom_name", Group: "intercom", Label: "Room name",
		Doc: "The name other speakers and Home Assistant call this speaker by; empty uses the speaker's name."},
		textV(func(c *Config) *string { return &c.IntercomName }, nil))
	reg(Key{Name: "intercom_auto_answer", Group: "intercom", Label: "Answer by itself", Sync: true,
		Doc: "Pick up incoming calls without a key press."},
		boolV(func(c *Config) *bool { return &c.IntercomAutoAnswer }))
	reg(Key{Name: "intercom_ring_secs", Group: "intercom", Label: "Ring for", Sync: true,
		Doc: "An unanswered call gives up after this long."},
		durV(5, 300, time.Second, func(c *Config) *time.Duration { return &c.IntercomRing }))
	reg(Key{Name: "intercom_max_secs", Group: "intercom", Label: "Longest call", Sync: true,
		Doc: "A call ends by itself after this long; 0 = no limit."},
		durV(0, 24*3600, time.Second, func(c *Config) *time.Duration { return &c.IntercomMax }))
	reg(Key{Name: "intercom_jitter_ms", Group: "intercom", Label: "Jitter buffer", Unit: "ms", Sync: true, Advanced: true,
		Doc: "How much audio the receiver holds back to smooth the network."},
		intV(20, 160, func(c *Config) *int { return &c.IntercomJitterMs }))
	reg(Key{Name: "intercom_gain_db", Group: "intercom", Label: "Uplink gain", Unit: "dB", Advanced: true,
		Doc: "The uplink's fixed gain when the VQE's call mode is missing; 0 = the default, 30."},
		value{kind: "int", min: 0, max: 50,
			get: func(c *Config) []string { return one(strconv.Itoa(int(c.IntercomGainDB))) },
			set: func(c *Config, v string) error {
				n, err := atoiRange(v, 0, 50)
				c.IntercomGainDB = float64(n)
				return err
			}})
	reg(Key{Name: "intercom_port", Group: "intercom", Label: "Media port", Advanced: true,
		Doc: "The UDP port for call audio."}, intV(1, 65535, func(c *Config) *int { return &c.IntercomPort }))
	reg(Key{Name: "vqe_models", Group: "intercom", Label: "VQE models", Advanced: true,
		Doc: "The VQE's model directory; empty uses <cfw>/vendor/vqe_models if there, else the firmware's."},
		textV(func(c *Config) *string { return &c.VQEModels }, nil))
	reg(Key{Name: "intercom_record_dir", Group: "intercom", Label: "Record calls to", Advanced: true,
		Doc: "Testing: each call's raw uplink is saved in this directory."},
		textV(func(c *Config) *string { return &c.IntercomRecordDir }, nil))

	// --- wi-fi setup mode ---
	reg(Key{Name: "wifi_setup", Group: "wifi_setup", Label: "Wi-Fi setup mode",
		Doc: "Mute the mic, then hold the voice key for 5 s: the speaker becomes an access point with a page for its saved networks."},
		boolV(func(c *Config) *bool { return &c.WifiSetup }))
	reg(Key{Name: "wifi_setup_ssid", Group: "wifi_setup", Label: "Network name",
		Doc: "The access point's name; empty uses yastation-<device ID>."},
		textV(func(c *Config) *string { return &c.WifiSetupSSID }, func(v string) error {
			if l := len(v); l == 0 || l > 32 {
				return fmt.Errorf("want 1..32 bytes")
			}
			return nil
		}))
	reg(Key{Name: "wifi_setup_psk", Group: "wifi_setup", Label: "Password", Sync: true,
		Doc: "8–63 characters for WPA2; not set, the network is open."},
		secretV(func(c *Config) *string { return &c.WifiSetupPSK }, func(v string) error {
			if l := len(v); l < 8 || l > 63 {
				return fmt.Errorf("want 8..63 characters")
			}
			return nil
		}))
	reg(Key{Name: "wifi_setup_channel", Group: "wifi_setup", Label: "Channel", Sync: true, Advanced: true,
		Doc: "The access point's Wi-Fi channel."}, intV(1, 13, func(c *Config) *int { return &c.WifiSetupChannel }))
	reg(Key{Name: "wifi_setup_address", Group: "wifi_setup", Label: "Address", Advanced: true,
		Doc: "The speaker's address on its access point, in a /24."},
		textV(func(c *Config) *string { return &c.WifiSetupAddress }, func(v string) error {
			ip, n, err := net.ParseCIDR(v)
			if err != nil {
				return err
			}
			if ones, _ := n.Mask.Size(); ip.To4() == nil || ones != 24 {
				return fmt.Errorf("want an IPv4 address in a /24, e.g. 10.42.0.1/24")
			}
			return nil
		}))
	reg(Key{Name: "hostapd_bin", Group: "wifi_setup", Label: "hostapd binary", Advanced: true,
		Doc: "Our hostapd build."}, textV(func(c *Config) *string { return &c.HostapdBin }, nil))
	reg(Key{Name: "wpa_conf", Group: "wifi_setup", Label: "wpa_supplicant config", Advanced: true,
		Doc: "Empty uses the running wpa_supplicant's -c."}, textV(func(c *Config) *string { return &c.WPAConf }, nil))

	// --- when the connection is lost ---
	reg(Key{Name: "net_watchdog_min", Group: "network", Label: "Reboot when offline for", Sync: true,
		Doc: "Reboot when the default gateway has been unreachable this long (doubling after each such reboot, up to 4 h); 0 turns it off."},
		durV(0, 1440, time.Minute, func(c *Config) *time.Duration { return &c.NetWatchdog }))
	reg(Key{Name: "net_wifi_recovery", Group: "network", Label: "Wi-Fi recovery", Sync: true, Advanced: true,
		Doc: "Before rebooting, bring the Wi-Fi back as the stock firmware does: reload the Wi-Fi config, then reset the controller."},
		boolV(func(c *Config) *bool { return &c.NetWifiRecovery }))
	reg(Key{Name: "net_wifi_reconfigure_min", Group: "network", Label: "Reload Wi-Fi config after", Sync: true, Advanced: true,
		Doc: "0 skips this step."}, durV(0, 1440, time.Minute, func(c *Config) *time.Duration { return &c.NetWifiReconfigure }))
	reg(Key{Name: "net_wifi_reset_min", Group: "network", Label: "Reset Wi-Fi after", Sync: true, Advanced: true,
		Doc: "0 skips this step."}, durV(0, 1440, time.Minute, func(c *Config) *time.Duration { return &c.NetWifiReset }))
	reg(Key{Name: "net_wifi_reset_every_min", Group: "network", Label: "Reset Wi-Fi again every", Sync: true, Advanced: true,
		Doc: "0 resets only once."}, durV(0, 1440, time.Minute, func(c *Config) *time.Duration { return &c.NetWifiResetEvery }))

	// --- mic ---
	reg(Key{Name: "mic_spl_offset", Group: "calibration", Label: "Mic calibration", Unit: "dB",
		Doc: "This speaker's dBFS to dB SPL offset, over the board's; 0 uses the board's."},
		floatV(-200, 200, func(c *Config) *float64 { return &c.MicSPLOffset }))
}

// overrideV: a hardware override prints empty unless the file sets it (the board's value rules).
func overrideV(name string, v value) value {
	get := v.get
	v.get = func(c *Config) []string {
		if !c.Has(name) {
			return one("")
		}
		return get(c)
	}
	return v
}

func nonEmpty(v string) error {
	if v == "" {
		return fmt.Errorf("empty")
	}
	return nil
}

// CurveString prints a brightness curve as the config writes it.
func CurveString(c []CurvePoint) string {
	parts := make([]string, len(c))
	for i, p := range c {
		parts[i] = strconv.FormatFloat(p.Lux, 'f', -1, 64) + ":" + strconv.Itoa(p.Level)
	}
	return strings.Join(parts, ", ")
}

// Changed lists the keys whose values differ between a and b, or that one file sets and the other
// doesn't (that matters for the hardware overrides, see Has), in registry order.
func Changed(a, b *Config) []string {
	var out []string
	for _, k := range Keys {
		if a.Has(k.Name) != b.Has(k.Name) || !equal(k.get(a), k.get(b)) {
			out = append(out, k.Name)
		}
	}
	return out
}

// NeedsRestart reports whether any of keys only takes effect on a restart.
func NeedsRestart(keys []string) bool {
	for _, n := range keys {
		if k := keyIndex[n]; k == nil || !k.Runtime {
			return true
		}
	}
	return false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
