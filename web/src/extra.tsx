import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Moment, Net, Peer } from './api'

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

export function MomentsTab({ peer, base }: { peer: Peer; base: string }) {
  const [list, setList] = useState<Moment[] | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    api.moments(peer.id, base).then(setList, (e) => setErr(e.message || String(e)))
  }, [peer.id])
  async function show(m: Moment) {
    setErr('')
    try {
      const r = await api.showMoment(peer.id, base, m.name)
      if (!r.shown) setErr(`${m.name}: nothing to show on this speaker`)
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  return (
    <section class="card">
      <h3>Moments</h3>
      <p class="muted small">What the speaker shows when something happens. Each resolves to the first of its candidates this speaker has.</p>
      {err && <p class="err">{err}</p>}
      {!list && <p class="muted">…</p>}
      {list && (
        <table class="nets">
          <tbody>
            {list.map((m) => (
              <tr>
                <td>
                  <b>{m.name}</b>
                </td>
                <td class="small">{m.shows ? m.shows : <span class="muted">— nothing here</span>}</td>
                <td>
                  <button disabled={!m.shows} onClick={() => show(m)}>
                    Show
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

type TapInfo = { name: string; board: string; screen: [number, number]; ring: number; ambilight: number }
type Frame = { seq: number; screen?: string; ring?: string; ambilight?: string }

const LUT = new Uint8Array(256).map((_, i) => Math.round(255 * Math.pow(i / 255, 1 / 2.2)))
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
    </section>
  )
}

function draw(c: HTMLCanvasElement | null, i: TapInfo, f: Frame, perceived: boolean) {
  if (!c) return
  const ctx = c.getContext('2d')!
  const show = (v: number) => (perceived ? LUT[v] : v)
  const [w, h] = i.screen || [0, 0]
  const P = 16
  const sw = w * P,
    sh = h * P
  const rr = Math.max(60, sh / 2)
  const rx = (w ? 20 + sw + 36 : 20) + rr
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
    ctx.fillStyle = R | G | B ? `rgb(${R},${G},${B})` : '#15181c'
    ctx.beginPath()
    ctx.arc(x, y, r, 0, 2 * Math.PI)
    ctx.fill()
  }
  if (f.screen && w) {
    const px = unhex(f.screen)
    const y0 = (height - sh) / 2
    for (let y = 0; y < h; y++)
      for (let x = 0; x < w; x++) {
        const k = (y * w + x) * 3
        dot(20 + x * P + P / 2, y0 + y * P + P / 2, P * 0.4, show(px[k]), show(px[k + 1]), show(px[k + 2]))
      }
  }
  if (f.ring && i.ring) {
    const px = unhex(f.ring)
    for (let k = 0; k < i.ring; k++) {
      const a = -Math.PI / 2 + (2 * Math.PI * k) / i.ring // index 0 at the top, clockwise
      dot(rx + rr * Math.cos(a), ry + rr * Math.sin(a), Math.min(8, ((rr * Math.PI) / i.ring) * 0.7), show(px[k * 3]), show(px[k * 3 + 1]), show(px[k * 3 + 2]))
    }
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
