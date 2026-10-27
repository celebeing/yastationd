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

type TapInfo = {
  name: string
  board: string
  screen: [number, number]
  ring: number
  ambilight: number
  expressions?: string[]
  patterns?: string[]
  commands?: string[]
}
type Frame = { seq: number; screen?: string; ring?: string; ambilight?: string }

// Drive values are linear light; a monitor wants gamma-encoded ones. A lit LED also starts at a
// visible level (LIT_FLOOR): on the glass even a drive of 1 is a dot you can see, while 1/2.2
// alone draws it darker than an unlit dot.
const LIT_FLOOR = 48
const LUT = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + (255 - LIT_FLOOR) * Math.pow(i / 255, 1 / 2.2))))
const LIN = new Uint8Array(256).map((_, i) => (i === 0 ? 0 : Math.round(LIT_FLOOR + ((255 - LIT_FLOOR) * i) / 255)))
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
      const r = await fetch(api.tapURL(peer.id, base, '/cmd/' + name), { method: 'POST', body: JSON.stringify(payload) })
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
          <input type="text" placeholder={`filter ${info.patterns!.length}`} value={filter} onInput={(e) => setFilter(e.currentTarget.value)} />
          <select value={cur} onChange={(e) => setPattern(e.currentTarget.value)}>
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

function draw(c: HTMLCanvasElement | null, i: TapInfo, f: Frame, perceived: boolean) {
  if (!c) return
  const ctx = c.getContext('2d')!
  const show = (v: number) => (perceived ? LUT[v] : LIN[v])
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
    ctx.fillStyle = R | G | B ? `rgb(${R},${G},${B})` : '#0b0c0e'
    ctx.beginPath()
    ctx.arc(x, y, r, 0, 2 * Math.PI)
    ctx.fill()
  }
  if (f.screen && w) {
    // Station 3's screen LEDs are square and touch each other: cells, not dots.
    const px = unhex(f.screen)
    const y0 = (height - sh) / 2
    for (let y = 0; y < h; y++)
      for (let x = 0; x < w; x++) {
        const k = (y * w + x) * 3
        const R = show(px[k]),
          G = show(px[k + 1]),
          B = show(px[k + 2])
        ctx.fillStyle = R | G | B ? `rgb(${R},${G},${B})` : '#0b0c0e'
        ctx.fillRect(20 + x * P, y0 + y * P, P, P)
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
        <select value={source} onChange={(e) => setSource(e.currentTarget.value)}>
          <option value="daemon">yastationd</option>
          <option value="system">system (kernel, drivers)</option>
        </select>
        <select value={String(lines)} onChange={(e) => setLines(+e.currentTarget.value)}>
          {[100, 300, 1000, 2000].map((n) => (
            <option value={String(n)}>{n} lines</option>
          ))}
        </select>
        <input type="text" placeholder="filter" value={filter} onInput={(e) => setFilter(e.currentTarget.value)} />
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
    </section>
  )
}

/** CalibrationTab: the light sensor against a lux meter (docs/sensor-calibration.md). The speaker
 * measures for ~15 s with its own lights off and stores the point in its config. */
export function CalibrationTab({ peer, base }: { peer: Peer; base: string }) {
  const [cal, setCal] = useState<api.LightCal | null>(null)
  const [lux, setLux] = useState('')
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
      <ol class="small steps">
        <li>Take a lux meter to the speaker in a well-lit room (30 lx or more; white light, not coloured).</li>
        <li>
          Hold the meter next to the speaker's top, facing up, at the same height — beside it, not on it, and without
          casting your own shadow on the speaker.
        </li>
        <li>Enter what it shows and press Measure. Step back: the speaker measures for about 15 s with its lights off.</li>
        <li>
          Optional: do it again in clearly different light (3× brighter or dimmer) — the two points also give the curve its
          slope.
        </li>
      </ol>
      <p class="small muted">
        A phone app is a poor meter: Android's light sensor is rough, an iPhone app measures with the camera and can be off
        several times.
      </p>
      <div class="row">
        <input type="number" min="1" step="any" placeholder="lux" value={lux} onInput={(e) => setLux(e.currentTarget.value)} />
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
