import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Pack, Packs, Peer } from './api'
import { toWav } from './assets'

// Behaviour → Packs and Behaviour → Sounds (docs/asset-packs.md). A pack carries sounds, animations
// and moments; the theme stack is the packs in force, top first, over the default theme. The
// owner's own moments, sounds and animations are the pack "custom".

const NAME = /^[a-z0-9][a-z0-9_-]{0,39}$/
const PACK_ID = /^[a-z0-9][a-z0-9-]{0,39}$/
const fmtKB = (b: number) => (b >= 1 << 20 ? (b / 1048576).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1024)) + ' KB')

function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 10000)
}

/** usePacks loads the packs state and runs an action that returns the new one. */
function usePacks(peer: Peer, base: string) {
  const [p, setP] = useState<Packs | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const load = () => api.packs(peer.id, base).then(setP, (e) => setErr(e.message || String(e)))
  useEffect(() => {
    load()
  }, [peer.id, base])
  async function act(f: () => Promise<Packs>) {
    setBusy(true)
    setErr('')
    try {
      setP(await f())
    } catch (e: any) {
      setErr(e.message || String(e))
    }
    setBusy(false)
  }
  return { p, err, setErr, busy, act, load }
}

function what(k: Pack): string {
  const n = (a: string[] | null, one: string) => (a?.length ? `${a.length} ${one}${a.length > 1 ? 's' : ''}` : '')
  return [n(k.moments, 'moment'), n(k.sounds, 'sound'), n(k.anims, 'animation')].filter(Boolean).join(', ') || 'nothing for this speaker'
}

export function PacksTab({ peer, base }: { peer: Peer; base: string }) {
  const { p, err, setErr, busy, act, load } = usePacks(peer, base)
  const [confirmDel, setConfirmDel] = useState('')
  if (!p) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  const byId = Object.fromEntries(p.packs.map((k) => [k.id, k]))
  // The stack shown is the one this speaker wears: its own (here) or the shared theme.
  const move = (i: number, d: number) => {
    const s = [...p.stack]
    ;[s[i], s[i + d]] = [s[i + d], s[i]]
    act(() => api.packStack(peer.id, base, s, p.here))
  }
  const takeOut = (id: string) => act(() => api.packStack(peer.id, base, p.stack.filter((x) => x !== id), p.here))
  return (
    <div class="assets">
      <section class="card">
        <h3>Theme</h3>
        <p class="muted small">
          The packs in force, the top one first: a moment, a sound or an animation comes from the first pack that has it, else from the default
          theme. Your own moments and sounds are “Your own”.
        </p>
        <p class="muted small">The theme is this speaker's own: packs are installed and worn on each speaker by itself.</p>
        {err && <p class="err">{err}</p>}
        <ol class="stack">
          {p.stack.map((id, i) => (
            <li class="row">
              <span class="grow">
                <b>{byId[id]?.name || id}</b> <span class="muted small">{byId[id] ? what(byId[id]) : 'not on this speaker: upload it below'}</span>
              </span>
              <button class="ghost icon" disabled={busy || i === 0} onClick={() => move(i, -1)} title="Higher" aria-label={`Move ${id} up`}>
                ↑
              </button>
              <button class="ghost icon" disabled={busy || i === p.stack.length - 1} onClick={() => move(i, 1)} title="Lower" aria-label={`Move ${id} down`}>
                ↓
              </button>
              {id !== 'custom' && (
                <button class="ghost icon" disabled={busy} onClick={() => takeOut(id)} title="Take out of the theme" aria-label={`Take ${id} out`}>
                  ✕
                </button>
              )}
            </li>
          ))}
          <li class="row muted">The default theme</li>
        </ol>
        {p.missing?.length ? <p class="small err">Not on this speaker: {p.missing.join(', ')}.</p> : null}
      </section>

      <section class="card">
        <h3>Installed</h3>
        <table class="assetlist">
          <tbody>
            {p.packs
              .filter((k) => k.id !== 'custom')
              .map((k) => (
                <tr>
                  <td>
                    <b>{k.name}</b> <span class="muted small">{[k.version, k.author].filter(Boolean).join(', ')}</span>
                    <div class="muted small">
                      {what(k)} · {fmtKB(k.bytes)}
                      {k.license ? ` · ${k.license}` : ''}
                    </div>
                  </td>
                  <td class="actions">
                    {!!k.anims?.length && (
                      <a class="button ghost" href={`#led/@${k.id}`} title="Play its animations on the LED page, without applying it">
                        Animations
                      </a>
                    )}
                    {k.position !== 0 && (
                      <button disabled={busy || !k.fits} onClick={() => act(() => api.packAction(peer.id, base, k.id, 'apply', p.here))} title="Put it on top of this speaker's theme">
                        Apply
                      </button>
                    )}
                    {!!k.moments?.length && (
                      <button
                        class="ghost"
                        disabled={busy}
                        onClick={() => act(() => api.packAction(peer.id, base, k.id, 'import'))}
                        title="Copy its moments into your own, to edit them on the Moments page"
                      >
                        Copy moments
                      </button>
                    )}
                    {confirmDel === k.id ? (
                      <button class="danger" disabled={busy} onClick={() => act(() => api.deletePack(peer.id, base, k.id))}>
                        Remove {k.name}
                      </button>
                    ) : (
                      <button class="ghost" disabled={busy} onClick={() => setConfirmDel(k.id)}>
                        Remove
                      </button>
                    )}
                  </td>
                </tr>
              ))}
          </tbody>
        </table>
        {p.packs.length === 1 && <p class="muted small">No packs yet. Packs live in their own repositories; upload one below.</p>}
      </section>

      <AddPack peer={peer} base={base} max={p.max_bytes} onDone={load} onErr={setErr} />
      <ExportTheme peer={peer} base={base} />
    </div>
  )
}

