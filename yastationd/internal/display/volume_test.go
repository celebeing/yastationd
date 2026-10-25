package display

import (
	"testing"
	"time"
)

func TestPixelVolume(t *testing.T) {
	f := PixelFront{W: 21, H: 12, Gamma: 1}
	v, ok := f.Volume(func() int { return 255 }).(*pixelVolume)
	if !ok {
		t.Fatal("a 21x12 screen did not give the pixel volume scene")
	}
	bar := func(fr FrontFrame) []uint8 {
		out := make([]uint8, 21)
		for x := range out {
			out[x] = fr.Px[11*21+x].R
		}
		return out
	}
	track := uint8(31) // 255 * volumeTrack, gamma 1

	v.Set(50, 0)
	t0 := time.Now()
	b := bar(mustRender(v, t0))
	// 50 % of 21 px = 10.5: ten full, one half, the rest the dim track
	if b[9] != 255 || b[10] < 130 || b[10] > 160 || b[11] != track || b[20] != track {
		t.Errorf("bar at 50 %%: %v", b)
	}
	// the number: "50" centred in the 4x9 digits, row 0 lit
	lit := 0
	for x := 0; x < 21; x++ {
		if mustRender(v, t0).Px[x].R > 0 {
			lit++
		}
	}
	if lit == 0 {
		t.Error("no number on the top row")
	}

	// a change cross-fades the number and glides the bar
	v.Set(55, 0)
	mid := mustRender(v, time.Now().Add(volumeFade/2))
	if b := bar(mid); b[11] == 255 {
		t.Errorf("bar jumped instead of gliding: %v", b)
	}
	half := false
	for _, p := range mid.Px[:9*21] {
		if p.R > 20 && p.R < 235 {
			half = true
		}
	}
	if !half {
		t.Error("no half-lit pixel mid cross-fade")
	}
	end := bar(mustRender(v, time.Now().Add(time.Second)))
	if end[10] != 255 || end[11] < 140 || end[11] > 165 { // 55 % = 11.55 px: 31 + 0.55 * 224
		t.Errorf("bar at 55 %%: %v", end)
	}

	// pushed past the top: the end pixel flashes
	v.Set(100, +1)
	if b := bar(mustRender(v, time.Now())); b[20] != 255 {
		t.Errorf("edge flash: %v", b)
	}
}

func mustRender(v *pixelVolume, now time.Time) FrontFrame {
	f, _ := v.Render(now, FrontFrame{})
	return f
}

func TestSmallScreenVolumeIsANumber(t *testing.T) {
	if _, ok := (PixelFront{W: 12, H: 8}).Volume(func() int { return 255 }).(*numberVolume); !ok {
		t.Error("a screen too small for the bar should show the number")
	}
}
