// The minis' 4-digit 7-segment clock, drawn after a photo of a Mini 3 with every element lit:
// thick segments with bevelled ends, a colon with a dash (DM) between its dots, set below the
// middle, and the degree mark at the top right. Units: a digit is 100 high. The element order is
// the frame tap's (hw.MatrixFrame.Drive): digits 0..3 × segments A..G, colon, DM, degree.
// yastationd/cmd/demoreel draws the same shapes.

export type Seg7Shape = { el: number; poly?: [number, number][]; circle?: [number, number, number] }

const T = 6.5 // half the stroke
const GAP = 2
const W = 54
export const SEG7_DIGIT_X = [0, 62, 166, 228]
export const SEG7_BOX = { x0: -2, y0: -9, x1: 303, y1: 102 }

const hbar = (x0: number, x1: number, y: number, t = T): [number, number][] => [
  [x0, y], [x0 + t, y - t], [x1 - t, y - t], [x1, y], [x1 - t, y + t], [x0 + t, y + t],
]
const vbar = (x: number, y0: number, y1: number): [number, number][] => [
  [x, y0], [x + T, y0 + T], [x + T, y1 - T], [x, y1], [x - T, y1 - T], [x - T, y0 + T],
]

export function seg7Shapes(colon = true, dot = true, degree = true): Seg7Shape[] {
  const out: Seg7Shape[] = []
  SEG7_DIGIT_X.forEach((ox, d) => {
    const L = ox + T, R = ox + W - T
    const segs: [number, number][][] = [
      hbar(L + GAP, R - GAP, T), // A
      vbar(R, T + GAP, 50 - GAP), // B
      vbar(R, 50 + GAP, 100 - T - GAP), // C
      hbar(L + GAP, R - GAP, 100 - T), // D
      vbar(L, 50 + GAP, 100 - T - GAP), // E
      vbar(L, T + GAP, 50 - GAP), // F
      hbar(L + GAP, R - GAP, 50), // G
    ]
    segs.forEach((poly, s) => out.push({ el: d * 7 + s, poly }))
  })
  if (colon) {
    out.push({ el: 28, circle: [141, 37, 8] })
    out.push({ el: 28, circle: [141, 82, 8] })
  }
  if (dot) out.push({ el: 29, poly: hbar(127, 155, 60, 5.5) })
  if (degree) out.push({ el: 30, circle: [294, 0, 6.5] })
  return out
}
