import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Moment, MomentCatalog, Peer, Show, Step, SyncKind, SyncState } from './api'
import { ShareSwitch } from './settings'

// The Moments tab: what the speaker shows and plays when something happens, and an editor for it
// (docs/display-expressions.md, "Moments"; docs/asset-packs.md). A moment is a list of
// candidates, the first one this speaker has plays: an expression, an animation of its front
// display, a scenario, a sound, a ring effect, or a sequence of steps of its own (each step can
// light the ring, show an animation or text and play a sound at once). Changes are saved on the
// speaker (<cfw>/moments.json) and apply at once.

type Kind = 'expression' | 'anim' | 'scenario' | 'sound' | 'ring' | 'sequence'

function kindOf(s: Show, cat: MomentCatalog): Kind {
  if (typeof s !== 'string') return 'sequence'
  if (s.startsWith('scenario:')) return 'scenario'
  if (s.startsWith('anim:')) return 'anim'
  if (s.startsWith('sound:') || s.startsWith('phrase:')) return 'sound'
  if (s.startsWith('ring:')) return 'ring'
  if ((cat.expressions || []).includes(s)) return 'expression'
  if ((cat.anims || []).some((a) => a.name === s)) return 'anim'
  return 'expression'
}

const refName = (s: string) => s.replace(/^(scenario|anim|sound|phrase|ring):/, '')

/** hasSound: one of a sound name's alternatives ("a|b") is on this speaker. */
const hasSound = (name: string, cat: MomentCatalog) => name.split('|').some((n) => (cat.sounds || []).includes(n))

/** stepParts: what a step does, in words. */
function stepParts(st: Step): string[] {
  const ring = st.ring && `ring ${st.ring}${st.color ? ' ' + hex(st.color) : ''}`
  return [st.express, st.anim || st.matrix, ring, st.text && `“${st.text}”`, st.sound && `sound ${st.sound}`, st.phrase && `say ${st.phrase}`].filter(Boolean) as string[]
}

/** describe: one line for a candidate. */
function describe(s: Show, cat: MomentCatalog): string {
  if (typeof s !== 'string') {
    return s.steps
      .map((st) => {
        const parts = stepParts(st)
        return (parts.length ? parts.join(' + ') : 'pause') + (st.hold ? ' (held)' : st.ms ? ` ${st.ms} ms` : '')
      })
      .join(' → ')
  }
  const k = kindOf(s, cat)
  if (k === 'sound') return `${s.startsWith('phrase:') ? 'say' : 'sound'} ${refName(s)}`
  return k === 'expression' ? s : `${k === 'anim' ? 'animation' : k} ${refName(s)}`
}

/** has: the reference exists on this speaker (the catalogue lists only what it has). */
function has(s: Show, cat: MomentCatalog): boolean {
  if (typeof s !== 'string') return true
  const n = refName(s)
  switch (kindOf(s, cat)) {
    case 'scenario':
      return (cat.scenarios || []).includes(n)
    case 'anim':
      return (cat.anims || []).some((a) => a.name === n)
    case 'sound':
      return hasSound(n, cat)
    case 'ring':
      return (cat.ring_effects || []).includes(n)
    default:
      return (cat.expressions || []).includes(n) || (cat.anims || []).some((a) => a.name === n)
  }
}

