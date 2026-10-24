package display

import "github.com/celebeing/yastationd/yastationd/internal/hw"

// uwu is the "UwU" glyph for orion's 21x12 front dot-matrix, drawn by hand: U = a
// rounded 5-px cup, w = two teeth separated by a blank column (reads as w, not u),
// 1-px letter gaps, vertically centered (rows 4..9). '.' = off, '#' = on.
// Column map: U at 1..5, w teeth at 8..12, U at 16..20.
var uwu = [12]string{
	".....................",
	".....................",
	".....................",
	".....................",
	".#...#..#.#.#...#...#",
	".#...#..#.#.#...#...#",
	".#...#..#.#.#...#...#",
	".#...#..#.#.#...#...#",
	"..#.#....#.#.....#.#.",
	"..###....###.....###.",
	".....................",
	".....................",
}

// UwuFrame renders the glyph full-screen (w x h px, top row first) in colour c. The glyph
// is a 21x12 mask, so a smaller screen keeps its top-left corner (clipped face, no crash)
// and a larger one leaves the rest black. h > 12 is centered by the caller if desired.
func UwuFrame(w, h int, c hw.RGB) []hw.RGB {
	f := make([]hw.RGB, w*h)
	for y := 0; y < h && y < len(uwu); y++ {
		for x := 0; x < w && x < len(uwu[y]); x++ {
			if uwu[y][x] != '.' {
				f[y*w+x] = c
			}
		}
	}
	return f
}
