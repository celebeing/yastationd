package display

import (
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

// The UwU glyph must stay a 21x12 mask, and its lit pixels must land at the exact
// (x, y) of the string art — regressions here silently scramble the face on glass.
func TestUwuFrame(t *testing.T) {
	for i, row := range uwu {
		if len(row) != 21 {
			t.Fatalf("uwu row %d: %d cols, want 21", i, len(row))
		}
	}
	f := UwuFrame(21, 12, hw.RGB{R: 255})
	if len(f) != 21*12 {
		t.Fatalf("frame len = %d", len(f))
	}
	n := 0
	for y, row := range uwu {
		for x := 0; x < 21; x++ {
			want := row[x] == '#'
			got := f[y*21+x] != (hw.RGB{})
			if got != want {
				t.Errorf("px (%d,%d): lit=%v, want %v (row %q)", x, y, got, want, row)
			}
			if got {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatal("glyph is empty")
	}
	// Smaller screens clip, not crash.
	if f2 := UwuFrame(4, 4, hw.RGB{}); len(f2) != 16 {
		t.Fatalf("clip frame len = %d", len(f2))
	}
}
