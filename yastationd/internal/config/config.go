// Package config reads <cfw>/yastationd.conf (see CFWRoot): one `key = value` per line,
// `#` comments, repeated keys accumulate (used for lists such as `probe` and `unmute`).
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // tz names are checked while parsing

	"github.com/celebeing/yastationd/yastationd/internal/ddp"
	"github.com/celebeing/yastationd/yastationd/internal/luxcal"
	"github.com/celebeing/yastationd/yastationd/internal/web"
)

// Config is the daemon's configuration. Every key is declared in registry.go (Keys), which
// parses and prints it; the fields here only hold the values.
type Config struct {
	File string // the file it was loaded from (set by main; the setup page edits it)

	DeviceID   string
	DeviceName string

	MQTTHost        string
	MQTTPort        int
	MQTTUser        string
	MQTTPass        string
	TopicPrefix     string // state/command topics: <prefix>/<id>/...
	DiscoveryPrefix string // HA discovery: <discovery>/<component>/yastation_<id>/<obj>/config

	// Root prepended to every /sys and /dev path; empty on the device, a fake tree in tests.
	SysRoot string

	// Board profile (internal/board): "auto" = detect from the device tree, or a codename. The
	// override file (if present) adjusts profile keys. The hardware keys further down (aplay_device,
	// amixer_card, unmute, mic_*, wake_model, command_model, spotter_libs) override the profile
	// only when they appear in this config — see Has.
	Board     string
	BoardFile string

	LuxPath string // empty = the board's IIO lux sensor
	// LuxCalibration: this unit's own "reading:lux" points, over the board profile's
	// (docs/sensor-calibration.md); nil = the profile's.
	LuxCalibration []luxcal.Point

	ClockBrightness int // clock brightness at first start (later: HA "Clock brightness")
	// AutoBrightnessMin: the dimmest auto brightness goes (1..255); 0 = the board's
	// matrix.min_brightness.
	AutoBrightnessMin   int
	RingBrightness      int // ring light brightness at first start (0..255)
	AmbilightBrightness int // rear ambilight brightness at first start (0..255)
	// DDPPort: UDP port for LED frames streamed to the ambilight (LedFx, WLED tools; 0 = off).
	DDPPort int
	// AmbilightFxWhite: the music effects use the strip's white channel (else what would go
	// there is drawn as R=G=B). On by default.
	AmbilightFxWhite bool
	AutoBrightness   bool // clock brightness from the light sensor at first start (later: HA switch)
	// Light-sensor reading -> clock brightness, interpolated on log(lux) between the points
	// (ascending lux) and clamped at the ends. The reading is the sensor's own scale, not true
	// lux: the sensor sits behind the grille and its spectrum differs from a lux meter's.
	BrightnessCurve []CurvePoint
	TZ              string

	VolumeBeep    bool   // tick the board's volume sound on a volume key press
	AnnounceChime bool   // chime before an announcement (audio from HA with no dialog going on)
	DisplayFaces  bool   // voice expressions (faces, clips) on the front display at first start (later: HA switch)
	ClipsDir      string // our converted stock clips (`make clips`), searched before the firmware's
	ScenariosDir  string // the user's scenarios (*.json), next to the built-in ones
	VolumeStep    int    // volume curve steps (5 % each) per key press; a voice «громче»/«тише» is 2 presses
	VolumeMax     int    // the loudest the speaker may go, in % (keys, the ring, HA, music); 100 = no cap

	// Hardware overrides of the board profile (effective only if set in the file).
	AmixerCard   string
	AplayDevice  string
	Unmute       []string // replaces the profile's unmute list
	MicDevice    string
	MicChannels  int
	MicRate      int
	MicChannel   int
	MicPDMGain   int
	MicSPLOffset float64 // this unit's dBFS -> dB SPL offset, over the profile's (0 = the profile's)
	SpotterLibs  string
	WakeModel    string
	CommandModel string

	// Wake word: spotter-host serve (vendor spotter + VQE) as a child process.
	WakeEnabled bool
	SpotterBin  string
	PrerollMs   int // VQE audio sent to HA from before the WAKE point (the wake word cut off); 0 = the board's
	// WakeWordInStream: the audio for HA starts WakeWordLead before the WAKE point instead, with
	// the wake word in it. WakeCapture saves the audio around each WAKE (docs/wake-word-boundary.md).
	WakeWordInStream bool
	WakeCapture      bool

	// A pending reply: how long we wait for HA's answer (HA has no timeout of its own) before
	// cancelling it, and what a wake word does meanwhile: "interrupt" (drop the connection, start
	// a new session once HA is back, the speech buffered meanwhile) or "ignore" (blink "busy").
	ReplyTimeout time.Duration
	// Boot: how long the first connection (broker + HA's satellite link) may take before the
	// speaker shows "first connection failed" (docs/status-indication.md).
	BootConnectTimeout time.Duration
	WakeWhileWaiting   string

	// Wyoming satellite server port for HA's Wyoming integration (10700 is the convention); 0 = off.
	WyomingPort int
	HARingLight bool // the ring as an HA light entity (ha_ring_light); off: MQTT topics only

	// Frame tap: an HTTP listen address (":8930") that streams the LED surfaces and takes commands,
	// for the browser preview (docs/led-preview.md). "" = off. No authentication: LAN only.
	FrameTapAddr string
	// LEDPreview: the frame tap on the web server (/tap/, behind the login) for the app's LED
	// preview tab (docs/web-ui.md).
	LEDPreview bool

	// TextLines: text on a pixel screen (orion) — 0 auto, 1 one line, 2 two lines
	// (docs/display-expressions.md, "Text on the pixel screen").
	TextLines int
	// MarqueeSpeed scales how fast running lines move (every front display; 1 = the default pace).
	MarqueeSpeed float64

	// Room sound sensors for HA (sound_floor, sound_level, sound_activity: levels of a raw mic
	// channel, once a minute; docs/hass-integration.md). Keeps the mic open even with the wake
	// word off; never sends audio.
	RoomSound bool

	// The web UI (docs/web-ui.md): HTTPAddr is the speaker's HTTP server ("" = none; the setup
	// page then listens on its own); WebUser and WebPassword (a pbkdf2-sha256 hash, "" = the
	// documented default password) the login.
	HTTPAddr    string
	WebUser     string
	WebPassword string

	// Wake-word arbitration (docs/wake-arbitration.md): speakers that hear the same «Алиса» agree
	// over MQTT which one answers — the loudest. ArbitrationWait is the longest a quiet claim
	// waits for louder ones.
	Arbitration     bool
	ArbitrationWait time.Duration

	// Logs off the speaker (internal/logship): the daemon's and the system's log lines go as
	// syslog over TCP to LogRemote ("host:port"; "" = off); while it is unreachable they wait in
	// a spool file on flash capped at LogSpoolKB.
	LogRemote  string
	LogSpoolKB int
	// NetWatchdog: with the default gateway unreachable this long the speaker reboots (a Wi-Fi
	// that stopped passing data needs it); longer after each watchdog reboot. 0 = off.
	NetWatchdog time.Duration
	// Before that the watchdog tries to bring the Wi-Fi back like the firmware does
	// (docs/storage.md): NetWifiRecovery on/off; wpa_supplicant reloads its configuration after
	// NetWifiReconfigure, the controller is reset after NetWifiReset and again every
	// NetWifiResetEvery (0 = that step off).
	NetWifiRecovery    bool
	NetWifiReconfigure time.Duration
	NetWifiReset       time.Duration
	NetWifiResetEvery  time.Duration

	// Video display mode (docs/video-mode.md): a frames file for the pixel front screen (the .led
	// pattern format, may be gzipped; path+".gz" is tried too) and its audio track (a WAV for aplay),
	// both made by cmd/videoconv. VideoAVOffset shifts the picture against the sound: positive =
	// the picture later (aplay's start-up and buffer latency).
	VideoFrames   string
	VideoAudio    string
	VideoAVOffset time.Duration

	// Music Assistant player: sendspin-host as a child process (off unless enabled and present).
	Sendspin         bool
	SendspinBin      string
	SendspinName     string // shown in Music Assistant; "" = device_name
	SendspinPort     int    // it listens here for the server (ws://<speaker>:<port>/sendspin)
	SendspinServer   string // optional ws:// URL it also dials itself
	SendspinUnpaired bool   // an unpaired server may play
	SendspinState    string // its identity (stable client_id) and pairing records
	// linked: the player volume in Music Assistant is the speaker's volume (keys, HA, assistant);
	// separate: it is a music-only gain under the speaker's volume.
	SendspinVolume string
	MusicDuckDB    int // music level while the assistant listens/talks/rings, dB below normal
	// The music on the front display (docs/music-sendspin.md): the track's title as a running line
	// when a new one starts (not on resume), the pause/resume animations, and the spectrum under
	// the clock (pixel screens only: on seg7 it would look poor, the clock stays).
	MusicTitle  bool
	MusicClips  bool
	MusicLevels bool

	// Wi-Fi setup mode (docs/wifi-setup.md): the speaker becomes an access point with a page for
	// editing the saved networks, entered by holding the voice key for 5 s with the mic muted
	// (the same combo on every board; orion has no volume keys). Lasts until it is left or the
	// speaker reboots.
	WifiSetup        bool
	WifiSetupSSID    string // "" = yastation-<device_id>
	WifiSetupPSK     string // "" = an open network
	WifiSetupChannel int
	WifiSetupAddress string // the speaker's address/prefix on its access point
	HostapdBin       string
	WPAConf          string // "" = the running wpa_supplicant's -c

	// Intercom (docs/intercom.md): 1-to-1 calls to another speaker over the LAN. Needs the same
	// IntercomSecret on every speaker; off without one.
	Intercom           bool
	IntercomSecret     string
	IntercomName       string // the room name others call us by; "" = device_name
	IntercomPort       int    // UDP media
	IntercomAutoAnswer bool
	IntercomRing       time.Duration // an unanswered call gives up
	IntercomMax        time.Duration // hard cap of a call
	IntercomJitterMs   int
	VQEModels          string  // the VQE's model dir ("" = <cfw>/vendor/vqe_models if there, else the firmware's)
	IntercomGainDB     float64 // the uplink's fixed gain over the VQE stream (0 = the default, 30)
	IntercomRecordDir  string  // testing: each call's raw uplink (before the guard) is saved here

	// Clock: SNTP sync when no chronyd/ntpd runs (internal/timesync). ntp_server entries are tried
	// first, then the servers in chrony_conf, then the default gateway.
	TimeSync   bool
	NTPServers []string
	ChronyConf string

	Probes        []string // host:port reachability probes for the health state machine
	HeartbeatFile string
	StateFile     string // persisted HA-set state (ring colour, display mode)
	LogLevel      string

	present map[string]bool // keys present in the parsed file
	Skipped []string        // lines ParseLenient left out, as errors
}

