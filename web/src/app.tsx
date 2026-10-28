import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Entity, Features, Peer } from './api'
import { CalibrationTab, LedTab, LogTab, WifiTab } from './extra'
import { MomentsTab } from './moments'
import { Control, name } from './controls'
import { ConfigPage } from './settings'
import { VideosTab } from './assets'

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
        <h1 class="brandmark">
          <img src="/favicon.svg" alt="" width={22} height={22} />
          Speakers
        </h1>
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
type Tab = 'overview' | 'log' | 'controls' | 'led' | 'videos' | 'general' | 'voice' | 'moments' | 'home' | 'music' | 'intercom' | 'calibration' | 'wifi' | 'account'

const GROUPS: { id: string; name: string; tabs: Tab[] }[] = [
  { id: 'status', name: 'Status', tabs: ['overview', 'log'] },
  { id: 'controls', name: 'Controls', tabs: ['controls', 'led'] },
  { id: 'assets', name: 'Assets', tabs: ['videos'] },
  { id: 'configuration', name: 'Configuration', tabs: ['general', 'voice', 'moments', 'home', 'music', 'intercom', 'calibration', 'wifi', 'account'] },
]

const TAB_NAMES: Record<Tab, string> = {
  overview: 'Sensors',
  log: 'Log',
  controls: 'Display and voice',
  led: 'LED',
  videos: 'Videos',
  general: 'General',
  voice: 'Voice',
  moments: 'Moments',
  home: 'Home integration',
  music: 'Music Assistant',
  intercom: 'Intercom',
  calibration: 'Calibration',
  wifi: 'Wi-Fi',
  account: 'Login',
}

