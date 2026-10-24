package app

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
	"github.com/celebeing/yastationd/yastationd/internal/wifisetup"
)

func TestSetupKeyCombo(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &App{log: log,
		ringStack:   display.NewStack(func(time.Time) display.RingFrame { return nil }),
		matrixStack: display.NewStack(func(time.Time) display.MatrixFrame { return display.MatrixFrame{} }),
		setup:       wifisetup.New(wifisetup.Options{Log: log})}
	a.sat = &satellite{a: a}

	if a.setupKey("volume_up", true) {
		t.Error("a key outside the combo was swallowed")
	}
	if a.setupKey("volume_down", true) || a.setupKey("voice", true) {
		t.Error("an incomplete combo swallowed a key")
	}
	if !a.setupKey("mute_key", true) {
		t.Error("the press completing the combo was not swallowed")
	}
	if a.combo.timer == nil {
		t.Fatal("hold timer not armed")
	}
	a.setupKey("voice", false)
	if a.combo.timer != nil {
		t.Error("releasing a key did not disarm the timer")
	}
	if a.setupKey("voice", true); a.combo.timer == nil {
		t.Error("pressing it again did not re-arm the timer")
	}
	a.combo.timer.Stop()
}
