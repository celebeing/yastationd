package display

import (
	"strings"
	"testing"
	"time"
)

func TestFontsWellFormed(t *testing.T) {
	for name, f := range map[string]pfont{"font7": font7, "font5": font5} {
		for r, g := range f.glyphs {
			if len(g) != f.h {
				t.Errorf("%s %q: %d rows, want %d", name, r, len(g), f.h)
				continue
			}
			for _, row := range g {
				if len(row) != len(g[0]) {
					t.Errorf("%s %q: ragged rows %q", name, r, g)
					break
				}
			}
		}
		// Every Russian letter, digit and the common punctuation has a glyph of its own or an alias.
		for _, r := range "АБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,!?:;-+=/()%°\"'«»—№…" {
			if g := f.glyph(r); len(g) > 0 && &g[0] == &f.glyphs[0][0] {
				t.Errorf("%s: no glyph for %q", name, r)
			}
		}
	}
}

func TestWrapText(t *testing.T) {
	got, ok := wrapText(font5, "Свет вкл", 21)
	if !ok || strings.Join(got, "|") != "Свет|вкл" {
		t.Fatalf("wrap %q %v", got, ok)
	}
	// Punctuation alone stays with the word before it, an opening quote with the word after.
	got, ok = wrapText(font5, "да — нет", 21)
	if !ok || got[0] != "да —" {
		t.Fatalf("dash: %q", got)
	}
	if _, ok := wrapText(font5, "Привет мир", 21); ok {
		t.Fatal("a word wider than a line is never broken")
	}
	for _, l := range got {
		if runesWidth(font5, l) > 21 {
			t.Fatalf("line %q is %d wide", l, runesWidth(font5, l))
		}
	}
}

func TestPixelTextLayouts(t *testing.T) {
	f := PixelFront{W: 21, H: 12}
	if l, fits := f.textLayout("ОК"); l != 1 || !fits {
		t.Error("a short word: one line")
	}
	if l, fits := f.textLayout("ОК ДА"); l != 2 || !fits {
		t.Errorf("auto: two short words fit two lines, got %d %v", l, fits)
	}
	if l, fits := f.textLayout("Это длинное сообщение"); l != 1 || fits {
		t.Error("auto: a long text scrolls in one line")
	}
	f.TextLines = 2
	if l, _ := f.textLayout("Это не все дома"); l != 2 {
		t.Error("forced two lines, paged")
	}
	if l, fits := f.textLayout("Привет мир"); l != 1 || fits {
		t.Error("forced two lines: a long word scrolls in one line")
	}
	if f.bigDigitsText("12:34") != "12:34" || f.bigDigitsText(" 5:07") != "5:07" || f.bigDigitsText("1234567") != "" {
		t.Error("big digits")
	}
	// A scrolling line moves.
	f.TextLines = 1
	a := f.textCanvas("Длинный текст", 0, 50*time.Millisecond)
	b := f.textCanvas("Длинный текст", 500*time.Millisecond, 50*time.Millisecond)
	same := true
	for i := range a.v {
		if a.v[i] != b.v[i] {
			same = false
		}
	}
	if same {
		t.Error("the marquee does not move")
	}
}

func TestMarqueeTime(t *testing.T) {
	if MarqueeTime("12", 300*time.Millisecond) != 0 {
		t.Fatal("seg7: text that fits takes time")
	}
	if got := MarqueeTime("ABCDEF", 300*time.Millisecond); got != 11*300*time.Millisecond {
		t.Fatalf("seg7: %v", got)
	}
	f := PixelFront{W: 21, H: 12}
	if f.MarqueeTime("Hi", 300*time.Millisecond) != 0 {
		t.Fatal("pixels: text that fits takes time")
	}
	long := "Some Band - A Rather Long Song Title That Scrolls"
	f.TextLines = 1
	want := time.Duration(runesWidth(font7, long)+21) * marqueePxStep(300*time.Millisecond)
	if got := f.MarqueeTime(long, 300*time.Millisecond); got != want {
		t.Fatalf("pixels, one line: %v, want %v", got, want)
	}
}

func TestClockLevels(t *testing.T) {
	f := PixelFront{W: 21, H: 12, Gamma: 1}
	now := time.Date(2026, 1, 1, 12, 34, 30, 0, time.UTC)
	bands := make([]float64, 21)
	bands[0], bands[20] = 1, 0.5
	px := f.ClockLevels(now, time.UTC, bands, 255).Px
	lit := func(x, y int) bool { p := px[y*21+x]; return p.R|p.G|p.B != 0 }
	if !lit(0, 11) || !lit(0, 6) || !lit(20, 11) || lit(20, 6) || lit(10, 11) {
		t.Fatal("bars: band 0 full, band 20 half, the middle empty")
	}
	top := false
	for x := 0; x < 21; x++ {
		top = top || lit(x, 1)
	}
	if !top {
		t.Fatal("no clock over the bars")
	}
	if (Seg7Front{}).HasLevels() {
		t.Fatal("seg7 offers levels")
	}
}
