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
		matrixStack: display.NewStack(func(time.Time) display.FrontFrame { return display.FrontFrame{} }),
		setup:       wifisetup.New(wifisetup.Options{Log: log})}
	a.sat = &satellite{a: a}

	if a.setupKey("voice", true) || a.combo.timer != nil {
		t.Error("the voice key armed setup with the mic on")
	}
	a.setupKey("voice", false)
	a.muted.Store(true)
	if a.setupKey("volume_down", true) || a.combo.timer != nil {
		t.Error("another key armed setup")
	}
	if a.setupKey("voice", true) {
		t.Error("the voice key was swallowed (muted, it only shows the mute indication)")
	}
	if a.combo.timer == nil {
		t.Fatal("hold timer not armed with the mic muted")
	}
	a.setupKey("voice", false)
	if a.combo.timer != nil {
		t.Error("releasing the voice key did not disarm the timer")
	}
	if a.setupKey("voice", true); a.combo.timer == nil {
		t.Error("pressing it again did not re-arm the timer")
	}
	a.combo.timer.Stop()
}