// Has reports whether key appeared in the parsed config (used for profile overrides).
func (c Config) Has(key string) bool { return c.present[key] }

// CFWRoot is where our files live on the device: /bkupgrade/cfw on bergamot and orion,
// /data/cfw on pickle. Deployment derives the authoritative root from the board profile; this
// filesystem check is the runtime bootstrap fallback before that profile can be loaded.
func CFWRoot() string {
	if st, err := os.Stat("/bkupgrade/cfw"); err == nil && st.IsDir() {
		return "/bkupgrade/cfw"
	}
	return "/data/cfw"
}

// CurvePoint maps a light-sensor reading to a clock brightness (0..255).
type CurvePoint struct {
	Lux   float64
	Level int
}

// DefaultBrightnessCurve, in lux (the sensors are calibrated, docs/sensor-calibration.md): the
// curve the black bergamot was tuned with on 2026-10-23 (reading 0.04 → 4, the dimmest still
// readable in the dark, which the board minimum replaces; 0.55 → 128, comfortable in a lit room;
// 10 → 255), turned into lux by that unit's calibration of 2026-10-26.
var DefaultBrightnessCurve = []CurvePoint{{0.7, 4}, {9, 128}, {150, 255}}

func parseCurve(v string) ([]CurvePoint, error) {
	var out []CurvePoint
	for _, f := range strings.Split(v, ",") {
		lx, lv, ok := strings.Cut(strings.TrimSpace(f), ":")
		if !ok {
			return nil, fmt.Errorf("want lux:level pairs, got %q", f)
		}
		x, err := strconv.ParseFloat(strings.TrimSpace(lx), 64)
		if err != nil || x <= 0 {
			return nil, fmt.Errorf("bad lux %q", lx)
		}
		y, err := atoiRange(strings.TrimSpace(lv), 0, 255)
		if err != nil {
			return nil, err
		}
		if len(out) > 0 && x <= out[len(out)-1].Lux {
			return nil, fmt.Errorf("lux values must ascend")
		}
		out = append(out, CurvePoint{x, y})
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("need at least 2 points")
	}
	return out, nil
}

