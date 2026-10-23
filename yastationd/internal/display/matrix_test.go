package display

import (
	"testing"
	"time"
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