export function MomentsTab({ peer, base }: { peer: Peer; base: string }) {
  const [list, setList] = useState<Moment[] | null>(null)
  const [cat, setCat] = useState<MomentCatalog | null>(null)
  const [open, setOpen] = useState('')
  const [err, setErr] = useState('')
  const [sync, setSyncState] = useState<SyncState | null>(null)
  const fail = (e: any) => setErr(e.message || String(e))
  async function loadAll() {
    try {
      const [r, sy] = await Promise.all([api.moments(peer.id, base), api.syncState(peer.id, base)])
      setList(r.moments)
      setCat(r.catalog)
      setSyncState(sy)
    } catch (e: any) {
      fail(e)
    }
  }
  useEffect(() => {
    loadAll()
  }, [peer.id])
  async function share(kind: SyncKind, item: string, on: boolean) {
    setErr('')
    try {
      setSyncState(await api.setSync(peer.id, base, kind, item, on))
    } catch (e: any) {
      fail(e)
    }
  }

  async function play(name: string, draft?: Show[]) {
    setErr('')
    try {
      const r = await api.showMoment(peer.id, base, name, draft)
      if (!r.shown) setErr(`${name}: nothing to show on this speaker`)
    } catch (e: any) {
      fail(e)
    }
  }
  async function save(name: string, shows: Show[] | null) {
    setErr('')
    try {
      const r = await api.setMoment(peer.id, base, name, shows)
      setList(r.moments)
      setOpen('')
    } catch (e: any) {
      fail(e)
    }
  }

  return (
    <section class="card">
      <h3>Moments</h3>
      <p class="muted small">
        What the speaker shows when something happens. Each moment has a list of candidates; the first one this speaker has plays.
        Changes are saved on the speaker and apply at once. A moment on every speaker is shared by the speakers with the same
        kind of display ({cat?.display || '…'}).
      </p>
      {err && <p class="err">{err}</p>}
      {(!list || !cat) && <p class="muted">…</p>}
      {list && cat && (
        <div class="moments">
          {list.map((m) => {
            const cur = m.current ?? m.custom ?? m.default
            const theme = m.from && m.from !== 'custom' ? m.from : ''
            return (
              <div class={'moment' + (open === m.name ? ' open' : '')} key={m.name}>
                <div class="mhead">
                  <div class="mname">
                    <b>{m.name}</b> {theme ? <span class="badge" title={m.custom ? 'Your own is below this pack in the theme' : ''}>{theme}</span> : m.custom && <span class="badge">custom</span>}
                    <div class="muted small">{m.when}</div>
                  </div>
                  <div class="mshows small">
                    {m.shows >= 0 ? describe(cur[m.shows], cat) : <span class="muted">nothing on this speaker</span>}
                  </div>
                  <div class="mbtns">
                    <ShareSwitch sync={sync} kind="moment" item={m.name} onShare={share} />
                    <button disabled={m.shows < 0} onClick={() => play(m.name)}>
                      Play
                    </button>
                    <button class="ghost" onClick={() => setOpen(open === m.name ? '' : m.name)}>
                      {open === m.name ? 'Close' : 'Edit'}
                    </button>
                  </div>
                </div>
                {open === m.name && theme && (
                  <p class="small warn-text">
                    The pack {theme} sets this moment and sits above your own: what you save here shows once “Your own” is above it (Behaviour → Packs),
                    or start from the pack's version there with “Copy moments”.
                  </p>
                )}
                {open === m.name && (
                  <MomentEditor
                    m={m}
                    cat={cat}
                    onPlay={(d) => play(m.name, d)}
                    onSave={(d) => save(m.name, d)}
                    onReset={() => save(m.name, null)}
                  />
                )}
              </div>
            )
          })}
        </div>
      )}
    </section>
  )
}

const clone = (s: Show[]): Show[] => JSON.parse(JSON.stringify(s))

