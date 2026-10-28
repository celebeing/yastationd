import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Net, Peer } from './api'
import { SEG7_BOX, seg7Shapes } from './seg7'

// The optional tabs: Wi-Fi, moments, the LED preview (docs/web-ui.md).

export function WifiTab({ peer, base }: { peer: Peer; base: string }) {
  const [nets, setNets] = useState<Net[] | null>(null)
  const [cur, setCur] = useState('')
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [ssid, setSsid] = useState('')
  const [psk, setPsk] = useState('')
  const [hidden, setHidden] = useState(false)
  const [prio, setPrio] = useState(0)

  async function load() {
    try {
      const w = await api.wifi(peer.id, base)
      setNets(w.networks)
      setCur(w.current)
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  useEffect(() => {
    load()
  }, [peer.id])

  async function add(e: Event) {
    e.preventDefault()
    setErr('')
    setMsg('')
    try {
      await api.wifiAdd(peer.id, base, { ssid, psk, hidden, priority: prio })
      setMsg(`“${ssid}” saved. It is used when the current network drops or after a reboot; the connection in use is not touched.`)
      setSsid('')
      setPsk('')
      load()
    } catch (x: any) {
      setErr(x.message || String(x))
    }
  }

  return (
    <div class="cols">
      <section class="card">
        <h3>Saved networks</h3>
        {!nets && !err && <p class="muted">…</p>}
        {nets && (
          <table class="nets">
            <thead>
              <tr>
                <th>Network</th>
                <th>Security</th>
                <th>Priority</th>
              </tr>
            </thead>
            <tbody>
              {nets.map((n) => (
                <tr>
                  <td>
                    {n.ssid} {n.ssid === cur && <span class="badge ok-badge">connected</span>}
                    {n.disabled && <span class="muted small"> (disabled)</span>}
                    {n.hidden && <span class="muted small"> (hidden)</span>}
                  </td>
                  <td>{n.security}</td>
                  <td>{n.priority}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p class="muted small">
          Changing or removing a network is done in setup mode (hold the voice key 5 s with the mic muted): there losing the connection is expected.
        </p>
      </section>
      <form class="card login" onSubmit={add}>
        <h3>Add a network</h3>
        <label>
          Name (SSID)
          <input value={ssid} onInput={(e) => setSsid(e.currentTarget.value)} required />
        </label>
        <label>
          Password (empty for an open network)
          <input type="password" value={psk} onInput={(e) => setPsk(e.currentTarget.value)} autocomplete="new-password" />
        </label>
        <label>
          Priority
          <input type="number" value={prio} onInput={(e) => setPrio(Number(e.currentTarget.value))} />
        </label>
        <label class="inline">
          <input type="checkbox" checked={hidden} onChange={(e) => setHidden(e.currentTarget.checked)} /> Hidden network
        </label>
        {err && <p class="err">{err}</p>}
        {msg && <p class="ok">{msg}</p>}
        <button type="submit">Add</button>
      </form>
    </div>
  )
}

type TapInfo = {
  name: string
  board: string
  screen: [number, number]
  seg7?: { digits: number; colon: boolean; dot: boolean; degree: boolean }
  ring: number
  ambilight: number
  expressions?: string[]
  ring_effects?: string[] | null
  patterns?: string[]
  commands?: string[]
}
type Frame = { seq: number; screen?: string; seg7?: string; ring?: string; ambilight?: string }

// Drive values are linear light; a monitor wants gamma-encoded ones. A lit LED also starts at a
// visible level (LIT_FLOOR): on the glass even a drive of 1 is a dot you can see, while 1/2.2
// alone draws it darker than an unlit dot.
const LIT_FLOOR = 48
const LUT = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + (255 - LIT_FLOOR) * Math.pow(i / 255, 1 / 2.2))))
const LIN = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + ((255 - LIT_FLOOR) * i) / 255)))
// An unlit LED and an unlit clock segment: not black, so the off ones read on the glass.
const UNLIT = '#0b0c0e'
const UNLIT_SEG7 = '#16171a'
const rgbCss = (R: number, G: number, B: number) => (R | G | B ? `rgb(${R},${G},${B})` : UNLIT)
const unhex = (s: string) => {
  const out = new Uint8Array(s.length / 2)
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.substr(i * 2, 2), 16)
  return out
}

