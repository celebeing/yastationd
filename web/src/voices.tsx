import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Peer, SyncKind, SyncState, VoiceEntry, VoiceGroup, VoicePreview, Voices } from './api'
import { ShareSwitch } from './settings'

// Configuration → People (docs/voice-id.md, "voices like faces in a photo album"). One speaker (the
// store, voice_store) keeps every speaker's requests and groups their voiceprints into people; the
// owner names, merges, moves and forgets here. Every other speaker shows where the store is.

type Props = { peer: Peer; base: string; peers: Peer[]; onSwitch: (id: string) => void }

const NOISE = 'noise'
const err2s = (e: any) => e?.message || String(e)

/** since: "just now", "5 min ago", "3 h ago", "2 d ago", or the date. */
function since(ms: number): string {
  if (!ms) return 'never'
  const s = Math.max(0, (Date.now() - ms) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return Math.round(s / 60) + ' min ago'
  if (s < 86400) return Math.round(s / 3600) + ' h ago'
  if (s < 14 * 86400) return Math.round(s / 86400) + ' d ago'
  return new Date(ms).toLocaleDateString()
}

/** when: the time of a request, short; today's without the date. */
function when(ms: number): string {
  const d = new Date(ms)
  const t = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  return d.toDateString() === new Date().toDateString() ? t : d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + t
}

const plural = (n: number, one: string, many = one + 's') => `${n} ${n === 1 ? one : many}`

export function VoicesTab(p: Props) {
  const [v, setV] = useState<Voices | null>(null)
  const [err, setErr] = useState('')
  const load = () =>
    api.voices(p.peer.id, p.base).then(
      (x) => {
        setV(x)
        setErr('')
      },
      (e) => setErr(err2s(e)),
    )
  useEffect(() => {
    load()
  }, [p.peer.id, p.base])
  if (!v) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  if (!v.on)
    return (
      <section class="card narrow">
        <h3>People</h3>
        <p>
          Voice recognition is off on this speaker. Turn on <b>Recognise voices</b> in <a href="#voice">Configuration → Voice</a>.
        </p>
      </section>
    )
  if (!v.store) return <NotStore {...p} v={v} />
  return <Album {...p} v={v} reload={load} />
}

// --- a speaker that is not the store ---

function NotStore(p: Props & { v: Voices }) {
  const { v } = p
  const store = v.store_id ? p.peers.find((x) => x.id === v.store_id) : undefined
  const station3 = p.peers.find((x) => x.model === 'Station 3')
  return (
    <div class="voices">
      <section class="card narrow">
        <h3>People</h3>
        <p>
          The household's voices live on one speaker: it keeps the requests every speaker hears and groups them into people. Naming, merging and
          the guest threshold preview are there.
        </p>
        {store ? (
          <p class="row">
            <button class="primary" onClick={() => p.onSwitch(store.id)}>
              Open the voices on {store.name}
            </button>
          </p>
        ) : v.store_id ? (
          <p class="muted">
            They are kept on the speaker <code>{v.store_id}</code>, which this app does not see right now.
          </p>
        ) : (
          <p>
            No speaker keeps them yet. On one speaker{station3 ? <> (a Station 3: {station3.name}, has more room and a faster CPU)</> : null}, turn
            on <b>Keep the voices here</b> in <a href="#voice">Configuration → Voice</a>. Only one speaker should keep them.
          </p>
        )}
      </section>
      <SpeakerStatus v={v} />
    </div>
  )
}

function SpeakerStatus({ v }: { v: Voices }) {
  return (
    <section class="card narrow">
      <h3>This speaker</h3>
      <dl class="grid">
        <dt>People it knows</dt>
        <dd>{v.people ? plural(v.people, 'person', 'people') : 'nobody yet: every voice is unknown'}</dd>
        <dt>Ignore guests</dt>
        <dd>
          {v.ignore_guests ? 'on' : 'off'}
          {v.ignore_guests && !v.people ? <span class="muted small"> (does nothing until someone is marked as the household)</span> : null}
        </dd>
        <dt>Guest threshold</dt>
        <dd>{v.threshold.toFixed(2)}</dd>
        <dt>Voiceprint model</dt>
        <dd>{v.model || '—'}</dd>
      </dl>
      <p class="muted small">
        Ignore guests and the guest threshold are in <a href="#voice">Configuration → Voice</a>.
      </p>
    </section>
  )
}

// --- the store: the album ---

type Labels = { label: (g: string) => string; groups: VoiceGroup[] }

/** labelsOf names every group: its name, "Not a person", or "Person N" in list order. */
function labelsOf(groups: VoiceGroup[]): Labels {
  const m: Record<string, string> = {}
  let n = 0
  for (const g of groups) m[g.id] = g.noise ? 'Not a person' : g.name || `Person ${++n}`
  return { groups, label: (id) => m[id] || (id === NOISE ? 'Not a person' : 'A voice no longer here') }
}

function Album(p: Props & { v: Voices; reload: () => Promise<void> }) {
  const { v, peer, base } = p
  const groups = v.groups || []
  const L = labelsOf(groups)
  const [sel, setSel] = useState('') // '' = all requests
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [tick, setTick] = useState(0) // bumps the log and the preview after an edit
  const speakerName = (id: string) => p.peers.find((x) => x.id === id)?.name || id
  const named = groups.filter((g) => g.household && !g.noise) // the household
  const real = groups.filter((g) => !g.noise)
  const selected = groups.find((g) => g.id === sel)

  async function act(f: () => Promise<unknown>) {
    setBusy(true)
    setErr('')
    try {
      await f()
    } catch (e) {
      setErr(err2s(e))
    }
    await p.reload()
    setTick((t) => t + 1)
    setBusy(false)
  }
  useEffect(() => {
    if (sel && !groups.some((g) => g.id === sel)) setSel('')
  }, [groups.map((g) => g.id).join()])

  return (
    <div class="voices">
      <section class="card">
        <h3>People</h3>
        <p class="muted small">
          This speaker keeps the requests every speaker hears ({plural(v.entries || 0, 'request')}) and groups their voiceprints into people, as a
          photo album groups faces. Name a group to know who it is, and mark it <b>Household</b> to have its requests taken: the speakers tell
          the household from guests (a named visitor stays a guest until marked). Merge groups
          that are one person, and move a request that landed in the wrong group: your edits stay, and new requests are grouped around them.
        </p>
        <p class="small">
          {named.length ? (
            <>
              The household: <b>{named.map((g) => L.label(g.id)).join(', ')}</b>.{' '}
            </>
          ) : null}
          Ignore guests is <b>{v.ignore_guests ? 'on' : 'off'}</b>, the guest threshold {v.threshold.toFixed(2)} (below, under Guests). Model: {v.model || '—'}.
        </p>
        {!v.entries ? (
          <p class="note">
            No requests yet. People appear here by themselves as the speakers hear requests: talk to any of them as usual and come back.
          </p>
        ) : !named.length ? (
          <p class="note">
            Nobody is marked as the household yet. Until someone is, every voice counts as unknown and Ignore guests does nothing. Find the
            group with your requests below, name it and mark it Household.
          </p>
        ) : null}
        {err && <p class="err">{err}</p>}
      </section>

      {groups.length > 0 && (
        <section>
          <div class="vgroups">
            {groups.map((g) => (
              <GroupCard
                key={g.id}
                g={g}
                label={L.label(g.id)}
                others={groups.filter((x) => x.id !== g.id)}
                L={L}
                selected={sel === g.id}
                busy={busy}
                speakerName={speakerName}
                onSelect={() => setSel(sel === g.id ? '' : g.id)}
                rename={(name) => act(() => api.renameVoice(peer.id, base, g.id, name))}
                household={(on) => act(() => api.setHousehold(peer.id, base, g.id, on))}
                merge={(into) => act(() => api.mergeVoices(peer.id, base, g.id, into))}
                forget={() =>
                  act(async () => {
                    await api.forgetVoice(peer.id, base, g.id)
                    if (sel === g.id) setSel('')
                  })
                }
              />
            ))}
          </div>
        </section>
      )}

      {(v.entries || 0) > 0 && (
        <Log
          key={sel}
          {...p}
          group={sel}
          title={selected ? `Requests of ${L.label(selected.id)}` : 'All requests'}
          onAll={sel ? () => setSel('') : undefined}
          L={L}
          tick={tick}
          busy={busy}
          speakerName={speakerName}
          move={(entry, to) => act(() => api.moveVoiceEntry(peer.id, base, entry, to))}
        />
      )}

      {real.length > 0 && <Preview {...p} L={L} named={named} tick={tick} speakerName={speakerName} reload={p.reload} />}

      {(v.entries || 0) > 0 || named.length ? (
        <section class="card">
          <h3>Forget everything</h3>
          <p class="muted small">
            Deletes every request, voiceprint, group and name kept here. The speakers forget the household too: every voice is unknown again.
          </p>
          <Confirm label="Forget everything" ask="Really forget every voice?" busy={busy} onYes={() => act(() => api.forgetAllVoices(peer.id, base))} />
        </section>
      ) : null}
    </div>
  )
}

/** Confirm: a button that asks again in place ("Really …? Yes / No"). */
function Confirm(p: { label: string; ask: string; busy: boolean; onYes: () => void; ghost?: boolean }) {
  const [asking, setAsking] = useState(false)
  if (!asking)
    return (
      <button class={p.ghost ? 'ghost' : 'danger'} disabled={p.busy} onClick={() => setAsking(true)}>
        {p.label}
      </button>
    )
  return (
    <span class="confirm" role="group">
      <span class="small">{p.ask}</span>
      <button
        class="danger"
        disabled={p.busy}
        onClick={() => {
          setAsking(false)
          p.onYes()
        }}
      >
        Yes
      </button>
      <button class="ghost" onClick={() => setAsking(false)}>
        No
      </button>
    </span>
  )
}

function genderOf(g: VoiceGroup) {
  return g.gender === 'unknown' ? '' : g.gender
}

function GroupCard(p: {
  g: VoiceGroup
  label: string
  others: VoiceGroup[]
  L: Labels
  selected: boolean
  busy: boolean
  speakerName: (id: string) => string
  onSelect: () => void
  rename: (name: string) => void
  household: (on: boolean) => void
  merge: (into: string) => void
  forget: () => void
}) {
  const { g } = p
  const [mode, setMode] = useState<'' | 'rename' | 'merge'>('')
  const [name, setName] = useState(g.name || '')
  const [into, setInto] = useState('')
  const speakers = (g.speakers || []).map(p.speakerName)
  const facts = [genderOf(g), plural(g.count, 'phrase'), g.last ? 'last heard ' + since(g.last) : ''].filter(Boolean)
  return (
    <article class={'vgroup' + (p.selected ? ' sel' : '') + (g.household ? ' named' : '') + (g.noise ? ' noise' : '')}>
      <button class="vg-head" onClick={p.onSelect} aria-pressed={p.selected} title={p.selected ? 'Show all requests' : 'Show its requests'}>
        <span class="vg-name">
          {p.label}
          {g.household && <span class="tag">household</span>}
        </span>
        <span class="muted small">{facts.join(' · ')}</span>
        {g.noise && <span class="muted small">False wakes, noise and silence: requests with no words in them.</span>}
        {speakers.length > 0 && <span class="muted small">Heard by {speakers.join(', ')}</span>}
      </button>
      {mode === 'rename' && (
        <form
          class="row tight vg-edit"
          onSubmit={(e) => {
            e.preventDefault()
            setMode('')
            p.rename(name.trim())
          }}
        >
          <input value={name} aria-label="Name" placeholder="A name" maxLength={40} onInput={(e) => setName(e.currentTarget.value)} autofocus />
          <button type="submit" disabled={p.busy || (!name.trim() && !g.name)}>
            {name.trim() || !g.name ? 'Save' : 'Remove the name'}
          </button>
          <button type="button" class="ghost" onClick={() => setMode('')}>
            Cancel
          </button>
        </form>
      )}
      {mode === 'merge' && (
        <div class="row tight vg-edit">
          <select aria-label="Merge into" value={into} onChange={(e) => setInto(e.currentTarget.value)}>
            <option value="">Merge into…</option>
            {p.others.map((o) => (
              <option value={o.id}>
                {p.L.label(o.id)} ({o.count})
              </option>
            ))}
          </select>
          <button
            class="primary"
            disabled={p.busy || !into}
            onClick={() => {
              setMode('')
              p.merge(into)
            }}
          >
            Merge
          </button>
          <button class="ghost" onClick={() => setMode('')}>
            Cancel
          </button>
        </div>
      )}
      {!mode && (
        <div class="row tight vg-actions">
          {!g.noise && (
            <button class="ghost" disabled={p.busy} onClick={() => (setName(g.name || ''), setMode('rename'))}>
              {g.name ? 'Rename' : 'Name'}
            </button>
          )}
          {!g.noise && (
            <button
              class="ghost"
              disabled={p.busy}
              onClick={() => p.household(!g.household)}
              title={g.household ? 'Its requests count as a guest’s again' : 'Its requests are taken: not a guest'}
            >
              {g.household ? 'Not household' : 'Household'}
            </button>
          )}
          {!g.noise && p.others.length > 0 && (
            <button class="ghost" disabled={p.busy} onClick={() => (setInto(''), setMode('merge'))}>
              Merge into…
            </button>
          )}
          <Confirm ghost label={g.noise ? 'Clear' : 'Forget'} ask={g.noise ? 'Delete these requests?' : `Really forget ${p.label}?`} busy={p.busy} onYes={p.forget} />
        </div>
      )}
    </article>
  )
}

// --- the log ---

const PAGE = 100

function verdictOf(e: VoiceEntry): { text: string; tone: string } {
  const v = e.verdict || ''
  const sc = e.score ? ' · ' + e.score.toFixed(2) : ''
  if (!v) return { text: '—', tone: '' }
  if (v === 'guest') return { text: 'Guest' + sc, tone: 'warn' }
  if (v === 'unknown') return { text: 'Unknown' + sc, tone: '' }
  return { text: v + sc, tone: 'good' }
}

function Log(
  p: Props & {
    group: string
    title: string
    onAll?: () => void
    L: Labels
    tick: number
    busy: boolean
    speakerName: (id: string) => string
    move: (entry: string, to: string) => void
  },
) {
  const [rows, setRows] = useState<VoiceEntry[] | null>(null)
  const [limit, setLimit] = useState(PAGE)
  const [err, setErr] = useState('')
  useEffect(() => {
    let live = true
    api.voiceLog(p.peer.id, p.base, p.group, limit).then(
      (r) => live && (setRows(r || []), setErr('')),
      (e) => live && setErr(err2s(e)),
    )
    return () => {
      live = false
    }
  }, [p.group, limit, p.tick])
  return (
    <section class="card">
      <div class="vlog-head">
        <h3>{p.title}</h3>
        {p.onAll && (
          <button class="ghost" onClick={p.onAll}>
            All requests
          </button>
        )}
      </div>
      {err && <p class="err">{err}</p>}
      {!rows ? (
        <p class="muted">Loading…</p>
      ) : !rows.length ? (
        <p class="muted">No requests.</p>
      ) : (
        <ul class="vlog">
          {rows.map((e) => {
            const vd = verdictOf(e)
            const words = e.transcript ? (
              <span class="vtext">“{e.transcript}”</span>
            ) : e.ignored ? (
              <span class="muted">ignored: not sent to Home Assistant</span>
            ) : (
              <span class="muted">no words</span>
            )
            return (
              <li>
                <span class="vmeta small">
                  <span title={new Date(e.at).toLocaleString()}>{when(e.at)}</span>
                  <span class="muted">{p.speakerName(e.speaker)}</span>
                </span>
                <span class="vbody">
                  {words}
                  <span class="vfacts small">
                    <span class={'chip ' + vd.tone} title="What the speaker decided, and how close the voice was to that person (1 = the same)">
                      {vd.text}
                    </span>
                    {e.early && (
                      <span class="muted" title="The early verdict, on «Алиса» and the start of the request: the one that decides when guests are ignored">
                        early: {e.early === 'guest' ? 'Guest' : e.early === 'unknown' ? 'Unknown' : e.early}
                        {e.early_score ? ' · ' + e.early_score.toFixed(2) : ''}
                      </span>
                    )}
                    {!p.group && <span class="muted">in {p.L.label(e.group)}</span>}
                    {e.secs > 0 && <span class="muted">{e.secs.toFixed(1)} s of speech</span>}
                  </span>
                </span>
                <select
                  class="vmove"
                  aria-label="Move to"
                  disabled={p.busy}
                  value=""
                  onChange={(x) => {
                    const to = x.currentTarget.value
                    x.currentTarget.value = ''
                    if (to) p.move(e.id, to)
                  }}
                >
                  <option value="">Move to…</option>
                  {p.L.groups
                    .filter((g) => g.id !== e.group && !g.noise)
                    .map((g) => (
                      <option value={g.id}>{p.L.label(g.id)}</option>
                    ))}
                  {e.group !== NOISE && <option value={NOISE}>Not a person</option>}
                  <option value="new">A new person</option>
                </select>
              </li>
            )
          })}
        </ul>
      )}
      {rows && rows.length >= limit && limit < 3000 && (
        <p class="row">
          <button class="ghost" onClick={() => setLimit(limit + PAGE * 2)}>
            Show more
          </button>
        </p>
      )}
    </section>
  )
}

// --- the threshold preview ---

const TMIN = 0.4,
  TMAX = 0.9
// The histogram's bins: scores from 0.2 to 1 in steps of 0.05; lower ones fall in the first.
const HLO = 0.2,
  HSTEP = 0.05,
  HBINS = 16

// The guests card: Ignore guests and the guest threshold themselves (the same settings as in
// Configuration → Voice, with their share switches), and what the threshold does to the last
// requests. Dragging the slider previews; letting go saves the threshold.
function Preview(p: Props & { v: Voices; L: Labels; named: VoiceGroup[]; tick: number; speakerName: (id: string) => string; reload: () => Promise<void> }) {
  const [th, setTh] = useState(() => Math.min(TMAX, Math.max(TMIN, p.v.threshold)))
  const [pv, setPv] = useState<VoicePreview | null>(null)
  const [err, setErr] = useState('')
  const [sync, setSync] = useState<SyncState | null>(null)
  const [saving, setSaving] = useState(false)
  const loadSync = () => api.syncState(p.peer.id, p.base).then(setSync, () => setSync(null))
  useEffect(() => {
    loadSync()
  }, [p.peer.id])
  useEffect(() => setTh(Math.min(TMAX, Math.max(TMIN, p.v.threshold))), [p.v.threshold])
  async function run(f: () => Promise<unknown>) {
    setSaving(true)
    setErr('')
    try {
      await f()
    } catch (e) {
      setErr(err2s(e))
    }
    await Promise.all([p.reload(), loadSync()])
    setSaving(false)
  }
  const share = (kind: SyncKind, item: string, on: boolean) => run(() => api.setSync(p.peer.id, p.base, kind, item, on))
  const saveThreshold = (x: number) => {
    if (Math.abs(x - p.v.threshold) > 0.004) run(() => api.putSettings(p.peer.id, p.base, { voice_guest_threshold: [x.toFixed(2)] }))
  }
  const setIgnore = (on: boolean) => run(() => api.control(p.peer.id, p.base, 'voice_ignore_guests', on ? 'ON' : 'OFF'))
  const timer = useRef<number | undefined>(undefined)
  useEffect(() => {
    let live = true
    clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      api.voicePreview(p.peer.id, p.base, th).then(
        (r) => live && (setPv(r), setErr('')),
        (e) => live && setErr(err2s(e)),
      )
    }, 300) as unknown as number
    return () => {
      live = false
      clearTimeout(timer.current)
    }
  }, [th, p.tick])

  const rows = pv?.rows || []
  const guests = rows.filter((r) => r.state === 'guest')
  const namedIds = new Set(p.named.map((g) => g.id))
  // One strip per person of the household (their own requests, scored against the closest named person),
  // and one for everyone else.
  const series = [
    ...p.named.map((g) => ({ key: g.id, label: p.L.label(g.id), own: true, scores: rows.filter((r) => r.group === g.id).map((r) => r.score) })),
    { key: '', label: 'Everyone else', own: false, scores: rows.filter((r) => !namedIds.has(r.group)).map((r) => r.score) },
  ].filter((s) => s.scores.length)
  const pos = (x: number) => ((Math.min(1, Math.max(HLO, x)) - HLO) / (1 - HLO)) * 100

  return (
    <section class="card">
      <h3>Guests</h3>
      <p class="muted small">
        The same settings as in <a href="#voice">Configuration → Voice</a>. <b>Ignore guests</b>: a request from a voice that is not the
        household's never reaches Home Assistant; the speaker shows the voice_guest moment instead. <b>Guest threshold</b>: how close a voice
        must be to a household person's to count as theirs; below it, a guest. Drag the slider to see what a value does to the last requests
        below; letting go saves it.
      </p>
      <div class="row vguest">
        <label class="vcheck">
          <input type="checkbox" checked={!!p.v.ignore_guests} disabled={saving} onChange={(e) => setIgnore(e.currentTarget.checked)} /> Ignore guests
        </label>
        <ShareSwitch sync={sync} kind="option" item="voice_ignore_guests" onShare={share} />
      </div>
      <div class="row vguest">
        <label class="field" for="vth">
          Guest threshold
        </label>
        <input
          id="vth"
          type="range"
          min={TMIN}
          max={TMAX}
          step={0.01}
          value={th}
          disabled={saving}
          onInput={(e) => setTh(Number(e.currentTarget.value))}
          onChange={(e) => saveThreshold(Number(e.currentTarget.value))}
          aria-valuetext={th.toFixed(2)}
        />
        <b class="vth">{th.toFixed(2)}</b>
        <ShareSwitch sync={sync} kind="setting" item="voice_guest_threshold" onShare={share} />
      </div>
      {err && <p class="err">{err}</p>}
      {!pv ? (
        <p class="muted">…</p>
      ) : !pv.people ? (
        <p class="note">Nobody is marked as the household yet, so every voice is unknown at any threshold. Mark the household's groups above first.</p>
      ) : !rows.length ? (
        <p class="muted">No requests with speech in them yet.</p>
      ) : (
        <>
          <p>
            Of the last {plural(rows.length, 'request')}, <b>{guests.length}</b> would be taken for guests.
          </p>
          <div class="vhist" role="img" aria-label="How close each request was to the closest person of the household, per person and for everyone else">
            {series.map((s) => {
              const bins = new Array(HBINS).fill(0)
              for (const x of s.scores) bins[Math.min(HBINS - 1, Math.max(0, Math.floor((x - HLO) / HSTEP)))]++
              const top = Math.max(...bins)
              return (
                <div class="vh-row">
                  <span class="vh-label small" title={s.label}>
                    {s.label} <span class="muted">({s.scores.length})</span>
                  </span>
                  <span class="vh-plot">
                    {bins.map((n, i) => {
                      const lo = HLO + i * HSTEP
                      // a bar on the wrong side of the threshold: the household taken for guests, or the others for the household
                      const wrong = n > 0 && (s.own ? lo + HSTEP <= th : lo >= th)
                      return (
                        <i
                          class={(s.own ? 'own' : 'rest') + (wrong ? ' wrong' : '')}
                          style={{ height: n ? Math.max(8, (n / top) * 100) + '%' : '0' }}
                          title={`${lo.toFixed(2)}–${(lo + HSTEP).toFixed(2)}: ${n}`}
                        />
                      )
                    })}
                    <b class="vh-th" style={{ left: pos(th) + '%' }} />
                  </span>
                </div>
              )
            })}
            <div class="vh-row vh-axis small muted">
              <span />
              <span class="vh-plot">
                <span style={{ left: '0%' }}>{HLO.toFixed(1)}</span>
                <span style={{ left: pos(0.6) + '%' }}>0.6</span>
                <span style={{ left: '100%' }}>1</span>
              </span>
            </div>
          </div>
          <p class="muted small">
            Each strip: how close a group's requests were to the closest person of the household. A household person's bars should sit right of the line, everyone
            else's left of it; bars on the wrong side are marked.
          </p>
          {guests.length > 0 && (
            <>
              <h3 class="vsub">Would be taken for guests</h3>
              <ul class="vlog vguests">
                {guests.map((r) => (
                  <li>
                    <span class="vmeta small">
                      <span title={new Date(r.at).toLocaleString()}>{when(r.at)}</span>
                      <span class="muted">{p.speakerName(r.speaker)}</span>
                    </span>
                    <span class="vbody">
                      {r.transcript ? <span class="vtext">“{r.transcript}”</span> : <span class="muted">no words</span>}
                      <span class="vfacts small">
                        <span class="muted">in {p.L.label(r.group)}</span>
                        <span class="muted">
                          {r.score.toFixed(2)}
                          {r.closest ? ' to ' + r.closest : ''}
                        </span>
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </>
      )}
    </section>
  )
}
