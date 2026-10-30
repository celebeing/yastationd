// The speakers' API (docs/web-ui.md). Each speaker is reached at its own URL; tokens are per
// speaker (localStorage), and the last login that worked is kept for this tab only
// (sessionStorage) so the other speakers can be logged into without asking again.

export type Peer = {
  id: string
  name: string
  board: string
  model: string
  version: string
  url: string
  seen?: number
}

export type Entity = {
  object: string
  component: string
  config: Record<string, any>
  value: string | null
  attributes?: Record<string, any>
  writable: boolean
  updated?: number
}

export type State = { info: Peer & { features?: Features }; entities: Entity[] }

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

const TOKENS = 'yst.tokens'
const CRED = 'yst.cred'
const LATEST = 'yst.latest'
const NO_LINK = 'no connection' // the request never reached the speaker

function tokens(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(TOKENS) || '{}')
  } catch {
    return {}
  }
}

export function tokenFor(id: string): string | undefined {
  return tokens()[id]
}

function setToken(id: string, tok: string | null) {
  const t = tokens()
  if (tok) t[id] = tok
  else delete t[id]
  try {
    localStorage.setItem(TOKENS, JSON.stringify(t))
  } catch {}
}

export function lastCred(): { user: string; password: string } | null {
  try {
    return JSON.parse(sessionStorage.getItem(CRED) || 'null')
  } catch {
    return null
  }
}

function setCred(user: string, password: string) {
  try {
    sessionStorage.setItem(CRED, JSON.stringify({ user, password }))
  } catch {}
}

/** base: "http://host/" or "" for the speaker that served the page. */
export function baseOf(p: Peer, home: string): string {
  if (p.id === home) return ''
  return p.url.replace(/\/+$/, '')
}

async function req<T>(base: string, path: string, opts: RequestInit = {}, token?: string, timeoutMs = 8000): Promise<T> {
  const headers: Record<string, string> = { ...(opts.headers as any) }
  if (token) headers['Authorization'] = 'Bearer ' + token
  if (opts.body && typeof opts.body === 'string' && !headers['Content-Type']) headers['Content-Type'] = 'application/json'
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMs)
  let r: Response
  try {
    r = await fetch(base + path, { ...opts, headers, signal: ctl.signal })
  } catch (e) {
    throw new ApiError(0, NO_LINK)
  } finally {
    clearTimeout(timer)
  }
  const text = await r.text()
  let body: any = null
  try {
    body = text ? JSON.parse(text) : null
  } catch {
    body = text
  }
  if (!r.ok) throw new ApiError(r.status, (body && body.error) || r.statusText)
  return body as T
}

export const info = (base: string) => req<Peer>(base, '/api/info')

export async function login(id: string, base: string, user: string, password: string): Promise<{ defaultPassword: boolean }> {
  const r = await req<{ token: string; default_password: boolean }>(base, '/api/login', {
    method: 'POST',
    body: JSON.stringify({ user, password }),
  })
  setToken(id, r.token)
  setCred(user, password)
  return { defaultPassword: r.default_password }
}

/** authed calls: a 401 forgets the token. */
async function authed<T>(id: string, base: string, path: string, opts: RequestInit = {}, timeoutMs?: number): Promise<T> {
  try {
    return await req<T>(base, path, opts, tokenFor(id), timeoutMs)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401) setToken(id, null)
    throw e
  }
}

/** tryLogin logs into a speaker with the tab's last working login, if any. */
export async function tryLogin(id: string, base: string): Promise<boolean> {
  if (tokenFor(id)) return true
  const c = lastCred()
  if (!c) return false
  try {
    await login(id, base, c.user, c.password)
    return true
  } catch {
    return false
  }
}

export const peers = (id: string, base: string) => authed<Peer[]>(id, base, '/api/peers')
export const state = (id: string, base: string) => authed<State>(id, base, '/api/state')
export type VideoAsset = { name: string; frames_bytes: number; audio_bytes: number; current: boolean; modified: number }
export type Assets = {
  videos: { supported: boolean; screen: [number, number]; items: VideoAsset[]; current: string; max_frames: number; max_audio: number }
  free: number
}
export const assets = (id: string, base: string) => authed<Assets>(id, base, '/api/assets')
export const useVideo = (id: string, base: string, name: string) =>
  authed<{ ok: boolean }>(id, base, '/api/assets/videos/' + name + '/use', { method: 'POST' })
export const deleteVideo = (id: string, base: string, name: string) =>
  authed<{ ok: boolean }>(id, base, '/api/assets/videos/' + name, { method: 'DELETE' })

