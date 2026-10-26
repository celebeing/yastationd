package hw

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSplitQuoted(t *testing.T) {
	cases := map[string][]string{
		"name='SPK mute' 0":        {"name=SPK mute", "0"},
		`name="AMP mute status" 1`: {"name=AMP mute status", "1"},
		"Master 128":               {"Master", "128"},
	}
	for in, want := range cases {
		if got := splitQuoted(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitQuoted(%q) = %q, want %q", in, got, want)
		}
	}
}

// makeRecord builds an input_event with the given timeval width (8 or 16 bytes).
func makeRecord(tvWidth int, typ, code uint16, val int32) []byte {
	b := make([]byte, tvWidth+8)
	binary.LittleEndian.PutUint16(b[tvWidth:], typ)
	binary.LittleEndian.PutUint16(b[tvWidth+2:], code)
	binary.LittleEndian.PutUint32(b[tvWidth+4:], uint32(val))
	return b
}

// evdevReader emulates the kernel: every Read returns only whole records, never splitting one
// and never returning more than fit in the caller's buffer. ReadEvents relies on this.
type evdevReader struct {
	rec  int
	data []byte
}

func (e *evdevReader) Read(p []byte) (int, error) {
	if len(e.data) == 0 {
		return 0, io.EOF
	}
	n := (len(p) / e.rec) * e.rec
	if n > len(e.data) {
		n = (len(e.data) / e.rec) * e.rec
	}
	copy(p, e.data[:n])
	e.data = e.data[n:]
	return n, nil
}

func TestReadEventsBothLayouts(t *testing.T) {
	for _, tv := range []int{8, 16} { // 16-byte and 24-byte records
		rec1 := makeRecord(tv, EvKey, KeyVolumeUp, 1)
		rec2 := makeRecord(tv, EvSyn, 0, 0) // dropped
		rec3 := makeRecord(tv, EvKey, KeyVolumeUp, 0)
		r := &evdevReader{rec: tv + 8, data: append(append(rec1, rec2...), rec3...)}

		var got []InputEvent
		ReadEvents(r, func(e InputEvent) { got = append(got, e) })
		if len(got) != 2 {
			t.Fatalf("tv=%d: got %d events, want 2 (SYN dropped)", tv, len(got))
		}
		if got[0].Code != KeyVolumeUp || got[0].Value != 1 || got[1].Value != 0 {
			t.Fatalf("tv=%d: decoded wrong: %+v", tv, got)
		}
	}
}

func TestParseRecordLastEight(t *testing.T) {
	rec := makeRecord(16, EvSw, SwMuteDevice, 1)
	ev := parseRecord(rec)
	if ev.Type != EvSw || ev.Code != SwMuteDevice || ev.Value != 1 {
		t.Fatalf("parseRecord: %+v", ev)
	}
}

// The bergamot ltr308 driver exposes in_intensity_* next to the SoC's saradc; FindLux must skip
// the saradc and pick the processed ltr308 channel.
func TestFindLuxBergamot(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, data string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("sys/bus/iio/devices/iio:device0/name", "meson-axg-saradc\n")
	mk("sys/bus/iio/devices/iio:device0/in_voltage0_input", "505\n")
	mk("sys/bus/iio/devices/iio:device1/name", "ltr308als01\n")
	mk("sys/bus/iio/devices/iio:device1/in_intensity_raw", "4\n")
	mk("sys/bus/iio/devices/iio:device1/in_intensity_input", "0.800000\n")

	p := FindLux(root, "ltr")
	if want := filepath.Join(root, "sys/bus/iio/devices/iio:device1/in_intensity_input"); p != want {
		t.Fatalf("FindLux = %q, want %q", p, want)
	}
	if lx, err := ReadLux(p); err != nil || lx != 0.8 {
		t.Fatalf("ReadLux = %v, %v; want 0.8", lx, err)
	}
}

