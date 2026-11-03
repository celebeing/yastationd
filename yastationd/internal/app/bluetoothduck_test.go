package app

import (
	"testing"
	"time"
)

func TestBluetoothFadeBoundsAndReverses(t *testing.T) {
	if got := nextBluetoothFade(255, 0, 255); got != 230 {
		t.Fatalf("first fade down = %d, want 230", got)
	}
	if got := nextBluetoothFade(5, 0, 255); got != 0 {
		t.Fatalf("fade must not go below mute: %d", got)
	}
	if got := nextBluetoothFade(230, 255, 255); got != 255 {
		t.Fatalf("fade must not exceed configured maximum: %d", got)
	}
}

func TestWakeDuckingBridgesVoiceGate(t *testing.T) {
	a := &App{}
	a.duckBluetoothNow()
	if until := time.Unix(0, a.bluetoothWakeUntil.Load()); until.Before(time.Now().Add(time.Second)) {
		t.Fatalf("wake mute bridge ends too soon: %v", until)
	}
}