function MomentEditor(p: { m: Moment; cat: MomentCatalog; onPlay: (d: Show[]) => void; onSave: (d: Show[]) => void; onReset: () => void }) {
  const { m, cat } = p
  const [draft, setDraft] = useState<Show[]>(() => clone(m.custom ?? m.default))
  const stage = ['listening', 'thinking', 'speaking', 'waiting'].includes(m.name)
  const set = (i: number, s: Show) => setDraft(draft.map((x, k) => (k === i ? s : x)))
  const move = (i: number, d: number) => {
    const n = [...draft]
    ;[n[i], n[i + d]] = [n[i + d], n[i]]
    setDraft(n)
  }
  const blank = (k: Kind): Show => {
    switch (k) {
      case 'expression':
        return (cat.expressions || [])[0] || ''
      case 'anim':
        return 'anim:' + ((cat.anims || [])[0]?.name || '')
      case 'scenario':
        return 'scenario:' + ((cat.scenarios || [])[0] || '')
      case 'sound':
        return 'sound:' + ((cat.sounds || [])[0] || '')
      case 'ring':
        return 'ring:' + ((cat.ring_effects || [])[0] || '')
      default:
        return { steps: [{ express: (cat.expressions || [])[0] || '', hold: stage }] }
    }
  }
  return (
    <div class="medit">
      {draft.length === 0 && <p class="muted small">No candidates: this moment shows nothing.</p>}
      {draft.map((s, i) => (
        <div class="cand">
          <div class="row">
            <span class="k">{i === 0 ? 'Show' : 'else'}</span>
            <select value={kindOf(s, cat)} onChange={(e) => set(i, blank(e.currentTarget.value as Kind))}>
              <option value="expression">expression</option>
              <option value="anim">animation</option>
              <option value="scenario">scenario</option>
              <option value="sound">sound</option>
              <option value="ring">ring</option>
              <option value="sequence">sequence</option>
            </select>
            {typeof s === 'string' && <RefPicker s={s} kind={kindOf(s, cat)} cat={cat} onChange={(v) => set(i, v)} />}
            {!has(s, cat) && <span class="err small">not on this speaker</span>}
            <span class="grow" />
            <button class="ghost icon" disabled={i === 0} onClick={() => move(i, -1)} title="Earlier" aria-label="Earlier">
              ↑
            </button>
            <button class="ghost icon" disabled={i === draft.length - 1} onClick={() => move(i, 1)} title="Later" aria-label="Later">
              ↓
            </button>
            <button class="ghost icon" onClick={() => setDraft(draft.filter((_, k) => k !== i))} title="Remove" aria-label="Remove">
              ✕
            </button>
          </div>
          {typeof s !== 'string' && <SequenceEditor steps={s.steps} cat={cat} stage={stage} onChange={(steps) => set(i, { steps })} />}
        </div>
      ))}
      <div class="row">
        <button class="ghost" onClick={() => setDraft([...draft, blank('expression')])}>
          + {draft.length ? 'Fallback' : 'Candidate'}
        </button>
      </div>
      <div class="row">
        <button class="primary" onClick={() => p.onSave(draft)}>
          Save
        </button>
        <button onClick={() => p.onPlay(draft)}>Play draft</button>
        {m.custom && (
          <button class="ghost" onClick={p.onReset}>
            Reset to default
          </button>
        )}
        <span class="muted small">Default: {m.default.length ? m.default.map((s) => describe(s, cat)).join(', else ') : 'nothing'}</span>
      </div>
    </div>
  )
}

function RefPicker({ s, kind, cat, onChange }: { s: string; kind: Kind; cat: MomentCatalog; onChange: (s: string) => void }) {
  const n = refName(s)
  if (kind === 'scenario')
    return (
      <select value={n} onChange={(e) => onChange('scenario:' + e.currentTarget.value)}>
        {(cat.scenarios || []).map((x) => (
          <option value={x}>{x}</option>
        ))}
      </select>
    )
  if (kind === 'anim') return <AnimPicker value={n} cat={cat} onChange={(v) => onChange('anim:' + v)} />
  if (kind === 'sound') return <SoundPicker name={n} phrase={s.startsWith('phrase:')} cat={cat} onChange={(v, ph) => onChange((ph ? 'phrase:' : 'sound:') + v)} />
  if (kind === 'ring')
    return (
      <select aria-label="Ring effect" value={n} onChange={(e) => onChange('ring:' + e.currentTarget.value)}>
        {(cat.ring_effects || []).map((x) => (
          <option value={x}>{x}</option>
        ))}
      </select>
    )
  return (
    <select value={n} onChange={(e) => onChange(e.currentTarget.value)}>
      {!(cat.expressions || []).includes(n) && <option value={n}>{n}</option>}
      {(cat.expressions || []).map((x) => (
        <option value={x}>{x}</option>
      ))}
    </select>
  )
}

