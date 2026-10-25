package display

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// ledFrame is one pattern line for a w x h screen: every pixel v (grey), lit lists the indices
// set to ffffff instead, and the duration.
func ledFrame(w, h int, v string, ms string, lit ...int) string {
	px := make([]string, w*h)
	for i := range px {
		px[i] = v
	}
	for _, i := range lit {
		px[i] = "ffffff"
	}
	return strings.Join(px, " ") + " " + ms
}

func writeLed(t *testing.T, dir, name string, lines ...string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name)+".led")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseLed(t *testing.T) {
	src := "loop\nsmooth 3\n" + ledFrame(3, 2, "808080", "20", 0) + "\n" + ledFrame(3, 2, "000000", "40") + "\n"
	c, err := ParseLed("x", strings.NewReader(src), 3, 2, 2.2)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Loop || len(c.Frames) != 2 || c.Duration() != 60*time.Millisecond {
		t.Fatalf("loop %v frames %d dur %v", c.Loop, len(c.Frames), c.Duration())
	}
	if p := c.Frames[0].Px[0]; p != (hw.RGB{R: 255, G: 255, B: 255}) {
		t.Fatalf("full white must stay full: %+v", p)
	}
	// Gamma 2.2: mid grey 0x80 comes out near 55, far darker than linear.
	if p := c.Frames[0].Px[1]; p.R < 50 || p.R > 60 || p.R != p.G || p.G != p.B {
		t.Fatalf("gamma: %+v", p)
	}
	// "loop" further down: the frames above it are an intro.
	mid := ledFrame(3, 2, "000000", "20", 0) + "\nloop\n" + ledFrame(3, 2, "000000", "20", 1) + "\n"
	if c, err := ParseLed("x", strings.NewReader(mid), 3, 2, 1); err != nil || !c.Loop || c.LoopFrom != 1 {
		t.Fatalf("mid-file loop: %v loop %v from %d", err, c.Loop, c.LoopFrom)
	}
	if _, err := ParseLed("x", strings.NewReader(ledFrame(3, 1, "000000", "20")), 3, 2, 1); err == nil {
		t.Fatal("a frame of the wrong size must fail")
	}
	if _, err := ParseLed("x", strings.NewReader("loop\n"), 3, 2, 1); err == nil {
		t.Fatal("no frames must fail")
	}
}

func TestPixelChainLoopsLastPart(t *testing.T) {
	dir := t.TempDir()
	writeLed(t, dir, "s/intro", ledFrame(2, 1, "000000", "100", 0))
	writeLed(t, dir, "s/loop", "loop", ledFrame(2, 1, "000000", "100", 1), ledFrame(2, 1, "000000", "100", 0, 1))
	l := &PixelLibrary{Dirs: []string{dir}, W: 2, H: 1}
	c, ok := l.Load("s/intro+s/loop")
	if !ok || !c.Looping() || c.LoopFrom != 1 || c.Duration() != 300*time.Millisecond {
		t.Fatalf("ok %v loop %v from %d dur %v", ok, c.Looping(), c.LoopFrom, c.Duration())
	}
	full := func() int { return 255 }
	r := c.Play(true, false, full)
	t0 := time.Now()
	lit := func(at time.Duration) []hw.RGB {
		f, done := r(t0.Add(at), FrontFrame{})
		if done {
			t.Fatalf("a looping clip ended at %v", at)
		}
		return f.Px
	}
	lit(0) // starts the clock
	if p := lit(50 * time.Millisecond); p[0].R != 255 || p[1].R != 0 {
		t.Fatalf("intro frame: %+v", p)
	}
	// 350 ms = past one pass: back into the loop part (its first frame), not the intro.
	if p := lit(350 * time.Millisecond); p[0].R != 0 || p[1].R != 255 {
		t.Fatalf("loop wrap: %+v", p)
	}
	// One-shot: pops after one pass; with hold keeps the last frame.
	once := c.Play(false, false, full)
	once(t0, FrontFrame{})
	if _, done := once(t0.Add(400*time.Millisecond), FrontFrame{}); !done {
		t.Fatal("one-shot did not end")
	}
	held := c.Play(false, true, func() int { return 128 })
	held(t0, FrontFrame{})
	f, done := held(t0.Add(time.Second), FrontFrame{})
	if done || f.Px[0].R != 128 || f.Px[1].R != 128 {
		t.Fatalf("hold: done %v px %+v (brightness 128)", done, f.Px)
	}
}

