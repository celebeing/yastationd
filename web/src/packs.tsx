import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Pack, Packs, Peer } from './api'
import { toWav } from './assets'

// Behaviour → Packs and Behaviour → Sounds (docs/asset-packs.md). A pack carries sounds, animations
// and moments; the theme stack is the packs in force, top first, over the default theme. The
// owner's own moments, sounds and animations are the pack "custom".

const NAME = /^[a-z0-9][a-z0-9_-]{0,39}$/
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
  const [renaming, setRenaming] = useState('')
  const [newName, setNewName] = useState('')
  if (!p) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  const byId = Object.fromEntries(p.packs.map((k) => [k.id, k]))
  // The stack shown is the one this speaker wears (theme_here when that is in force).
  const setStack = (s: string[]) => act(() => api.packStack(peer.id, base, s, p.here))
  const move = (i: number, d: number) => {
    const s = [...p.stack]
    ;[s[i], s[i + d]] = [s[i + d], s[i]]
    setStack(s)
  }
  const off = (id: string) => setStack(p.stack.filter((x) => x !== id))
  const on = (id: string) => act(() => api.packAction(peer.id, base, id, 'apply', p.here))
  const mine = p.packs.filter((k) => k.editable)
  const installed = p.packs.filter((k) => !k.editable)
  const exportOne = async (k: Pack) => {
    setErr('')
    try {
      const as = k.id === 'custom' ? 'my-theme' : ''
      const r = await api.exportPack(peer.id, base, k.id, as)
      save(r.blob, r.name)
    } catch (e: any) {
      setErr(e.message || String(e))
    }
  }
  const remove = (k: Pack) =>
    confirmDel === k.id ? (
      <button class="danger" disabled={busy} onClick={() => act(() => api.deletePack(peer.id, base, k.id))}>
        Remove {k.name}
      </button>
    ) : (
      <button class="ghost" disabled={busy} onClick={() => setConfirmDel(k.id)}>
        Remove
      </button>
    )
  const onOff = (k: Pack) =>
    k.position >= 0 ? (
      <button class="ghost" disabled={busy} onClick={() => off(k.id)} title="Stop wearing it (it stays installed)">
        Turn off
      </button>
    ) : (
      <button disabled={busy || !k.fits} onClick={() => on(k.id)} title="Wear it, on top">
        Turn on
      </button>
    )
  return (
    <div class="assets">
      <section class="card">
        <h3>Theme</h3>
        <p class="muted small">
          What this speaker wears, the top first: a moment, a sound or an animation comes from the highest pack that has it, else from the built-in
          behaviour, always at the bottom. Your packs (marked “yours”) can sit anywhere; put one on top to have the last word.
        </p>
        {err && <p class="err">{err}</p>}
        <ol class="stack">
          {p.stack.map((id, i) => (
            <li class="row">
              <span class="grow">
                <b>{byId[id]?.name || id}</b> {byId[id]?.editable && <span class="badge">yours</span>}{' '}
                <span class="muted small">{byId[id] ? what(byId[id]) : 'not on this speaker: upload it below'}</span>
              </span>
              <button class="ghost icon" disabled={busy || i === 0} onClick={() => move(i, -1)} title="Higher" aria-label={`Move ${id} up`}>
                ↑
              </button>
              <button class="ghost icon" disabled={busy || i === p.stack.length - 1} onClick={() => move(i, 1)} title="Lower" aria-label={`Move ${id} down`}>
                ↓
              </button>
              <button class="ghost icon" disabled={busy} onClick={() => off(id)} title="Turn off" aria-label={`Turn ${id} off`}>
                ✕
              </button>
            </li>
          ))}
          <li class="row muted">Built-in behaviour</li>
        </ol>
        {p.missing?.length ? <p class="small err">Not on this speaker: {p.missing.join(', ')}.</p> : null}
      </section>

      <section class="card">
        <h3>Your packs</h3>
        <p class="muted small">
          Packs you make here: the Moments page edits one of them, the Sounds page adds to one. Export one to keep it or to install it on another
          speaker.
        </p>
        <table class="assetlist">
          <tbody>
            {mine.map((k) => (
              <tr>
                <td>
                  {renaming === k.id ? (
                    <form
                      class="row"
                      onSubmit={(e) => {
                        e.preventDefault()
                        const v = (e.currentTarget.elements.namedItem('name') as HTMLInputElement).value
                        setRenaming('')
                        act(() => api.renamePack(peer.id, base, k.id, v))
                      }}
                    >
                      <input name="name" aria-label="Pack name" value={k.name} autofocus />
                      <button type="submit">Save</button>
                    </form>
                  ) : (
                    <b>{k.name}</b>
                  )}{' '}
                  {k.id === p.target && <span class="badge">edited</span>}
                  <div class="muted small">
                    {what(k)} · {fmtKB(k.bytes)} · {k.position >= 0 ? 'on' : 'off'}
                  </div>
                </td>
                <td class="actions">
                  {!!k.anims?.length && (
                    <a class="button ghost" href={`#led/@${k.id}`} title="Play its animations on the LED page">
                      Animations
                    </a>
                  )}
                  {onOff(k)}
                  <button class="ghost" disabled={busy} onClick={() => setRenaming(renaming === k.id ? '' : k.id)}>
                    Rename
                  </button>
                  <button class="ghost" disabled={busy} onClick={() => exportOne(k)} title="Download it as a pack archive">
                    Export
                  </button>
                  {remove(k)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <form
          class="row"
          onSubmit={(e) => {
            e.preventDefault()
            act(() => api.newPack(peer.id, base, newName)).then(() => setNewName(''))
          }}
        >
          <input aria-label="New pack name" placeholder="name of a new pack" value={newName} onInput={(e) => setNewName(e.currentTarget.value)} />
          <button type="submit" disabled={busy}>
            New pack
          </button>
        </form>
      </section>

      <section class="card">
        <h3>Installed</h3>
        <p class="muted small">Packs from elsewhere, read-only here: “Copy to my pack” makes one of yours from it, in its place in the theme.</p>
        <table class="assetlist">
          <tbody>
            {installed.map((k) => (
              <tr>
                <td>
                  <b>{k.name}</b> <span class="muted small">{[k.version, k.author].filter(Boolean).join(', ')}</span>
                  <div class="muted small">
                    {what(k)} · {fmtKB(k.bytes)} · {k.position >= 0 ? 'on' : 'off'}
                    {k.license ? ` · ${k.license}` : ''}
                  </div>
                </td>
                <td class="actions">
                  {!!k.anims?.length && (
                    <a class="button ghost" href={`#led/@${k.id}`} title="Play its animations on the LED page, without turning it on">
                      Animations
                    </a>
                  )}
                  {onOff(k)}
                  <button class="ghost" disabled={busy} onClick={() => act(() => api.packAction(peer.id, base, k.id, 'copy'))} title="Make an editable copy, in its place in the theme">
                    Copy to my pack
                  </button>
                  {remove(k)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {installed.length === 0 && <p class="muted small">No packs yet. Packs live in their own repositories; upload one below.</p>}
      </section>

      <AddPack peer={peer} base={base} max={p.max_bytes} onDone={load} onErr={setErr} />
    </div>
  )
}

type PackImport = { file: string; error?: string }

function AddPack(p: { peer: Peer; base: string; max: number; onDone: () => void; onErr: (e: string) => void }) {
  const [files, setFiles] = useState<File[]>([])
  const [active, setActive] = useState(-1)
  const [pct, setPct] = useState(-1)
  const [imports, setImports] = useState<PackImport[]>([])
  async function go() {
    if (!files.length) return
    setImports([])
    p.onErr('')
    let installed = false
    for (const [i, file] of files.entries()) {
      setActive(i)
      setPct(0)
      try {
        if (file.size > p.max) throw new Error(`over ${p.max >> 20} MB`)
        await api.uploadAsset(p.peer.id, p.base, '/api/packs', file, (n) => setPct(n / file.size))
        installed = true
        setImports((r) => [...r, { file: file.name }])
      } catch (e: unknown) {
        const error = e instanceof Error ? e.message : String(e)
        setImports((r) => [...r, { file: file.name, error }])
      }
    }
    setActive(-1)
    setPct(-1)
    setFiles([])
    if (installed) p.onDone()
  }
  const importing = active >= 0
  const selected = files.length === 1 ? '1 pack selected' : `${files.length} packs selected`
  return (
    <section class="card">
      <h3>Import packs</h3>
      <p class="muted small">
        Choose one or more .zip packs. Each is checked and installed separately, so a rejected pack does not stop the rest.
      </p>
      <div class="row">
        <input
          type="file"
          accept=".zip,application/zip"
          multiple
          aria-label="Pack archives"
          disabled={importing}
          onChange={(e) => {
            setFiles(Array.from(e.currentTarget.files || []))
            e.currentTarget.value = ''
          }}
        />
        <button class="primary" disabled={!files.length || importing} onClick={go}>
          {importing ? 'Importing…' : 'Import'}
        </button>
      </div>
      {files.length > 0 && !importing && <p class="small muted">{selected}</p>}
      {importing && (
        <div class="progress" aria-live="polite">
          Importing {active + 1} of {files.length}: {files[active].name} <span class="gauge">
            <i style={{ width: `${Math.round(pct * 100)}%` }} />
          </span>
        </div>
      )}
      {imports.length > 0 && (
        <ul class="small">
          {imports.map((r) => (
            <li class={r.error ? 'err' : ''}>
              {r.file}: {r.error || 'installed.'}
            </li>
          ))}
        </ul>
      )}
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
  const [into, setInto] = useState('')
  if (!p) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  const mine = p.packs.filter((k) => k.editable)
  const target = mine.find((k) => k.id === into) || mine.find((k) => k.id === p.target) || mine[0]
  const own = target?.sounds || []
  const name = (id: string) => p.packs.find((k) => k.id === id)?.name || id
  const from = (n: string) => {
    const id = p.stack.find((id) => p.packs.find((k) => k.id === id)?.sounds?.includes(n))
    return id ? name(id) : 'stock'
  }
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
          The sounds the moments play by name: the stock ones and the worn packs'. A sound with a stock name in a pack replaces it while that pack is
          on. Your sounds go into one of your packs:
        </p>
        <div class="row">
          <span class="k">Your pack</span>
          <select aria-label="The pack your sounds go into" value={target?.id} onChange={(e) => setInto(e.currentTarget.value)}>
            {mine.map((k) => (
              <option value={k.id}>
                {k.name}
                {k.position < 0 ? ' (off)' : ''}
              </option>
            ))}
          </select>
        </div>
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
                    <button class="ghost" disabled={busy} onClick={() => act(() => api.deletePackAsset(peer.id, base, target!.id, 'sounds', n)).then(reloadAll)}>
                      Remove from {target!.name}
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
        pack={target?.id || 'custom'}
        packName={target?.name || 'Your own'}
        onDone={() => {
          load()
          reloadAll()
        }}
      />
    </div>
  )
}

function AddSound(p: { peer: Peer; base: string; pack: string; packName: string; onDone: () => void }) {
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
      await api.uploadAsset(p.peer.id, p.base, api.packAssetPath(p.pack, 'sounds', name), body, () => {})
      setMsg(`${name}: added to ${p.packName}.`)
      setFile(null)
      p.onDone()
    } catch (e: any) {
      setMsg(e.message || String(e))
    }
    setBusy(false)
  }
  return (
    <section class="card">
      <h3>Add a sound to {p.packName}</h3>
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
