package luxcal

import (
	"math"
	"testing"
)

func TestLux(t *testing.T) {
	c, err := Parse("6709:200, 306:10")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []struct{ r, want float64 }{{306, 10}, {6709, 200}, {0, 0}, {-1, 0}} {
		if got := Lux(c, k.r); math.Abs(got-k.want) > 1e-6 {
			t.Errorf("Lux(%v) = %v, want %v", k.r, got, k.want)
		}
	}
	// A power law: the geometric middle of the readings gives the geometric middle in lux, and it
	// extends past the ends.
	if got := Lux(c, math.Sqrt(306*6709)); math.Abs(got-math.Sqrt(2000)) > 1e-6 {
		t.Errorf("middle %v", got)
	}
	if got, want := Lux(c, 6709*4), 200*math.Pow(4, math.Log(20)/math.Log(6709.0/306)); math.Abs(got-want) > 1e-6 {
		t.Errorf("extrapolated %v want %v", got, want)
	}
	one, _ := Parse("2:30")
	if Lux(one, 4) != 60 || Lux(nil, 4) != 4 {
		t.Error("one point is a scale; none is the reading")
	}
	if _, err := Parse("1:10, 2:5"); err == nil {
		t.Error("falling points rejected")
	}
}