function AddPack(p: { peer: Peer; base: string; max: number; onDone: () => void; onErr: (e: string) => void }) {
  const [file, setFile] = useState<File | null>(null)
  const [pct, setPct] = useState(-1)
  const [msg, setMsg] = useState('')
  async function go() {
    if (!file) return
    setMsg('')
    p.onErr('')
    try {
      if (file.size > p.max) throw new Error(`over ${p.max >> 20} MB`)
      setPct(0)
      await api.uploadAsset(p.peer.id, p.base, '/api/packs', file, (n) => setPct(n / file.size))
      setMsg(`${file.name}: installed. Apply it to wear it.`)
      setFile(null)
      p.onDone()
    } catch (e: any) {
      setMsg(e.message || String(e))
    }
    setPct(-1)
  }
  return (
    <section class="card">
      <h3>Add a pack</h3>
      <p class="muted small">A pack is a .zip with pack.json at its root (docs/asset-packs.md). Its files are checked on the speaker before it is installed.</p>
      <div class="row">
        <input type="file" accept=".zip,application/zip" aria-label="Pack archive" onChange={(e) => setFile(e.currentTarget.files?.[0] || null)} />
        <button class="primary" disabled={!file || pct >= 0} onClick={go}>
          Upload
        </button>
      </div>
      {pct >= 0 && (
        <div class="progress">
          Uploading <span class="gauge">
            <i style={{ width: `${Math.round(pct * 100)}%` }} />
          </span>
        </div>
      )}
      {msg && <p class="small">{msg}</p>}
    </section>
  )
}

function ExportTheme({ peer, base }: { peer: Peer; base: string }) {
  const [id, setId] = useState('my-theme')
  const [name, setName] = useState('')
  const [msg, setMsg] = useState('')
  async function go() {
    setMsg('')
    try {
      const r = await api.exportTheme(peer.id, base, id, name)
      save(r.blob, r.name)
      setMsg(`${r.name}, ${fmtKB(r.blob.size)}`)
    } catch (e: any) {
      setMsg(e.message || String(e))
    }
  }
  return (
    <section class="card">
      <h3>Save your own as a pack</h3>
      <p class="muted small">Your moments, sounds and animations as a pack archive: keep it, or install it on another speaker.</p>
      <div class="row">
        <input aria-label="Pack id" value={id} placeholder="id" onInput={(e) => setId(e.currentTarget.value)} />
        <input aria-label="Pack name" value={name} placeholder="name" onInput={(e) => setName(e.currentTarget.value)} />
        <button class="primary" disabled={!PACK_ID.test(id) || id === 'custom'} onClick={go}>
          Download
        </button>
      </div>
      {msg && <p class="small">{msg}</p>}
    </section>
  )
}