/** uploadAsset PUTs a file to the speaker with progress (fetch has no upload progress). */
export function uploadAsset(id: string, base: string, path: string, body: Blob, onProgress: (sent: number) => void): Promise<any> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest()
    x.open('PUT', base + path)
    const tok = tokenFor(id)
    if (tok) x.setRequestHeader('Authorization', 'Bearer ' + tok)
    x.upload.onprogress = (e) => onProgress(e.loaded)
    x.onload = () => {
      let b: any = null
      try {
        b = JSON.parse(x.responseText)
      } catch {}
      if (x.status >= 200 && x.status < 300) resolve(b)
      else reject(new ApiError(x.status, (b && b.error) || x.statusText))
    }
    x.onerror = () => reject(new ApiError(0, NO_LINK))
    x.send(body)
  })
}

export const control = (id: string, base: string, object: string, payload: string) =>
  authed<{ ok: boolean }>(id, base, '/api/control/' + encodeURIComponent(object), { method: 'POST', body: payload, headers: { 'Content-Type': 'text/plain' } })
export async function setPassword(id: string, base: string, user: string, password: string) {
  const r = await authed<{ token: string }>(id, base, '/api/password', { method: 'POST', body: JSON.stringify({ user, password }) })
  setToken(id, r.token)
  setCred(user, password)
}

export type Features = { wifi?: boolean; led_preview?: boolean; moments?: boolean; videos?: boolean; packs?: boolean; voices?: boolean }
export type Net = { id: number; ssid: string; security: string; priority: number; disabled: boolean; hidden: boolean }
/** A moment's candidate: a reference ("wink", "anim:media/Like", "scenario:startup") or a sequence. */
/** A step of a sequence (docs/asset-packs.md): what it shows and plays at once; a step with none is a pause. */
export type Step = {
  express?: string
  anim?: string
  matrix?: string
  ring?: string
  color?: number[]
  period?: number
  else?: string
  text?: string
  sound?: string
  phrase?: string
  ms?: number
  hold?: boolean
}
export type Show = string | { steps: Step[] }
/** custom: the owner's candidates (null = the defaults); shows: the index of the one shown here (-1 none). */
/** from: the pack whose candidates are in force ("" = the defaults); current: those candidates. */
export type Moment = { name: string; when: string; default: Show[]; custom: Show[] | null; shows: number; from?: string; current?: Show[] }
export type MomentCatalog = {
  display: string
  expressions: string[] | null
  scenarios: string[] | null
  anims: { name: string; ms: number; loop?: boolean }[] | null
  ring_effects: string[] | null
  colour_effects: string[] | null
  sounds: string[] | null
}

export type DDP = { port: number; pixels: number; order: string; first: string }
export const infoFull = (base: string) => req<Peer & { features?: Features; ddp?: DDP }>(base, '/api/info')
export const wifi = (id: string, base: string) => authed<{ networks: Net[]; current: string; setup_mode: boolean }>(id, base, '/api/wifi')
export const wifiAdd = (id: string, base: string, n: { ssid: string; psk: string; hidden: boolean; priority: number }) =>
  authed<{ ok: boolean }>(id, base, '/api/wifi/networks', { method: 'POST', body: JSON.stringify(n) })
/** One of the owner's packs, as the Moments page offers it to edit (on: worn). */
export type OwnPack = { id: string; name: string; on: boolean }
/** The moments with pack's own entries as `custom` (target: the pack they are of). */
export type MomentsState = { moments: Moment[]; target: string; editable: OwnPack[] }
const packQ = (pack?: string) => (pack ? '?pack=' + encodeURIComponent(pack) : '')
export const moments = (id: string, base: string, pack?: string) => authed<MomentsState & { catalog: MomentCatalog }>(id, base, '/api/moments' + packQ(pack))
/** setMoment saves candidates into one of the owner's packs (null: its entry goes, the layers below show). */
export const setMoment = (id: string, base: string, name: string, shows: Show[] | null, pack?: string) =>
  authed<MomentsState>(id, base, '/api/moments/' + encodeURIComponent(name) + packQ(pack), shows ? { method: 'PUT', body: JSON.stringify(shows) } : { method: 'DELETE' })
export type LightCal = { calibration: string; source: 'unit' | 'board' | 'none'; reading: number; lux: number; measured?: number; before?: number }
export const lightCal = (id: string, base: string) => authed<LightCal>(id, base, '/api/calibrate/light')
/** calibrateLight takes ~16 s on the speaker (it measures), hence the long timeout. */
export const calibrateLight = (id: string, base: string, lux: number) =>
  authed<LightCal>(id, base, '/api/calibrate/light', { method: 'POST', body: JSON.stringify({ lux }) }, 40000)
