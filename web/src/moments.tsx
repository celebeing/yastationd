import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Moment, MomentCatalog, Peer, Show, Step, SyncKind, SyncState } from './api'
import { ShareSwitch } from './settings'

// The Moments tab: what the speaker shows when something happens, and an editor for it
// (docs/display-expressions.md, "Moments"). A moment is a list of candidates, the first one this
// speaker has plays: an expression, an animation of its front display, a scenario, or a sequence
// of steps of its own. Changes are saved on the speaker (<cfw>/moments.json) and apply at once.

type Kind = 'expression' | 'anim' | 'scenario' | 'sequence'

function kindOf(s: Show, cat: MomentCatalog): Kind {
  if (typeof s !== 'string') return 'sequence'
  if (s.startsWith('scenario:')) return 'scenario'
  if (s.startsWith('anim:')) return 'anim'
  if ((cat.expressions || []).includes(s)) return 'expression'
  if ((cat.anims || []).some((a) => a.name === s)) return 'anim'
  return 'expression'
}

const refName = (s: string) => s.replace(/^(scenario|anim):/, '')

/** describe: one line for a candidate. */
function describe(s: Show, cat: MomentCatalog): string {
  if (typeof s !== 'string') {
    return s.steps
      .map((st) => {
        const parts = [st.express, st.anim || st.matrix, st.ring && `ring ${st.ring}`, st.text && `“${st.text}”`].filter(Boolean)
        return parts.join(' + ') + (st.hold ? ' (held)' : st.ms ? ` ${st.ms} ms` : '')
      })
      .join(' → ')
  }
  const k = kindOf(s, cat)
  return k === 'expression' ? s : `${k === 'anim' ? 'animation' : 'scenario'} ${refName(s)}`
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
            const cur = m.custom ?? m.default
            return (
              <div class={'moment' + (open === m.name ? ' open' : '')} key={m.name}>
                <div class="mhead">
                  <div class="mname">
                    <b>{m.name}</b> {m.custom && <span class="badge">custom</span>}
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
              <option value="sequence">sequence</option>
            </select>
            {typeof s === 'string' && <RefPicker s={s} kind={kindOf(s, cat)} cat={cat} onChange={(v) => set(i, v)} />}
            {!has(s, cat) && <span class="err small">not on this speaker</span>}
            <span class="grow" />
            <button class="ghost icon" disabled={i === 0} onClick={() => move(i, -1)} title="Earlier">
              ↑
            </button>
            <button class="ghost icon" disabled={i === draft.length - 1} onClick={() => move(i, 1)} title="Later">
              ↓
            </button>
            <button class="ghost icon" onClick={() => setDraft(draft.filter((_, k) => k !== i))} title="Remove">
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

type StepKind = 'express' | 'anim' | 'ring' | 'text'
const stepKind = (st: Step): StepKind => (st.express ? 'express' : st.anim || st.matrix ? 'anim' : st.ring ? 'ring' : st.text !== undefined ? 'text' : 'express')

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
        return (
          <div class="row step">
            <span class="k">{i + 1}.</span>
            <select value={k} onChange={(e) => set(i, { ...blank(e.currentTarget.value as StepKind), hold: st.hold })}>
              <option value="express">expression</option>
              <option value="anim">animation</option>
              <option value="ring">ring</option>
              <option value="text">text</option>
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
            {!hold && (
              <input
                class="ms"
                type="number"
                min="0"
                step="100"
                value={st.ms || ''}
                placeholder={auto ? String(auto) : 'auto'}
                title="Milliseconds; empty = the animation's own length"
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
            <button class="ghost icon" disabled={i === 0} onClick={() => move(i, -1)}>
              ↑
            </button>
            <button class="ghost icon" disabled={i === last} onClick={() => move(i, 1)}>
              ↓
            </button>
            <button class="ghost icon" disabled={steps.length === 1} onClick={() => onChange(steps.filter((_, x) => x !== i))}>
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
