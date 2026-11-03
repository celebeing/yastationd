package bluetooth

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestNumericComparisonWaitsForWebConfirmation(t *testing.T) {
	m := &Manager{view: View{Pairing: true, Devices: []Device{}}}
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
