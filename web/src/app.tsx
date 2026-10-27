import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Entity, Features, Peer } from './api'
import { CalibrationTab, LedTab, LogTab, WifiTab } from './extra'
import { MomentsTab } from './moments'

// The app (docs/web-ui.md): the speakers on the LAN, and per speaker its sensors, runtime
// controls, config and login. It is served by every speaker; the one it came from is "home" and
// lists the others.

type Status = 'ok' | 'login' | 'offline' | 'checking'

export function App() {
  const [home, setHome] = useState<Peer | null>(null)
  const [homeErr, setHomeErr] = useState('')
  const [authed, setAuthed] = useState(false)

  useEffect(() => {
    api
      .info('')
      .then(async (p) => {
        setHome(p)
        if (api.tokenFor(p.id)) {
          try {
            await api.peers(p.id, '')
            setAuthed(true)
          } catch {}
        }
      })
      .catch((e) => setHomeErr(String(e.message || e)))
  }, [])

  if (homeErr) return <div class="center">This speaker does not answer: {homeErr}</div>
  if (!home) return <div class="center muted">…</div>
  if (!authed) return <Login peer={home} base="" onDone={() => setAuthed(true)} />
  return <Speakers home={home} />
}

function Login(props: { peer: Peer; base: string; onDone: () => void; compact?: boolean }) {
  const c = api.lastCred()
  const [user, setUser] = useState(c?.user || 'admin')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  async function submit(e: Event) {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await api.login(props.peer.id, props.base, user, password)
      props.onDone()
    } catch (x: any) {
      setErr(x.message || String(x))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form class={props.compact ? 'card login' : 'card login center-card'} onSubmit={submit}>
      <h2>{props.peer.name}</h2>
      <label>
        User
        <input value={user} onInput={(e) => setUser(e.currentTarget.value)} autocomplete="username" />
      </label>
      <label>
        Password
        <input type="password" value={password} onInput={(e) => setPassword(e.currentTarget.value)} autocomplete="current-password" autofocus />
      </label>
      {err && <p class="err">{err}</p>}
      <button type="submit" disabled={busy}>
        Log in
      </button>
      <p class="muted small">Until changed, the login is admin / yastation.</p>
    </form>
  )
}

function Speakers({ home }: { home: Peer }) {
  const [list, setList] = useState<Peer[]>([home])
  const [status, setStatus] = useState<Record<string, Status>>({})
  const [sel, setSel] = useState(home.id)
  const [latest, setLatest] = useState<{ tag: string; url: string; date: string } | null>(null)
  const recheck = useRef(false)
  useEffect(() => {
    api.latestRelease().then(setLatest)
  }, [])
  // A speaker newer than the known release: the answer is stale, ask GitHub again (once).
  useEffect(() => {
    if (!latest || recheck.current || !list.some((p) => api.behind(latest.tag, p.version))) return
    recheck.current = true
    api.latestRelease(true).then((r) => r && setLatest(r))
  }, [latest, list])

  async function refresh() {
    let ps: Peer[]
    try {
      ps = await api.peers(home.id, '')
    } catch {
      return
    }
    setList(ps)
    for (const p of ps) {
      const base = api.baseOf(p, home.id)
      setStatus((s) => ({ ...s, [p.id]: s[p.id] || 'checking' }))
      api
        .info(base)
        .then(async () => {
          const ok = await api.tryLogin(p.id, base)
          if (ok) {
            try {
              await api.peers(p.id, base)
            } catch {
              setStatus((s) => ({ ...s, [p.id]: 'login' }))
              return
            }
          }
          setStatus((s) => ({ ...s, [p.id]: ok ? 'ok' : 'login' }))
        })
        .catch(() => setStatus((s) => ({ ...s, [p.id]: 'offline' })))
    }
  }

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 30000)
    return () => clearInterval(t)
  }, [])

  const peer = list.find((p) => p.id === sel) || home
  return (
    <div class="layout">
      <aside>
        <h1>Speakers</h1>
        {list.map((p) => (
          <button class={'speaker' + (p.id === sel ? ' sel' : '')} onClick={() => setSel(p.id)}>
            <span class={'dot ' + (status[p.id] || 'checking')} title={status[p.id]} />
            <span class="sname">{p.name}</span>
            <span class="muted small">
              {p.model} · {p.version}
              {latest && api.behind(p.version, latest.tag) && <span class="badge">{latest.tag} out</span>}
            </span>
          </button>
        ))}
        <button class={'speaker about' + (sel === ABOUT ? ' sel' : '')} onClick={() => setSel(ABOUT)}>
          <span />
          <span class="sname">About</span>
        </button>
      </aside>
      <main>
        {sel === ABOUT ? (
          <About list={list} latest={latest} />
        ) : (
        <Speaker
          key={peer.id}
          peer={peer}
          base={api.baseOf(peer, home.id)}
          status={status[peer.id] || 'checking'}
          onLogin={() => setStatus((s) => ({ ...s, [peer.id]: 'ok' }))}
        />
        )}
      </main>
    </div>
  )
}

