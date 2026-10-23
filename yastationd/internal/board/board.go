// Package board describes the hardware of one Yandex Station model (its "board profile"): LED
// ring and front display wiring, input devices and key codes, mixer controls, mic-array layout
// and the vendor voice front-end preset. Everything model-specific lives here, so supporting a
// new station is a new profile file rather than code changes.
//
// Profiles use the daemon's config format (key = value, '#' comments, repeated keys accumulate).
// Built-in profiles are embedded from profiles/*.conf; the running device is matched by its
// device-tree "compatible" strings, and a local override file can adjust any key.
package board

import (
	"bufio"
	"embed"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed profiles/*.conf
var builtin embed.FS

// Profile is one board's hardware description.
type Profile struct {
	Name  string   // codename, e.g. "bergamot"
	Model string   // human name for HA, e.g. "Station Mini 3"
	Match []string // device-tree compatible strings that select this profile

	Ring   RingSpec
	Matrix MatrixSpec
	Input  InputSpec
	Audio  AudioSpec
	Mic    MicSpec
	Wake   WakeSpec
	Lux    LuxSpec
	Power  PowerSpec
}

// RingSpec: an RGB ring of Pixels, each pixel = one sysfs LED per colour. LED names come from
// the LED pattern with {color} and {i} substituted. Kind "none" = no ring.
type RingSpec struct {
	Kind   string // "sysfs-rgb" | "none"
	Pixels int
	LED    string   // e.g. "bergamot:{color}:logo{i}"
	Colors []string // sysfs colour names in R,G,B order, e.g. red,green,blue
}

// MatrixSpec: the front display. Kind "seg7" = Digits 7-segment digits, one sysfs LED per
// segment from the Segment pattern ({seg} = A..G, {digit} = 0..Digits-1), plus the colon (one
// or more LEDs), the dot and the degree mark. Kind "none" = no display.
type MatrixSpec struct {
	Kind    string // "seg7" | "none"
	Digits  int
	Segment string
	Colon   []string
	Dot     string
	Degree  string
}

// InputSpec: evdev devices by their kernel name (from /proc/bus/input/devices) and what their
// key/switch codes mean. Actions: volume_up, volume_down, voice, mute_key (re-read the mute
// switch); switch actions: mic_mute.
type InputSpec struct {
	Devices  []string       // device names to read, e.g. aw933xx, gpio-keys
	Keys     map[int]string // EV_KEY code -> action
	Switches map[int]string // EV_SW code -> action
}

// AudioSpec: playback and mixer controls (ALSA card Card).
type AudioSpec struct {
	Playback      string        // aplay device, e.g. "default"
	Card          string        // amixer card
	Volume        string        // full control name of the master volume
	VolumeMax     int           // its maximum raw value
	VolumeDefault int           // raw volume set on first start, before the user ever set one (0 = leave the boot value)
	VolumeMap     []VolumePoint // stock volume curve; nil = a straight line over 20 levels
	VolumeSound   string        // wav ticked on a volume key press ("" = none; absent file = none)
	AnnounceSound string        // wav chimed before an announcement ("" = none; absent file = none)
	Unmute        []string      // amixer cset lines run before playback
	MicGain       string        // hardware mic gain control ("" = none)
	MicGainValue  int           // applied at startup (-1 = leave alone)
}

// VolumePoint is one point of the stock volume curve: platform level -> raw mixer value, linear in
// between. The stock firmware steps the level by 5 % (quasar.cfg volume_capability:
// changeStepPercent 5, volume_manager_settings.platformToAlsaMap), so the last point's level is
// the top (20 = 100 %) and percent p is level p/5.
type VolumePoint struct{ Level, Raw int }

// volumeCurve is the map in use: the profile's, or a straight 0..VolumeMax line over 20 levels.
func (a AudioSpec) volumeCurve() []VolumePoint {
	if len(a.VolumeMap) >= 2 {
		return a.VolumeMap
	}
	return []VolumePoint{{0, 0}, {20, a.VolumeMax}}
}

// VolumeLevels is the number of steps from silence to the top (20 = 5 % each).
func (a AudioSpec) VolumeLevels() int { m := a.volumeCurve(); return m[len(m)-1].Level }

// RawFromLevel maps a (fractional) platform level onto the mixer. Like the stock firmware, a map
// with a first step (bergamot's 0:0 1:33 20:255) never sets the raw values between 0 and that
// step: below it the amp is inaudible, so any level above 0 is at least the first step.
func (a AudioSpec) RawFromLevel(l float64) int {
	m := a.volumeCurve()
	if l <= 0 {
		return m[0].Raw
	}
	if len(m) > 2 && l <= float64(m[1].Level) {
		return m[1].Raw
	}
	for i := 1; i < len(m); i++ {
		if l <= float64(m[i].Level) {
			lo, hi := m[i-1], m[i]
			return lo.Raw + int(math.Round((l-float64(lo.Level))*float64(hi.Raw-lo.Raw)/float64(hi.Level-lo.Level)))
		}
	}
	return m[len(m)-1].Raw
}

// LevelFromRaw is the inverse of RawFromLevel (fractional; raw values off the curve land between
// levels).
func (a AudioSpec) LevelFromRaw(raw int) float64 {
	m := a.volumeCurve()
	if raw <= m[0].Raw {
		return float64(m[0].Level)
	}
	for i := 1; i < len(m); i++ {
		if raw <= m[i].Raw {
			lo, hi := m[i-1], m[i]
			return float64(lo.Level) + float64(raw-lo.Raw)*float64(hi.Level-lo.Level)/float64(hi.Raw-lo.Raw)
		}
	}
	return float64(m[len(m)-1].Level)
}

// RawFromPercent / PercentFromRaw: HA's 0..100 % on the same curve, rounded so a percentage set
// from HA reads back unchanged (except 1..4 %, which land on the first step = 5 %).
func (a AudioSpec) RawFromPercent(pct int) int {
	return a.RawFromLevel(float64(pct) * float64(a.VolumeLevels()) / 100)
}

func (a AudioSpec) PercentFromRaw(raw int) int {
	return int(math.Round(a.LevelFromRaw(raw) * 100 / float64(a.VolumeLevels())))
}

// StepVolume moves raw by n whole levels (negative = down), snapping to the level grid first, so
// keys always land on the stock steps.
func (a AudioSpec) StepVolume(raw, n int) int {
	l := int(math.Round(a.LevelFromRaw(raw))) + n
	return a.RawFromLevel(float64(max(0, min(l, a.VolumeLevels()))))
}

// MicSpec: the raw mic-array capture device as one interleaved stream.
type MicSpec struct {
	Device   string // e.g. hw:0,1
	Channels int    // interleaved channels delivered
	Rate     int    // capture rate (3 x 16 kHz)
	Voice    int    // channel used for raw (no-VQE) voice streaming
}

// WakeSpec: the vendor voice front-end (VQE) + spotter models for spotter-host serve.
type WakeSpec struct {
	VQEPreset    string // vendor preset name, e.g. "bergamot"
	Mics         []int  // capture channels that are the VQE's mics, in preset order
	Ref          int    // capture channel carrying the speaker feedback (AEC reference), -1 = none
	Model        string // activation («Алиса») model dir
	CommandModel string // command model dir ("" = none)
	Libs         string // vendor libs dir (LD_LIBRARY_PATH)
}

// PowerSpec: the USB-C PD sink (power_supply name, "" = none), the lowest contract voltage that
// counts as healthy, and the stock voice line to play when it isn't (a wav: aplay can't do opus).
type PowerSpec struct {
	PD            string
	MinMilliVolts int
	AlertSound    string
}

// LuxSpec: the ambient-light IIO device name prefix ("" = none), and the hardware gain and
// integration time to set at start ("" = leave the driver's) — the stock ones give bergamot's
// ltr308 behind the grille only a couple of counts indoors.
type LuxSpec struct {
	IIOPrefix       string
	Gain            string
	IntegrationTime string
}

// Parse reads a profile, starting from an empty one.
func Parse(r io.Reader) (Profile, error) {
	var p Profile
	return p, p.apply(r)
}

// apply layers key = value lines from r onto p (used for the built-in file and overrides).
func (p *Profile) apply(r io.Reader) error {
	sc := bufio.NewScanner(r)
	cleared := map[string]bool{} // list keys reset on first use in this file
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		if !ok {
			return fmt.Errorf("line %d: expected key = value", line)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if err := p.set(k, v, cleared); err != nil {
			return fmt.Errorf("line %d: %s: %w", line, k, err)
		}
	}
	return sc.Err()
}

func (p *Profile) set(k, v string, cleared map[string]bool) error {
	var err error
	// Repeatable keys replace the inherited list on their first occurrence in a file.
	list := func(dst *[]string) {
		if !cleared[k] {
			*dst, cleared[k] = nil, true
		}
		*dst = append(*dst, v)
	}
	switch {
	case k == "name":
		p.Name = v
	case k == "model":
		p.Model = v
	case k == "match":
		list(&p.Match)
	case k == "ring.kind":
		p.Ring.Kind = v
	case k == "ring.pixels":
		p.Ring.Pixels, err = strconv.Atoi(v)
	case k == "ring.led":
		p.Ring.LED = v
	case k == "ring.colors":
		p.Ring.Colors = splitList(v)
	case k == "matrix.kind":
		p.Matrix.Kind = v
	case k == "matrix.digits":
		p.Matrix.Digits, err = strconv.Atoi(v)
	case k == "matrix.segment":
		p.Matrix.Segment = v
	case k == "matrix.colon":
		p.Matrix.Colon = splitList(v)
	case k == "matrix.dot":
		p.Matrix.Dot = v
	case k == "matrix.degree":
		p.Matrix.Degree = v
	case k == "input.device":
		list(&p.Input.Devices)
	case strings.HasPrefix(k, "key."):
		err = setCode(&p.Input.Keys, strings.TrimPrefix(k, "key."), v)
	case strings.HasPrefix(k, "switch."):
		err = setCode(&p.Input.Switches, strings.TrimPrefix(k, "switch."), v)
	case k == "audio.playback":
		p.Audio.Playback = v
	case k == "audio.card":
		p.Audio.Card = v
	case k == "audio.volume":
		p.Audio.Volume = v
	case k == "audio.volume_max":
		p.Audio.VolumeMax, err = strconv.Atoi(v)
	case k == "audio.volume_default":
		p.Audio.VolumeDefault, err = strconv.Atoi(v)
	case k == "audio.volume_map":
		p.Audio.VolumeMap, err = parseVolumeMap(v)
	case k == "audio.volume_sound":
		p.Audio.VolumeSound = v
	case k == "audio.announce_sound":
		p.Audio.AnnounceSound = v
	case k == "audio.unmute":
		list(&p.Audio.Unmute)
	case k == "audio.mic_gain":
		p.Audio.MicGain = v
	case k == "audio.mic_gain_value":
		p.Audio.MicGainValue, err = strconv.Atoi(v)
	case k == "mic.device":
		p.Mic.Device = v
	case k == "mic.channels":
		p.Mic.Channels, err = strconv.Atoi(v)
	case k == "mic.rate":
		p.Mic.Rate, err = strconv.Atoi(v)
	case k == "mic.voice":
		p.Mic.Voice, err = strconv.Atoi(v)
	case k == "wake.vqe_preset":
		p.Wake.VQEPreset = v
	case k == "wake.mics":
		p.Wake.Mics, err = splitInts(v)
	case k == "wake.ref":
		p.Wake.Ref, err = strconv.Atoi(v)
	case k == "wake.model":
		p.Wake.Model = v
	case k == "wake.command_model":
		p.Wake.CommandModel = v
	case k == "wake.libs":
		p.Wake.Libs = v
	case k == "power.pd":
		p.Power.PD = v
	case k == "power.min_mv":
		p.Power.MinMilliVolts, err = strconv.Atoi(v)
	case k == "power.alert_sound":
		p.Power.AlertSound = v
	case k == "lux.iio_prefix":
		p.Lux.IIOPrefix = v
	case k == "lux.gain":
		p.Lux.Gain = v
	case k == "lux.integration_time":
		p.Lux.IntegrationTime = v
	default:
		return fmt.Errorf("unknown key")
	}
	return err
}

func setCode(m *map[int]string, code, action string) error {
	n, err := strconv.Atoi(code)
	if err != nil {
		return err
	}
	if *m == nil {
		*m = map[int]string{}
	}
	if action == "" || action == "none" {
		delete(*m, n)
	} else {
		(*m)[n] = action
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func splitInts(v string) ([]int, error) {
	var out []int
	for _, s := range splitList(v) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// parseVolumeMap reads "level:raw, level:raw, ...".
func parseVolumeMap(v string) ([]VolumePoint, error) {
	var out []VolumePoint
	for _, s := range splitList(v) {
		l, r, ok := strings.Cut(s, ":")
		lv, err1 := strconv.Atoi(strings.TrimSpace(l))
		rv, err2 := strconv.Atoi(strings.TrimSpace(r))
		if !ok || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%q: want level:raw", s)
		}
		out = append(out, VolumePoint{lv, rv})
	}
	return out, nil
}

// Validate checks what the daemon relies on.
func (p Profile) Validate() error {
	switch p.Ring.Kind {
	case "none":
	case "sysfs-rgb":
		if p.Ring.Pixels <= 0 || p.Ring.LED == "" || len(p.Ring.Colors) != 3 {
			return fmt.Errorf("ring: sysfs-rgb needs pixels > 0, led and 3 colors")
		}
	default:
		return fmt.Errorf("ring.kind %q unknown", p.Ring.Kind)
	}
	switch p.Matrix.Kind {
	case "none":
	case "seg7":
		if p.Matrix.Digits != 4 { // display/matrix.go draws 4 digits; widen it when a board needs it
			return fmt.Errorf("matrix: seg7 supports 4 digits, got %d", p.Matrix.Digits)
		}
		if p.Matrix.Segment == "" {
			return fmt.Errorf("matrix: seg7 needs a segment pattern")
		}
	default:
		return fmt.Errorf("matrix.kind %q unknown", p.Matrix.Kind)
	}
	if p.Audio.Volume == "" || p.Audio.VolumeMax <= 0 {
		return fmt.Errorf("audio: volume control and volume_max are required")
	}
	if p.Audio.VolumeDefault < 0 || p.Audio.VolumeDefault > p.Audio.VolumeMax {
		return fmt.Errorf("audio: volume_default must be 0..volume_max")
	}
	if m := p.Audio.VolumeMap; m != nil {
		if len(m) < 2 || m[0] != (VolumePoint{0, 0}) || m[len(m)-1].Raw > p.Audio.VolumeMax {
			return fmt.Errorf("audio: volume_map needs >= 2 points, starting at 0:0, raw <= volume_max")
		}
		for i := 1; i < len(m); i++ {
			if m[i].Level <= m[i-1].Level || m[i].Raw <= m[i-1].Raw {
				return fmt.Errorf("audio: volume_map must increase")
			}
		}
	}
	if p.Mic.Device != "" && (p.Mic.Channels <= 0 || p.Mic.Rate != 48000 || p.Mic.Voice >= p.Mic.Channels) {
		return fmt.Errorf("mic: need channels > voice and rate 48000 (decimated 3x to 16 kHz)")
	}
	return nil
}

// Builtin returns the embedded profile by codename.
func Builtin(name string) (Profile, error) {
	f, err := builtin.Open("profiles/" + name + ".conf")
	if err != nil {
		return Profile{}, fmt.Errorf("no built-in board profile %q (have: %s)", name, strings.Join(Names(), ", "))
	}
	defer f.Close()
	return Parse(f)
}

// Names lists the built-in profiles.
func Names() []string {
	ents, _ := builtin.ReadDir("profiles")
	var out []string
	for _, e := range ents {
		out = append(out, strings.TrimSuffix(e.Name(), ".conf"))
	}
	sort.Strings(out)
	return out
}

// Detect picks the built-in profile whose match strings appear in the device tree's
// compatible list (under root, for tests). Returns "" if none matches.
func Detect(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "/proc/device-tree/compatible"))
	if err != nil {
		return ""
	}
	compat := map[string]bool{}
	for _, s := range strings.Split(string(b), "\x00") {
		compat[strings.TrimSpace(s)] = true
	}
	for _, name := range Names() {
		p, err := Builtin(name)
		if err != nil {
			continue
		}
		for _, m := range p.Match {
			if compat[m] {
				return name
			}
		}
	}
	return ""
}

// Load resolves the profile: explicit name (or auto-detect), then the override file (if it
// exists) on top. The result is validated.
func Load(root, name, overridePath string) (Profile, error) {
	if name == "" || name == "auto" {
		if name = Detect(root); name == "" {
			return Profile{}, fmt.Errorf("board not detected from %s/proc/device-tree/compatible; set board = <name> (have: %s)",
				root, strings.Join(Names(), ", "))
		}
	}
	p, err := Builtin(name)
	if err != nil {
		return p, err
	}
	if overridePath != "" {
		if f, err := os.Open(filepath.Join(root, overridePath)); err == nil {
			err = p.apply(f)
			f.Close()
			if err != nil {
				return p, fmt.Errorf("%s: %w", overridePath, err)
			}
		}
	}
	return p, p.Validate()
}
