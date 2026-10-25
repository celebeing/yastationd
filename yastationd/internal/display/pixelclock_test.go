package display

import (
	"strings"
	"testing"
	"time"
)

// rows renders a frame as text: '#' lit, '.' dark.
func rows(f FrontFrame, w int) []string {
	var out []string
	for y := 0; y*w < len(f.Px); y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			if f.Px[y*w+x].R > 0 {
				b.WriteByte('#')
			} else {
				b.WriteByte('.')
			}
		}
		out = append(out, b.String())
	}
	return out
}

func TestPixelClock(t *testing.T) {
	f := PixelFront{W: 21, H: 12, Gamma: 2.2}
	at := func(h, m, s int) time.Time { return time.Date(2026, 10, 26, h, m, s, 0, time.UTC) }

	r := rows(f.Clock(at(12, 34, 30), time.UTC, 255), 21)
	if r[0] != strings.Repeat(".", 21) || r[1] != "...#.####...####.#..#" {
		t.Errorf("12:34 top rows:\n%s", strings.Join(r, "\n"))
	}
	// no leading zero: "8:59" is 16 columns, centred (2 columns left of it)
	r = rows(f.Clock(at(8, 59, 30), time.UTC, 255), 21)
	if !strings.HasPrefix(r[1], "..####.") {
		t.Errorf("8:59 not centred:\n%s", strings.Join(r, "\n"))
	}
	// full brightness, full level: drive 255
	px := f.Clock(at(12, 34, 30), time.UTC, 255).Px
	if px[1*21+3].R != 255 {
		t.Errorf("lit pixel = %d", px[1*21+3].R)
	}
	// the cross-fade: mid-way, a pixel lit only in the old minute is half (perceived) -> gamma
	mid := f.Clock(at(12, 35, 0).Add(clockFade/2), time.UTC, 255).Px
	old := f.Clock(at(12, 34, 30), time.UTC, 255).Px
	now := f.Clock(at(12, 35, 30), time.UTC, 255).Px
	found := false
	for i := range mid {
		if old[i].R == 255 && now[i].R == 0 {
			if mid[i].R < 40 || mid[i].R > 70 { // 255 * 0.5^2.2 = 55
				t.Errorf("fading-out pixel %d = %d", i, mid[i].R)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no pixel differs between 12:34 and 12:35")
	}
}

func TestPixelClockDateAndUnsynced(t *testing.T) {
	f := PixelFront{W: 21, H: 12, Gamma: 1}
	r := rows(f.ClockDate(time.Date(2026, 10, 26, 9, 5, 30, 0, time.UTC), time.UTC, 255), 21)
	lit := func(lines []string) bool { return strings.Contains(strings.Join(lines, ""), "#") }
	if !lit(r[:6]) || !lit(r[6:]) {
		t.Errorf("clock_date needs a time line and a date line:\n%s", strings.Join(r, "\n"))
	}
	dim := f.ClockDate(time.Date(2026, 10, 26, 9, 5, 30, 0, time.UTC), time.UTC, 255).Px
	for _, p := range dim[7*21:] {
		if p.R > 0 && p.R != 115 { // 255 * dateLevel, gamma 1
			t.Fatalf("date pixel = %d, want the dimmer date level", p.R)
		}
	}
	on := rows(f.Unsynced(time.Date(2026, 1, 1, 0, 0, 2, 0, time.UTC), 255), 21)
	off := rows(f.Unsynced(time.Date(2026, 1, 1, 0, 0, 3, 0, time.UTC), 255), 21)
	if strings.Count(strings.Join(on, ""), "#") != 2 || lit(off) {
		t.Errorf("unsynced colon:\n%s\n--\n%s", strings.Join(on, "\n"), strings.Join(off, "\n"))
	}
}
