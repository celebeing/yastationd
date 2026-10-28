package app

import (
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/config"
	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
	"github.com/celebeing/yastationd/yastationd/internal/intercom"
	"github.com/celebeing/yastationd/yastationd/internal/wake"
)

// fakeAudio puts an aplay that swallows its input and an arecord that streams silence on PATH.
func fakeAudio(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"aplay":   "#!/bin/sh\nexec cat > /dev/null\n",
		"arecord": "#!/bin/sh\nexec cat /dev/zero\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
}

func testIntercomApp(t *testing.T, id, name string) *App {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Default()
	cfg.DeviceID, cfg.DeviceName = id, name
	cfg.Intercom, cfg.IntercomSecret, cfg.MQTTHost = true, "0123456789abcdef-test", "127.0.0.1"
	cfg.WyomingPort = 0
	cfg.AnnounceChime = false
	cfg.IntercomRing = 2 * time.Second
	cfg.IntercomPort = freeUDPPort(t)
	a := &App{log: log,
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(12) }),
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
		mixer:       hw.NewMixer("0"),
		mic:         &hw.Mic{Device: "fake", Channels: 1, Rate: 16000, Log: log},
		store:       newStateStore(filepath.Join(t.TempDir(), "state"), defaultState(128, 128, 128, false)),
	}
	a.conf.Store(&cfg)
	a.sat = &satellite{a: a}
	a.ic = a.newIntercom()
	return a
}

func TestIntercomVoiceScenario(t *testing.T) {
	fakeAudio(t)
	office := testIntercomApp(t, "office", "Кабинет")
	bedroom := testIntercomApp(t, "bedroom", "Спальня")
	// signalling straight across instead of through a broker; peers as their info would announce
	link := func(from, to *App) {
		from.ic.m = intercom.NewManager(intercomOptions(from, func(dst string, b []byte) { go to.ic.m.Handle(b) }))
		from.ic.peers[to.cfg().DeviceID] = intercom.Peer{ID: to.cfg().DeviceID, Name: to.ic.name}
	}
	link(office, bedroom)
	link(bedroom, office)
	t.Cleanup(func() { office.ic.m.Hangup("test"); bedroom.ic.m.Hangup("test") })

	wait := func(a *App, want intercom.State) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
			if a.ic.m.Status().State == want {
				return
			}
		}
		t.Fatalf("%s: %v, want %v", a.cfg().DeviceID, a.ic.m.Status(), want)
	}

	cmd := func(a *App, phrase string) { a.onWakeEvent(wake.Event{Kind: "CMD", Phrase: phrase}) }
	alisa := func(a *App) { a.onWakeEvent(wake.Event{Kind: "WAKE", Phrase: "алиса"}) }

	// outgoing by voice: HA's script asks on the shared topic; the speaker that sent HA the
	// request just now takes it, the other one doesn't
	bedroom.cmdIntercomCall([]byte(`{"to":"office"}`))
	time.Sleep(50 * time.Millisecond)
	if bedroom.ic.m.Status().State != intercom.Idle {
		t.Fatal("a speaker nobody talked to took the call request")
	}
	office.sat.lastHeard.Store(time.Now().UnixNano())
	office.cmdIntercomCall([]byte(`{"to":"bedroom"}`))
	wait(office, intercom.Calling)
	wait(bedroom, intercom.Ringing)
	if !bedroom.ringStack.Has("intercom") {
		t.Error("no ringing indication")
	}
	cmd(bedroom, "выключи свет") // not a call command
	if bedroom.ic.m.Status().State != intercom.Ringing {
		t.Fatal("«выключи свет» touched the call")
	}
	cmd(bedroom, "продолжи") // answers, with or without «Алиса»
	wait(office, intercom.InCall)
	wait(bedroom, intercom.InCall)
	time.Sleep(200 * time.Millisecond) // a little audio both ways
	bedroom.ic.mu.Lock()
	running := bedroom.ic.stop != nil
	bedroom.ic.mu.Unlock()
	if !running {
		t.Fatal("no call audio on the callee")
	}
	if !office.icActive() || !office.assistantBusy() {
		t.Error("music would not duck during the call")
	}
	cmd(office, "стоп") // in a call only after «Алиса»
	if office.ic.m.Status().State != intercom.InCall {
		t.Fatal("«стоп» without «Алиса» hung up")
	}
	alisa(office)
	cmd(office, "хватит")
	wait(office, intercom.Idle)
	wait(bedroom, intercom.Idle)
	time.Sleep(50 * time.Millisecond)
	bedroom.ic.mu.Lock()
	running = bedroom.ic.stop != nil
	bedroom.ic.mu.Unlock()
	if running || bedroom.ringStack.Has("intercom") {
		t.Fatal("call audio or ring left after the hang-up")
	}

	// a lone «Алиса» doesn't answer; «Алиса, стоп» declines
	bedroom.ic.m.Call("office", "Кабинет")
	wait(office, intercom.Ringing)
	alisa(office)
	time.Sleep(100 * time.Millisecond)
	if office.ic.m.Status().State != intercom.Ringing {
		t.Fatal("a lone «Алиса» did something to the ringing call")
	}
	cmd(office, "стоп")
	wait(bedroom, intercom.Idle)
	if s := bedroom.ic.m.Status(); s.Ended != "rejected: declined" {
		t.Fatalf("ended %q", s.Ended)
	}

	// the voice key: answer when ringing, hang up otherwise; not ours when idle
	if office.icVoiceKey() {
		t.Error("idle voice key was taken")
	}
	bedroom.ic.m.Call("office", "Кабинет")
	wait(office, intercom.Ringing)
	office.icVoiceKey()
	wait(bedroom, intercom.InCall)
	bedroom.icVoiceKey()
	wait(office, intercom.Idle)

	// the mic-mute switch declines a ringing call and ends a running one
	office.ic.m.Call("bedroom", "Спальня")
	wait(bedroom, intercom.Ringing)
	bedroom.setMute(true)
	wait(office, intercom.Idle)
	if s := office.ic.m.Status(); s.Ended != "rejected: declined" {
		t.Fatalf("mute while ringing: %q", s.Ended)
	}
	bedroom.setMute(false)
	office.ic.m.Call("bedroom", "Спальня")
	wait(bedroom, intercom.Ringing)
	bedroom.icVoiceKey()
	wait(office, intercom.InCall)
	office.setMute(true)
	wait(bedroom, intercom.Idle)
	if s := bedroom.ic.m.Status(); s.Ended != "hung up by the peer" {
		t.Fatalf("mute in a call: %q", s.Ended)
	}
	if office.ringStack.Has("intercom") {
		t.Error("call indication left after the call")
	}
}

func freeUDPPort(t *testing.T) int {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}
