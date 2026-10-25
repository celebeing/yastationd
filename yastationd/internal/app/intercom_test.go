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
	a := &App{cfg: cfg, log: log,
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return display.Blank(12) }),
		matrixStack: display.NewStack(func(time.Time) display.MatrixFrame { return display.MatrixFrame{} }),
		mixer:       hw.NewMixer("0"),
		mic:         &hw.Mic{Device: "fake", Channels: 1, Rate: 16000, Log: log},
		store:       newStateStore(filepath.Join(t.TempDir(), "state"), defaultState(128, 128, 128, false)),
	}
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
		from.ic.peers[to.cfg.DeviceID] = intercom.Peer{ID: to.cfg.DeviceID, Name: to.ic.name}
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
		t.Fatalf("%s: %v, want %v", a.cfg.DeviceID, a.ic.m.Status(), want)
	}

	office.icTranscript("Соедини со спальней")
	wait(office, intercom.Calling)
	wait(bedroom, intercom.Ringing)
	if !bedroom.ringStack.Has("intercom") {
		t.Error("no ringing indication")
	}
	bedroom.icTranscript("да")
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
	office.icTranscript("Алиса, заверши звонок")
	wait(office, intercom.Idle)
	wait(bedroom, intercom.Idle)
	time.Sleep(50 * time.Millisecond)
	bedroom.ic.mu.Lock()
	running = bedroom.ic.stop != nil
	bedroom.ic.mu.Unlock()
	if running || bedroom.ringStack.Has("intercom") {
		t.Fatal("call audio or ring left after the hang-up")
	}

	// the voice key: answer when ringing, hang up otherwise; not ours when idle
	if office.icVoiceKey() {
		t.Error("idle voice key was taken")
	}
	bedroom.icTranscript("позвони в кабинет")
	wait(office, intercom.Ringing)
	office.icVoiceKey()
	wait(bedroom, intercom.InCall)
	bedroom.icVoiceKey()
	wait(office, intercom.Idle)

	// declining by voice
	office.icTranscript("набери спальню")
	wait(bedroom, intercom.Ringing)
	bedroom.icTranscript("нет, не сейчас")
	wait(office, intercom.Idle)
	if s := office.ic.m.Status(); s.Ended != "rejected: declined" {
		t.Fatalf("ended %q", s.Ended)
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
