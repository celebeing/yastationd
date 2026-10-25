package display

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// volFrame is a pattern line with pixel i lit (its index marks the frame).
func volFrame(i int) string {
	px := make([]string, 252)
	for j := range px {
		px[j] = "000000"
		if j == i {
			px[j] = "ffffff"
		}
	}
	return strings.Join(px, " ") + " 20\n"
}

func litIndex(f FrontFrame, _ bool) int {
	for i, p := range f.Px {
		if p.R > 0 {
			return i
		}
	}
	return -1
}

func TestPatternVolume(t *testing.T) {
	dir := t.TempDir()
	var sweep strings.Builder
	for i := 0; i < 101; i++ { // one frame per percent
		sweep.WriteString(volFrame(i))
	}
	os.WriteFile(filepath.Join(dir, "volume.led"), []byte(sweep.String()), 0o644)
	os.MkdirAll(filepath.Join(dir, "rcu_volume/volume_edge"), 0o755)
	os.WriteFile(filepath.Join(dir, "rcu_volume/volume_edge/10.led"), []byte(volFrame(200)+volFrame(201)), 0o644)
	f := PixelFront{W: 21, H: 12, Patterns: &PixelLibrary{Dirs: []string{dir}, W: 21, H: 12}}
	v, ok := f.Volume(func() int { return 255 }).(*patternVolume)
	if !ok {
		t.Fatal("pixel front with volume.led did not give the pattern scene")
	}
	t0 := time.Now()
	v.Set(40, 0)
	if got := litIndex(v.Render(t0, FrontFrame{})); got != 40 {
		t.Fatalf("first show = frame %d, want 40 (no glide from 0)", got)
	}
	v.Set(45, 0)
	mid := litIndex(v.Render(t0.Add(volumeGlide/2), FrontFrame{}))
	if mid <= 40 || mid >= 45 {
		t.Errorf("glide midway = %d, want between 40 and 45", mid)
	}
	if got := litIndex(v.Render(t0.Add(10*volumeGlide), FrontFrame{})); got != 45 {
		t.Errorf("glide end = %d", got)
	}
	v.Set(100, +1) // pushed past the top: the edge pattern, then back to the level
	if got := litIndex(v.Render(time.Now(), FrontFrame{})); got != 200 {
		t.Errorf("edge = %d, want the edge pattern", got)
	}
	if got := litIndex(v.Render(time.Now().Add(time.Second), FrontFrame{})); got != 100 {
		t.Errorf("after the edge = %d, want the level", got)
	}
	v.Set(0, -1) // no bottom edge pattern: just the level
	if v.edge != 0 {
		t.Error("edge without a pattern")
	}

	// no patterns: the number
	if _, ok := (PixelFront{W: 21, H: 12}).Volume(func() int { return 255 }).(*numberVolume); !ok {
		t.Error("pixel front without patterns should fall back to the number")
	}
}