/** AnimPicker: the front display's animations (a hundred on Station 3), typed to filter. */
function AnimPicker({ value, cat, onChange }: { value: string; cat: MomentCatalog; onChange: (v: string) => void }) {
  const id = 'anims-' + cat.display
  return (
    <>
      <input class="anim" list={id} value={value} placeholder="animation" onChange={(e) => onChange(e.currentTarget.value)} />
      <datalist id={id}>
        {(cat.anims || []).map((a) => (
          <option value={a.name}>{`${(a.ms / 1000).toFixed(1)} s${a.loop ? ', loops' : ''}`}</option>
        ))}
      </datalist>
    </>
  )
}

type StepKind = 'express' | 'anim' | 'ring' | 'text' | 'sound' | 'pause'
function stepKind(st: Step): StepKind {
  if (st.express) return 'express'
  if (st.anim || st.matrix) return 'anim'
  if (st.ring) return 'ring'
  if (st.text !== undefined) return 'text'
  return st.sound || st.phrase ? 'sound' : 'pause'
}

/** hex and rgb convert a ring colour between the step's [r,g,b] and a colour input's #rrggbb. */
const hex = (c: number[]) => '#' + c.map((v) => v.toString(16).padStart(2, '0')).join('')
const rgb = (h: string) => [1, 3, 5].map((i) => parseInt(h.slice(i, i + 2), 16))

/** SoundPicker: a sound name (or none) and whether it is said as speech (stopping other playback). */
function SoundPicker(p: { name: string; phrase: boolean; cat: MomentCatalog; none?: boolean; onChange: (name: string, phrase: boolean) => void }) {
  const sounds = p.cat.sounds || []
  return (
    <>
      <select aria-label="Sound" value={p.name} onChange={(e) => p.onChange(e.currentTarget.value, p.phrase)}>
        {p.none && <option value="">no sound</option>}
        {p.name && !sounds.includes(p.name) && <option value={p.name}>{p.name}</option>}
        {sounds.map((x) => (
          <option value={x}>{x}</option>
        ))}
      </select>
      {p.name && (
        <label class="inline small" title="Stop other playback and say it, like a spoken line">
          <input type="checkbox" checked={p.phrase} onChange={(e) => p.onChange(p.name, e.currentTarget.checked)} /> as speech
        </label>
      )}
    </>
  )
}

