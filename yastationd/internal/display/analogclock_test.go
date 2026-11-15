package display

import (
	"strings"
	"testing"
	"time"
)

// levels renders a canvas as text: '#' a hand, '+' a main mark, '-' another mark, '.' dark.
func levels(c canvas) []string {
	var out []string
	for y := 0; y < c.h; y++ {
		var b strings.Builder
		for x := 0; x < c.w; x++ {
			switch v := c.v[y*c.w+x]; {
			case v > 0.9:
				b.WriteByte('#')
			case v > 0.3:
				b.WriteByte('+')
			case v > 0.1:
				b.WriteByte('-')
			default:
				b.WriteByte('.')
			}
		}
		out = append(out, b.String())
	}
	return out
}

// The pictures match the study's model pixel for pixel (docs/display-expressions.md, "The
// analog clock"): the dial is 21x11 with its centre on (10, 5), the last row dark.
func TestAnalogFace(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 30, 0, time.UTC) }
	for _, tc := range []struct {
		t    time.Time
		want []string
	}{
		{at(15, 0), []string{
			".-.....-..+..-.....-.",
			"..........#..........",
			"..........#..........",
			"..........#..........",
			".....................",
			"+.........#######...+",
			".....................",
			".....................",
			".....................",
			".....................",
			".-.....-..+..-.....-.",
			".....................",
		}},
		{at(10, 10), []string{
			".-.....-..+..-.....-.",
			"................##...",
			"......##.......##....",
			"......###............",
			"........##...........",
			"+........##.........+",
			".....................",
			".....................",
			".....................",
			".....................",
			".-.....-..+..-.....-.",
			".....................",
		}},
	} {
		c := newCanvas(21, 12)
		analogFace(c, tc.t)
		if got := levels(c); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n%s", tc.t.Format("15:04"), strings.Join(got, "\n"))
		}
	}
}

func TestClockAnalogFront(t *testing.T) {
	f := PixelFront{W: 21, H: 12, Gamma: 2.2}
	if !f.HasClockAnalog() || (Seg7Front{}).HasClockAnalog() {
		t.Fatal("offered where it should not be, or not where it should")
	}
	fr := f.ClockAnalog(time.Date(2026, 1, 1, 15, 0, 30, 0, time.UTC), time.UTC, 255)
	if len(fr.Px) != 21*12 || fr.Px[5*21+10].R != 255 {
		t.Fatalf("the hub is not lit at full brightness: %v", fr.Px[5*21+10])
	}
	// a main mark at 0.4 perceived: drive 255 * 0.4^2.2
	if got := fr.Px[5*21+0].R; got < 32 || got > 34 {
		t.Errorf("9 o'clock mark drive %d, want ~33", got)
	}
}
