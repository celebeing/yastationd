package bluetooth

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestNumericComparisonWaitsForWebConfirmation(t *testing.T) {
	m := &Manager{log: slog.New(slog.NewTextHandler(io.Discard, nil)), view: View{Pairing: true, Devices: []Device{}}}
	path := dbus.ObjectPath("/org/bluez/hci0/dev_01_02_03_04_05_06")
	done := make(chan *dbus.Error, 1)
	go func() { done <- m.incoming(path, 42) }()
	deadline := time.Now().Add(time.Second)
	for !m.View().Pending && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	v := m.View()
	if !v.Pending || v.Code != "000042" || v.Device != "Phone" {
		t.Fatalf("pairing state: %+v", v)
	}
	if err := m.Confirm(); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("numeric confirmation: %v", err)
	}
}

// BlueZ asks about A2DP with the Advanced Audio UUID; the paired phone must get it, others not.
func TestAuthorizeServiceAudioOnly(t *testing.T) {
	m := &Manager{log: slog.New(slog.NewTextHandler(io.Discard, nil)), allowed: map[dbus.ObjectPath]bool{}}
	phone := dbus.ObjectPath("/org/bluez/hci0/dev_01_02_03_04_05_06")
	m.allowed[phone] = true
	a := &agent{m: m}
	for uuid, want := range map[string]bool{advancedAudio: true, avrcp: true, "0000111f-0000-1000-8000-00805f9b34fb": false} {
		if got := a.AuthorizeService(phone, uuid) == nil; got != want {
			t.Errorf("%s: allowed %v, want %v", uuid, got, want)
		}
	}
	if a.AuthorizeService("/org/bluez/hci0/dev_0A_0B_0C_0D_0E_0F", advancedAudio) == nil {
		t.Error("an unknown device got audio")
	}
}