export function LedTab({ peer, base }: { peer: Peer; base: string }) {
  const cv = useRef<HTMLCanvasElement>(null)
  const [info, setInfo] = useState<TapInfo | null>(null)
  const [err, setErr] = useState('')
  const [fps, setFps] = useState(0)
  const [perceived, setPerceived] = useState(true)
  const pRef = useRef(true)
  pRef.current = perceived

  useEffect(() => {
    let es: EventSource | null = null
    let closed = false
    fetch(api.tapURL(peer.id, base, '/info'))
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status === 404 ? 'LED preview is off on this speaker (led_preview = true)' : r.statusText))))
      .then((i: TapInfo) => {
        if (closed) return
        setInfo(i)
        es = new EventSource(api.tapURL(peer.id, base, '/frames'))
        let n = 0,
          t0 = performance.now()
        es.onmessage = (ev) => {
          draw(cv.current, i, JSON.parse(ev.data), pRef.current)
          n++
          const t = performance.now()
          if (t - t0 > 1000) {
            setFps(Math.round((n * 1000) / (t - t0)))
            n = 0
            t0 = t
          }
        }
        es.onerror = () => setErr('stream interrupted, retrying…')
      })
      .catch((e) => setErr(e.message || String(e)))
    return () => {
      closed = true
      es?.close()
    }
  }, [peer.id])

  return (
    <section class="card">
      <h3>
        LED preview <span class="muted small">{info ? `${fps} fps` : ''}</span>
      </h3>
      {err && <p class="err">{err}</p>}
      <canvas ref={cv} class="led" />
      <label class="inline small">
        <input type="checkbox" checked={perceived} onChange={(e) => setPerceived(e.currentTarget.checked)} /> perceived brightness (undo the LEDs' gamma)
      </label>
      {info && <Playground peer={peer} base={base} info={info} onErr={setErr} />}
      <DDPInfo base={base} />
    </section>
  )
}

/** DDPInfo: what a DDP sender (LedFx) must be told by hand — DDP has no discovery. */
function DDPInfo({ base }: { base: string }) {
  const [d, setD] = useState<api.DDP | null>(null)
  useEffect(() => {
    api.infoFull(base).then((i) => setD(i.ddp || null), () => setD(null))
  }, [base])
  if (!d) return null
  const host = base ? new URL(base).hostname : location.hostname
  return (
    <p class="small muted">
      DDP receiver for LedFx / WLED tools (type DDP, not WLED): <code>{host}</code> port <code>{d.port}</code>,{' '}
      <code>{d.pixels}</code> pixels, {d.order} (RGBW accepted), pixel 0 at the {d.first}. A stream takes the ambilight
      over as sent; 2.5 s after it stops the light is back.
    </p>
  )
}

/** Playground: play any of the speaker's screen animations and expressions, as the old frame-tap
 * viewer did (the tap's /cmd/ endpoint, behind the same login). */
