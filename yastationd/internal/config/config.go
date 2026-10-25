// Package config reads <cfw>/yastationd.conf (see CFWRoot): one `key = value` per line,
// `#` comments, repeated keys accumulate (used for lists such as `probe` and `unmute`).
package config

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

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

	ClockBrightness     int  // manual clock brightness at first start (later: HA "Clock brightness")
	RingBrightness      int  // ring light brightness at first start (0..255)
	AmbilightBrightness int  // rear ambilight brightness at first start (0..255)
	AutoBrightness      bool // clock brightness from the light sensor at first start (later: HA switch)
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

	// Hardware overrides of the board profile (effective only if set in the file).
	AmixerCard   string
	AplayDevice  string
	Unmute       []string // replaces the profile's unmute list
	MicDevice    string
	MicChannels  int
	MicRate      int
	MicChannel   int
	MicPDMGain   int
	SpotterLibs  string
	WakeModel    string
	CommandModel string

	// Wake word: spotter-host serve (vendor spotter + VQE) as a child process.
	WakeEnabled bool
	SpotterBin  string
	PrerollMs   int // VQE audio before the wake sent to HA, so the first word survives

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

	// Wi-Fi setup mode (docs/wifi-setup.md): the speaker becomes an access point with a page for
	// editing the saved networks, entered by holding volume-down + voice + mute for 5 s. Lasts
	// until it is left or the speaker reboots.
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
	IntercomRecordDir  string // testing: each call's raw uplink (before the guard) is saved here

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
}

// Has reports whether key appeared in the parsed config (used for profile overrides).
func (c Config) Has(key string) bool { return c.present[key] }

// CFWRoot is where our files live on the device: /bkupgrade/cfw on bergamot, /data/cfw on
// pickle/orion. Deployment derives the authoritative root from the board profile; this
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

// DefaultBrightnessCurve: calibrated on a bergamot 2026-10-23 against a lux meter next to it —
// ~1 lx (lights off) reads 0.03 and wants 4 (the dimmest still readable in the dark), 37 lx
// (normal room light) reads 0.55 and wants 128, office light reads 10+ and gets 255.
var DefaultBrightnessCurve = []CurvePoint{{0.04, 4}, {0.55, 128}, {10, 255}}

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
		ClockBrightness:     128,
		AutoBrightness:      true,
		BrightnessCurve:     DefaultBrightnessCurve,
		RingBrightness:      128,
		AmbilightBrightness: 128,
		TZ:                  "Europe/Moscow",
		VolumeStep:          1,
		VolumeBeep:          true,
		AnnounceChime:       true,
		DisplayFaces:        true,
		ClipsDir:            cfw + "/clips",
		ScenariosDir:        cfw + "/scenarios",
		WyomingPort:         10700,
		SendspinBin:         cfw + "/bin/sendspin-host",
		SendspinPort:        8928,
		SendspinUnpaired:    true,
		SendspinState:       cfw + "/sendspin/state.json",
		SendspinVolume:      "linked",
		MusicDuckDB:         -26,
		WifiSetup:           true,
		WifiSetupChannel:    6,
		WifiSetupAddress:    "10.42.0.1/24",
		HostapdBin:          cfw + "/bin/hostapd",
		IntercomPort:        10710,
		IntercomRing:        30 * time.Second,
		IntercomMax:         30 * time.Minute,
		IntercomJitterMs:    40,
		WakeEnabled:         true,
		SpotterBin:          cfw + "/bin/spotter-host",
		ReplyTimeout:        90 * time.Second,
		BootConnectTimeout:  40 * time.Second,
		WakeWhileWaiting:    "interrupt",
		PrerollMs:           300,
		TimeSync:            true,
		ChronyConf:          "/etc/chrony.conf",
		HeartbeatFile:       "/tmp/yastationd.heartbeat",
		StateFile:           cfw + "/yastationd.state",
		LogLevel:            "info",
	}
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	return Parse(f)
}

func Parse(r io.Reader) (Config, error) {
	c := Default()
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
			return c, fmt.Errorf("line %d: expected key = value", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if err := c.set(k, v); err != nil {
			return c, fmt.Errorf("line %d: %s: %w", line, k, err)
		}
	}
	return c, sc.Err()
}

