package config

import (
	"strings"
	"testing"
)

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