function Playground({ peer, base, info, onErr }: { peer: Peer; base: string; info: TapInfo; onErr: (s: string) => void }) {
  const [filter, setFilter] = useState('')
  const [pattern, setPattern] = useState('')
  const [loop, setLoop] = useState(false)
  const has = (c: string) => (info.commands || []).includes(c)
  const patterns = (info.patterns || []).filter((n) => n.toLowerCase().includes(filter.toLowerCase()))
  const cur = patterns.includes(pattern) ? pattern : patterns[0] || ''

  async function cmd(name: string, payload: unknown) {
    onErr('')
    try {
      // the body is the MQTT payload: JSON for most commands, the plain value for a select's
      const body = typeof payload === 'string' ? payload : JSON.stringify(payload)
      const r = await fetch(api.tapURL(peer.id, base, '/cmd/' + name), { method: 'POST', body })
      if (!r.ok) onErr(`${name}: ${(await r.text()) || r.statusText}`)
    } catch (e: any) {
      onErr(`${name}: ${e.message || e}`)
    }
  }

  return (
    <>
      {has('display') && (info.patterns || []).length > 0 && (
        <div class="row">
          <span class="k">Animation</span>
          <input type="text" aria-label="Filter the animations" placeholder={`filter ${info.patterns!.length}`} value={filter} onInput={(e) => setFilter(e.currentTarget.value)} />
          <select aria-label="Animation" value={cur} onChange={(e) => setPattern(e.currentTarget.value)}>
            {patterns.map((n) => (
              <option value={n}>{n}</option>
            ))}
          </select>
          <label class="inline small">
            <input type="checkbox" checked={loop} onChange={(e) => setLoop(e.currentTarget.checked)} /> loop
          </label>
          <button disabled={!cur} onClick={() => cmd('display', { surface: 'matrix', op: 'clip', args: { name: cur, loop }, ttl_ms: loop ? 60000 : 0 })}>
            Play
          </button>
          <button onClick={() => cmd('display', { surface: 'matrix', op: 'clear' })}>Clear</button>
        </div>
      )}
      {has('ring_animation') && info.ring > 0 && (info.ring_effects || []).length > 0 && (
        <div class="row">
          <span class="k">Ring</span>
          {info.ring_effects!
            .filter((n) => n !== 'none')
            .map((n) => (
              <button onClick={() => cmd('ring_animation', n)}>{n}</button>
            ))}
          <button onClick={() => cmd('ring_animation', 'off')}>Stop</button>
        </div>
      )}
      {has('express') && (info.expressions || []).length > 0 && (
        <div class="row">
          <span class="k">Expression</span>
          {['off', ...info.expressions!.filter((n) => n !== 'off')].map((n) => (
            <button onClick={() => cmd('express', { name: n, ttl_ms: 15000 })}>{n}</button>
          ))}
        </div>
      )}
    </>
  )
}

// The glass: room around the front display for its glow, a reused canvas for it, and the fabric's
// weave as a tile (a fine square weave turned 45° on Station 3, diagonal stripes on the minis).
const GLASS_PAD = 24
let front: HTMLCanvasElement | null = null
function frontCanvas(w: number, h: number) {
  front ??= document.createElement('canvas')
  if (front.width !== Math.ceil(w) || front.height !== Math.ceil(h)) {
    front.width = Math.ceil(w)
    front.height = Math.ceil(h)
  }
  return front
}
const tiles: Record<string, HTMLCanvasElement> = {}
function weaveTile(kind: 'grid' | 'stripes') {
  if (tiles[kind]) return tiles[kind]
  const n = 8,
    period = 4
  const t = document.createElement('canvas')
  t.width = t.height = n
  const c = t.getContext('2d')!
  const img = c.createImageData(n, n)
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++) {
      const a = 0.5 + 0.5 * Math.cos((2 * Math.PI * (x + y)) / period),
        b = 0.5 + 0.5 * Math.cos((2 * Math.PI * (x - y)) / period)
      const hole = kind === 'stripes' ? (1 - a) ** 2 : ((1 - a) * (1 - b)) ** 0.7
      const v = Math.round(255 * (1 - 0.5 * hole)),
        o = (y * n + x) * 4
      img.data[o] = img.data[o + 1] = img.data[o + 2] = v
      img.data[o + 3] = 255
    }
  c.putImageData(img, 0, 0)
  return (tiles[kind] = t)
}

/** drawRing draws a ring as the board's LEDs look: a belt round the body, blended from pixel to
 * pixel — a circle on the round pickle (Mini 3 Pro), a square on the box-shaped orion (Station 3,
 * drawn bigger) — or bergamot's small disc of sectors (Mini 3: a pie, half the size); others as
 * dots. Index 0 at the top, clockwise, as on the units. col(k) is pixel k's shown RGB. */