// Default is the configuration with no file: every key at its default.
func Default() Config {
	cfw := CFWRoot()
	return Config{
		DeviceID:            "mini3",
		DeviceName:          "Yandex Station Mini 3",
		MQTTPort:            1883,
		TopicPrefix:         "yastation",
		DiscoveryPrefix:     "homeassistant",
		Board:               "auto",
		BoardFile:           cfw + "/board.conf",
		ClockBrightness:     255,
		AutoBrightness:      true,
		BrightnessCurve:     DefaultBrightnessCurve,
		RingBrightness:      128,
		AmbilightBrightness: 128,
		DDPPort:             ddp.DefaultPort,
		TZ:                  "Europe/Moscow",
		VolumeStep:          1,
		VolumeMax:           100,
		VolumeBeep:          true,
		AnnounceChime:       true,
		DisplayFaces:        true,
		ClipsDir:            cfw + "/clips",
		ScenariosDir:        cfw + "/scenarios",
		VideoFrames:         cfw + "/media/video.led",
		VideoAudio:          cfw + "/media/video.wav",
		WyomingPort:         10700,
		SendspinBin:         cfw + "/bin/sendspin-host",
		SendspinPort:        8928,
		SendspinUnpaired:    true,
		SendspinState:       cfw + "/sendspin/state.json",
		SendspinVolume:      "linked",
		MusicDuckDB:         -26,
		AmbilightFxWhite:    true,
		MusicTitle:          true,
		MarqueeSpeed:        1,
		MusicClips:          true,
		MusicLevels:         true,
		WifiSetup:           true,
		WifiSetupChannel:    6,
		WifiSetupAddress:    "10.42.0.1/24",
		HostapdBin:          cfw + "/bin/hostapd",
		IntercomPort:        10710,
		IntercomRing:        30 * time.Second,
		IntercomMax:         30 * time.Minute,
		IntercomJitterMs:    40,
		WakeEnabled:         true,
		RoomSound:           true,
		LogSpoolKB:          1024,
		Arbitration:         true,
		HTTPAddr:            ":80",
		LEDPreview:          true,
		WebUser:             web.DefaultUser,
		ArbitrationWait:     300 * time.Millisecond,
		NetWatchdog:         10 * time.Minute,
		NetWifiRecovery:     true,
		NetWifiReconfigure:  2 * time.Minute,
		NetWifiReset:        4 * time.Minute,
		NetWifiResetEvery:   10 * time.Minute,
		SpotterBin:          cfw + "/bin/spotter-host",
		ReplyTimeout:        90 * time.Second,
		BootConnectTimeout:  40 * time.Second,
		WakeWhileWaiting:    "interrupt",
		TimeSync:            true,
		ChronyConf:          "/etc/chrony.conf",
		HeartbeatFile:       "/tmp/yastationd.heartbeat",
		StateFile:           cfw + "/yastationd.state",
		LogLevel:            "info",
	}
}