const ABOUT = '\u0000about'

function About({ list, latest }: { list: Peer[]; latest: { tag: string; url: string; date: string } | null }) {
  return (
    <section class="card about-card">
      <h2>yastationd</h2>
      <p>
        Custom firmware for Yandex smart speakers: offline Home Assistant speakers. MIT license.
      </p>
      <p>
        <a href={api.REPO} target="_blank">
          {api.REPO.replace('https://', '')}
        </a>{' '}
        ·{' '}
        <a href={api.REPO + '/blob/main/CHANGELOG.md'} target="_blank">
          changelog
        </a>{' '}
        ·{' '}
        <a href={api.REPO + '/tree/main/docs'} target="_blank">
          docs
        </a>
      </p>
      <h3>Versions</h3>
      <p class="small">
        {latest ? (
          <>
            Latest release: <a href={latest.url} target="_blank">{latest.tag}</a>. Checked by this browser on GitHub; the
            speakers themselves never go online.
          </>
        ) : (
          <span class="muted">GitHub is not reachable from this browser: no update check.</span>
        )}
      </p>
      <table class="nets">
        <tbody>
          {list.map((p) => (
            <tr>
              <td>{p.name}</td>
              <td class="small">{p.version}</td>
              <td class="small">{latest ? (api.behind(p.version, latest.tag) ? <span class="badge">update: {latest.tag}</span> : <span class="muted">up to date</span>) : ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p class="muted small">Updating is done from a computer: docs/deploying.md.</p>
    </section>
  )
}

// Two levels: a group, then its tabs (docs/web-ui.md).
type Tab = 'overview' | 'log' | 'controls' | 'moments' | 'led' | 'options' | 'calibration' | 'wifi' | 'config' | 'account'

const GROUPS: { id: string; name: string; tabs: Tab[] }[] = [
  { id: 'status', name: 'Status', tabs: ['overview', 'log'] },
  { id: 'controls', name: 'Controls', tabs: ['controls', 'moments', 'led'] },
  { id: 'settings', name: 'Settings', tabs: ['options', 'calibration', 'wifi', 'config', 'account'] },
]

const TAB_NAMES: Record<Tab, string> = {
  overview: 'Sensors',
  log: 'Log',
  controls: 'Display and voice',
  moments: 'Moments',
  led: 'LED',
  options: 'Options',
  calibration: 'Calibration',
  wifi: 'Wi-Fi',
  config: 'Config file',
  account: 'Login',
}

function Speaker(props: { peer: Peer; base: string; status: Status; onLogin: () => void }) {
  const { peer, base } = props
  // The open section is in the address (#options), so it can be linked and survives a reload.
  const fromHash = () => {
    const h = location.hash.slice(1) as Tab
    return h in TAB_NAMES ? h : 'overview'
  }
  const [tab, setTabState] = useState<Tab>(fromHash)
  const setTab = (t: Tab) => {
    setTabState(t)
    history.replaceState(null, '', '#' + t)
  }
  const [ents, setEnts] = useState<Entity[] | null>(null)
  const [err, setErr] = useState('')
  const [needLogin, setNeedLogin] = useState(false)
  const [features, setFeatures] = useState<Features>({})

  async function load() {
    try {
      const s = await api.state(peer.id, base)
      setEnts(s.entities)
      setFeatures((s.info as any).features || {})
      setErr('')
      setNeedLogin(false)
    } catch (e: any) {
      if (e instanceof api.ApiError && e.status === 401) setNeedLogin(true)
      else setErr(e.message || String(e))
    }
  }

  useEffect(() => {
    load()
    const t = setInterval(load, 5000)
    return () => clearInterval(t)
  }, [peer.id])

  const available = (t: Tab) =>
    t === 'led' ? !!features.led_preview : t === 'wifi' ? !!features.wifi : t === 'moments' ? !!features.moments : true

  if (props.status === 'offline') return <div class="card">{peer.name} does not answer at {peer.url}.</div>
  if (needLogin || props.status === 'login')
    return (
      <Login
        compact
        peer={peer}
        base={base}
        onDone={() => {
          props.onLogin()
          setNeedLogin(false)
          load()
        }}
      />
    )

  return (
    <div>
      <header class="sp-head">
        <div>
          <h2>{peer.name}</h2>
          <div class="muted small">
            {peer.model} ({peer.board}) · {peer.version} · <a href={peer.url}>{peer.url}</a> ·{' '}
            <a href={(base || '') + '/metrics'} target="_blank">
              metrics
            </a>
          </div>
        </div>
      </header>
      <div class="sp-body">
        <nav class="pnav" aria-label="Speaker sections">
          {GROUPS.map((g) => (
            <div class="pgroup">
              <div class="pcap">{g.name}</div>
              {g.tabs.filter(available).map((t) => (
                <button class={'pitem' + (tab === t ? ' on' : '')} aria-current={tab === t ? 'page' : undefined} onClick={() => setTab(t)}>
                  {TAB_NAMES[t]}
                </button>
              ))}
            </div>
          ))}
        </nav>
        <div class="sp-main">
      {err && <p class="err">{err}</p>}
      {!ents && <p class="muted">…</p>}
      {ents && tab === 'overview' && <Overview ents={ents} />}
      {ents && tab === 'controls' && <Controls which="runtime" ents={ents} peer={peer} base={base} reload={load} />}
      {ents && tab === 'options' && <Controls which="options" ents={ents} peer={peer} base={base} reload={load} />}
      {tab === 'calibration' && <CalibrationTab peer={peer} base={base} />}
      {tab === 'moments' && <MomentsTab peer={peer} base={base} />}
      {tab === 'led' && <LedTab peer={peer} base={base} />}
      {tab === 'wifi' && <WifiTab peer={peer} base={base} />}
      {tab === 'log' && <LogTab peer={peer} base={base} />}
      {tab === 'config' && <ConfigEditor peer={peer} base={base} />}
      {tab === 'account' && <Account peer={peer} base={base} />}
        </div>
      </div>
    </div>
  )
}

const name = (e: Entity) => e.config.name || e.object
const isSensor = (e: Entity) => ['sensor', 'binary_sensor', 'event'].includes(e.component)

function fmtValue(e: Entity): string {
  if (e.value === null || e.value === undefined || e.value === 'None') return '—'
  if (e.component === 'event') {
    try {
      return JSON.parse(e.value).event_type
    } catch {
      return e.value
    }
  }
  if (e.object === 'uptime') {
    const s = Number(e.value)
    const d = Math.floor(s / 86400),
      h = Math.floor((s % 86400) / 3600),
      m = Math.floor((s % 3600) / 60)
    return (d ? d + 'd ' : '') + h + 'h ' + m + 'm'
  }
  const u = e.config.unit_of_measurement
  return u ? `${e.value} ${u}` : e.value
}

function ago(t?: number) {
  if (!t) return ''
  const s = Math.max(0, Math.round(Date.now() / 1000 - t))
  if (s < 90) return s + ' s ago'
  if (s < 5400) return Math.round(s / 60) + ' min ago'
  return Math.round(s / 3600) + ' h ago'
}

function Overview({ ents }: { ents: Entity[] }) {
  const sensors = ents.filter(isSensor)
  const main = sensors.filter((e) => e.config.entity_category !== 'diagnostic')
  const diag = sensors.filter((e) => e.config.entity_category === 'diagnostic')
  const block = (title: string, list: Entity[]) => (
    <section class="card">
      <h3>{title}</h3>
      <dl class="grid">
        {list.map((e) => (
          <>
            <dt>{name(e)}</dt>
            <dd title={e.updated ? 'updated ' + ago(e.updated) : ''}>
              {fmtValue(e)}
              {e.attributes && e.object === 'lux' && e.attributes.reading !== undefined && (
                <span class="muted small"> (reading {e.attributes.reading})</span>
              )}
            </dd>
          </>
        ))}
      </dl>
    </section>
  )
  return (
    <div class="cols">
      {block('Sensors', main)}
      {block('Diagnostics', diag)}
    </div>
  )
}

// Controls: the runtime controls ("runtime": lights, display, expressions…) or the settings HA
// keeps as config entities ("options").
function Controls(props: { which: 'runtime' | 'options'; ents: Entity[]; peer: Peer; base: string; reload: () => void }) {
  const ctl = props.ents.filter((e) => e.writable && (e.config.entity_category === 'config') === (props.which === 'options'))
  const [err, setErr] = useState('')
  async function send(e: Entity, payload: string) {
    setErr('')
    try {
      await api.control(props.peer.id, props.base, e.object, payload)
      setTimeout(props.reload, 300)
    } catch (x: any) {
      setErr(`${name(e)}: ${x.message || x}`)
    }
  }
  // A settings page (the Slack/Discord kind): one narrow column, the name and its control on one
  // line, so a switch is never a screen's width away from what it switches.
  return (
    <div class="settings">
      <h3>{props.which === 'options' ? 'Options' : 'Display, lights and voice'}</h3>
      {err && <p class="err">{err}</p>}
      {ctl.map((e) => (
        <div class="ctl">
          <span class="ctl-name">{name(e)}</span>
          <Control e={e} send={(p) => send(e, p)} />
        </div>
      ))}
    </div>
  )
}

function Control({ e, send }: { e: Entity; send: (p: string) => void }) {
  const c = e.config
  switch (e.component) {
    case 'switch': {
      const on = e.value === (c.payload_on || 'ON')
      return (
        <label class="switch">
          <input type="checkbox" checked={on} onChange={(x) => send(x.currentTarget.checked ? c.payload_on || 'ON' : c.payload_off || 'OFF')} />
          <span />
        </label>
      )
    }
    case 'select':
      return (
        <select value={e.value || ''} onChange={(x) => send(x.currentTarget.value)}>
          {(c.options || []).map((o: string) => (
            <option value={o}>{o}</option>
          ))}
        </select>
      )
    case 'number':
      return <NumberControl e={e} send={send} />
    case 'button':
      return <button onClick={() => send(c.payload_press || 'PRESS')}>Press</button>
    case 'text':
    case 'notify':
      return <TextControl e={e} send={send} />
  }
  return <span class="muted">{e.value}</span>
}

function NumberControl({ e, send }: { e: Entity; send: (p: string) => void }) {
  const c = e.config
  const [v, setV] = useState(e.value || '')
  const editing = useRef(false)
  useEffect(() => {
    if (!editing.current) setV(e.value || '')
  }, [e.value])
  const commit = () => {
    editing.current = false
    if (v !== e.value) send(v)
  }
  const slider = c.mode !== 'box' && c.max - c.min <= 1000
  return (
    <span class="num">
      {slider && (
        <input
          type="range"
          min={c.min}
          max={c.max}
          step={c.step || 1}
          value={v}
          onInput={(x) => {
            editing.current = true
            setV(x.currentTarget.value)
          }}
          onChange={commit}
        />
      )}
      <input
        type="number"
        min={c.min}
        max={c.max}
        step={c.step || 1}
        value={v}
        onInput={(x) => {
          editing.current = true
          setV(x.currentTarget.value)
        }}
        onChange={commit}
      />
      {c.unit_of_measurement && <span class="muted small">{c.unit_of_measurement}</span>}
    </span>
  )
}

function TextControl({ e, send }: { e: Entity; send: (p: string) => void }) {
  const [v, setV] = useState(e.component === 'text' ? e.value || '' : '')
  return (
    <form
      class="text"
      onSubmit={(x) => {
        x.preventDefault()
        send(v)
        if (e.component === 'notify') setV('')
      }}
    >
      <input value={v} maxLength={e.config.max || 255} onInput={(x) => setV(x.currentTarget.value)} />
      <button type="submit">{e.component === 'notify' ? 'Show' : 'Set'}</button>
    </form>
  )
}

function ConfigEditor({ peer, base }: { peer: Peer; base: string }) {
  const [text, setText] = useState<string | null>(null)
  const [orig, setOrig] = useState('')
  const [meta, setMeta] = useState<{ file: string; trial: boolean } | null>(null)
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function load() {
    try {
      const c = await api.getConfig(peer.id, base)
      setText(c.text)
      setOrig(c.text)
      setMeta({ file: c.file, trial: c.trial })
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  useEffect(() => {
    load()
  }, [peer.id])

  async function save() {
    if (text === null) return
    setBusy(true)
    setErr('')
    setMsg('')
    try {
      const r = await api.putConfig(peer.id, base, text)
      if (!r.restarting) {
        setMsg('No changes.')
        return
      }
      setMsg('Saved. The speaker restarts with the new config on trial…')
      await waitBack(base)
      setMsg('Back up. The new config is on trial for a minute: it stays if the speaker is as healthy as before, else the old one comes back by itself.')
      // the restart dropped our token
      await api.tryLogin(peer.id, base)
      await load()
    } catch (e: any) {
      setErr(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }

  if (text === null) return err ? <p class="err">{err}</p> : <p class="muted">…</p>
  return (
    <section class="card">
      <h3>
        Config <span class="muted small">{meta?.file}</span>
        {meta?.trial && <span class="badge">on trial</span>}
      </h3>
      <textarea class="config" spellcheck={false} value={text} onInput={(e) => setText(e.currentTarget.value)} />
      <div class="row">
        <button onClick={save} disabled={busy || text === orig}>
          Save and restart
        </button>
        <button class="ghost" onClick={() => setText(orig)} disabled={busy || text === orig}>
          Revert
        </button>
        <a class="small" href="https://github.com/celebeing/yastationd/blob/main/yastationd/configs/yastationd.conf.example" target="_blank">
          all keys
        </a>
      </div>
      {err && <p class="err">{err}</p>}
      {msg && <p class="ok">{msg}</p>}
    </section>
  )
}

async function waitBack(base: string) {
  await new Promise((r) => setTimeout(r, 2000))
  for (let i = 0; i < 30; i++) {
    try {
      await api.info(base)
      return
    } catch {
      await new Promise((r) => setTimeout(r, 1000))
    }
  }
}

function Account({ peer, base }: { peer: Peer; base: string }) {
  const [user, setUser] = useState(api.lastCred()?.user || 'admin')
  const [p1, setP1] = useState('')
  const [p2, setP2] = useState('')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  async function submit(e: Event) {
    e.preventDefault()
    setErr('')
    setMsg('')
    if (p1.length < 4) return setErr('At least 4 characters.')
    if (p1 !== p2) return setErr('The passwords differ.')
    try {
      await api.setPassword(peer.id, base, user, p1)
      setMsg('Changed. Other browsers have to log in again.')
      setP1('')
      setP2('')
    } catch (x: any) {
      setErr(x.message || String(x))
    }
  }
  return (
    <form class="card login" onSubmit={submit}>
      <h3>Login of {peer.name}</h3>
      <label>
        User
        <input value={user} onInput={(e) => setUser(e.currentTarget.value)} autocomplete="username" />
      </label>
      <label>
        New password
        <input type="password" value={p1} onInput={(e) => setP1(e.currentTarget.value)} autocomplete="new-password" />
      </label>
      <label>
        Again
        <input type="password" value={p2} onInput={(e) => setP2(e.currentTarget.value)} autocomplete="new-password" />
      </label>
      {err && <p class="err">{err}</p>}
      {msg && <p class="ok">{msg}</p>}
      <button type="submit">Change</button>
      <p class="muted small">Written into this speaker's config (web_user, web_password). Each speaker has its own login.</p>
    </form>
  )
}
