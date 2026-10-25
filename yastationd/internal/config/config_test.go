package config

import (
	"strings"
	"testing"
	"time"
)

func TestVideoKeys(t *testing.T) {
	c, err := Parse(strings.NewReader("video_frames = /x/a.led.gz\nvideo_audio = /x/a.wav\nvideo_av_offset_ms = -120\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.VideoFrames != "/x/a.led.gz" || c.VideoAudio != "/x/a.wav" || c.VideoAVOffset != -120*time.Millisecond {
		t.Fatalf("video keys: %q %q %v", c.VideoFrames, c.VideoAudio, c.VideoAVOffset)
	}
	if d := Default(); !strings.HasSuffix(d.VideoFrames, "/media/video.led") || !strings.HasSuffix(d.VideoAudio, "/media/video.wav") {
		t.Errorf("defaults %q %q", d.VideoFrames, d.VideoAudio)
	}
	if _, err := Parse(strings.NewReader("video_av_offset_ms = 5000\n")); err == nil {
		t.Error("offset out of range accepted")
	}
}

func TestParseBasics(t *testing.T) {
	in := `
# comment
device_id = kitchen
mqtt_host = broker.example
mqtt_port = 1883
volume_step = 2
unmute = name='SPK mute' 0
unmute = name='AMP mute status' 0
probe = broker=broker.example:1883@broker
probe = pipeline=voice-server:24117@pipeline
auto_brightness = true
ambilight_brightness = 96
`
	c, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if c.DeviceID != "kitchen" || c.MQTTHost != "broker.example" || c.MQTTPort != 1883 {
		t.Fatalf("basic fields: %+v", c)
	}
	if c.VolumeStep != 2 || !c.AutoBrightness || c.AmbilightBrightness != 96 {
		t.Fatalf("typed fields: %+v", c)
	}
	if len(c.Unmute) != 2 || len(c.Probes) != 2 {
		t.Fatalf("list accumulation: %+v", c)
	}
}

func TestParseUnknownKey(t *testing.T) {
	if _, err := Parse(strings.NewReader("nope = 1")); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestParseRangeCheck(t *testing.T) {
	if _, err := Parse(strings.NewReader("clock_brightness = 999")); err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestPathSysRoot(t *testing.T) {
	c := Default()
	if c.Path("/sys/x") != "/sys/x" {
		t.Fatal("empty sysroot")
	}
	c.SysRoot = "/fake/"
	if got := c.Path("/sys/x"); got != "/fake/sys/x" {
		t.Fatalf("sysroot prefix: %q", got)
	}
}

func TestHasTracksSetKeys(t *testing.T) {
	c, err := Parse(strings.NewReader("device_id = x\nmic_channel = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("mic_channel") || c.Has("mic_device") || c.MicChannel != 2 {
		t.Fatalf("Has/values wrong: %+v", c)
	}
	if c.Board != "auto" {
		t.Fatalf("Board default = %q", c.Board)
	}
}

func TestBrightnessCurve(t *testing.T) {
	c, err := parseCurve("0.04:4, 0.55:128,10:255")
	if err != nil || len(c) != 3 || c[1] != (CurvePoint{0.55, 128}) {
		t.Fatalf("parseCurve = %v, %v", c, err)
	}
	for _, bad := range []string{"0.5:10", "1:10,0.5:20", "x:1,2:3", "0:1,2:3", "1:300,2:3"} {
		if _, err := parseCurve(bad); err == nil {
			t.Errorf("parseCurve(%q) accepted", bad)
		}
	}
}

func TestOrionExample(t *testing.T) {
	c, err := Load("../../configs/orion.conf.example")
	if err != nil {
		t.Fatal(err)
	}
	if c.Board != "orion" || c.DeviceName != "Yandex Station 3" || !c.WakeEnabled ||
		c.WyomingPort != 10700 || c.VolumeBeep {
		t.Fatalf("unexpected Orion example: %+v", c)
	}
}

func TestEditKeepsTheRest(t *testing.T) {
	in := "# header\ndevice_id = mini3\nmqtt_host   = old.lan\n# mqtt_host = example\nprobe = a=old.lan:1883@broker\nlog_level = info\nprobe = b=old.lan:8123\n"
	out := string(Edit([]byte(in), map[string][]string{
		"mqtt_host":  {"new.lan"},
		"probe":      {"a=new.lan:1883@broker", "b=new.lan:8123", "c=new.lan:10300@pipeline"},
		"ntp_server": {"ntp.lan"},
		"mqtt_pass":  nil,
	}))
	want := "# header\ndevice_id = mini3\nmqtt_host = new.lan\n# mqtt_host = example\n" +
		"probe = a=new.lan:1883@broker\nprobe = b=new.lan:8123\nprobe = c=new.lan:10300@pipeline\n" +
		"log_level = info\n\n# set from the Wi-Fi setup page\nntp_server = ntp.lan\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	c, err := Parse(strings.NewReader(out))
	if err != nil || c.MQTTHost != "new.lan" || len(c.Probes) != 3 || len(c.NTPServers) != 1 {
		t.Fatalf("parse back: %v %+v", err, c)
	}
	if got := string(Edit([]byte("a = 1"), map[string][]string{"a": {"2"}})); got != "a = 2\n" {
		t.Errorf("no trailing newline: %q", got)
	}
	if got := string(Edit([]byte(in), map[string][]string{"probe": {}})); strings.Contains(got, "\nprobe") {
		t.Errorf("empty list did not remove the key:\n%s", got)
	}
}

func TestIntercomKeys(t *testing.T) {
	c, err := Parse(strings.NewReader("intercom = true\nintercom_secret = 0123456789abcdef\nintercom_name = Кабинет\nintercom_ring_secs = 20\nintercom_jitter_ms = 60\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Intercom || c.IntercomName != "Кабинет" || c.IntercomRing != 20*time.Second ||
		c.IntercomJitterMs != 60 || c.IntercomPort != 10710 || c.IntercomMax != 30*time.Minute {
		t.Fatalf("%+v", c)
	}
	if _, err := Parse(strings.NewReader("intercom_secret = short\n")); err == nil {
		t.Fatal("a short secret was accepted")
	}
}