function drawRing(ctx: CanvasRenderingContext2D, board: string, n: number, col: (k: number) => number[], cx: number, cy: number, rr: number, dotR: number) {
  const ang = (k: number) => -Math.PI / 2 + (2 * Math.PI * k) / n
  const css = (c: number[]) => rgbCss(c[0], c[1], c[2])
  const half = Math.PI / n
  const belt = (radius: number, square: boolean, width: number) => {
    const M = 360
    const at = (t: number) => {
      const c = Math.cos(t),
        s = Math.sin(t),
        m = square ? Math.max(Math.abs(c), Math.abs(s)) : 1
      return [cx + (radius * c) / m, cy + (radius * s) / m]
    }
    ctx.lineWidth = width
    ctx.lineCap = 'square'
    for (let j = 0; j < M; j++) {
      const t0 = -Math.PI / 2 + (2 * Math.PI * j) / M
      const u = (j / M) * n,
        k = Math.floor(u) % n,
        fr = u - Math.floor(u)
      const a = col(k),
        b = col((k + 1) % n)
      ctx.strokeStyle = css(a.map((v, q) => Math.round(v + (b[q] - v) * fr)))
      const [x0, y0] = at(t0),
        [x1, y1] = at(t0 + (2 * Math.PI) / M)
      ctx.beginPath()
      ctx.moveTo(x0, y0)
      ctx.lineTo(x1, y1)
      ctx.stroke()
    }
  }
  if (board === 'pickle') belt(rr, false, dotR * 0.9)
  else if (board === 'orion') belt(rr * 0.9, true, dotR * 1.4)
  else if (board === 'bergamot') {
    const r = rr / 2
    for (let k = 0; k < n; k++) {
      ctx.fillStyle = css(col(k))
      ctx.beginPath()
      ctx.moveTo(cx, cy)
      ctx.arc(cx, cy, r, ang(k) - half + 0.05, ang(k) + half - 0.05)
      ctx.closePath()
      ctx.fill()
    }
  } else {
    for (let k = 0; k < n; k++) {
      ctx.fillStyle = css(col(k))
      ctx.beginPath()
      ctx.arc(cx + rr * Math.cos(ang(k)), cy + rr * Math.sin(ang(k)), dotR, 0, 2 * Math.PI)
      ctx.fill()
    }
  }
}

