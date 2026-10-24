package display

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Two frames: the left digit's upper box (A B F G) at full level for 100 ms, then off for 50 ms.
const testScled = `loop
255 255 0 0 0 255 255 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 | 100
0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 0 | 0 0 0 0 0 0 128 | 10 200 0 0 | 50
`

func TestParseScled(t *testing.T) {
	c, err := ParseScled("t", strings.NewReader(testScled))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Loop || len(c.Frames) != 2 || c.Duration() != 150*time.Millisecond {
		t.Fatalf("clip = %+v", c)
	}
	if c.Frames[0].Seg[0] != [7]uint8{255, 255, 0, 0, 0, 255, 255} || c.Frames[1].Seg[3][6] != 128 {
		t.Fatalf("levels = %+v", c.Frames)
	}
	m := c.Frames[1].matrixFrame(255)
	if !m.Graded || m.Level.Colon != 200 { // colon = max(DH, DL)
		t.Fatalf("matrix frame = %+v", m)
	}
	if _, err := ParseScled("bad", strings.NewReader("1 2 3 | 4\n")); err == nil {
		t.Fatal("malformed line accepted")
	}
}

func TestBinscledRoundTrip(t *testing.T) {
	want, _ := ParseScled("t", strings.NewReader(testScled))
	// Build the binary form by hand: flag, 32 levels, duration u16 BE.
	var b []byte
	for i, f := range want.Frames {
		r := make([]byte, binscledFrame)
		if i == 0 {
			r[0] = 1
		}
		for d := range f.Seg {
			copy(r[1+d*7:], f.Seg[d][:])
		}
		copy(r[29:], f.Extra[:])
		ms := f.Dur.Milliseconds()
		r[33], r[34] = byte(ms>>8), byte(ms)
		b = append(b, r...)
	}
	got, err := ParseBinscled("t", b)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Loop || len(got.Frames) != len(want.Frames) || got.Frames[1] != want.Frames[1] {
		t.Fatalf("binary = %+v, want %+v", got, want)
	}
	again, err := ParseScled("t", strings.NewReader(string(FormatScled(got))))
	if err != nil || again.Frames[0] != want.Frames[0] || !again.Loop {
		t.Fatalf("text round trip: %+v %v", again, err)
	}
	if _, err := ParseBinscled("t", b[:34]); err == nil {
		t.Fatal("truncated binary accepted")
	}
}

func TestPlayClip(t *testing.T) {
	c, _ := ParseScled("t", strings.NewReader(testScled))
	t0 := time.Unix(1000, 0)
	once := PlayClip(c, false, func() int { return 128 })
	f, done := once(t0, MatrixFrame{})
	if done || f.Level.Seg[0][0] != 255 || f.Brightness != 128 {
		t.Fatalf("first frame %+v done=%v", f, done)
	}
	if f, _ = once(t0.Add(120*time.Millisecond), MatrixFrame{}); f.Level.Seg[0][0] != 0 {
		t.Fatalf("second frame %+v", f)
	}
	if _, done = once(t0.Add(150*time.Millisecond), MatrixFrame{}); !done {
		t.Fatal("one-shot clip did not end")
	}
	loop := PlayClip(c, true, func() int { return 255 })
	loop(t0, MatrixFrame{})
	if f, done = loop(t0.Add(160*time.Millisecond), MatrixFrame{}); done || f.Level.Seg[0][0] != 255 {
		t.Fatalf("looping clip at 160 ms: %+v done=%v", f, done)
	}
}

func TestClipLibrary(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "boot.scled"), []byte(testScled), 0o644)
	l := &ClipLibrary{Dirs: []string{filepath.Join(dir, "missing"), dir}}
	if c, ok := l.Load("player_pause", "boot"); !ok || c.Name != "boot" {
		t.Fatalf("alias lookup: %+v %v", c, ok)
	}
	for _, own := range []string{"eyes", "happy", "sad", "wink", "sleep"} {
		if _, ok := l.Load(own); !ok {
			t.Errorf("embedded clip %s missing", own)
		}
	}
	if _, ok := l.Load("../boot"); ok {
		t.Fatal("path in a clip name accepted")
	}
	names := strings.Join(l.Names(), ",")
	if !strings.Contains(names, "boot") || !strings.Contains(names, "happy") {
		t.Fatalf("names = %s", names)
	}
}

func TestClipFromRaw(t *testing.T) {
	c, err := ClipFromRaw([]RawFrame{{Seg: []string{"ABFG", "", "", "abfg"}, Ms: 200}, {Text: "12:34", Ms: 300}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Frames[0].Seg[0] != [7]uint8{255, 255, 0, 0, 0, 255, 255} || c.Frames[0].Seg[3] != c.Frames[0].Seg[0] {
		t.Fatalf("seg frame = %+v", c.Frames[0])
	}
	if c.Frames[1].Extra[0] != 255 || c.Frames[1].Seg[0][1] != 255 || c.Duration() != 500*time.Millisecond {
		t.Fatalf("text frame = %+v", c.Frames[1])
	}
	for _, bad := range [][]RawFrame{nil, {{Seg: []string{"AZ"}, Ms: 10}}, {{Text: "x", Ms: 0}}, {{Seg: []string{"A", "A", "A", "A", "A"}, Ms: 1}}} {
		if _, err := ClipFromRaw(bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