export function SoundsTab({ peer, base }: { peer: Peer; base: string }) {
  const { p, err, setErr, busy, act, load } = usePacks(peer, base)
  const [all, setAll] = useState<string[]>([])
  const reloadAll = () => api.moments(peer.id, base).then((m) => setAll(m.catalog.sounds || []), () => {})
  useEffect(() => {
    reloadAll()
  }, [peer.id, base])
  if (!p) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  const own = p.packs.find((k) => k.id === 'custom')?.sounds || []
  const from = (n: string) => (own.includes(n) ? 'yours' : p.stack.find((id) => p.packs.find((k) => k.id === id)?.sounds?.includes(n)) || 'stock')
  async function play(n: string) {
    setErr('')
    try {
      await api.showMoment(peer.id, base, 'volume_change', ['sound:' + n])
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  return (
    <div class="assets">
      <section class="card">
        <h3>Sounds</h3>
        <p class="muted small">
          The sounds the moments play by name: the stock ones, the theme's, yours. A sound of yours with a stock name replaces it while “Your own”
          is in the theme above the default.
        </p>
        {err && <p class="err">{err}</p>}
        <table class="assetlist">
          <tbody>
            {all.map((n) => (
              <tr>
                <td>
                  <b>{n}</b> <span class="muted small">{from(n)}</span>
                </td>
                <td class="actions">
                  <button class="ghost" onClick={() => play(n)}>
                    Play
                  </button>
                  {own.includes(n) && (
                    <button class="ghost" disabled={busy} onClick={() => act(() => api.deleteCustomAsset(peer.id, base, 'sounds', n)).then(reloadAll)}>
                      Remove
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      <AddSound
        peer={peer}
        base={base}
        onDone={() => {
          load()
          reloadAll()
        }}
      />
    </div>
  )
}

function AddSound(p: { peer: Peer; base: string; onDone: () => void }) {
  const [file, setFile] = useState<File | null>(null)
  const [name, setName] = useState('')
  const [msg, setMsg] = useState('')
  const [busy, setBusy] = useState(false)
  async function go() {
    if (!file) return
    setMsg('')
    setBusy(true)
    try {
      const body = file.type === 'audio/wav' || file.name.toLowerCase().endsWith('.wav') ? file : await toWav(file)
      await api.uploadAsset(p.peer.id, p.base, `/api/packs/custom/sounds/${name}`, body, () => {})
      setMsg(`${name}: added.`)
      setFile(null)
      p.onDone()
    } catch (e: any) {
      setMsg(e.message || String(e))
    }
    setBusy(false)
  }
  return (
    <section class="card">
      <h3>Add a sound</h3>
      <p class="muted small">Any audio file the browser can play; it is converted to WAV here. Name it like a stock sound to replace that one.</p>
      <div class="row">
        <input
          type="file"
          accept="audio/*"
          aria-label="Sound file"
          onChange={(e) => {
            const f = e.currentTarget.files?.[0] || null
            setFile(f)
            if (f && !name)
              setName(
                f.name
                  .replace(/\.[^.]+$/, '')
                  .toLowerCase()
                  .replace(/[^a-z0-9_-]+/g, '_')
                  .slice(0, 40),
              )
          }}
        />
        <input aria-label="Sound name" value={name} placeholder="name" onInput={(e) => setName(e.currentTarget.value)} />
        <button class="primary" disabled={!file || !NAME.test(name) || busy} onClick={go}>
          {busy ? 'Uploading…' : 'Add'}
        </button>
      </div>
      {msg && <p class="small">{msg}</p>}
    </section>
  )
}