func (c *Config) set(k, v string) error {
	var err error
	if c.present == nil {
		c.present = map[string]bool{}
	}
	c.present[k] = true
	switch k {
	case "device_id":
		c.DeviceID = v
	case "device_name":
		c.DeviceName = v
	case "mqtt_host":
		c.MQTTHost = v
	case "mqtt_port":
		c.MQTTPort, err = strconv.Atoi(v)
	case "mqtt_user":
		c.MQTTUser = v
	case "mqtt_pass":
		c.MQTTPass = v
	case "topic_prefix":
		c.TopicPrefix = v
	case "discovery_prefix":
		c.DiscoveryPrefix = v
	case "sysroot":
		c.SysRoot = v
	case "board":
		c.Board = v
	case "board_file":
		c.BoardFile = v
	case "lux_path":
		c.LuxPath = v
	case "clock_brightness":
		c.ClockBrightness, err = atoiRange(v, 0, 255)
	case "ring_brightness":
		c.RingBrightness, err = atoiRange(v, 0, 255)
	case "ambilight_brightness":
		c.AmbilightBrightness, err = atoiRange(v, 0, 255)
	case "auto_brightness":
		c.AutoBrightness, err = strconv.ParseBool(v)
	case "auto_brightness_curve":
		c.BrightnessCurve, err = parseCurve(v)
	case "tz":
		c.TZ = v
	case "amixer_card":
		c.AmixerCard = v
	case "mic_device":
		c.MicDevice = v
	case "mic_channels":
		c.MicChannels, err = atoiRange(v, 1, 16)
	case "mic_rate":
		c.MicRate, err = atoiRange(v, 16000, 96000)
	case "wake_enabled":
		c.WakeEnabled, err = strconv.ParseBool(v)
	case "spotter_bin":
		c.SpotterBin = v
	case "spotter_libs":
		c.SpotterLibs = v
	case "wake_model":
		c.WakeModel = v
	case "command_model":
		c.CommandModel = v
	case "boot_connect_timeout":
		var n int
		n, err = atoiRange(v, 1, 600)
		c.BootConnectTimeout = time.Duration(n) * time.Second
	case "reply_timeout":
		var n int
		n, err = atoiRange(v, 10, 600)
		c.ReplyTimeout = time.Duration(n) * time.Second
	case "wake_while_waiting":
		if v != "interrupt" && v != "ignore" {
			return fmt.Errorf("want interrupt or ignore")
		}
		c.WakeWhileWaiting = v
	case "preroll_ms":
		c.PrerollMs, err = atoiRange(v, 0, 2000)
	case "mic_pdm_gain":
		c.MicPDMGain, err = atoiRange(v, -1, 48)
	case "mic_channel":
		c.MicChannel, err = atoiRange(v, 0, 15)
	case "wyoming_port":
		c.WyomingPort, err = atoiRange(v, 0, 65535)
	case "sendspin":
		c.Sendspin, err = strconv.ParseBool(v)
	case "sendspin_bin":
		c.SendspinBin = v
	case "sendspin_name":
		c.SendspinName = v
	case "sendspin_port":
		c.SendspinPort, err = atoiRange(v, 1, 65535)
	case "sendspin_server":
		c.SendspinServer = v
	case "sendspin_unpaired":
		c.SendspinUnpaired, err = strconv.ParseBool(v)
	case "sendspin_state":
		c.SendspinState = v
	case "sendspin_volume":
		if v != "linked" && v != "separate" {
			return fmt.Errorf("want linked or separate")
		}
		c.SendspinVolume = v
	case "wifi_setup":
		c.WifiSetup, err = strconv.ParseBool(v)
	case "wifi_setup_ssid":
		if l := len(v); l == 0 || l > 32 {
			return fmt.Errorf("want 1..32 bytes")
		}
		c.WifiSetupSSID = v
	case "wifi_setup_psk":
		if l := len(v); l < 8 || l > 63 {
			return fmt.Errorf("want 8..63 characters")
		}
		c.WifiSetupPSK = v
	case "wifi_setup_channel":
		c.WifiSetupChannel, err = atoiRange(v, 1, 13)
	case "wifi_setup_address":
		ip, n, perr := net.ParseCIDR(v)
		if perr != nil {
			return perr
		}
		if ones, _ := n.Mask.Size(); ip.To4() == nil || ones != 24 {
			return fmt.Errorf("want an IPv4 address in a /24, e.g. 10.42.0.1/24")
		}
		c.WifiSetupAddress = v
	case "hostapd_bin":
		c.HostapdBin = v
	case "wpa_conf":
		c.WPAConf = v
	case "intercom":
		c.Intercom, err = strconv.ParseBool(v)
	case "intercom_secret":
		if len(v) < 16 {
			return fmt.Errorf("want at least 16 characters")
		}
		c.IntercomSecret = v
	case "intercom_name":
		c.IntercomName = v
	case "intercom_port":
		c.IntercomPort, err = atoiRange(v, 1, 65535)
	case "intercom_auto_answer":
		c.IntercomAutoAnswer, err = strconv.ParseBool(v)
	case "intercom_ring_secs":
		var n int
		n, err = atoiRange(v, 5, 300)
		c.IntercomRing = time.Duration(n) * time.Second
	case "intercom_max_secs":
		var n int
		n, err = atoiRange(v, 0, 24*3600)
		c.IntercomMax = time.Duration(n) * time.Second
	case "intercom_record_dir":
		c.IntercomRecordDir = v
	case "intercom_jitter_ms":
		c.IntercomJitterMs, err = atoiRange(v, 20, 160)
	case "music_duck_db":
		c.MusicDuckDB, err = atoiRange(v, -60, 0)
	case "aplay_device":
		c.AplayDevice = v
	case "display_faces":
		c.DisplayFaces, err = strconv.ParseBool(v)
	case "clips_dir":
		c.ClipsDir = v
	case "scenarios_dir":
		c.ScenariosDir = v
	case "announce_chime":
		c.AnnounceChime, err = strconv.ParseBool(v)
	case "volume_beep":
		c.VolumeBeep, err = strconv.ParseBool(v)
	case "volume_step":
		c.VolumeStep, err = atoiRange(v, 1, 4)
	case "unmute":
		c.Unmute = append(c.Unmute, v)
	case "time_sync":
		c.TimeSync, err = strconv.ParseBool(v)
	case "ntp_server":
		c.NTPServers = append(c.NTPServers, v)
	case "chrony_conf":
		c.ChronyConf = v
	case "probe":
		c.Probes = append(c.Probes, v)
	case "heartbeat_file":
		c.HeartbeatFile = v
	case "state_file":
		c.StateFile = v
	case "log_level":
		c.LogLevel = v
	default:
		return fmt.Errorf("unknown key")
	}
	return err
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
	if len(added) > 0 {
		out = append(out, "\n# set from the Wi-Fi setup page\n")
		out = append(out, added...)
	}
	return []byte(strings.Join(out, ""))
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
