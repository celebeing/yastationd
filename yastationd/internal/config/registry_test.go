package config

import (
	"strings"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	groups := map[string]bool{}
	for _, g := range Groups {
		groups[g.ID] = true
	}
	for _, k := range Keys {
		if !groups[k.Group] || k.Label == "" || k.Doc == "" {
			t.Errorf("%s: group %q, label %q, doc %q", k.Name, k.Group, k.Label, k.Doc)
		}
		if k.Sync && k.Hidden {
			t.Errorf("%s: hidden keys are not synced", k.Name)
		}
		def := k.Default()
		if k.List() || len(def) != 1 || def[0] == "" {
			continue // empty and list defaults can't be written back as a line
		}
		c, err := Parse(strings.NewReader(k.Name + " = " + def[0] + "\n"))
		if err != nil {
			t.Errorf("%s = %s: %v", k.Name, def[0], err)
			continue
		}
		if got := k.Get(&c); !equal(got, def) {
			t.Errorf("%s: %v back as %v", k.Name, def, got)
		}
	}
}

func TestChanged(t *testing.T) {
	a, _ := Parse(strings.NewReader("volume_step = 2\nprobe = a=h:1@lan\n"))
	b, _ := Parse(strings.NewReader("volume_step = 3\nprobe = a=h:1@lan\nmic_channel = 0\n"))
	got := Changed(&a, &b)
	if strings.Join(got, ",") != "volume_step,mic_channel" {
		t.Fatalf("Changed = %v", got)
	}
	if NeedsRestart([]string{"volume_step"}) || !NeedsRestart(got) {
		t.Fatal("NeedsRestart")
	}
}

func TestRegistryParsing(t *testing.T) {
	good := map[string]func(c Config) bool{
		"text_lines = auto":                   func(c Config) bool { return c.TextLines == 0 },
		"text_lines = 2":                      func(c Config) bool { return c.TextLines == 2 },
		"auto_brightness_curve = 1:2, 10:200": func(c Config) bool { return len(c.BrightnessCurve) == 2 },
		"reply_timeout = 30":                  func(c Config) bool { return c.ReplyTimeout.Seconds() == 30 },
		"arbitration_wait_ms = 250":           func(c Config) bool { return c.ArbitrationWait.Milliseconds() == 250 },
		"net_watchdog_min = 0":                func(c Config) bool { return c.NetWatchdog == 0 },
		"http_addr = ":                        func(c Config) bool { return c.HTTPAddr == "" },
		"http_addr = 0.0.0.0:8080":            func(c Config) bool { return c.HTTPAddr == "0.0.0.0:8080" },
		"wifi_setup_address = 10.1.2.1/24":    func(c Config) bool { return c.WifiSetupAddress == "10.1.2.1/24" },
		"marquee_speed = 0.5":                 func(c Config) bool { return c.MarqueeSpeed == 0.5 },
		"intercom_gain_db = 12":               func(c Config) bool { return c.IntercomGainDB == 12 },
		"sendspin_volume = separate":          func(c Config) bool { return c.SendspinVolume == "separate" },
		"tz = Asia/Tokyo":                     func(c Config) bool { return c.TZ == "Asia/Tokyo" },
	}
	for line, ok := range good {
		c, err := Parse(strings.NewReader(line + "\n"))
		if err != nil || !ok(c) {
			t.Errorf("%q: %v", line, err)
		}
	}
	for _, line := range []string{"text_lines = 3", "auto_brightness_curve = 5:1", "reply_timeout = 5", "http_addr = nope",
		"wifi_setup_address = 10.1.2.1/16", "marquee_speed = 9", "intercom_secret = short", "wifi_setup_psk = 1234567",
		"sendspin_volume = loud", "tz = Mars/Base", "web_password = plain", "device_id = ", "volume_beep = maybe"} {
		if _, err := Parse(strings.NewReader(line + "\n")); err == nil {
			t.Errorf("%q accepted", line)
		}
	}
	// A list adds a line each time and prints them back in order.
	c, _ := Parse(strings.NewReader("ntp_server = a\nntp_server = b\n"))
	if got := Lookup("ntp_server").Get(&c); strings.Join(got, ",") != "a,b" {
		t.Fatalf("list: %v", got)
	}
	// An override prints empty until the file sets it, even to its zero value.
	if got := Lookup("mic_channel").Get(&c); got[0] != "" {
		t.Fatalf("unset override: %v", got)
	}
	c, _ = Parse(strings.NewReader("mic_channel = 0\n"))
	if got := Lookup("mic_channel").Get(&c); got[0] != "0" {
		t.Fatalf("set override: %v", got)
	}
	if NeedsRestart(nil) || !NeedsRestart([]string{"nope"}) {
		t.Fatal("NeedsRestart edge cases")
	}
}

func TestEditNoteDoesNotPileUp(t *testing.T) {
	base := "device_id = x\n"
	s := string(EditNote([]byte(base), map[string][]string{"volume_step": {"2"}}, "set from the web app"))
	if s != base+"\n# set from the web app\nvolume_step = 2\n" {
		t.Fatalf("append: %q", s)
	}
	s = string(EditNote([]byte(s), map[string][]string{"volume_step": nil}, "set from the web app"))
	if s != base {
		t.Fatalf("remove leaves the note: %q", s)
	}
	// A note of another writer goes too when its keys are gone.
	s = string(EditNote([]byte(base+"\n# shared by the other speakers\nvolume_step = 2\n"), map[string][]string{"volume_step": nil}, "set from the web app"))
	if s != base {
		t.Fatalf("another writer's note: %q", s)
	}
	s = string(EditNote([]byte(base+"\n# set from the web app\nvolume_step = 2\n"), map[string][]string{"volume_max": {"80"}}, "set from the web app"))
	if s != base+"\n# set from the web app\nvolume_step = 2\nvolume_max = 80\n" {
		t.Fatalf("second key under the same note: %q", s)
	}
}