export const removeLightCal = (id: string, base: string) => authed<LightCal>(id, base, '/api/calibrate/light', { method: 'DELETE' })
/** diag fetches the speaker's diagnostics archive (docs/guides/diagnostics.md): redacted unless raw. */
/** download GETs a file behind the login: its bytes and the name the speaker gives it. */
async function download(id: string, base: string, path: string, fallback: string, timeoutMs = 30000): Promise<{ blob: Blob; name: string }> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), timeoutMs)
  let r: Response
  try {
    const tok = tokenFor(id)
    r = await fetch(base + path, { headers: tok ? { Authorization: 'Bearer ' + tok } : {}, signal: ctl.signal })
  } catch {
    throw new ApiError(0, NO_LINK)
  } finally {
    clearTimeout(timer)
  }
  if (!r.ok) {
    let msg = r.statusText
    try {
      msg = (await r.json()).error || msg
    } catch {}
    if (r.status === 401) setToken(id, null)
    throw new ApiError(r.status, msg)
  }
  const name = /filename="([^"]+)"/.exec(r.headers.get('Content-Disposition') || '')?.[1] || fallback
  return { blob: await r.blob(), name }
}

/** diag: the speaker's diagnostics archive (the script takes up to ~2 min on a busy speaker). */
export const diag = (id: string, base: string, raw: boolean) => download(id, base, '/api/diag' + (raw ? '?raw=1' : ''), `diag-${id}.tar.gz`, 150000)

/** An installed asset pack as the Packs page sees it (docs/asset-packs.md). */
export type Pack = {
  id: string
  name: string
  version?: string
  author?: string
  source?: string
  license?: string
  displays?: string[]
  moments: string[] | null
  sounds: string[] | null
  anims: string[] | null
  fits: boolean
  bytes: number
  position: number
  /** one of the owner's packs (made here); installed ones are read-only */
  editable?: boolean
}

/** One installed pack animation for a preview in the browser, in the LED preview stream's terms
 * (the display as its /info, frames as its /frames: drive values, hex), painted by ledframe.ts. */
export type PackAnimation = {
  screen: [number, number]
  seg7?: { digits: number; colon: boolean; dot: boolean; degree: boolean }
  frames: { screen?: string; seg7?: string; ms: number }[]
}
/** PUT /api/packs: what the import did (updated: replaced another version, was; skipped: the same version is there). */
export type PackImported = { id: string; version?: string; was?: string; updated?: boolean; skipped?: boolean }
/** stack: the packs this speaker wears, top first (a pack left out is off); here: from
 * theme_here (v0.21.1's "Apply here"), not the theme key; target: the owner's pack edits go to. */
export type Packs = {
  packs: Pack[]
  stack: string[]
  here: boolean
  target: string
  display: string
  missing: string[] | null
  free: number
  max_bytes: number
}
export const packs = (id: string, base: string) => authed<Packs>(id, base, '/api/packs')
/** packStack writes the theme (here: this speaker's own; [] with here ends it). */
export const packStack = (id: string, base: string, stack: string[], here = false) =>
  authed<Packs>(id, base, '/api/packs/stack', { method: 'PUT', body: JSON.stringify({ stack, here }) })
/** packAction: apply = on top of the stack; copy = an editable copy of it, in its place. */
export const packAction = (id: string, base: string, pack: string, what: 'apply' | 'copy', here = false) =>
  authed<Packs>(id, base, `/api/packs/${encodeURIComponent(pack)}/${what}${here ? '?here=1' : ''}`, { method: 'POST' })
export const newPack = (id: string, base: string, name: string) =>
  authed<Packs>(id, base, '/api/packs/new', { method: 'POST', body: JSON.stringify({ name }) })
export const renamePack = (id: string, base: string, pack: string, name: string) =>
  authed<Packs>(id, base, `/api/packs/${encodeURIComponent(pack)}/rename`, { method: 'POST', body: JSON.stringify({ name }) })
export const deletePack = (id: string, base: string, pack: string) => authed<Packs>(id, base, '/api/packs/' + encodeURIComponent(pack), { method: 'DELETE' })
/** packAnimation loads an installed pack's validated frames for an in-browser preview. */
export const packAnimation = (id: string, base: string, pack: string, name: string) =>
  authed<PackAnimation>(id, base, `/api/packs/${encodeURIComponent(pack)}/anims/${encodeURIComponent(name)}`)
/** packAssetPath: where a sound or animation of one of the owner's packs is put (PUT) or removed. */
export const packAssetPath = (pack: string, kind: 'sounds' | 'anims', name: string) =>
  `/api/packs/${encodeURIComponent(pack)}/${kind}/${encodeURIComponent(name)}`
