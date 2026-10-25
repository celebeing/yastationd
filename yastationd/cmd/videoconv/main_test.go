package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/display"
)

func TestFrameMsStaysOnTheTimeline(t *testing.T) {
	for _, fps := range []int{24, 25, 30, 50} {
		total := 0
		for i := 0; i < 6572; i++ {
			d := frameMs(i, fps)
			if d < 1000/fps || d > 1000/fps+1 {
				t.Fatalf("fps %d frame %d: %d ms", fps, i, d)
			}
			total += d
			if want := (i + 1) * 1000 / fps; total < want-1 || total > want+1 {
				t.Fatalf("fps %d: after %d frames %d ms, want ~%d", fps, i+1, total, want)
			}
		}
	}
}

func TestWriteFramesRoundTrip(t *testing.T) {
	const w, h = 3, 2
	raw := make([]byte, 4*w*h*3)
	for i := range raw {
		raw[i] = byte(i)
	}
	for _, name := range []string{"f.led", "f.led.gz"} {
		p := filepath.Join(t.TempDir(), name)
		n, err := writeFrames(p, bytes.NewReader(raw), w*h*3, 30, filepath.Ext(name) == ".gz")
		if err != nil || n != 4 {
			t.Fatalf("%s: %d frames, %v", name, n, err)
		}
		v, err := display.LoadVideo(p, w, h, 1)
		if err != nil {
			t.Fatal(err)
		}
		if v.Frames() != 4 || v.Duration() != 133*time.Millisecond {
			t.Fatalf("%s: %d frames, %v", name, v.Frames(), v.Duration())
		}
		if px := v.Clip.Frames[1].Px[0]; px.R != 18 || px.G != 19 || px.B != 20 {
			t.Errorf("%s: frame 1 pixel 0 = %+v", name, px)
		}
	}
	// a truncated last frame is an error, not a silently short file
	if _, err := writeFrames(filepath.Join(t.TempDir(), "t.led"), bytes.NewReader(raw[:len(raw)-1]), w*h*3, 30, false); err == nil {
		t.Error("truncated input accepted")
	}
}

// End to end on a synthetic clip ffmpeg generates itself (no media involved). Skipped without ffmpeg.
func TestConvertWithFFmpeg(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	out, err := exec.Command("ffmpeg", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=25:duration=1",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:v", "ffv1", "-c:a", "pcm_s16le", src).CombinedOutput()
	if err != nil {
		t.Skipf("ffmpeg cannot make the test clip: %v %s", err, out)
	}
	for _, fit := range []string{"pad", "crop"} {
		frames, wav := filepath.Join(dir, fit+".led.gz"), filepath.Join(dir, fit+".wav")
		n, err := convertFrames("ffmpeg", src, frames, 21, 12, 30, fit)
		if err != nil || n < 29 || n > 31 {
			t.Fatalf("%s: %d frames, %v", fit, n, err)
		}
		v, err := display.LoadVideo(frames, 21, 12, 1)
		if err != nil || v.Frames() != n {
			t.Fatalf("%s: reload: %v", fit, err)
		}
		if fit == "pad" { // 4:3 in 21x12 -> 16 columns, black at the sides
			if p := v.Clip.Frames[0].Px[0]; p.R+p.G+p.B != 0 {
				t.Errorf("pad: corner pixel %+v not black", p)
			}
		}
		if err := convertAudio("ffmpeg", src, wav); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(wav)
		if err != nil || len(b) < 44 || string(b[:4]) != "RIFF" {
			t.Fatalf("wav: %v", err)
		}
	}
}