function draw(c: HTMLCanvasElement | null, i: TapInfo, f: Frame, perceived: boolean) {
  if (!c) return
  const ctx = c.getContext('2d')!
  const show = (v: number) => (perceived ? LUT[v] : LIN[v])
  const [w, h] = i.screen || [0, 0]
  const P = 16
  const S7 = 1.3 // px per seg7 unit (a digit is 100 high)
  const sw = i.seg7 ? (SEG7_BOX.x1 - SEG7_BOX.x0) * S7 : w * P,
    sh = i.seg7 ? (SEG7_BOX.y1 - SEG7_BOX.y0) * S7 : h * P
  const rr = Math.max(60, sh / 2)
  const rx = (sw ? 20 + sw + 36 : 20) + rr
  const height = Math.max(sh, 2 * rr) + 40
  const ry = height / 2
  const ax = rx + rr + 40
  const width = i.ambilight ? ax + 30 : rx + rr + 20
  if (c.width !== width || c.height !== height) {
    c.width = width
    c.height = height
  }
  ctx.fillStyle = '#000'
  ctx.fillRect(0, 0, width, height)
  const dot = (x: number, y: number, r: number, R: number, G: number, B: number) => {
    ctx.fillStyle = rgbCss(R, G, B)
    ctx.beginPath()
    ctx.arc(x, y, r, 0, 2 * Math.PI)
    ctx.fill()
  }
  // The front display is drawn on its own canvas, then put on the glass: its light diffused (a soft
  // edge and a wide glow) and seen through the fabric's weave, as in cmd/demoreel.
  const fx = 20,
    fy = (height - sh) / 2
  const fc = frontCanvas(sw + 2 * GLASS_PAD, sh + 2 * GLASS_PAD)
  const fctx = fc.getContext('2d')!
  fctx.fillStyle = '#000'
  fctx.fillRect(0, 0, fc.width, fc.height)
  let edge = 0,
    halo = 0,
    weave: 'grid' | 'stripes' = 'grid'
  if (f.screen && w) {
    // Station 3's screen LEDs are square and touch each other: cells, not dots.
    const px = unhex(f.screen)
    for (let y = 0; y < h; y++)
      for (let x = 0; x < w; x++) {
        const k = (y * w + x) * 3
        const R = show(px[k]),
          G = show(px[k + 1]),
          B = show(px[k + 2])
        fctx.fillStyle = rgbCss(R, G, B)
        fctx.fillRect(GLASS_PAD + x * P, GLASS_PAD + y * P, P, P)
      }
    ;[edge, halo] = [P * 0.12, P * 0.7]
  }
  if (f.seg7 && i.seg7) {
    const lv = unhex(f.seg7)
    const ox = GLASS_PAD - SEG7_BOX.x0 * S7,
      oy = GLASS_PAD - SEG7_BOX.y0 * S7
    for (const sh7 of seg7Shapes(i.seg7.colon, i.seg7.dot, i.seg7.degree)) {
      const v = show(lv[sh7.el])
      fctx.fillStyle = v ? `rgb(${v},${v},${v})` : UNLIT_SEG7
      fctx.beginPath()
      if (sh7.circle) fctx.arc(ox + sh7.circle[0] * S7, oy + sh7.circle[1] * S7, sh7.circle[2] * S7, 0, 2 * Math.PI)
      else sh7.poly!.forEach(([x, y], k) => (k ? fctx.lineTo : fctx.moveTo).call(fctx, ox + x * S7, oy + y * S7))
      fctx.fill()
    }
    ;[edge, halo, weave] = [S7 * 1.2, S7 * 9, 'stripes']
  }
  if (sw) {
    const gx = fx - GLASS_PAD,
      gy = fy - GLASS_PAD
    ctx.save()
    ctx.filter = `blur(${edge}px)`
    ctx.drawImage(fc, gx, gy)
    ctx.globalCompositeOperation = 'lighter'
    ctx.globalAlpha = 0.3
    ctx.filter = `blur(${halo}px)`
    ctx.drawImage(fc, gx, gy)
    ctx.globalAlpha = 1
    ctx.filter = 'none'
    ctx.globalCompositeOperation = 'multiply'
    ctx.fillStyle = ctx.createPattern(weaveTile(weave), 'repeat')!
    ctx.fillRect(gx, gy, fc.width, fc.height)
    ctx.restore()
  }
  if (f.ring && i.ring) {
    const px = unhex(f.ring)
    const col = (k: number) => [show(px[k * 3]), show(px[k * 3 + 1]), show(px[k * 3 + 2])]
    drawRing(ctx, i.board, i.ring, col, rx, ry, rr, Math.min(8, ((rr * Math.PI) / i.ring) * 0.7))
  }
  if (f.ambilight && i.ambilight) {
    const px = unhex(f.ambilight),
      step = (height - 40) / i.ambilight
    for (let k = 0; k < i.ambilight; k++) {
      const W = px[k * 4 + 3]
      const mix = (v: number) => Math.min(255, show(Math.min(255, v + W)))
      dot(ax, 20 + step * (k + 0.5), Math.min(9, step * 0.4), mix(px[k * 4]), mix(px[k * 4 + 1]), mix(px[k * 4 + 2]))
    }
  }
}

/** LogTab: the tail of the daemon's log or the system log (kernel, Wi-Fi driver…), refreshed while
 * following; a filter keeps the matching lines (docs/web-ui.md). */