export const deletePackAsset = (id: string, base: string, pack: string, kind: 'sounds' | 'anims', name: string) =>
  authed<Packs>(id, base, packAssetPath(pack, kind, name), { method: 'DELETE' })
/** exportPack: a pack as an archive (as: another id, needed for custom; name: another name). */
export const exportPack = (id: string, base: string, pack: string, as = '', name = '') =>
  download(id, base, `/api/packs/${encodeURIComponent(pack)}/export?id=${encodeURIComponent(as)}&name=${encodeURIComponent(name)}`, (as || pack) + '.zip')

export const log = (id: string, base: string, source: string, lines: number) =>
  authed<{ source: string; lines: string[] }>(id, base, `/api/log?source=${encodeURIComponent(source)}&lines=${lines}`)
/** showMoment plays a moment as it is set, or a draft of candidates (not saved). */
export const showMoment = (id: string, base: string, name: string, draft?: Show[]) =>
  authed<{ shown: boolean }>(id, base, '/api/moment/' + encodeURIComponent(name), { method: 'POST', body: draft ? JSON.stringify(draft) : '' })

/** tapURL: the frame tap behind the login; the token goes in the query (EventSource). */
export function tapURL(id: string, base: string, path: string): string {
  return `${base}/tap${path}?token=${encodeURIComponent(tokenFor(id) || '')}`
}

const REPO_PATH = 'celebeing/yastationd'
export const REPO = 'https://github.com/' + REPO_PATH
const LATEST_TTL_MS = 10 * 60_000

/** The newest release as GitHub has it (date: not filled in yet). */
export type Release = { tag: string; url: string; date: string }

/** latestRelease asks GitHub from the browser (the speakers have no internet; the browser
 * usually does, and GitHub's API allows any origin) for the newest vX.Y.Z tag; every tag gets a
 * GitHub Release from CI. The answer is kept for this tab for 10 minutes unless `fresh`.
 * null when GitHub cannot be reached. */
export async function latestRelease(fresh = false): Promise<Release | null> {
  try {
    const cached = JSON.parse(sessionStorage.getItem(LATEST) || 'null')
    if (!fresh && cached && Date.now() - cached.at < LATEST_TTL_MS) return cached.rel
  } catch {}
  try {
    const r = await fetch(`https://api.github.com/repos/${REPO_PATH}/tags?per_page=100`, { headers: { Accept: 'application/vnd.github+json' } })
    if (!r.ok) return null
    const tags: { name: string }[] = await r.json()
    let best = ''
    for (const t of tags) if (/^v\d+\.\d+\.\d+$/.test(t.name) && (!best || behind(best, t.name))) best = t.name
    if (!best) return null
    const rel: Release = { tag: best, url: `${REPO}/releases/tag/${best}`, date: '' }
    try {
      sessionStorage.setItem(LATEST, JSON.stringify({ at: Date.now(), rel }))
    } catch {}
    return rel
  } catch {
    return null
  }
}

/** behind: version (git describe, e.g. v0.12.1-3-gabc) is older than the release tag. */
export function behind(version: string, tag: string): boolean {
  const parse = (v: string) => (v.match(/^v(\d+)\.(\d+)\.(\d+)/) || []).slice(1).map(Number)
  const a = parse(version),
    b = parse(tag)
  if (a.length !== 3 || b.length !== 3) return false
  for (let i = 0; i < 3; i++) if (a[i] !== b[i]) return a[i] < b[i]
  return false
}

/** A config key as the settings pages show it (docs/settings.md). */
export type SettingKey = {
  name: string
  group: string
  label: string
  doc: string
  kind: 'bool' | 'int' | 'float' | 'enum' | 'text' | 'secret' | 'list'
  min?: number
  max?: number
  unit?: string
  options?: string[]
  runtime: boolean
  sync: boolean
  advanced?: boolean
  value: string[]
  default: string[]
  board?: string[]
  set: boolean
  changed: boolean
}
export type Settings = { groups: { ID: string; Title: string; Page: string }[]; keys: SettingKey[]; trial: boolean; skipped?: string[] }
export const settings = (id: string, base: string) => authed<Settings>(id, base, '/api/settings')
/** putSettings: key → its lines ([] = back to the default). restarting: a key needed a restart. */
export const putSettings = (id: string, base: string, changes: Record<string, string[]>) =>
  authed<{ ok: boolean; restarting: boolean; changed: string[] }>(id, base, '/api/settings', { method: 'PUT', body: JSON.stringify({ changes }) })

