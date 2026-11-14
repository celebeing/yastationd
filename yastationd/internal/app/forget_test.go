package app

import (
	"slices"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/web"
)

// fakeRetained is a broker holding the retained messages of the speaker being forgotten.
type fakeRetained struct {
	kept    map[string]string
	filters []string
	cleared []string
}

func (f *fakeRetained) Retained(filters []string, _ time.Duration) (map[string][]byte, error) {
	f.filters = filters
	out := map[string][]byte{}
	for t, p := range f.kept {
		out[t] = []byte(p)
	}
	return out, nil
}

func (f *fakeRetained) Publish(topic, payload string) {
	if payload == "" {
		f.cleared = append(f.cleared, topic)
	}
}

func TestForgetSpeaker(t *testing.T) {
	a, srv := newWebApp(t)
	bus := &fakeRetained{kept: map[string]string{
		"yastation/gone/availability":                    "offline",
		"yastation/gone/web/info":                        `{"id":"gone"}`,
		"yastation/gone/intercom/info":                   `{"id":"gone"}`,
		"homeassistant/sensor/yastation_gone/lux/config": "{}",
	}}
	a.forgetBus = bus
	a.onWebInfo("yastation/gone/web/info", []byte(`{"id":"gone","name":"Old"}`))
	a.web.throttle = web.Throttle{}
	_, out := call(t, "POST", srv.URL+"/api/login", "", `{"user":"admin","password":"yastation"}`)
	tok, _ := out["token"].(string)

	if code, _ := call(t, "DELETE", srv.URL+"/api/peers/t1", tok, ""); code != 409 {
		t.Fatalf("forgetting itself: %d", code)
	}
	bus.kept["yastation/gone/availability"] = "online"
	if code, _ := call(t, "DELETE", srv.URL+"/api/peers/gone", tok, ""); code != 409 || len(bus.cleared) != 0 {
		t.Fatalf("forgetting an online speaker: %d, cleared %v", code, bus.cleared)
	}
	bus.kept["yastation/gone/availability"] = "offline"
	code, out := call(t, "DELETE", srv.URL+"/api/peers/gone", tok, "")
	if code != 200 || out["cleared"] != float64(4) {
		t.Fatalf("forget: %d %v", code, out)
	}
	if !slices.Equal(bus.filters, []string{"yastation/gone/#", "homeassistant/+/yastation_gone/#", "yastation/timers/gone/+"}) {
		t.Fatalf("filters %v", bus.filters)
	}
	slices.Sort(bus.cleared)
	if len(bus.cleared) != 4 || bus.cleared[0] != "homeassistant/sensor/yastation_gone/lux/config" {
		t.Fatalf("cleared %v", bus.cleared)
	}
	if _, ok := a.web.peers["gone"]; ok {
		t.Fatal("still listed")
	}
	if code, _ := call(t, "DELETE", srv.URL+"/api/peers/a%2Fb", tok, ""); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
}

// An empty web card (a forgotten speaker's, from the broker) takes the speaker off the list.
func TestEmptyWebCardDropsPeer(t *testing.T) {
	a, _ := newWebApp(t)
	a.onWebInfo("yastation/gone/web/info", []byte(`{"id":"gone","name":"Old"}`))
	if _, ok := a.web.peers["gone"]; !ok {
		t.Fatal("not listed")
	}
	a.onWebInfo("yastation/gone/web/info", nil)
	if _, ok := a.web.peers["gone"]; ok {
		t.Fatal("still listed")
	}
}
