import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Entity, Peer, SettingKey, Settings, SyncKind, SyncState } from './api'
import { Control, name } from './controls'

// Settings (docs/settings.md). Two pages over one registry of config keys: "Settings" holds what
// applies at once (saved as you change it), "Config" what needs a restart (changed together, then
// applied by a restart on trial), with the raw file one click away. Each row: the name, the
// control, a share switch that makes every speaker take the value, and a paragraph on what it does.

// The HA config entities kept in the speaker's state (not the config file), with their place and
// explanation on the settings page.
const OPTIONS: Record<string, { group: string; doc: string }> = {
  auto_brightness: { group: 'display', doc: "The clock's brightness follows the light sensor." },
  clock_brightness: { group: 'display', doc: "The clock's brightness while auto brightness is off." },
  display_faces: { group: 'display', doc: 'Faces and clips on the front display while the assistant listens, thinks and talks.' },
  text_duration: { group: 'display', doc: 'How long a message from Home Assistant stays on the display.' },
  animation_duration: { group: 'display', doc: 'How long an expression or ring animation started from Home Assistant plays.' },
  ambilight_fx_brightness: { group: 'display', doc: "The ambilight's brightness under the music effects." },
  follow_up: { group: 'voice', doc: 'Whether the speaker listens again after it answers, for a reply without the wake word.' },
}

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

function Row(p: { label: string; doc: string; control: any; share?: any; note?: any }) {
  return (
    <div class="setting">
      <div class="srow">
        <span class="sname">{p.label}</span>
        <span class="sctl">{p.control}</span>
        {p.share || <span />}
      </div>
      <p class="sdoc">
        {p.doc}
        {p.note}
      </p>
    </div>
  )
}

const isDefault = (k: SettingKey, v: string[]) => v.join('\n') === k.default.join('\n')

// defaultNote: the default, shown only when the value differs from it.
function defaultNote(k: SettingKey) {
  if (k.kind === 'secret' || !k.default.length || k.default.join('') === '' || isDefault(k, k.value)) return null
  const d = k.kind === 'bool' ? (k.default[0] === 'true' ? 'on' : 'off') : k.default.join(', ') + (k.unit ? ' ' + k.unit : '')
  return <span class="muted"> Default: {d}.</span>
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
          <input type="checkbox" checked={v === 'true'} onChange={(e) => commit([String(e.currentTarget.checked)])} />
          <span />
        </label>
      )
    case 'enum':
      return (
        <select value={v} onChange={(e) => commit([e.currentTarget.value])}>
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
      return <textarea class="lines" rows={Math.max(2, value.length + 1)} value={text} onInput={(e) => setText(e.currentTarget.value)} onBlur={done} />
    case 'secret':
      return (
        <input
          type="password"
          autocomplete="new-password"
          placeholder={v ? 'set — type to change' : 'not set'}
          value={text === v ? '' : text}
          onInput={(e) => setText(e.currentTarget.value)}
          onBlur={done}
          onKeyDown={enter}
        />
      )
    default:
      return <input class="wide" value={text} onInput={(e) => setText(e.currentTarget.value)} onBlur={done} onKeyDown={enter} />
  }
}

function groupsOf(s: Settings, pick: (k: SettingKey) => boolean) {
  return s.groups
    .map((g) => ({ id: g.ID, title: g.Title, keys: s.keys.filter((k) => k.group === g.ID && pick(k)) }))
    .filter((g) => g.keys.length > 0)
}

/** The settings that apply at once: the HA options and the runtime config keys. */
export function SettingsTab(p: Ctx & { ents: Entity[]; reload: () => void }) {
  const { s, sync, err, setErr, load, share } = useSettings(p)
  const [saved, setSaved] = useState('')
  async function commit(k: SettingKey, v: string[]) {
    setErr('')
    try {
      await api.putSettings(p.peer.id, p.base, { [k.name]: isDefault(k, v) && !k.set ? [] : v })
      setSaved(k.label)
      setTimeout(() => setSaved(''), 1500)
      await load()
    } catch (e: any) {
      setErr(`${k.label}: ${e.message || e}`)
      await load()
    }
  }
  async function send(e: Entity, payload: string) {
    setErr('')
    try {
      await api.control(p.peer.id, p.base, e.object, payload)
      setTimeout(p.reload, 300)
    } catch (x: any) {
      setErr(`${name(e)}: ${x.message || x}`)
    }
  }
  if (!s) return err ? <p class="err">{err}</p> : <p class="muted">…</p>
  const opts = p.ents.filter((e) => e.writable && e.config.entity_category === 'config')
  const groups = groupsOf(s, (k) => k.runtime && !k.advanced)
  const optGroups = new Set(opts.map((e) => OPTIONS[e.object]?.group || 'display'))
  for (const id of optGroups)
    if (!groups.some((g) => g.id === id)) groups.unshift({ id, title: s.groups.find((g) => g.ID === id)?.Title || id, keys: [] })
  const order = s.groups.map((g) => g.ID)
  groups.sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id))
  return (
    <div class="settings">
      <p class="muted small">
        These apply at once. Settings that need a restart are on the Config page.
        {saved && <span class="ok"> Saved: {saved}.</span>}
      </p>
      {err && <p class="err">{err}</p>}
      {sync && !sync.connected && <p class="muted small">No MQTT broker: sharing with the other speakers is off.</p>}
      {groups.map((g) => (
        <section>
          <h3>{g.title}</h3>
          {opts
            .filter((e) => (OPTIONS[e.object]?.group || 'display') === g.id)
            .map((e) => (
              <Row
                label={name(e)}
                doc={OPTIONS[e.object]?.doc || ''}
                control={<Control e={e} send={(v) => send(e, v)} />}
                share={sync?.option_ids.includes(e.object) && <ShareSwitch sync={sync} kind="option" item={e.object} onShare={share} />}
              />
            ))}
          {g.keys.map((k) => (
            <Row
              label={k.label}
              doc={k.doc}
              note={defaultNote(k)}
              control={<KeyControl k={k} value={k.value} commit={(v) => commit(k, v)} />}
              share={k.sync && <ShareSwitch sync={sync} kind="setting" item={k.name} onShare={share} />}
            />
          ))}
        </section>
      ))}
    </div>
  )
}