export type SharedAt = { at: number; by: string }
export type SyncState = {
  connected: boolean
  settings: Record<string, SharedAt>
  options: Record<string, SharedAt>
  moments: Record<string, SharedAt>
  option_ids: string[]
}
export type SyncKind = 'setting' | 'option' | 'moment'
export type Vitals = {
  daemon_uptime_s: number
  system_uptime_s?: number
  load?: number[]
  cpus: number
  mem_total?: number
  mem_free?: number
  mem_available?: number
  daemon_rss?: number
  flash_total?: number
  flash_free?: number
  temps?: { zone: string; c: number }[]
}
export const vitals = (id: string, base: string) => authed<Vitals>(id, base, '/api/vitals')
export const syncState = (id: string, base: string) => authed<SyncState>(id, base, '/api/sync')
export const setSync = (id: string, base: string, kind: SyncKind, item: string, on: boolean) =>
  authed<SyncState>(id, base, '/api/sync', { method: 'POST', body: JSON.stringify({ kind, item, on }) })

/** Voice ID (docs/voice-id.md): a group of voiceprints the store thinks are one voice. */
export type VoiceGroup = {
  id: string
  /** set by the owner */
  name?: string
  /** marked by the owner as one of the household (not a guest); a name alone doesn't make one */
  household?: boolean
  /** the "not a person" group: false wakes, noise, silence */
  noise?: boolean
  count: number
  /** the members' mean P(female); -1: no classifier */
  female: number
  gender: 'male' | 'female' | 'unknown'
  /** the newest member's time, unix ms */
  last: number
  speakers: string[] | null
}
/** GET /api/voices: every speaker with voice ID says where the store is; the store adds the album. */
export type Voices = {
  on: boolean
  store: boolean
  threshold: number
  model?: string
  people?: number
  ignore_guests?: boolean
  store_id?: string
  groups?: VoiceGroup[]
  entries?: number
}
/** A request in the store's log (verdict: a name, guest or unknown — what the speaker decided). */
export type VoiceEntry = {
  id: string
  at: number
  speaker: string
  src?: string
  transcript?: string
  ignored?: boolean
  secs: number
  female: number
  age: number
  model?: string
  verdict?: string
  score: number
  early?: string // the early verdict: «Алиса» and the start of the request
  early_score?: number
  early_secs?: number
  group: string
  pinned?: boolean
}
export type VoicePreviewRow = {
  id: string
  at: number
  speaker: string
  transcript?: string
  group: string
  score: number
  closest?: string
  state: 'known' | 'guest' | 'unknown'
  name?: string
}
export type VoicePreview = { threshold: number; people: number; guests: number; rows: VoicePreviewRow[] | null }
export const voices = (id: string, base: string) => authed<Voices>(id, base, '/api/voices')
export const voiceLog = (id: string, base: string, group: string, limit: number) =>
  authed<VoiceEntry[] | null>(id, base, `/api/voices/log?group=${encodeURIComponent(group)}&limit=${limit}`)
export const voicePreview = (id: string, base: string, threshold: number) =>
  authed<VoicePreview>(id, base, '/api/voices/preview?threshold=' + threshold.toFixed(2))
type Ok = { ok: boolean }
/** renameVoice: an empty name unnames the group (its requests stay together). */
export const renameVoice = (id: string, base: string, group: string, name: string) =>
  authed<Ok>(id, base, '/api/voices/groups/' + encodeURIComponent(group), { method: 'POST', body: JSON.stringify({ name }) })
/** setHousehold: the group is one of the household (its requests are taken), or not (a guest's). */
export const setHousehold = (id: string, base: string, group: string, household: boolean) =>
  authed<Ok>(id, base, '/api/voices/groups/' + encodeURIComponent(group), { method: 'POST', body: JSON.stringify({ household }) })
export const forgetVoice = (id: string, base: string, group: string) =>
  authed<Ok & { forgotten: number }>(id, base, '/api/voices/groups/' + encodeURIComponent(group), { method: 'DELETE' })
export const mergeVoices = (id: string, base: string, from: string, into: string) =>
  authed<Ok>(id, base, '/api/voices/merge', { method: 'POST', body: JSON.stringify({ from, into }) })
/** moveVoiceEntry: to a group id, "noise" or "new" (a person of its own). */
export const moveVoiceEntry = (id: string, base: string, entry: string, group: string) =>
  authed<Ok>(id, base, '/api/voices/entries/' + encodeURIComponent(entry), { method: 'POST', body: JSON.stringify({ group }) })
export const forgetAllVoices = (id: string, base: string) => authed<Ok>(id, base, '/api/voices', { method: 'DELETE' })