func TestPixelLibraryNamesAndSafety(t *testing.T) {
	dir := t.TempDir()
	writeLed(t, dir, "error", ledFrame(2, 1, "000000", "20", 0))
	writeLed(t, dir, "media/Pause", ledFrame(2, 1, "000000", "20", 1))
	writeLed(t, dir, "dark", ledFrame(2, 1, "000000", "20"))
	l := &PixelLibrary{Dirs: []string{dir}, W: 2, H: 1}
	if got := strings.Join(l.Names(), ","); got != "dark,error,media/Pause" {
		t.Fatalf("names %s", got)
	}
	if _, ok := l.Load("dark"); ok {
		t.Fatal("a blank pattern must be skipped")
	}
	for _, bad := range []string{"../error", "/error", "media/../error", ".hidden", "a\\b", ""} {
		if _, ok := l.Load(bad); ok {
			t.Fatalf("%q loaded", bad)
		}
	}
	if _, ok := l.Load("missing", "media/Pause"); !ok {
		t.Fatal("the second candidate must be found")
	}
}

func TestPixelFrontResolves(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"oknix/01-activation", "oknix/02-activation-to-listening"} {
		writeLed(t, dir, n, ledFrame(21, 12, "000000", "20", 0))
	}
	writeLed(t, dir, "oknix/03-listening-loop", "loop", ledFrame(21, 12, "000000", "20", 5))
	f := PixelFront{W: 21, H: 12, Patterns: &PixelLibrary{Dirs: []string{dir}, W: 21, H: 12}}
	a, ok := f.Anim("listening")
	if !ok || !a.Looping() || a.(PixelClip).LoopFrom != 2 {
		t.Fatalf("listening: ok %v %+v", ok, a)
	}
	if _, ok := f.Anim("sad"); ok {
		t.Fatal("sad has no pixel animation")
	}
	if f.Kind() != "pixels 21x12" {
		t.Fatal(f.Kind())
	}
	// The clock is drawn natively; scenes without a pixel drawing fall back to the seg7 picture.
	if c := f.Clock(time.Date(2026, 1, 1, 12, 34, 0, 0, time.UTC), time.UTC, 255); len(c.Px) != 252 {
		t.Fatalf("pixel clock: %d px", len(c.Px))
	}
	if c := f.Number(42, 255); c.Px != nil || c.Seg.Seg[3] == 0 {
		t.Fatalf("number fallback: %+v", c)
	}
	if px := ScreenPixels(f.Number(42, 255), 21, 12, 1); len(px) != 252 {
		t.Fatalf("fallback render: %d px", len(px))
	}
	native := FrontFrame{Px: []hw.RGB{{R: 200}}}
	if px := ScreenPixels(native, 1, 1, 0.5); px[0].R != 99 || native.Px[0].R != 200 {
		t.Fatalf("fade must scale a copy: %+v / %+v", px, native.Px)
	}
}

// TestPixelAnimsOnFirmware checks every pixelAnims entry against a real firmware tree:
// YASTATIOND_SCREEN_PATTERNS=<dump>/system/vendor/quasar/ledpatterns/screen go test ./internal/display
func TestPixelAnimsOnFirmware(t *testing.T) {
	dir := os.Getenv("YASTATIOND_SCREEN_PATTERNS")
	if dir == "" {
		t.Skip("YASTATIOND_SCREEN_PATTERNS not set")
	}
	f := PixelFront{W: 21, H: 12, Patterns: &PixelLibrary{Dirs: []string{dir}, W: 21, H: 12, Gamma: 2.2}}
	for name, cands := range pixelAnims {
		if len(cands) == 0 {
			continue
		}
		a, ok := f.Anim(name)
		if !ok {
			t.Errorf("%s: %v not found or blank", name, cands)
			continue
		}
		t.Logf("%-14s %-75s %6v loop=%v", name, cands[0], a.Duration(), a.Looping())
	}
	for _, n := range f.Anims() {
		if _, err := f.Patterns.find(n); err != nil {
			t.Errorf("%s: %v", n, err)
		}
	}
}
