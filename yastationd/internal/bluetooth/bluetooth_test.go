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

func TestIsDBusErrorByName(t *testing.T) {
	err := dbus.Error{Name: "org.bluez.Error.AlreadyExists"}
	if !isDBusError(err, "org.bluez.Error.AlreadyExists") || !isDBusError(&err, "org.bluez.Error.AlreadyExists") {
		t.Fatal("the BlueZ error name was not recognised")
	}
	if isDBusError(err, "org.bluez.Error.Failed") {
		t.Fatal("another error name matched")
	}
}

func TestPhoneVolumeScale(t *testing.T) {
	for pct := 0; pct <= 100; pct++ {
		if got := pctFromVolume(volumeFromPct(pct)); abs(got-pct) > 1 {
			t.Fatalf("%d %% -> %d %%", pct, got)
		}
	}
	if pctFromVolume(0x7f7f) != 100 || pctFromVolume(0xffff) != 0 || pctFromVolume(0x0040) != 50 {
		t.Fatal("level bytes: full, muted, one channel at half")
	}
}