// Get/Set must address the control by full name as separate argv entries (amixer cget/cset
// do not resolve the short "Master").
func TestMixerArgs(t *testing.T) {
	var got [][]string
	m := &Mixer{Card: "0", run: func(args ...string) (string, error) {
		got = append(got, args)
		return "numid=1,iface=MIXER,name='Master Playback Volume'\n  ; type=INTEGER\n  : values=200\n", nil
	}}
	if v, err := m.Get(MasterControl); err != nil || v != 200 {
		t.Fatalf("Get = %d, %v", v, err)
	}
	if err := m.Set(MasterControl, 120); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"-c", "0", "cget", "name=Master Playback Volume"},
		{"-c", "0", "cset", "name=Master Playback Volume", "120"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

// The decimator passes speech-band tones, suppresses what would alias above 8 kHz, and works
// for both capture rates in use (bergamot/pickle 48 kHz, orion 96 kHz through mroute).
func TestDecimator(t *testing.T) {
	rms := func(rate int, hz float64) float64 {
		d, err := NewDecimator(rate)
		if err != nil {
			t.Fatal(err)
		}
		var sum float64
		n := 0
		for i := range rate {
			if v, ok := d.Push(int16(10000 * math.Sin(2*math.Pi*hz*float64(i)/float64(rate)))); ok && i > rate/10 {
				sum += float64(v) * float64(v)
				n++
			}
		}
		return math.Sqrt(sum / float64(n))
	}
	for _, rate := range []int{48000, 96000} {
		if pass := rms(rate, 1000); pass < 6500 || pass > 7600 { // 10000/sqrt2 ≈ 7071
			t.Errorf("%d: 1 kHz rms = %.0f", rate, pass)
		}
		// A tone just under the input Nyquist is stopband for the FIR, and decimation folds
		// it in-band (48000: 22 kHz -> 6 kHz; 96000: 46 kHz -> 2 kHz) — it must be killed.
		alias := float64(rate)/2 - 2000
		if stop := rms(rate, alias); stop > 300 {
			t.Errorf("%d: alias %.0f Hz rms = %.0f, want strongly attenuated", rate, alias, stop)
		}
	}
}

func TestHighpassKillsDC(t *testing.T) {
	h := NewHighpass(150, 48000)
	var last int16
	for i := 0; i < 48000; i++ {
		last = h.Push(5000)
	}
	if last > 5 || last < -5 {
		t.Fatalf("DC after 1 s = %d", last)
	}
}

func TestFindInputByName(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "proc/bus/input"), 0o755)
	os.WriteFile(filepath.Join(root, "proc/bus/input/devices"), []byte(`I: Bus=0019 Vendor=0001
N: Name="gpio-keys"
H: Handlers=kbd event0 

I: Bus=0000 Vendor=0000
N: Name="aw933xx"
H: Handlers=kbd event1 
`), 0o644)
	if p, err := FindInput(root, "aw933xx"); err != nil || p != root+"/dev/input/event1" {
		t.Fatalf("FindInput = %q, %v", p, err)
	}
	if _, err := FindInput(root, "nope"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestRingFromPattern(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		for _, c := range []string{"red", "green", "blue"} {
			d := filepath.Join(root, "sys/class/leds", "x:"+c+":p"+strconv.Itoa(i))
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, "trigger"), []byte("[none] ledfb"), 0o644)
			os.WriteFile(filepath.Join(d, "brightness"), []byte("0"), 0o644)
		}
	}
	r, err := NewRing(root, 3, "x:{color}:p{i}", []string{"red", "green", "blue"})
	if err != nil || r.Pixels() != 3 {
		t.Fatalf("NewRing: %v", err)
	}
	if err := r.Show([]RGB{{R: 1}, {G: 2}, {B: 3}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/leds/x:blue:p2/brightness")); string(b) != "3" {
		t.Fatalf("blue p2 = %q", b)
	}
	var none *Ring
	if none.Pixels() != 0 || none.Show([]RGB{{R: 1}}) != nil {
		t.Fatal("nil ring should be a no-op")
	}
}

// Orion's ring: 36 px across three AW21036 chips. The pattern must produce the real
// device names and Show must land each colour channel on its own pixel file — including
// across the chip-third boundaries (11→12, 23→24), the classic off-by-one/broken-chain spot.
func TestRing36OrionLayout(t *testing.T) {
	root := t.TempDir()
	for i := range 36 {
		for _, c := range []string{"red", "green", "blue"} {
			d := filepath.Join(root, "sys/class/leds", "orion:"+c+":ring"+strconv.Itoa(i))
			os.MkdirAll(d, 0o755)
			os.WriteFile(filepath.Join(d, "trigger"), []byte("ledfb-ring [ledfb-ring]"), 0o644)
			os.WriteFile(filepath.Join(d, "brightness"), []byte("0"), 0o644)
		}
		os.WriteFile(filepath.Join(root, "sys/class/leds/orion:ring"+string(rune('A'+i/12))+":sync/brightness"), []byte("0"), 0o755)
	}
	r, err := NewRing(root, 36, "orion:{color}:ring{i}", []string{"red", "green", "blue"})
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}
	if r.Pixels() != 36 {
		t.Fatalf("pixels = %d", r.Pixels())
	}
	// One frame: red on the three boundary pixels, blue on the last, as hex channels.
	f := make([]RGB, 36)
	f[0], f[11], f[12], f[23], f[24], f[35] = RGB{R: 255}, RGB{R: 128}, RGB{R: 1}, RGB{R: 64}, RGB{R: 32}, RGB{B: 255}
	if err := r.Show(f); err != nil {
		t.Fatal(err)
	}
	for _, px := range []struct {
		i    int
		leds map[string]string
	}{
		{0, map[string]string{"red": "255"}},
		{11, map[string]string{"red": "128"}},
		{12, map[string]string{"red": "1"}},
		{23, map[string]string{"red": "64"}},
		{24, map[string]string{"red": "32"}},
		{35, map[string]string{"blue": "255"}},
	} {
		for c, want := range px.leds {
			b, _ := os.ReadFile(filepath.Join(root, "sys/class/leds", "orion:"+c+":ring"+strconv.Itoa(px.i), "brightness"))
			if string(b) != want {
				t.Errorf("px %d %s = %q, want %s", px.i, c, b, want)
			}
		}
	}
	// Untouched pixels must still be 0 and no sync node was touched by NewRing/Show.
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/leds/orion:red:ring5/brightness")); string(b) != "0" {
		t.Errorf("untouched px = %q", b)
	}
	// Prove the whole frame is a no-op when nothing changed (flicker-free rule at 36 px).
	if err := r.Show(f); err != nil {
		t.Fatal(err)
	}
}

