package app

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/ddp"
	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// A streamed frame takes the ambilight at the effects' brightness while fresh; then the light
// state is back. With effects off, frames are ignored.
func TestAmbilightFxLayer(t *testing.T) {
	root := t.TempDir()
	colors := []string{"red", "green", "blue", "white"}
	for i := range 2 {
		for _, c := range colors {
			dir := filepath.Join(root, "sys/class/leds", "s:"+c+":"+strconv.Itoa(i))
			os.MkdirAll(dir, 0o755)
			os.WriteFile(filepath.Join(dir, "trigger"), []byte("[none]"), 0o644)
			os.WriteFile(filepath.Join(dir, "brightness"), []byte("0"), 0o644)
		}
	}
	strip, err := hw.NewAmbilight(root, 2, "s:{color}:{i}", colors, []string{""})
	if err != nil {
		t.Fatal(err)
	}
	a := newMusicApp(t)
	a.ambilight = strip
	a.fx.kick = make(chan struct{}, 1)
	a.store.update(func(s *persistState) {
		s.AmbilightFx, s.AmbilightFxBright = true, 128
		s.AmbilightOn, s.AmbilightRGBW, s.AmbilightBright = true, hw.RGBW{W: 255}, 255
	})

	a.onDDPFrame(ddp.Frame{Channels: 3, Data: []byte{200, 100, 0, 0, 0, 50}})
	select {
	case <-a.fx.kick:
	default:
		t.Fatal("no kick for the render loop")
	}
	f := a.ambilightShown(time.Now())
	if f[0] != (hw.RGBW{R: 100, G: 50}) || f[1] != (hw.RGBW{B: 25}) {
		t.Fatalf("streamed frame at half brightness: %v", f)
	}
	if f := a.ambilightShown(time.Now().Add(fxStale + time.Second)); f[0] != (hw.RGBW{W: 255}) {
		t.Fatalf("after the stream: %v, want the light state", f)
	}

	a.store.update(func(s *persistState) { s.AmbilightFx = false })
	a.onDDPFrame(ddp.Frame{Channels: 4, Data: []byte{1, 2, 3, 4, 5, 6, 7, 8}})
	if f := a.ambilightShown(time.Now()); f[0] != (hw.RGBW{W: 255}) {
		t.Fatalf("effects off: %v", f)
	}
}
