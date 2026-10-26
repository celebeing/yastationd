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

export type State = { info: Peer; entities: Entity[] }

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
  }
}

const TOKENS = 'yst.tokens'
const CRED = 'yst.cred'

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

async function req<T>(base: string, path: string, opts: RequestInit = {}, token?: string): Promise<T> {
  const headers: Record<string, string> = { ...(opts.headers as any) }
  if (token) headers['Authorization'] = 'Bearer ' + token
  if (opts.body && typeof opts.body === 'string' && !headers['Content-Type']) headers['Content-Type'] = 'application/json'
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), 8000)
  let r: Response
  try {
    r = await fetch(base + path, { ...opts, headers, signal: ctl.signal })
  } catch (e) {
    throw new ApiError(0, 'нет связи')
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

export function logout(id: string) {
  setToken(id, null)
}

/** authed calls: a 401 forgets the token. */
async function authed<T>(id: string, base: string, path: string, opts: RequestInit = {}): Promise<T> {
  try {
    return await req<T>(base, path, opts, tokenFor(id))
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
export const control = (id: string, base: string, object: string, payload: string) =>
  authed<{ ok: boolean }>(id, base, '/api/control/' + encodeURIComponent(object), { method: 'POST', body: payload, headers: { 'Content-Type': 'text/plain' } })
export const getConfig = (id: string, base: string) => authed<{ file: string; text: string; trial: boolean }>(id, base, '/api/config')
export const putConfig = (id: string, base: string, text: string) =>
  authed<{ ok: boolean; restarting: boolean }>(id, base, '/api/config', { method: 'PUT', body: JSON.stringify({ text }) })
export async function setPassword(id: string, base: string, user: string, password: string) {
  const r = await authed<{ token: string }>(id, base, '/api/password', { method: 'POST', body: JSON.stringify({ user, password }) })
  setToken(id, r.token)
  setCred(user, password)
}

export type Features = { wifi?: boolean; led_preview?: boolean; moments?: boolean }
export type Net = { id: number; ssid: string; security: string; priority: number; disabled: boolean; hidden: boolean }
export type Moment = { name: string; candidates: string[]; shows: string }

export const infoFull = (base: string) => req<Peer & { features?: Features }>(base, '/api/info')
export const wifi = (id: string, base: string) => authed<{ networks: Net[]; current: string; setup_mode: boolean }>(id, base, '/api/wifi')
export const wifiAdd = (id: string, base: string, n: { ssid: string; psk: string; hidden: boolean; priority: number }) =>
  authed<{ ok: boolean }>(id, base, '/api/wifi/networks', { method: 'POST', body: JSON.stringify(n) })
export const moments = (id: string, base: string) => authed<Moment[]>(id, base, '/api/moments')
export const showMoment = (id: string, base: string, name: string) =>
  authed<{ shown: boolean }>(id, base, '/api/moment/' + encodeURIComponent(name), { method: 'POST' })

/** tapURL: the frame tap behind the login; the token goes in the query (EventSource). */
export function tapURL(id: string, base: string, path: string): string {
  return `${base}/tap${path}?token=${encodeURIComponent(tokenFor(id) || '')}`
}

export const REPO = 'https://github.com/celebeing/yastationd'

/** latestRelease asks GitHub from the browser (the speakers have no internet; the browser
 * usually does, and GitHub's API allows any origin). null when it cannot be reached. */
export async function latestRelease(): Promise<{ tag: string; url: string; date: string } | null> {
  try {
    const cached = JSON.parse(sessionStorage.getItem('yst.latest') || 'null')
    if (cached && Date.now() - cached.at < 3600_000) return cached.rel
  } catch {}
  try {
    const r = await fetch('https://api.github.com/repos/celebeing/yastationd/releases/latest', { headers: { Accept: 'application/vnd.github+json' } })
    if (!r.ok) return null
    const j = await r.json()
    const rel = { tag: j.tag_name as string, url: j.html_url as string, date: (j.published_at || '').slice(0, 10) }
    try {
      sessionStorage.setItem('yst.latest', JSON.stringify({ at: Date.now(), rel }))
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
