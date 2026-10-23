package hw

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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