export function LogTab({ peer, base }: { peer: Peer; base: string }) {
  const [source, setSource] = useState('daemon')
  const [lines, setLines] = useState(300)
  const [follow, setFollow] = useState(true)
  const [filter, setFilter] = useState('')
  const [text, setText] = useState<string[]>([])
  const [err, setErr] = useState('')
  const box = useRef<HTMLPreElement>(null)

  useEffect(() => {
    let stop = false
    const load = () =>
      api.log(peer.id, base, source, lines).then(
        (r) => {
          if (stop) return
          setErr('')
          setText(r.lines)
        },
        (e) => !stop && setErr(e.message || String(e)),
      )
    load()
    const t = follow ? setInterval(load, 3000) : undefined
    return () => {
      stop = true
      if (t) clearInterval(t)
    }
  }, [peer.id, base, source, lines, follow])

  const f = filter.trim().toLowerCase()
  const shown = f ? text.filter((l) => l.toLowerCase().includes(f)) : text
  useEffect(() => {
    const b = box.current
    if (b && follow) b.scrollTop = b.scrollHeight
  }, [shown.length, follow, text])

  const cls = (l: string) => (/level=ERROR|\berror\b|panic|fail/i.test(l) ? 'lerr' : /level=WARN|warn/i.test(l) ? 'lwarn' : '')
  return (
    <section class="card">
      <div class="row">
        <select aria-label="Log" value={source} onChange={(e) => setSource(e.currentTarget.value)}>
          <option value="daemon">yastationd</option>
          <option value="system">system (kernel, drivers)</option>
        </select>
        <select aria-label="Lines" value={String(lines)} onChange={(e) => setLines(+e.currentTarget.value)}>
          {[100, 300, 1000, 2000].map((n) => (
            <option value={String(n)}>{n} lines</option>
          ))}
        </select>
        <input type="text" aria-label="Filter the lines" placeholder="filter" value={filter} onInput={(e) => setFilter(e.currentTarget.value)} />
        <label class="inline small">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.currentTarget.checked)} /> follow
        </label>
        <span class="muted small">
          {shown.length}
          {f ? ` of ${text.length}` : ''} lines
        </span>
      </div>
      {err && <p class="err">{err}</p>}
      <pre class="log" ref={box}>
        {shown.map((l) => (
          <div class={cls(l)}>{l}</div>
        ))}
      </pre>
      <DiagDownload peer={peer} base={base} />
    </section>
  )
}

/** DiagDownload: the speaker's diagnostics archive for a bug report (docs/diagnostics.md). */
function DiagDownload({ peer, base }: { peer: Peer; base: string }) {
  const [raw, setRaw] = useState(false)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  async function go() {
    setBusy(true)
    setMsg('')
    try {
      const { blob, name } = await api.diag(peer.id, base, raw)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = name
      a.click()
      setTimeout(() => URL.revokeObjectURL(url), 10000)
      setMsg(`${name}, ${Math.round(blob.size / 1024)} KB`)
    } catch (e: any) {
      setMsg(e.message || String(e))
    }
    setBusy(false)
  }
  return (
    <div class="diag">
      <h3>Diagnostics</h3>
      <p class="muted small">
        Everything about this speaker that helps find a problem, in one archive: versions, the system, logs, the config, the network, the
        audio devices, the guards. Passwords, Wi-Fi names, MAC addresses, serials and what was said are blanked out, so it can go with a bug
        report; addresses on your network stay.
      </p>
      <div class="row">
        <button class="primary" disabled={busy} onClick={go}>
          {busy ? 'Collecting… (up to two minutes)' : 'Download diagnostics'}
        </button>
        <label class="inline small">
          <input type="checkbox" checked={raw} onChange={(e) => setRaw(e.currentTarget.checked)} /> keep secrets and names (for yourself only)
        </label>
      </div>
      {msg && <p class="small muted">{msg}</p>}
    </div>
  )
}

/** CalibrationTab: the light sensor against a lux meter (docs/sensor-calibration.md). The speaker
 * measures for ~15 s with its own lights off and stores the point in its config. */