// Load reads a config file strictly (see Parse).
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	return Parse(f)
}

// Parse reads a config, failing on the first bad line (ParseLenient skips them instead).
func Parse(r io.Reader) (Config, error) {
	c, errs := ParseLenient(r)
	if len(errs) > 0 {
		return c, errs[0]
	}
	return c, nil
}

// LineError is a line ParseLenient skipped.
type LineError struct {
	Line int
	Text string // the line, trimmed
	Key  string
	Err  error
}

func (e LineError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("line %d: %v", e.Line, e.Err)
	}
	return fmt.Sprintf("line %d: %s: %v", e.Line, e.Key, e.Err)
}

// ParseLenient parses like Parse but skips a bad line (its key keeps the default) and goes on,
// reporting every skipped line. The daemon loads its file this way: a key an older build took
// and this one refuses must not keep the speaker from starting.
func ParseLenient(r io.Reader) (Config, []LineError) {
	c := Default()
	var errs []LineError
	sc := bufio.NewScanner(r)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			errs = append(errs, LineError{line, s, "", fmt.Errorf("expected key = value")})
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		saved := c
		if err := c.set(k, v); err != nil {
			present := c.present
			c = saved // the key keeps the value it had
			c.present = present
			delete(c.present, k)
			errs = append(errs, LineError{line, s, k, err})
		}
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, LineError{line, "", "", err})
	}
	for _, e := range errs {
		c.Skipped = append(c.Skipped, e.Error())
	}
	return c, errs
}

