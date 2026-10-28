import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Entity, Peer, SettingKey, Settings, SyncKind, SyncState } from './api'
import { Control, name } from './controls'

// Configuration (docs/settings.md): pages over one registry of config keys, by topic. Each row:
// the name, the control, a share switch that makes every speaker take the value, and a paragraph
// on what it does.

// The HA config entities kept in the speaker's state (not the config file), with their place and
// explanation on the settings page.
const OPTIONS: Record<string, { group: string; doc: string }> = {
  auto_brightness: { group: 'display', doc: "The clock's brightness follows the light sensor." },
  clock_brightness: { group: 'display', doc: 'The fixed brightness while auto brightness is off; with it on, the brightest it goes in bright light.' },
  display_faces: { group: 'display', doc: 'Faces and clips on the front display while the assistant listens, thinks and talks.' },
  text_duration: { group: 'display', doc: 'How long a message from Home Assistant stays on the display.' },
  animation_duration: { group: 'display', doc: 'How long an expression or ring animation started from Home Assistant plays.' },
  ambilight_fx_brightness: { group: 'ambilight', doc: "The ambilight's brightness under the music effects." },
  follow_up: { group: 'voice', doc: 'Whether the speaker listens again after it answers, for a reply without the wake word.' },
}

// Config keys shown right after an option of the same section, not among the section's keys.
const AFTER: Record<string, string> = { auto_brightness_min: 'clock_brightness' }
const optOrder = Object.keys(OPTIONS)

type Ctx = { peer: Peer; base: string }