export function CalibrationTab({ peer, base }: { peer: Peer; base: string }) {
  const [cal, setCal] = useState<api.LightCal | null>(null)
  const [lux, setLux] = useState('')
  const [method, setMethod] = useState<'incident' | 'paper'>('incident')
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [confirm, setConfirm] = useState(false)
  const load = () =>
    api.lightCal(peer.id, base).then(
      (c) => setCal(c),
      (e) => setErr(e.status === 404 ? 'This speaker has no light sensor.' : e.message || String(e)),
    )
  useEffect(() => {
    load()
  }, [peer.id])

  async function measure() {
    const v = parseFloat(lux.replace(',', '.'))
    if (!(v > 0)) return setErr('Enter the lux your meter shows.')
    setErr('')
    setMsg('')
    setBusy(true)
    try {
      const c = await api.calibrateLight(peer.id, base, v)
      setCal(c)
      setMsg(
        `Done: the sensor read ${c.measured?.toPrecision(4)} for ${v} lx` +
          (c.before ? ` (it would have said ${Math.round(c.before)} lx before).` : '.'),
      )
    } catch (e: any) {
      setErr(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setConfirm(false)
    try {
      setCal(await api.removeLightCal(peer.id, base))
      setMsg('The unit calibration is removed: the board\'s base calibration applies.')
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }

  return (
    <section class="card narrow">
      <h3>Light sensor</h3>
      {cal && (
        <p class="small">
          Now: <b>{Math.round(cal.lux * 10) / 10} lx</b> <span class="muted">(reading {cal.reading})</span> ·{' '}
          {cal.source === 'unit' ? (
            <>
              this speaker's calibration <code>{cal.calibration}</code>
            </>
          ) : cal.source === 'board' ? (
            "the model's base calibration (not this unit's)"
          ) : (
            'not calibrated'
          )}
        </p>
      )}
      <div class="row methods">
        <label class="inline small">
          <input type="radio" name="calmethod" checked={method === 'incident'} onChange={() => setMethod('incident')} /> I have an
          incident light meter
        </label>
        <label class="inline small">
          <input type="radio" name="calmethod" checked={method === 'paper'} onChange={() => setMethod('paper')} /> A phone or camera
          and a sheet of white paper
        </label>
      </div>
      {method === 'incident' ? (
        <ol class="small steps">
          <li>In a well-lit room (30 lx or more; white light, not coloured), put the meter in incident mode.</li>
          <li>
            Hold it next to the speaker's top, the dome or diffuser facing up, at the same height — beside the speaker, not on
            it, and without casting your shadow on the speaker.
          </li>
          <li>Enter the lux it shows (or its EV / exposure below) and press Measure. Step back: the speaker measures for about 15 s with its lights off.</li>
          <li>Optional: again in clearly different light (3× brighter or dimmer) — two points also give the curve its slope.</li>
        </ol>
      ) : (
        <ol class="small steps">
          <li>In a well-lit room (white light, not coloured), lay a sheet of plain white matte paper (printer paper, not glossy) flat on the speaker's top.</li>
          <li>
            Meter the sheet with a phone or camera in reflected mode: spot metering on the sheet, the sheet filling the whole
            metered area, no shadow of yours or the phone on it, no glare. A light-meter app shows EV; a camera app in manual
            or pro mode shows the exposure (aperture, shutter, ISO).
          </li>
          <li>Enter it below. Then take the paper off, step back and press Measure: the speaker measures without the sheet, for about 15 s with its lights off.</li>
          <li>Optional: again in clearly different light (3× brighter or dimmer) for the slope.</li>
        </ol>
      )}
      <p class="small muted">
        A phone is no incident meter (its light sensor or its camera aimed at the room are off by several times), but metering
        reflected light off white paper works.
      </p>
      <ExposureToLux key={method} method={method} onLux={setLux} />
      <div class="row">
        <input type="number" aria-label="Lux" min="1" step="any" placeholder="lux" value={lux} onInput={(e) => setLux(e.currentTarget.value)} />
        <button class="primary" disabled={busy} onClick={measure}>
          {busy ? 'Measuring…' : 'Measure'}
        </button>
        {cal?.source === 'unit' &&
          (confirm ? (
            <>
              <span class="small">Remove this speaker's calibration?</span>
              <button onClick={remove}>Remove</button>
              <button onClick={() => setConfirm(false)}>Keep</button>
            </>
          ) : (
            <button class="ghost" onClick={() => setConfirm(true)}>
              Remove calibration
            </button>
          ))}
      </div>
      {msg && <p class="small ok">{msg}</p>}
      {err && <p class="err">{err}</p>}
    </section>
  )
}

// EV (or an exposure) to lux, EV100 = log2(N²/t) − log2(ISO/100) (docs/sensor-calibration.md):
// - incident meter: E = C/100 · 2^EV100, the calibration constant C = 250 (a flat diffuser;
//   ISO 2720 allows 240–400, domes are often 320–340);
// - reflected meter on white paper: the meter reads luminance L = K/100 · 2^EV100 (K = 12.5), and
//   a matte white sheet of reflectance PAPER_R under E lux has L = PAPER_R·E/π, so
//   E = π·L/PAPER_R (≈ 0.46 · 2^EV100: white paper meters ~2.4 stops above the incident EV).
const INCIDENT_C = 250
const REFLECTED_K = 12.5
const PAPER_R = 0.85

function evToLux(ev100: number, method: 'incident' | 'paper'): number {
  return method === 'incident' ? (INCIDENT_C / 100) * Math.pow(2, ev100) : (Math.PI * (REFLECTED_K / 100) * Math.pow(2, ev100)) / PAPER_R
}

/** parseShutter reads "1/60", "1/2", "0.5", "2", "2s" as seconds. */
function parseShutter(s: string): number {
  const v = s.trim().replace(/s$/i, '').replace(',', '.')
  const m = v.match(/^(\d+(?:\.\d+)?)\s*\/\s*(\d+(?:\.\d+)?)$/)
  if (m) return +m[1] / +m[2]
  return v === '' ? NaN : +v
}

const num = (s: string) => (s.trim() === '' ? NaN : +s.trim().replace(',', '.'))

/** ExposureToLux: the meter's EV (or aperture, shutter and ISO) to lux, filled into the lux field
 * as it is typed. */
function ExposureToLux({ method, onLux }: { method: 'incident' | 'paper'; onLux: (s: string) => void }) {
  const [open, setOpen] = useState(method === 'paper') // white paper is always metered as EV or an exposure
  const [mode, setMode] = useState<'ev' | 'exp'>('ev')
  const [ev, setEv] = useState('')
  const [iso, setIso] = useState('100')
  const [f, setF] = useState('')
  const [t, setT] = useState('')

  let ev100 = NaN
  const s = num(iso)
  if (mode === 'ev') ev100 = num(ev) - Math.log2(s / 100)
  else {
    const n = num(f),
      sec = parseShutter(t)
    if (n > 0 && sec > 0) ev100 = Math.log2((n * n) / sec) - Math.log2(s / 100)
  }
  const lux = Number.isFinite(ev100) && s > 0 ? evToLux(ev100, method) : NaN
  useEffect(() => {
    if (open && Number.isFinite(lux)) onLux(String(lux < 100 ? Math.round(lux * 10) / 10 : Math.round(lux)))
  }, [open, lux])

  if (!open)
    return (
      <p class="small">
        <button class="linklike" onClick={() => setOpen(true)}>
          My meter shows EV or an exposure (aperture, shutter, ISO)
        </button>
      </p>
    )
  return (
    <div class="exposure">
      {method === 'incident' ? (
        <p class="small">
          In <b>incident</b> mode (the dome or flat diffuser on, facing up beside the speaker's top), not reflected or spot.
          Computed with the incident constant C = 250; a meter with a dome may be calibrated to about 330, then the true light
          is ~30 % less.
        </p>
      ) : (
        <p class="small">
          The <b>reflected</b> reading off the white sheet (spot, the sheet filling it). Computed for plain white paper
          (reflectance 85 %) and the reflected constant K = 12.5.
        </p>
      )}
      <div class="row">
        <label class="inline small">
          <input type="radio" name="expmode" checked={mode === 'ev'} onChange={() => setMode('ev')} /> EV
        </label>
        <label class="inline small">
          <input type="radio" name="expmode" checked={mode === 'exp'} onChange={() => setMode('exp')} /> aperture and shutter
        </label>
      </div>
      <div class="row">
        {mode === 'ev' ? (
          <label class="field">
            EV <input type="text" inputMode="decimal" value={ev} placeholder="e.g. 7.3" onInput={(e) => setEv(e.currentTarget.value)} />
          </label>
        ) : (
          <>
            <label class="field">
              f/ <input type="text" inputMode="decimal" value={f} placeholder="2.8" onInput={(e) => setF(e.currentTarget.value)} />
            </label>
            <label class="field">
              shutter <input type="text" value={t} placeholder="1/60" onInput={(e) => setT(e.currentTarget.value)} />
            </label>
          </>
        )}
        <label class="field">
          ISO <input type="text" inputMode="numeric" value={iso} onInput={(e) => setIso(e.currentTarget.value)} />
        </label>
        <span class="small muted">
          {Number.isFinite(lux) ? `EV₁₀₀ ${ev100.toFixed(1)} → ${lux < 100 ? lux.toFixed(1) : Math.round(lux)} lx` : 'fill in the fields'}
        </span>
      </div>
    </div>
  )
}