/** The settings that need a restart: changed together, applied by one restart on trial. */
export function ConfigTab(p: Ctx) {
  const { s, sync, err, setErr, load, share } = useSettings(p)
  const [pending, setPending] = useState<Record<string, string[]>>({})
  const [text, setText] = useState(false)
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  if (text) return <ConfigEditor peer={p.peer} base={p.base} onClose={() => (setText(false), load())} />
  if (!s) return err ? <p class="err">{err}</p> : <p class="muted">…</p>
  const n = Object.keys(pending).length
  async function apply() {
    setBusy(true)
    setErr('')
    setMsg('')
    try {
      const r = await api.putSettings(p.peer.id, p.base, pending)
      setPending({})
      if (r.restarting) {
        setMsg('Saved. The speaker restarts with the new settings on trial…')
        await waitBack(p.base)
        await api.tryLogin(p.peer.id, p.base)
        setMsg('Back up. The new settings are on trial for a minute: they stay if the speaker is as healthy as before, else the old ones come back by themselves.')
      } else setMsg(r.changed.length ? 'Applied.' : 'No changes.')
      await load()
    } catch (e: any) {
      setErr(e.message || String(e))
    } finally {
      setBusy(false)
    }
  }
  const row = (k: SettingKey) => {
    const v = pending[k.name] ?? k.value
    return (
      <Row
        label={k.label + (pending[k.name] ? ' •' : '')}
        doc={k.doc}
        note={defaultNote(k)}
        control={<KeyControl k={k} value={v} commit={(nv) => setPending({ ...pending, [k.name]: nv })} />}
        share={k.sync && <ShareSwitch sync={sync} kind="setting" item={k.name} onShare={share} />}
      />
    )
  }
  return (
    <div class="settings">
      <div class="applybar">
        <span class="small">
          {n ? `${n} change${n > 1 ? 's' : ''} to apply. ` : ''}These need a restart: the speaker restarts on trial and rolls back by itself if it is worse off.
          {s.trial && <span class="badge">on trial</span>}
        </span>
        <span class="row tight">
          <button class="primary" disabled={!n || busy} onClick={apply}>
            Apply and restart
          </button>
          <button class="ghost" disabled={!n || busy} onClick={() => setPending({})}>
            Discard
          </button>
          <button class="ghost" disabled={busy} onClick={() => setText(true)}>
            Edit the config file
          </button>
        </span>
      </div>
      {err && <p class="err">{err}</p>}
      {msg && <p class="ok">{msg}</p>}
      {groupsOf(s, (k) => !k.runtime && !k.advanced).map((g) => (
        <section>
          <h3>{g.title}</h3>
          {g.keys.map(row)}
        </section>
      ))}
      <details class="advanced">
        <summary>Advanced: paths, ports, test hooks, hardware overrides</summary>
        {groupsOf(s, (k) => !!k.advanced).map((g) => (
          <section>
            <h3>{g.title}</h3>
            {g.keys.map(row)}
          </section>
        ))}
      </details>
    </div>
  )
}

function ConfigEditor({ peer, base, onClose }: Ctx & { onClose: () => void }) {
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
        setMsg(r.changed?.length ? `Applied without a restart: ${r.changed.join(', ')}.` : 'Saved; nothing to apply.')
        await load()
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
          Save
        </button>
        <button class="ghost" onClick={() => setText(orig)} disabled={busy || text === orig}>
          Revert
        </button>
        <button class="ghost" onClick={onClose} disabled={busy}>
          Back to the form
        </button>
      </div>
      <p class="muted small">Changes to settings that apply at once take effect without a restart; any other change restarts the speaker on trial.</p>
      {err && <p class="err">{err}</p>}
      {msg && <p class="ok">{msg}</p>}
    </section>
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
