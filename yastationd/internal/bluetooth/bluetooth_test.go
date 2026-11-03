package bluetooth

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestViewKeepsAnEmptyDeviceListForJSON(t *testing.T) {
	m := &Manager{view: View{Devices: []Device{}}}
	v := m.View()
	if v.Devices == nil {
		t.Fatal("empty Bluetooth device list must encode as [] rather than null")
	}
}

func TestDeviceIDRoundTripsWithoutPathSeparators(t *testing.T) {
	path := dbus.ObjectPath("/org/bluez/hci0/dev_01_02_03_04_05_06")
	id := base64.RawURLEncoding.EncodeToString([]byte(path))
	if strings.Contains(id, "/") {
		t.Fatalf("device ID contains a URL path separator: %q", id)
	}
	if got, ok := (&Manager{adapter: "/org/bluez/hci0"}).devicePathLocked(id); !ok || got != path {
		t.Fatalf("device ID round trip = %q, %v", got, ok)
	}
}
