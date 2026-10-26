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
