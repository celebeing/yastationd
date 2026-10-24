package display

import (
	"testing"
	"time"

	"github.com/celebeing/yastationd/yastationd/internal/hw"
)

func TestTextDigitsColonDot(t *testing.T) {
	f := Text("12:34", 128)
	if f.Seg[0] != glyphs['1'] || f.Seg[1] != glyphs['2'] || f.Seg[2] != glyphs['3'] || f.Seg[3] != glyphs['4'] {
		t.Fatal("digit segments wrong")
	}
	if !f.Colon {
		t.Fatal("colon should be on")
	}
	if f.Brightness != 128 {
		t.Fatalf("brightness: %d", f.Brightness)
	}
}

func TestTextOverflowCut(t *testing.T) {
	f := Text("12345", 10) // only 4 digit cells
	if f.Seg[3] != glyphs['4'] {
		t.Fatal("4th cell should be '4'")
	}
}

func TestNumberRightAlign(t *testing.T) {
	f := Number(7, 10)
	if f.Seg[0] != 0 || f.Seg[1] != 0 || f.Seg[2] != 0 || f.Seg[3] != glyphs['7'] {
		t.Fatal("7 should be right-aligned")
	}
	if Number(12345, 10).Seg[0] != glyphs['-'] {
		t.Fatal("overflow should render dashes")
	}
}

func TestClockFormats(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	tm := time.Date(2026, 10, 22, 9, 5, 0, 0, loc)
	f := Clock(tm, loc, 50)
	if f.Seg[0] != glyphs['0'] || f.Seg[1] != glyphs['9'] || f.Seg[2] != glyphs['0'] || f.Seg[3] != glyphs['5'] {
		t.Fatal("09:05 render wrong")
	}
	if !f.Colon {
		t.Fatal("clock colon on")
	}
}

func TestCountdownCountsDownAndFinishes(t *testing.T) {
	now := time.Now()
	r := Countdown(now.Add(90*time.Second), func() int { return 10 })
	f, done := r(now, MatrixFrame{})
	if done {
		t.Fatal("should not be done yet")
	}
	// 90 s -> "01:30": positions 0='0',1='1',2='3',3='0'
	if f.Seg[0] != glyphs['0'] || f.Seg[1] != glyphs['1'] || f.Seg[2] != glyphs['3'] || f.Seg[3] != glyphs['0'] {
		t.Fatalf("01:30 expected")
	}
	if _, done := r(now.Add(91*time.Second), MatrixFrame{}); !done {
		t.Fatal("should finish at zero")
	}
}

func TestMarqueeFitsStatic(t *testing.T) {
	r := Marquee("12", 100*time.Millisecond, func() int { return 5 })
	f, _ := r(time.Now(), MatrixFrame{})
	if f.Seg[0] != glyphs['1'] {
		t.Fatal("short text should render statically")
	}
}

func TestCyrillicFoldsCase(t *testing.T) {
	up, lo := Text("ПРИВ", 1), Text("прив", 1)
	if up.Seg != lo.Seg || up.Seg[0] == 0 || up.Seg[3] == 0 {
		t.Fatalf("ПРИВ = %v, прив = %v", up.Seg, lo.Seg)
	}
	if Text("Ж", 1).Seg[0] != 0 {
		t.Fatal("Ж should be blank")
	}
}

func TestMarqueeScrollsLongText(t *testing.T) {
	r := Marquee("123456", 100*time.Millisecond, func() int { return 1 })
	now := time.Now()
	f0, _ := r(now, MatrixFrame{})
	f4, _ := r(now.Add(450*time.Millisecond), MatrixFrame{})
	if f0.Seg == f4.Seg || f4.Seg != Text("1234", 1).Seg {
		t.Fatalf("frame@0 %v, frame@4 steps %v", f0.Seg, f4.Seg)
	}
}

func TestSeg7SmootherJoinsAndMorphs(t *testing.T) {
	s := Seg7Smoother{Up: 80 * time.Millisecond, Down: 400 * time.Millisecond, Morph: 120 * time.Millisecond}
	now := time.Unix(100, 0)
	// "eyes" then "wink" as two layers of one slot: segment A0 is lit in both.
	a := MatrixFrame{Brightness: 255, Graded: true}
	a.Level.Seg[0][0], a.Level.Seg[3][0] = 255, 255
	b := MatrixFrame{Brightness: 255, Graded: true}
	b.Level.Seg[0][0], b.Level.Seg[3][6] = 255, 255
	s.Frame(now, a, Top{Name: "scenario", Prio: PrioExpression, Seq: 1})
	for i := 1; i <= 3; i++ { // the step change: a replaced layer in the same slot
		f := s.Frame(now.Add(time.Duration(i)*33*time.Millisecond), b, Top{Name: "scenario", Prio: PrioExpression, Seq: 2})
		if f.Level.Seg[0][0] != 255 {
			t.Fatalf("shared segment dipped to %d at step %d", f.Level.Seg[0][0], i)
		}
		if i == 1 && (f.Level.Seg[3][6] == 0 || f.Level.Seg[3][6] == 255) {
			t.Fatalf("new segment did not cross-fade: %d", f.Level.Seg[3][6])
		}
	}
	// A plain digit change within one layer morphs instead of snapping.
	top := Top{Name: "volbubble", Prio: PrioLocal, Seq: 3}
	s.Frame(now.Add(time.Second), Number(40, 255), top)
	s.Frame(now.Add(2*time.Second), Number(40, 255), top)
	f := s.Frame(now.Add(2*time.Second+33*time.Millisecond), Number(45, 255), top)
	changed := false
	for d := 0; d < hw.Digits; d++ {
		for e := 0; e < 7; e++ {
			if v := f.Level.Seg[d][e]; v != 0 && v != 255 {
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("digits snapped instead of morphing")
	}
}