// LoadLenient reads a file with ParseLenient.
func LoadLenient(path string) (Config, []LineError, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, nil, err
	}
	defer f.Close()
	c, errs := ParseLenient(f)
	return c, errs, nil
}

func (c *Config) set(k, v string) error {
	if c.present == nil {
		c.present = map[string]bool{}
	}
	c.present[k] = true
	key := keyIndex[k]
	if key == nil {
		return fmt.Errorf("unknown key")
	}
	return key.set(c, v)
}

func atoiRange(v string, lo, hi int) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%d out of range %d..%d", n, lo, hi)
	}
	return n, nil
}

// Location resolves TZ; the binary embeds tzdata, so names like Europe/Moscow work on the
// device, which has no zoneinfo.
func (c Config) Location() *time.Location {
	if loc, err := time.LoadLocation(c.TZ); err == nil {
		return loc
	}
	return time.FixedZone("MSK", 3*3600)
}

// Path maps an absolute device path into SysRoot.
func (c Config) Path(p string) string {
	if c.SysRoot == "" {
		return p
	}
	return strings.TrimRight(c.SysRoot, "/") + p
}

// Edit rewrites the config text with new values for some keys: a key's first active line is
// replaced by its new lines (several for a list key such as probe), its other lines are dropped,
// and a key that wasn't there is appended. An empty list removes the key. Everything else
// (comments, commented-out examples, other keys) stays byte for byte. The caller validates the
// result with Parse.
func Edit(data []byte, changes map[string][]string) []byte {
	return EditNote(data, changes, "set from the Wi-Fi setup page")
}

// EditNote is Edit with the comment above appended keys.
func EditNote(data []byte, changes map[string][]string, note string) []byte {
	var out []string
	done := map[string]bool{}
	lines := strings.SplitAfter(string(data), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		s := strings.TrimSpace(line)
		k, _, ok := strings.Cut(s, "=")
		k = strings.TrimSpace(k)
		vals, edit := changes[k]
		if s == "" || strings.HasPrefix(s, "#") || !ok || !edit {
			out = append(out, line)
			continue
		}
		if !done[k] {
			done[k] = true
			for _, v := range vals {
				out = append(out, k+" = "+v+"\n")
			}
		}
	}
	if n := len(out); n > 0 && !strings.HasSuffix(out[n-1], "\n") {
		out[n-1] += "\n"
	}
	var added []string
	for _, k := range sortedKeys(changes) {
		if done[k] {
			continue
		}
		for _, v := range changes[k] {
			added = append(added, k+" = "+v+"\n")
		}
	}
	// A note left last (its keys removed since) goes, or takes the new keys under it, so
	// repeated edits don't pile notes up.
	end := len(out)
	for end > 0 && strings.TrimSpace(out[end-1]) == "" {
		end--
	}
	block := end // the last note's keys run to the end: new keys join them
	for block > 0 && !strings.HasPrefix(strings.TrimSpace(out[block-1]), "#") && strings.TrimSpace(out[block-1]) != "" {
		block--
	}
	if len(added) > 0 && block > 0 && out[block-1] == "# "+note+"\n" {
		out = append(out[:end], added...)
		return []byte(strings.Join(out, ""))
	}
	if end > 0 && ownNote(out[end-1]) {
		end--
		for end > 0 && strings.TrimSpace(out[end-1]) == "" {
			end--
		}
		out = out[:end]
	}
	if len(added) > 0 {
		out = append(out, "\n# "+note+"\n")
		out = append(out, added...)
	}
	return []byte(strings.Join(out, ""))
}

// ownNote: a comment line EditNote writes above the keys it appends (any writer's).
func ownNote(line string) bool {
	for _, p := range []string{"# set from the ", "# shared by the other speakers", "# the light sensor, calibrated from the web app"} {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