func TestOrionAmbilightLayout(t *testing.T) {
	root := t.TempDir()
	colors := []string{"red", "green", "blue", "white"}
	dies := []string{"a", "b"}
	for i := range 12 {
		for _, die := range dies {
			for _, color := range colors {
				dir := filepath.Join(root, "sys/class/leds",
					"orion:"+color+":ambilight"+strconv.Itoa(i)+die)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "trigger"), []byte("none [ledfb-ambilight]"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "brightness"), []byte("0"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	strip, err := NewAmbilight(root, 12, "orion:{color}:ambilight{i}{die}", colors, dies)
	if err != nil {
		t.Fatal(err)
	}
	if strip.Pixels() != 12 {
		t.Fatalf("pixels = %d", strip.Pixels())
	}
	frame := make([]RGBW, 12)
	frame[0] = RGBW{R: 1, G: 2, B: 3, W: 4}
	frame[11] = RGBW{R: 10, G: 20, B: 30, W: 40}
	if err := strip.Show(frame); err != nil {
		t.Fatal(err)
	}
	for _, die := range dies {
		for color, want := range map[string]string{
			"red": "10", "green": "20", "blue": "30", "white": "40",
		} {
			path := filepath.Join(root, "sys/class/leds",
				"orion:"+color+":ambilight11"+die, "brightness")
			if got, err := os.ReadFile(path); err != nil || string(got) != want {
				t.Errorf("%s = %q, %v; want %s", path, got, err, want)
			}
		}
	}
	if got, _ := os.ReadFile(filepath.Join(root,
		"sys/class/leds/orion:white:ambilight0a/brightness")); string(got) != "4" {
		t.Errorf("first white die = %q, want 4", got)
	}

	var none *Ambilight
	if none.Pixels() != 0 || none.Show([]RGBW{{W: 1}}) != nil {
		t.Fatal("nil ambilight should be a no-op")
	}
}

// With the framebuffer, R, G, B go there (B, G, R as 16-bit LE, level x2) and their LEDs get the
// ledfb trigger; W stays on sysfs.
func TestOrionAmbilightFB(t *testing.T) {
	root := t.TempDir()
	colors := []string{"red", "green", "blue", "white"}
	dies := []string{"a", "b"}
	for i := range 12 {
		for _, die := range dies {
			for _, color := range colors {
				dir := filepath.Join(root, "sys/class/leds", "orion:"+color+":ambilight"+strconv.Itoa(i)+die)
				os.MkdirAll(dir, 0o755)
				os.WriteFile(filepath.Join(dir, "trigger"), []byte("[none] ledfb-ambilight"), 0o644)
				os.WriteFile(filepath.Join(dir, "brightness"), []byte("0"), 0o644)
			}
		}
	}
	os.MkdirAll(filepath.Join(root, "sys/class/graphics/fb0"), 0o755)
	os.WriteFile(filepath.Join(root, "sys/class/graphics/fb0/name"), []byte("ambilight\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "dev"), 0o755)
	fb := filepath.Join(root, "dev/fb0")
	os.WriteFile(fb, make([]byte, 72), 0o644)

	pattern := "orion:{color}:ambilight{i}{die}"
	strip, err := NewAmbilight(root, 12, pattern, colors, dies)
	if err != nil {
		t.Fatal(err)
	}
	if err := strip.UseFB(root, "ambilight", "rgb48", "", pattern, colors, dies); err != nil {
		t.Fatal(err)
	}
	frame := make([]RGBW, 12)
	frame[1] = RGBW{R: 255, G: 2, B: 1, W: 9}
	if err := strip.Show(frame); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(fb)
	if got := b[6:12]; string(got) != string([]byte{2, 0, 4, 0, 0xfe, 1}) {
		t.Fatalf("fb pixel 1 = % x", got)
	}
	led := func(c, f string) string {
		v, _ := os.ReadFile(filepath.Join(root, "sys/class/leds", "orion:"+c+":ambilight1a", f))
		return string(v)
	}
	if led("red", "trigger") != "ledfb-ambilight" || led("white", "trigger") == "ledfb-ambilight" {
		t.Fatalf("triggers: red %q, white %q", led("red", "trigger"), led("white", "trigger"))
	}
	if led("white", "brightness") != "9" || led("red", "brightness") != "0" {
		t.Fatalf("sysfs: white %q, red %q", led("white", "brightness"), led("red", "brightness"))
	}
}

// rgbw64: the device-tree node is switched by an overlay and the device rebound, then all four
// channels go through the framebuffer (B, G, R, W as 16-bit LE).
func TestOrionAmbilightFBRGBW(t *testing.T) {
	root := t.TempDir()
	colors := []string{"red", "green", "blue", "white"}
	dies := []string{"a", "b"}
	for i := range 12 {
		for _, die := range dies {
			for _, color := range colors {
				dir := filepath.Join(root, "sys/class/leds", "orion:"+color+":ambilight"+strconv.Itoa(i)+die)
				os.MkdirAll(dir, 0o755)
				os.WriteFile(filepath.Join(dir, "trigger"), []byte("[none] ledfb-ambilight"), 0o644)
				os.WriteFile(filepath.Join(dir, "brightness"), []byte("0"), 0o644)
			}
		}
	}
	node := filepath.Join(root, "sys/firmware/devicetree/base/ambilight-framebuffer")
	dev := filepath.Join(root, "sys/devices/platform/ambilight-framebuffer")
	drv := filepath.Join(root, "sys/bus/platform/drivers/ledfb")
	fbs := filepath.Join(root, "sys/class/graphics/fb0")
	for _, d := range []string{node, dev, drv, fbs, filepath.Join(root, "dev"), filepath.Join(root, overlayDir)} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(node+"/compatible", []byte("yandex,ledfb-rgb48\x00"), 0o644)
	os.Symlink(node, dev+"/of_node")
	os.Symlink(drv, dev+"/driver")
	os.Symlink(dev, fbs+"/device")
	os.WriteFile(drv+"/bind", nil, 0o644)
	os.WriteFile(drv+"/unbind", nil, 0o644)
	os.WriteFile(fbs+"/name", []byte("ambilight\n"), 0o644)
	os.WriteFile(fbs+"/bits_per_pixel", []byte("64\n"), 0o644)
	fb := filepath.Join(root, "dev/fb0")
	os.WriteFile(fb, make([]byte, 96), 0o644)

	pattern := "orion:{color}:ambilight{i}{die}"
	strip, err := NewAmbilight(root, 12, pattern, colors, dies)
	if err != nil {
		t.Fatal(err)
	}
	if err := strip.UseFB(root, "ambilight", "rgbw64", "yandex,ledfb-rgbw64", pattern, colors, dies); err != nil {
		t.Fatal(err)
	}
	ov := filepath.Join(root, overlayDir, "yastationd-ambilight")
	if b, _ := os.ReadFile(ov + "/status"); string(b) != "1" {
		t.Fatalf("overlay status %q", b)
	}
	if b, _ := os.ReadFile(ov + "/dtbo"); !strings.Contains(string(b), "yandex,ledfb-rgbw64") || !strings.Contains(string(b), "/ambilight-framebuffer") {
		t.Fatal("overlay does not set the node's compatible")
	}
	for _, f := range []string{"unbind", "bind"} {
		if b, _ := os.ReadFile(drv + "/" + f); string(b) != "ambilight-framebuffer" {
			t.Fatalf("%s got %q", f, b)
		}
	}
	frame := make([]RGBW, 12)
	frame[1] = RGBW{R: 1, G: 2, B: 3, W: 255}
	if err := strip.Show(frame); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(fb)
	if got := b[8:16]; string(got) != string([]byte{6, 0, 4, 0, 2, 0, 0xfe, 1}) {
		t.Fatalf("fb pixel 1 = % x", got)
	}
	w, _ := os.ReadFile(filepath.Join(root, "sys/class/leds/orion:white:ambilight1b/trigger"))
	if string(w) != "ledfb-ambilight" {
		t.Fatalf("white trigger %q", w)
	}
}
