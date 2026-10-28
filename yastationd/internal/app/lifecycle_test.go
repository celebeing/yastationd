package app

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/health"
	"github.com/celebeing/yastationd/yastationd/internal/wyoming"
)

func TestLifecycle(t *testing.T) {
	a := newExpressApp(t, true)
	a.cfg().WyomingPort = 10700
	a.cfg().BootConnectTimeout = 300 * time.Millisecond
	a.cfg().StateFile = t.TempDir() + "/state" // greeting marker lives under cfg.Path too
	a.cfg().SysRoot = t.TempDir()
	a.sat = &satellite{a: a}
	a.hmon = health.New("", nil) // never online: once ready, the speaker shows degraded
	stop := make(chan struct{})
	defer close(stop)
	go a.statusLoop(stop)

	time.Sleep(100 * time.Millisecond)
	if life(a.lifeState.Load()) != lifeBooting || !a.ringStack.Has(lifeSlot) || !a.matrixStack.Has(lifeSlot) {
		t.Fatal("booting: no boot indication")
	}
	time.Sleep(time.Second)
	if life(a.lifeState.Load()) != lifeBootFailed {
		t.Fatalf("no connection after the timeout: state %d", a.lifeState.Load())
	}
	a.sat.mu.Lock()
	a.sat.main = &wyoming.Conn{} // HA's satellite link comes up (no broker configured)
	a.sat.mu.Unlock()
	time.Sleep(700 * time.Millisecond)
	if life(a.lifeState.Load()) != lifeReady || a.ringStack.Has(lifeSlot) {
		t.Fatal("connected: still in the boot phase")
	}
	if a.ringStack.Has("degraded") {
		t.Fatal("degraded at once after ready: must wait out the grace")
	}
}

func TestRTCTimeTrusted(t *testing.T) {
	a := &App{}
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if a.rtcTimeTrusted(now) {
		t.Fatal("no RTC on the board: wait for the sync")
	}
	a.prof.Platform.RTCBacked = true
	if !a.rtcTimeTrusted(now) || a.rtcTimeTrusted(time.Unix(0, 0)) || a.rtcTimeTrusted(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("RTC: trust a real date, not a reset one")
	}
}
