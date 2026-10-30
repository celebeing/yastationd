import { SEG7_BOX, seg7Shapes } from './seg7'

// The front display's frame as the LED preview stream carries it (internal/frametap: drive
// values, hex) and the one painter of it: the LED page draws it under its glass, the Packs
// page's animation previews draw it plain.

/** FrontInfo: the front display, as the stream's /info describes it. */
export type FrontInfo = {
  screen: [number, number] // w, h; 0, 0 = no pixel screen
  seg7?: { digits: number; colon: boolean; dot: boolean; degree: boolean }
}
/** FrontFrame: one frame of it: RGB per pixel, or a level per seg7 element (hw.MatrixFrame.Drive order). */
export type FrontFrame = { screen?: string; seg7?: string }

// Drive values are linear light; a monitor wants gamma-encoded ones. A lit LED also starts at a
// visible level (LIT_FLOOR): on the glass even a drive of 1 is a dot you can see, while 1/2.2
// alone draws it darker than an unlit dot.
const LIT_FLOOR = 48
export const LUT = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + (255 - LIT_FLOOR) * Math.pow(i / 255, 1 / 2.2))))
export const LIN = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + ((255 - LIT_FLOOR) * i) / 255)))
// An unlit LED and an unlit clock segment: not black, so the off ones read on the glass.
export const UNLIT = '#0b0c0e'
export const UNLIT_SEG7 = '#16171a'
export const rgbCss = (R: number, G: number, B: number) => (R | G | B ? `rgb(${R},${G},${B})` : UNLIT)
export const unhex = (s: string) => {
  const out = new Uint8Array(s.length / 2)
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.substr(i * 2, 2), 16)
  return out
}

/** frontSize: the painted front display's size at P px per screen pixel, S7 px per seg7 unit. */
export function frontSize(i: FrontInfo, P: number, S7: number): [number, number] {
  if (i.seg7) return [(SEG7_BOX.x1 - SEG7_BOX.x0) * S7, (SEG7_BOX.y1 - SEG7_BOX.y0) * S7]
  const [w, h] = i.screen || [0, 0]
  return [w * P, h * P]
}

/** paintFront paints frame f of the front display at (ox, oy): Station 3's screen as square cells
 * (its LEDs touch each other), a seg7 display as its segments; show maps a drive value to a
 * monitor's (LUT: as the eye sees the LEDs). Returns whether it painted anything. */
export function paintFront(ctx: CanvasRenderingContext2D, i: FrontInfo, f: FrontFrame, show: Uint8Array, ox: number, oy: number, P: number, S7: number) {
  const [w, h] = i.screen || [0, 0]
  if (f.screen && w) {
    const px = unhex(f.screen)
    for (let y = 0; y < h; y++)
      for (let x = 0; x < w; x++) {
        const k = (y * w + x) * 3
        ctx.fillStyle = rgbCss(show[px[k]], show[px[k + 1]], show[px[k + 2]])
        ctx.fillRect(ox + x * P, oy + y * P, P, P)
      }
    return true
  }
  if (f.seg7 && i.seg7) {
    const lv = unhex(f.seg7)
    const x0 = ox - SEG7_BOX.x0 * S7,
      y0 = oy - SEG7_BOX.y0 * S7
    for (const sh of seg7Shapes(i.seg7.colon, i.seg7.dot, i.seg7.degree)) {
      const v = show[lv[sh.el]]
      ctx.fillStyle = v ? `rgb(${v},${v},${v})` : UNLIT_SEG7
      ctx.beginPath()
      if (sh.circle) ctx.arc(x0 + sh.circle[0] * S7, y0 + sh.circle[1] * S7, sh.circle[2] * S7, 0, 2 * Math.PI)
      else sh.poly!.forEach(([x, y], k) => (k ? ctx.lineTo : ctx.moveTo).call(ctx, x0 + x * S7, y0 + y * S7))
      ctx.fill()
    }
    return true
  }
  return false
}
