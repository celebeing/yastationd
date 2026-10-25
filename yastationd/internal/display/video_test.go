package display

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// ledFile builds a 2x1 frames file: frame i lights pixel 0 at level 10*(i+1), each ms long.
func ledFile(ms ...int) []byte {
	var b []byte
	for i, d := range ms {
		v := byte(10 * (i + 1))
		b = AppendLedFrame(b, []byte{v, v, v, 0, 0, 0}, d)
	}
	return b
}

func TestAppendLedFrameRoundTrip(t *testing.T) {
	line := AppendLedFrame(nil, []byte{0x12, 0xab, 0xff, 0, 1, 2}, 33)
	if string(line) != "12abff 000102 33\n" {
		t.Fatalf("line %q", line)
	}
	c, err := ParseLed("x", bytes.NewReader(line), 2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c.Frames[0].Px[0] != (hw.RGB{R: 0x12, G: 0xab, B: 0xff}) || c.Frames[0].Dur != 33*time.Millisecond {
		t.Fatalf("parsed %+v", c.Frames[0])
	}
}

func TestVideoIndexByTime(t *testing.T) {
	c, err := ParseLed("v", bytes.NewReader(ledFile(33, 34, 33)), 2, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVideo(c)
	if v.Duration() != 100*time.Millisecond || v.Frames() != 3 {
		t.Fatalf("duration %v, %d frames", v.Duration(), v.Frames())
	}
	for el, want := range map[time.Duration]int{
		-time.Second: 0, 0: 0, 32 * time.Millisecond: 0, 33 * time.Millisecond: 1,
		66 * time.Millisecond: 1, 67 * time.Millisecond: 2, 99 * time.Millisecond: 2,
		100 * time.Millisecond: -1, time.Hour: -1,
	} {
		if got := v.Index(el); got != want {
			t.Errorf("Index(%v) = %d, want %d", el, got, want)
		}
	}
	f, ok := v.Frame(50*time.Millisecond, 255)
	if !ok || f.Px[0].R != 20 {
		t.Fatalf("frame at 50 ms = %+v, %v", f.Px, ok)
	}
	if f, _ := v.Frame(50*time.Millisecond, 128); f.Px[0].R != 10 {
		t.Errorf("brightness not applied: %+v", f.Px[0])
	}
	if _, ok := v.Frame(time.Second, 255); ok {
		t.Error("a frame past the end")
	}
}

func TestLoadVideo(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.led")
	if err := os.WriteFile(plain, ledFile(20, 20), 0o644); err != nil {
		t.Fatal(err)
	}
	// gzipped, named without .gz: found as path+".gz" and recognised by its magic
	var z bytes.Buffer
	zw := gzip.NewWriter(&z)
	zw.Write(ledFile(20, 20, 20))
	zw.Close()
	if err := os.WriteFile(filepath.Join(dir, "packed.led.gz"), z.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := LoadVideo(plain, 2, 1, 1); err != nil || v.Frames() != 2 {
		t.Fatalf("plain: %v", err)
	}
	v, err := LoadVideo(filepath.Join(dir, "packed.led"), 2, 1, 2.2)
	if err != nil || v.Frames() != 3 {
		t.Fatalf("gzipped: %v", err)
	}
	// the screen's gamma is applied on load, as for a stock pattern: 30/255 at 2.2 -> 2
	if got := v.Clip.Frames[2].Px[0].R; got != 2 {
		t.Errorf("gamma-corrected level %d, want 2", got)
	}
	if _, err := LoadVideo(filepath.Join(dir, "missing.led"), 2, 1, 1); err == nil {
		t.Error("missing file loaded")
	}
	if _, err := LoadVideo(plain, 3, 1, 1); err == nil {
		t.Error("wrong screen size accepted")
	}
}