function useSettings({ peer, base }: Ctx) {
  const [s, setS] = useState<Settings | null>(null)
  const [sync, setSyncState] = useState<SyncState | null>(null)
  const [err, setErr] = useState('')
  async function load() {
    try {
      const [st, sy] = await Promise.all([api.settings(peer.id, base), api.syncState(peer.id, base)])
      setS(st)
      setSyncState(sy)
      setErr('')
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  useEffect(() => {
    load()
  }, [peer.id])
  async function share(kind: SyncKind, item: string, on: boolean) {
    setErr('')
    try {
      setSyncState(await api.setSync(peer.id, base, kind, item, on))
      if (on) return
      await load()
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  return { s, sync, err, setErr, load, share }
}

/** ShareSwitch: the value goes to every speaker (and they keep taking it) while on. */
export function ShareSwitch(p: { sync: SyncState | null; kind: SyncKind; item: string; onShare: (kind: SyncKind, item: string, on: boolean) => void }) {
  const table = p.sync ? (p.kind === 'setting' ? p.sync.settings : p.kind === 'option' ? p.sync.options : p.sync.moments) : {}
  const at = table[p.item]
  const off = !p.sync || !p.sync.connected
  const title = off
    ? 'Sharing goes through the MQTT broker, and this speaker has none now.'
    : at
      ? `Every speaker takes this value. Last changed ${new Date(at.at).toLocaleString()} on ${at.by}. Click to stop sharing; each speaker keeps the value it has.`
      : "Click to give every speaker this speaker's value, now and whenever it changes."
  return (
    <button
      type="button"
      class={'share' + (at ? ' on' : '')}
      aria-pressed={!!at}
      disabled={off}
      title={title}
      onClick={() => p.onShare(p.kind, p.item, !at)}
    >
      {at ? 'All speakers' : 'Only here'}
    </button>
  )
}

function Row(p: { label: string; doc: string; control: any; share?: any; note?: any; changed?: boolean; onReset?: () => void; tag?: string }) {
  return (
    <div class={'setting' + (p.changed ? ' changed' : '')}>
      <div class="srow">
        <span class="sname">
          {p.label}
          {p.tag && <span class="tag quiet">{p.tag}</span>}
          {p.changed && <span class="tag">changed</span>}
        </span>
        <span class="sctl">{p.control}</span>
        {p.share || <span />}
      </div>
      <p class="sdoc">
        {p.doc}
        {p.note}
        {p.changed && p.onReset && (
          <>
            {' '}
            <button type="button" class="linklike" onClick={p.onReset}>
              Reset
            </button>
          </>
        )}
      </p>
    </div>
  )
}

// ref: the value in force when the key is unset — the board's where it has one, else the default.
const ref = (k: SettingKey) => k.board ?? k.default
const same = (a: string[], b: string[]) => a.join('\n') === b.join('\n')
const isDefault = (k: SettingKey, v: string[]) => same(v, ref(k))

// defaultNote: what the key is when unset, shown when the value differs from it.
function defaultNote(k: SettingKey, changed: boolean) {
  if (!changed || k.kind === 'secret') return null
  const r = ref(k)
  const shown = r.join('') === '' ? 'empty' : k.kind === 'bool' ? (r[0] === 'true' ? 'on' : 'off') : r.join(', ') + (k.unit ? ' ' + k.unit : '')
  return <span class="muted"> {k.board ? "The board's value" : 'Default'}: {shown}.</span>
}

/** KeyControl edits one key; commit gets its new lines ([] = back to the default). */
function KeyControl({ k, value, commit }: { k: SettingKey; value: string[]; commit: (v: string[]) => void }) {
  const v = value[0] ?? ''
  const [text, setText] = useState(k.kind === 'list' ? value.join('\n') : v)
  useEffect(() => setText(k.kind === 'list' ? value.join('\n') : v), [value.join('\n')])
  const done = () => {
    if (k.kind === 'list') {
      const lines = text.split('\n').map((l) => l.trim()).filter(Boolean)
      if (lines.join('\n') !== value.join('\n')) commit(lines)
    } else if (k.kind === 'secret') {
      if (text && text !== v) commit([text])
    } else if (text !== v) commit([text])
  }
  const enter = (e: KeyboardEvent) => {
    if (e.key === 'Enter') (e.currentTarget as HTMLInputElement).blur()
  }
  switch (k.kind) {
    case 'bool':
      return (
        <label class="switch">
          <input type="checkbox" aria-label={k.label} checked={v === 'true'} onChange={(e) => commit([String(e.currentTarget.checked)])} />
          <span />
        </label>
      )
    case 'enum':
      return (
        <select aria-label={k.label} value={v} onChange={(e) => commit([e.currentTarget.value])}>
          {(k.options || []).map((o) => (
            <option value={o}>{o}</option>
          ))}
        </select>
      )
    case 'int':
    case 'float':
      return (
        <span class="num">
          <input
            type="number"
            aria-label={k.label}
            min={k.min}
            max={k.max}
            step={k.kind === 'float' ? 'any' : 1}
            value={text}
            onInput={(e) => setText(e.currentTarget.value)}
            onBlur={done}
            onKeyDown={enter}
          />
          {k.unit && <span class="muted small">{k.unit}</span>}
        </span>
      )
    case 'list':
      return <textarea class="lines" aria-label={k.label} rows={Math.max(2, value.length + 1)} value={text} onInput={(e) => setText(e.currentTarget.value)} onBlur={done} />
    case 'secret':
      return (
        <input
          type="password"
          aria-label={k.label}
          autocomplete="new-password"
          placeholder={v ? 'set — type to change' : 'not set'}
          value={text === v ? '' : text}
          onInput={(e) => setText(e.currentTarget.value)}
          onBlur={done}
          onKeyDown={enter}
        />
      )
    default:
      return <input class="wide" aria-label={k.label} value={text} onInput={(e) => setText(e.currentTarget.value)} onBlur={done} onKeyDown={enter} />
  }
}

function groupsOf(s: Settings, pick: (k: SettingKey) => boolean) {
  return s.groups
    .map((g) => ({ id: g.ID, title: g.Title, keys: s.keys.filter((k) => k.group === g.ID && pick(k)) }))
    .filter((g) => g.keys.length > 0)
}

// The configuration pages: which groups of keys each shows. Every page mixes keys that apply at
// once (saved as they change) with keys that need a restart (marked; their changes wait in a
// floating bar for one "Apply and restart").
export type ConfigPageID = 'general' | 'voice' | 'home' | 'music' | 'intercom' | 'wifi' | 'calibration'

// Which sections a page holds comes from the daemon (config.Groups); here only the words on top.
const INTRO: Record<ConfigPageID, string> = {
  general: 'How the speaker looks and sounds.',
  voice: 'The wake word, the dialog with Home Assistant, and which speaker answers.',
  home: 'Fitting the speaker into your home: its name, Home Assistant, the web app, time and logs. Usually set once.',
  music: 'The speaker as a Music Assistant player (Sendspin).',
  intercom: 'Calls between the speakers.',
  wifi: '',
  calibration: '',
}

export function ConfigPage(p: Ctx & { page: ConfigPageID; ents?: Entity[]; reload?: () => void }) {
  const { s, sync, err, setErr, load, share } = useSettings(p)
  const [pending, setPending] = useState<Record<string, string[]>>({})
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  const intro = INTRO[p.page]
  if (!s) return err ? <p class="err">{err}</p> : <p class="muted">…</p>

  function flash(m: string) {
    setMsg(m)
    setTimeout(() => setMsg(''), 2500)
  }
  // A key that applies at once is saved now; one that needs a restart waits for Apply.
  async function commit(k: SettingKey, v: string[]) {
    setErr('')
    if (!k.runtime) {
      setPending({ ...pending, [k.name]: v })
      return
    }
    try {
      await api.putSettings(p.peer.id, p.base, { [k.name]: v.length && isDefault(k, v) && !k.set ? [] : v })
      flash(`Saved: ${k.label}.`)
    } catch (e: any) {
      setErr(`${k.label}: ${e.message || e}`)
    }
    await load()
  }
  async function apply() {
    setBusy(true)
    setErr('')
    setMsg('')
    try {
      const r = await api.putSettings(p.peer.id, p.base, pending)
      setPending({})
      if (r.restarting) {
        setMsg('The speaker restarts with the new settings on trial…')
        await waitBack(p.base)
        await api.tryLogin(p.peer.id, p.base)
        flash('Back up. The new settings are on trial for a minute: they stay if the speaker is as healthy as before, else the old ones come back by themselves.')
      } else flash(r.changed.length ? 'Applied.' : 'No changes.')
      await load()
    } catch (e: any) {
      setErr(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }
  async function send(e: Entity, payload: string) {
    setErr('')
    try {
      await api.control(p.peer.id, p.base, e.object, payload)
      setTimeout(() => p.reload?.(), 300)
    } catch (x: any) {
      setErr(`${name(e)}: ${x.message || x}`)
    }
  }

  const row = (k: SettingKey) => {
    const p0 = pending[k.name]
    const v = p0 ?? k.value
    const changed = p0 ? p0.length > 0 && !isDefault(k, p0) : k.changed
    return (
      <Row
        label={k.label + (p0 ? ' •' : '')}
        tag={k.runtime ? undefined : 'restart'}
        doc={k.doc}
        note={defaultNote(k, changed)}
        changed={changed}
        onReset={() => commit(k, [])}
        control={<KeyControl k={k} value={v} commit={(nv) => commit(k, nv)} />}
        share={k.sync && <ShareSwitch sync={sync} kind="setting" item={k.name} onShare={share} />}
      />
    )
  }
  const onPage = (group: string) => s.groups.find((g) => g.ID === group)?.Page === p.page
  // A page: its sections, then its rarely touched keys folded under "Advanced".
  const groups = groupsOf(s, (k) => onPage(k.group) && !k.advanced)
  const folded = groupsOf(s, (k) => onPage(k.group) && !!k.advanced)
  const opts = (p.ents || [])
    .filter((e) => e.writable && e.config.entity_category === 'config' && onPage(OPTIONS[e.object]?.group || 'display'))
    .sort((a, b) => (optOrder.indexOf(a.object) + 1 || 99) - (optOrder.indexOf(b.object) + 1 || 99))
  const shown = new Set(opts.map((e) => e.object))
  const placed = (k: SettingKey) => !!AFTER[k.name] && shown.has(AFTER[k.name])
  for (const e of opts) {
    const id = OPTIONS[e.object]?.group || 'display'
    if (!groups.some((g) => g.id === id)) groups.push({ id, title: s.groups.find((g) => g.ID === id)?.Title || id, keys: [] })
  }
  const order = s.groups.map((g) => g.ID)
  groups.sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id))
  const n = Object.keys(pending).length
  return (
    <div class="settings">
      {intro && (
        <p class="muted small">
          {intro}
        </p>
      )}
      {s.trial && <p class="small"><span class="badge">on trial</span> The last settings are on trial: they stay if the speaker is as healthy as before.</p>}
      {s.skipped && s.skipped.length > 0 && (
        <p class="err small">
          The config file has lines this version skips (their keys keep the default): {s.skipped.join('; ')}. Setting the key here
          replaces the line.
        </p>
      )}
      {err && <p class="err">{err}</p>}
      {sync && !sync.connected && <p class="muted small">No MQTT broker: sharing with the other speakers is off.</p>}
      {groups.map((g) => (
        <section>
          {groups.length > 1 || !['voice', 'music', 'intercom'].includes(p.page) ? <h3>{g.title}</h3> : null}
          {opts
            .filter((e) => (OPTIONS[e.object]?.group || 'display') === g.id)
            .map((e) => (
              <>
                <Row
                  label={name(e)}
                  doc={OPTIONS[e.object]?.doc || ''}
                  control={<Control e={e} send={(v) => send(e, v)} />}
                  share={sync?.option_ids.includes(e.object) && <ShareSwitch sync={sync} kind="option" item={e.object} onShare={share} />}
                />
                {g.keys.filter((k) => AFTER[k.name] === e.object).map(row)}
              </>
            ))}
          {g.keys.filter((k) => !placed(k)).map(row)}
        </section>
      ))}
      {folded.length > 0 && (
        <details class="advanced">
          <summary>Advanced</summary>
          {folded.map((g) => (
            <section>
              {folded.length > 1 && <h3>{g.title}</h3>}
              {g.keys.map(row)}
            </section>
          ))}
        </details>
      )}
      {(n > 0 || msg || busy) && (
        <div class="applybar" role="status">
          <span class="small">{n > 0 ? `${n} change${n > 1 ? 's' : ''} need${n > 1 ? '' : 's'} a restart. It is on trial: a speaker worse off rolls back by itself.` : msg}</span>
          {n > 0 && (
            <span class="row tight">
              <button class="primary" disabled={busy} onClick={apply}>
                Apply and restart
              </button>
              <button class="ghost" disabled={busy} onClick={() => setPending({})}>
                Discard
              </button>
            </span>
          )}
        </div>
      )}
    </div>
  )
}

export async function waitBack(base: string) {
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
