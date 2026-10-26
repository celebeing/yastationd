package wifisetup

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddNetworkOnly(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "wpa_supplicant.conf")
	os.WriteFile(conf, []byte("ctrl_interface=/var/run/wpa_supplicant\nnetwork={\n\tssid=\"home\"\n\tpsk=\"secret123\"\n}\n"), 0o600)
	m := New(Options{WPAConf: conf, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := m.AddNetwork("home", "x12345678", false, 0); !errors.Is(err, ErrNetworkExists) {
		t.Fatalf("a saved network is never replaced: %v", err)
	}
	if err := m.AddNetwork("cottage", "password1", false, 2); err != nil {
		t.Fatal(err)
	}
	nets, _, err := m.SavedNetworks()
	if err != nil || len(nets) != 2 || nets[1].SSID != "cottage" || nets[1].Priority != 2 {
		t.Fatalf("nets %+v %v", nets, err)
	}
	b, _ := os.ReadFile(conf)
	if !strings.Contains(string(b), "secret123") {
		t.Fatal("the existing network is kept as it was")
	}
	if _, err := os.Stat(conf + ".before-web"); err != nil {
		t.Fatal("the old file is kept")
	}
}