function Speaker(props: { peer: Peer; base: string; status: Status; onLogin: () => void }) {
  const { peer, base } = props
  // The open section is in the address (#options), so it can be linked and survives a reload.
  const fromHash = () => {
    const h = location.hash.slice(1)
    if (h === 'options' || h === 'settings') return 'general' // old names
    if (h === 'config' || h === 'advanced') return 'general'
    return h in TAB_NAMES ? (h as Tab) : 'overview'
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
    t === 'led' ? !!features.led_preview : t === 'moments' ? !!features.moments : t === 'videos' ? !!features.videos : true

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
      <SpeakerCard peer={peer} base={base} ents={ents} reload={load} />
      <div class="sp-body">
        <nav class="pnav" aria-label="Speaker sections">
          {GROUPS.filter((g) => g.tabs.some(available)).map((g) => (
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
      {(tab === 'general' || tab === 'voice') && <ConfigPage key={tab} page={tab} ents={ents || []} peer={peer} base={base} reload={load} />}
      {(['home', 'music', 'intercom'] as Tab[]).includes(tab) && <ConfigPage key={tab} page={tab as any} peer={peer} base={base} />}
      {tab === 'calibration' && <CalibrationTab peer={peer} base={base} />}
      {tab === 'calibration' && <ConfigPage page="calibration" peer={peer} base={base} />}
      {tab === 'moments' && <MomentsTab peer={peer} base={base} />}
      {tab === 'led' && <LedTab peer={peer} base={base} />}
      {tab === 'videos' && <VideosTab peer={peer} base={base} reload={load} />}
      {tab === 'wifi' &&
        (features.wifi ? (
          <WifiTab peer={peer} base={base} />
        ) : (
          <p class="muted">The Wi-Fi setup mode is off on this speaker: turn it on below to manage the saved networks here.</p>
        ))}
      {tab === 'wifi' && <ConfigPage page="wifi" peer={peer} base={base} />}
      {tab === 'log' && <LogTab peer={peer} base={base} />}
      {tab === 'account' && <Account peer={peer} base={base} />}
        </div>
      </div>
    </div>
  )
}

// SpeakerCard: the top of the speaker panel, the same on every tab — who it is and where, how it
// is doing, and the controls reached for most (volume, mute, the display).
function SpeakerCard(p: { peer: Peer; base: string; ents: Entity[] | null; reload: () => void }) {
  const { peer, base } = p
  const ent = (o: string) => p.ents?.find((e) => e.object === o)
  const host = (() => {
    try {
      return new URL(peer.url).host
    } catch {
      return peer.url
    }
  })()
  const [err, setErr] = useState('')
  async function send(o: string, payload: string) {
    setErr('')
    try {
      await api.control(peer.id, base, o, payload)
      setTimeout(p.reload, 300)
    } catch (x: any) {
      setErr(x.message || String(x))
    }
  }
  const vol = ent('volume')
  const health = ent('health')?.value
  const ha = ent('pipeline_ok')?.value
  const micOff = ent('mic_mute')?.value === 'ON'
  const power = ent('power_problem')?.value === 'ON'
  const lux = ent('lux')
  const chips: [string, string, string][] = [] // text, tone, title
  if (health) chips.push([health, health === 'online' ? 'good' : health === 'degraded' ? 'warn' : 'bad', 'Broker, Home Assistant and the LAN as the speaker sees them'])
  if (ha) chips.push([ha === 'ON' ? 'Voice pipeline up' : 'Voice pipeline down', ha === 'ON' ? 'good' : 'bad', "Home Assistant's link to this speaker"])
  if (micOff) chips.push(['Mic off', 'warn', 'The mic mute key is on'])
  if (power) chips.push(['Power problem', 'bad', 'The supply is too weak'])
  if (lux?.value) chips.push([fmtValue(lux), '', 'Light at the speaker'])
  // Mute: volume 0, the level before kept in this browser to come back to.
  const muteKey = 'yst.unmute.' + peer.id
  const muted = vol?.value === '0'
  function toggleMute() {
    if (!vol) return
    if (muted) {
      let back = '30'
      try {
        back = localStorage.getItem(muteKey) || back
      } catch {}
      send('volume', back)
    } else {
      try {
        localStorage.setItem(muteKey, vol.value || '30')
      } catch {}
      send('volume', '0')
    }
  }
  const mode = ent('display_mode')
  return (
    <header class="spcard">
      <div class="spid">
        <h2>{peer.name}</h2>
        <div class="spaddr">
          <a href={peer.url}>{host}</a>
        </div>
        <div class="muted small">
          {peer.model} ({peer.board}) · {peer.version} ·{' '}
          <a href={(base || '') + '/metrics'} target="_blank">
            metrics
          </a>
        </div>
        {chips.length > 0 && (
          <div class="chips">
            {chips.map(([t, tone, title]) => (
              <span class={'chip ' + tone} title={title}>
                {t}
              </span>
            ))}
          </div>
        )}
      </div>
      <VitalsStrip peer={peer} base={base} />
      <div class="spctl">
        {vol && (
          <div class="ctl">
            <span class="ctl-name">Volume</span>
            <VolumeSlider e={vol} send={(v) => send('volume', v)} />
            <button class={'mute' + (muted ? ' on' : '')} aria-pressed={muted} onClick={toggleMute} title={muted ? 'Back to the volume before' : 'Volume 0'}>
              {muted ? 'Unmute' : 'Mute'}
            </button>
          </div>
        )}
        {mode && (
          <div class="ctl">
            <span class="ctl-name">Display</span>
            <Control e={mode} send={(v) => send('display_mode', v)} />
          </div>
        )}
        {err && <p class="err small">{err}</p>}
      </div>
    </header>
  )
}

// The speaker's and the daemon's vitals (GET /api/vitals), refreshed every 5 s; hidden on a
// speaker too old to have them.
function VitalsStrip({ peer, base }: { peer: Peer; base: string }) {
  const [v, setV] = useState<api.Vitals | null>(null)
  useEffect(() => {
    let live = true
    const load = () =>
      api.vitals(peer.id, base).then(
        (x) => live && setV(x),
        () => live && setV(null),
      )
    load()
    const t = setInterval(load, 5000)
    return () => {
      live = false
      clearInterval(t)
    }
  }, [peer.id, base])
  if (!v) return null
  // label, value, title, tone, and how full its gauge is (0..1; none for the uptimes)
  const cells: { l: string; v: string; title: string; tone?: string; frac?: number }[] = []
  const tone = (f: number, warn: number, bad: number) => (f >= bad ? 'bad' : f >= warn ? 'warn' : '')
  if (v.system_uptime_s) cells.push({ l: 'Speaker up', v: fmtSpan(v.system_uptime_s), title: 'Since the speaker booted' })
  cells.push({ l: 'Daemon up', v: fmtSpan(v.daemon_uptime_s), title: 'Since yastationd started' })
  if (v.load) {
    const f = v.load[0] / Math.max(1, v.cpus)
    cells.push({ l: 'Load', v: v.load.map((l) => l.toFixed(2)).join(' '), title: `Load average over 1, 5 and 15 min; the gauge: the last minute over ${v.cpus} cores`, tone: tone(f, 0.7, 1), frac: f })
  }
  if (v.temps && v.temps.length) {
    const hot = v.temps.reduce((a, b) => (b.c > a.c ? b : a))
    cells.push({ l: 'Temperature', v: Math.round(hot.c) + ' °C', title: v.temps.map((t) => `${t.zone}: ${t.c.toFixed(1)} °C`).join('\n') + '\nthe gauge: 30 to 90 °C', tone: hot.c >= 80 ? 'bad' : hot.c >= 70 ? 'warn' : '', frac: (hot.c - 30) / 60 })
  }
  if (v.mem_total && v.mem_available !== undefined) {
    const used = v.mem_total - v.mem_available
    cells.push({ l: 'Memory used', v: fmtPair(used, v.mem_total), title: 'In use of the total (what is not MemAvailable)', tone: tone(used / v.mem_total, 0.8, 0.9), frac: used / v.mem_total })
  }
  if (v.daemon_rss) cells.push({ l: 'Daemon memory', v: fmtBytes(v.daemon_rss), title: "yastationd's resident memory; the gauge: its share of the total", frac: v.mem_total ? v.daemon_rss / v.mem_total : undefined })
  if (v.flash_total && v.flash_free !== undefined) {
    const used = v.flash_total - v.flash_free
    cells.push({ l: 'Flash used', v: fmtPair(used, v.flash_total), title: 'In use of the total, on the file system the CFW lives on', tone: tone(used / v.flash_total, 0.9, 0.97), frac: used / v.flash_total })
  }
  return (
    <dl class="vitals">
      {cells.map((c) => (
        <div title={c.title}>
          <dt>{c.l}</dt>
          <dd class={c.tone || ''}>{c.v}</dd>
          <span class={'gauge ' + (c.tone || '')} aria-hidden="true">
            {c.frac !== undefined && <i style={{ width: Math.round(Math.max(0.02, Math.min(1, c.frac)) * 100) + '%' }} />}
          </span>
        </div>
      ))}
    </dl>
  )
}

function fmtSpan(s: number) {
  const d = Math.floor(s / 86400),
    h = Math.floor((s % 86400) / 3600),
    m = Math.floor((s % 3600) / 60)
  if (d) return `${d} d ${h} h`
  if (h) return `${h} h ${m} min`
  return m ? `${m} min` : `${Math.floor(s)} s`
}

// "399 / 490 MB": a part of a whole, the unit once when both share it.
function fmtPair(part: number, whole: number) {
  const a = fmtBytes(part), b = fmtBytes(whole)
  const [an, au] = a.split(' '), [bn, bu] = b.split(' ')
  return au === bu ? `${an} / ${bn} ${bu}` : `${a} / ${b}`
}

function fmtBytes(b: number) {
  const mb = b / 1048576
  return mb >= 1024 ? (mb / 1024).toFixed(1) + ' GB' : mb >= 10 ? Math.round(mb) + ' MB' : mb.toFixed(1) + ' MB'
}

function VolumeSlider({ e, send }: { e: Entity; send: (v: string) => void }) {
  const [v, setV] = useState(e.value || '0')
  const dragging = useRef(false)
  useEffect(() => {
    if (!dragging.current) setV(e.value || '0')
  }, [e.value])
  return (
    <span class="num">
      <input
        type="range"
        aria-label="Volume"
        min={e.config.min ?? 0}
        max={e.config.max ?? 100}
        step={e.config.step || 1}
        value={v}
        onInput={(x) => {
          dragging.current = true
          setV(x.currentTarget.value)
        }}
        onChange={(x) => {
          dragging.current = false
          send(x.currentTarget.value)
        }}
      />
      <span class="vol">{v}</span>
    </span>
  )
}

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
