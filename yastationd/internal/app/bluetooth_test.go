package app

import (
	"fmt"
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/bluetooth"
)

type fakeBluetooth struct{ v bluetooth.View }

func (b *fakeBluetooth) View() bluetooth.View { return b.v }
func (b *fakeBluetooth) Pair() error {
	if b.v.Pairing {
		return fmt.Errorf("a Bluetooth pairing session is already active")
	}
	b.v.Pairing = true
	b.v.Expires = 1
	return nil
}
func (b *fakeBluetooth) Confirm() error {
	if !b.v.Pending {
		return fmt.Errorf("there is no Bluetooth pairing request to confirm")
	}
	b.v.Pending = false
	return nil
}
func (b *fakeBluetooth) Cancel() { b.v.Pairing = false; b.v.Pending = false; b.v.Code = "" }
func (b *fakeBluetooth) Disconnect(id string) error {
	for i := range b.v.Devices {
		if b.v.Devices[i].ID == id {
			b.v.Devices[i].Connected = false
			return nil
		}
	}
	return fmt.Errorf("Bluetooth device not found")
}
func (b *fakeBluetooth) Forget(id string) error {
	for i, d := range b.v.Devices {
		if d.ID == id {
			b.v.Devices = append(b.v.Devices[:i], b.v.Devices[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("Bluetooth device not found")
}
func (*fakeBluetooth) Close() {}

func TestBluetoothAPIRequiresLoginAndBoundsPairing(t *testing.T) {
	a, srv := newWebApp(t)
	b := &fakeBluetooth{v: bluetooth.View{Available: true, Devices: []bluetooth.Device{{ID: "dev_01", Name: "Phone", Connected: true}}}}
	a.bt = b
	if code, _ := call(t, "GET", srv.URL+"/api/bluetooth", "", ""); code != 401 {
		t.Fatalf("unauthed bluetooth: %d", code)
	}
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok := out["token"].(string)
	if code, out := call(t, "GET", srv.URL+"/api/bluetooth", tok, ""); code != 200 || out["available"] != true {
		t.Fatalf("initial Bluetooth state: %d %v", code, out)
	}
	if code, out := call(t, "POST", srv.URL+"/api/bluetooth/pair", tok, ""); code != 202 || out["pairing"] != true {
		t.Fatalf("pair: %d %v", code, out)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/bluetooth/pair", tok, ""); code != 409 {
		t.Fatalf("second pairing: %d", code)
	}
	if code, out := call(t, "DELETE", srv.URL+"/api/bluetooth/pair", tok, ""); code != 200 || out["pairing"] != false {
		t.Fatalf("cancel: %d %v", code, out)
	}
	if code, _ := call(t, "POST", srv.URL+"/api/bluetooth/pair/confirm", tok, ""); code != 409 {
		t.Fatalf("confirm without code: %d", code)
	}
	if code, out := call(t, "POST", srv.URL+"/api/bluetooth/devices/dev_01/disconnect", tok, ""); code != 200 || out["devices"].([]any)[0].(map[string]any)["connected"] != false {
		t.Fatalf("disconnect: %d %v", code, out)
	}
	if code, out := call(t, "DELETE", srv.URL+"/api/bluetooth/devices/dev_01", tok, ""); code != 200 || len(out["devices"].([]any)) != 0 {
		t.Fatalf("forget: %d %v", code, out)
	}
}
