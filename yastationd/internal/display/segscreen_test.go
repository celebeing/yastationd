package display

import (
	"testing"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func TestSeg7ToPixels(t *testing.T) {
	// "12:34": all four cells must be visible on the 21x12 orion screen.
	f := Text("12:34", 255)
	px := Seg7ToPixels(f, 21, 12, hw.RGB{R: 255, G: 128, B: 64})
	if len(px) != 21*12 {
		t.Fatalf("frame len = %d", len(px))
	}
	lit := 0
	for _, c := range px {
		if c != (hw.RGB{}) {
			lit++
		}
	}
	if lit < 20 {
		t.Fatalf("only %d pixels lit", lit)
	}
	// A colon-only frame proves the exact, separated 2x2 geometry (x=10, rows 3-4/7-8).
	colon := Seg7ToPixels(hw.MatrixFrame{Colon: true, Brightness: 255}, 21, 12, hw.RGB{R: 255})
	for _, p := range []int{3*21 + 10, 4*21 + 10, 7*21 + 10, 8*21 + 10} {
		if colon[p] == (hw.RGB{}) {
			t.Fatalf("colon px %d is dark", p)
		}
	}
	// Brightness scales each channel, not just the shape.
	dim := Seg7ToPixels(Text("8888", 128), 21, 12, hw.RGB{R: 255, G: 128, B: 64})
	var got hw.RGB
	for _, c := range dim {
		if c != (hw.RGB{}) {
			got = c
			break
		}
	}
	if got != (hw.RGB{R: 128, G: 64, B: 32}) {
		t.Fatalf("dim colour = %+v", got)
	}
	if got := Seg7ToPixels(f, 14, 6, hw.RGB{R: 255}); got != nil {
		t.Fatalf("too-small screen returned %d pixels", len(got))
	}
}

func TestSeg7ToPixelsBlank(t *testing.T) {
	px := Seg7ToPixels(hw.MatrixFrame{}, 21, 12, hw.RGB{R: 255, G: 255, B: 255})
	for i, c := range px {
		if c != (hw.RGB{}) {
			t.Fatalf("blank px %d = %+v", i, c)
		}
	}
}
