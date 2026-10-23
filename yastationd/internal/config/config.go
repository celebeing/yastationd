// Package config reads <cfw>/yastationd.conf (see CFWRoot): one `key = value` per line,
// `#` comments, repeated keys accumulate (used for lists such as `probe` and `unmute`).
package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
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

	ClockBrightness int  // manual clock brightness at first start (later: HA "Clock brightness")
	RingBrightness  int  // HA light brightness at first start (0..255)
	AutoBrightness  bool // clock brightness from the light sensor at first start (later: HA switch)
	// Light-sensor reading -> clock brightness, interpolated on log(lux) between the points
	// (ascending lux) and clamped at the ends. The reading is the sensor's own scale, not true
	// lux: the sensor sits behind the grille and its spectrum differs from a lux meter's.
	BrightnessCurve []CurvePoint
	TZ              string

	VolumeStep int // raw volume steps per key press

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

	// Wyoming satellite server port for HA's Wyoming integration (10700 is the convention); 0 = off.
	WyomingPort int

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

// CFWRoot is where our files live on the device: /bkupgrade/cfw where /bkupgrade exists
// (bergamot), else /data/cfw (pickle has no /bkupgrade — and must not get one: its S40wifi
// would then look for wpa_supplicant.conf under /bkupgrade/data). Same rule as the run.sh gate.
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
		DeviceID:        "mini3",
		DeviceName:      "Yandex Station Mini 3",
		MQTTPort:        1883,
		TopicPrefix:     "yastation",
		DiscoveryPrefix: "homeassistant",
		Board:           "auto",
		BoardFile:       cfw + "/board.conf",
		ClockBrightness: 128,
		AutoBrightness:  true,
		BrightnessCurve: DefaultBrightnessCurve,
		RingBrightness:  128,
		TZ:              "Europe/Moscow",
		VolumeStep:      8,
		WyomingPort:     10700,
		WakeEnabled:     true,
		SpotterBin:      cfw + "/bin/spotter-host",
		PrerollMs:       300,
		TimeSync:        true,
		ChronyConf:      "/etc/chrony.conf",
		HeartbeatFile:   "/tmp/yastationd.heartbeat",
		StateFile:       cfw + "/yastationd.state",
		LogLevel:        "info",
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
	case "preroll_ms":
		c.PrerollMs, err = atoiRange(v, 0, 2000)
	case "mic_pdm_gain":
		c.MicPDMGain, err = atoiRange(v, -1, 48)
	case "mic_channel":
		c.MicChannel, err = atoiRange(v, 0, 15)
	case "wyoming_port":
		c.WyomingPort, err = atoiRange(v, 0, 65535)
	case "aplay_device":
		c.AplayDevice = v
	case "volume_step":
		c.VolumeStep, err = atoiRange(v, 1, 255)
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
