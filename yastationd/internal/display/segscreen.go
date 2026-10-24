package display

import "github.com/celebeing/yastationd/yastationd/internal/hw"

// MatrixFrame is a board-independent 4-digit 7-seg picture. On orion the same frame is
// rendered as pixels: every source glyph row is doubled vertically, filling 10 of the
// panel's 12 rows while preserving the compact 3-px digits needed to fit four digits,
// gaps and colon across 21 columns. Text/marquee/countdown therefore reuse the existing
// stack unchanged.
const (
	screenFontW  = 3 // digit cell width
	screenFontH  = 5 // source glyph rows
	screenScaleY = 2 // fill 10 of orion's 12 physical rows
)

// segBlocks maps a 7-seg frame to pixel blocks: every segment becomes a 2x3 block of lit
// pixels inside its cell (A top, G middle, D bottom as full rows; B/C, F/E as side
// columns), giving solid, readable digits rather than wireframes.
var segBlocks = [7][screenFontH][screenFontW]bool{}

func init() {
	// Row 0 = A, row 4 = D, row 2 = G; verticals occupy the two rows above/below G.
	for x := range screenFontW {
		segBlocks[0][0][x] = true
		segBlocks[3][4][x] = true
		segBlocks[6][2][x] = true
	}
	segBlocks[1][1][screenFontW-1] = true // B right, upper
	segBlocks[1][2][screenFontW-1] = true
	segBlocks[2][3][screenFontW-1] = true // C right, lower
	segBlocks[2][4][screenFontW-1] = true
	segBlocks[4][3][0] = true // E left, lower
	segBlocks[4][4][0] = true
	segBlocks[5][1][0] = true // F left, upper
	segBlocks[5][2][0] = true
}

// Seg7ToPixels renders a 7-seg MatrixFrame onto a w x h pixel screen (top row first).
// Brightness scales the colour; 0 brightness shows black. Returns nil when the screen
// cannot hold four digits (caller must not write a clipped status display).
func Seg7ToPixels(f hw.MatrixFrame, w, h int, c hw.RGB) []hw.RGB {
	if w < 4*screenFontW+3 || h < screenFontH*screenScaleY {
		return nil
	}
	k := f.Brightness
	if k < 0 {
		k = 0
	}
	px := make([]hw.RGB, w*h)
	col := c.Scale(k)
	// Digit cells: x0 + i*(cellW+1), 3x10 px each and vertically centered.
	x0 := (w - (4*screenFontW + 3)) / 2
	y0 := (h - screenFontH*screenScaleY) / 2
	for d := 0; d < hw.Digits; d++ {
		cx, cy := x0+d*(screenFontW+1), y0
		for s := 0; s < 7; s++ {
			if f.Seg[d]&(1<<s) == 0 {
				continue
			}
			for yy := 0; yy < screenFontH; yy++ {
				for xx := 0; xx < screenFontW; xx++ {
					if segBlocks[s][yy][xx] {
						for sy := range screenScaleY {
							px[(cy+yy*screenScaleY+sy)*w+cx+xx] = col
						}
					}
				}
			}
		}
	}
	// Colon: two separated 2px-tall dots in the gap between digits 1 and 2.
	gapx := x0 + screenFontW + 1 + screenFontW
	if f.Colon {
		for _, yy := range []int{2, 6} {
			for sy := range screenScaleY {
				px[(y0+yy+sy)*w+gapx] = col
			}
		}
	}
	// Dot (DM) and degree occupy the last physical row when the panel has one to spare.
	auxY := y0 + screenFontH*screenScaleY
	if auxY < h && f.DM {
		px[auxY*w+x0+3*(screenFontW+1)+screenFontW-1] = col
	}
	if auxY < h && f.Degree {
		px[auxY*w+x0+1*(screenFontW+1)+screenFontW-1] = col
	}
	return px
}