function SequenceEditor({ steps, cat, stage, onChange }: { steps: Step[]; cat: MomentCatalog; stage: boolean; onChange: (s: Step[]) => void }) {
  const set = (i: number, st: Step) => onChange(steps.map((x, k) => (k === i ? st : x)))
  const move = (i: number, d: number) => {
    const n = [...steps]
    ;[n[i], n[i + d]] = [n[i + d], n[i]]
    const lastHold = steps[steps.length - 1].hold // hold belongs to whichever step is last
    onChange(n.map((x, k) => ({ ...x, hold: k === n.length - 1 ? lastHold : undefined })))
  }
  const animMs = (name?: string) => (cat.anims || []).find((a) => a.name === name)?.ms
  const blank = (k: StepKind): Step => {
    switch (k) {
      case 'anim':
        return { anim: (cat.anims || [])[0]?.name || '' }
      case 'ring':
        return { ring: (cat.ring_effects || [])[0] || '', ms: 1000 }
      case 'text':
        return { text: '', ms: 3000 }
      case 'sound':
        return { sound: (cat.sounds || [])[0] || '' }
      case 'pause':
        return { ms: 1000 }
      default:
        return { express: (cat.expressions || [])[0] || '' }
    }
  }
  const last = steps.length - 1
  return (
    <div class="steps">
      {steps.map((st, i) => {
        const k = stepKind(st)
        const hold = i === last && !!st.hold
        const auto = k === 'anim' ? animMs(st.anim || st.matrix) : undefined
        const snd = st.sound || st.phrase || ''
        const setSound = (name: string, phrase: boolean) => set(i, { ...st, sound: name && !phrase ? name : undefined, phrase: name && phrase ? name : undefined })
        const colour = k === 'ring' && (cat.colour_effects || []).includes(st.ring || '')
        return (
          <div class="row step">
            <span class="k">{i + 1}.</span>
            <select value={k} onChange={(e) => set(i, { ...blank(e.currentTarget.value as StepKind), hold: st.hold })}>
              <option value="express">expression</option>
              <option value="anim">animation</option>
              <option value="ring">ring</option>
              <option value="text">text</option>
              <option value="sound">sound</option>
              <option value="pause">pause</option>
            </select>
            {k === 'express' && (
              <select value={st.express} onChange={(e) => set(i, { ...st, express: e.currentTarget.value })}>
                {(cat.expressions || []).map((x) => (
                  <option value={x}>{x}</option>
                ))}
              </select>
            )}
            {k === 'anim' && <AnimPicker value={st.anim || st.matrix || ''} cat={cat} onChange={(v) => set(i, { ...st, anim: v, matrix: undefined })} />}
            {k === 'ring' && (
              <select value={st.ring} onChange={(e) => set(i, { ...st, ring: e.currentTarget.value })}>
                {(cat.ring_effects || []).map((x) => (
                  <option value={x}>{x}</option>
                ))}
              </select>
            )}
            {k === 'text' && <input value={st.text} placeholder="text" onInput={(e) => set(i, { ...st, text: e.currentTarget.value })} />}
            {colour && (
              <input type="color" aria-label="Ring colour" value={hex(st.color || [255, 255, 255])} onInput={(e) => set(i, { ...st, color: rgb(e.currentTarget.value) })} />
            )}
            {colour && st.ring !== 'solid' && (
              <input
                class="ms"
                type="number"
                min="100"
                step="100"
                aria-label="Period, ms"
                title="The effect's period in ms; empty = its own"
                value={st.period || ''}
                placeholder="period"
                onInput={(e) => set(i, { ...st, period: +e.currentTarget.value || undefined })}
              />
            )}
            {k === 'anim' && (
              <select aria-label="When the animation is not on this display" title="When the animation is not on this display" value={st.else || ''} onChange={(e) => set(i, { ...st, else: e.currentTarget.value || undefined })}>
                <option value="">else nothing</option>
                <option value="blank">else blank</option>
                <option value="setup">else setup scene</option>
                <option value="alarm">else blinking 00:00</option>
              </select>
            )}
            {k !== 'pause' && <SoundPicker name={snd} phrase={!!st.phrase} cat={cat} none={k !== 'sound'} onChange={setSound} />}
            {!hold && (
              <input
                class="ms"
                type="number"
                min="0"
                step="100"
                value={st.ms || ''}
                placeholder={auto ? String(auto) : 'auto'}
                title="Milliseconds; empty = the animation's (or the sound's) own length"
                onInput={(e) => set(i, { ...st, ms: +e.currentTarget.value || undefined })}
              />
            )}
            {!hold && <span class="muted small">ms</span>}
            {i === last && (
              <label class="inline small" title={stage ? 'A voice stage lasts until the next one: keep its last step' : 'Stay until the moment ends'}>
                <input type="checkbox" checked={hold} onChange={(e) => set(i, { ...st, hold: e.currentTarget.checked || undefined, ms: undefined })} /> hold
              </label>
            )}
            <span class="grow" />
            <button class="ghost icon" disabled={i === 0} onClick={() => move(i, -1)} title="Earlier" aria-label="Step earlier">
              ↑
            </button>
            <button class="ghost icon" disabled={i === last} onClick={() => move(i, 1)} title="Later" aria-label="Step later">
              ↓
            </button>
            <button class="ghost icon" disabled={steps.length === 1} onClick={() => onChange(steps.filter((_, x) => x !== i))} title="Remove" aria-label="Remove the step">
              ✕
            </button>
          </div>
        )
      })}
      <div class="row">
        <button
          class="ghost"
          onClick={() => {
            const n = steps.map((x) => ({ ...x, hold: undefined }))
            onChange([...n, { ...blank('anim'), hold: steps[last]?.hold }])
          }}
        >
          + Step
        </button>
        {stage && !steps[last]?.hold && <span class="small err">A voice stage should hold its last step, or the speaker goes blank while it still listens.</span>}
      </div>
    </div>
  )
}
