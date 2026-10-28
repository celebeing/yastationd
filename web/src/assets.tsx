import { useEffect, useState } from 'preact/hooks'
import * as api from './api'
import type { Peer } from './api'

// Assets (docs/assets.md): the owner's own media on the speaker. Videos for the video mode are the
// only kind so far: a video file converted here in the browser (no ffmpeg needed), or files made
// by cmd/videoconv, uploaded into the speaker's library.

// Room left free on the speaker's flash after an upload, for its logs and config.
const KEEP_FREE = 20 << 20

const fmtMB = (b: number) => (b >= 10 << 20 ? Math.round(b / 1048576) + ' MB' : b >= 1 << 20 ? (b / 1048576).toFixed(1) + ' MB' : Math.max(1, Math.round(b / 1024)) + ' KB')

/** A video's name on the speaker, from its file name: lower-case letters, digits, - and _. */
function slug(file: string) {
  const s = file
    .replace(/\.[^.]*$/, '')
    .replace(/\.led$/, '')
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^[-_]+|-+$/g, '')
    .slice(0, 40)
  return s || 'video'
}

export function VideosTab({ peer, base, reload }: { peer: Peer; base: string; reload: () => void }) {
  const [a, setA] = useState<api.Assets | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')
  const [confirmDel, setConfirmDel] = useState('')
  const load = () =>
    api.assets(peer.id, base).then(
      (x) => {
        setA(x)
        setErr('')
      },
      (e) => setErr(e.message || String(e)),
    )
  useEffect(() => {
    load()
  }, [peer.id, base])

  async function act(name: string, what: 'use' | 'play' | 'delete') {
    setBusy(name)
    setErr('')
    try {
      if (what === 'delete') await api.deleteVideo(peer.id, base, name)
      else await api.useVideo(peer.id, base, name)
      if (what === 'play') await api.control(peer.id, base, 'display_mode', 'video')
      setConfirmDel('')
      await load()
      setTimeout(reload, 300)
    } catch (e: any) {
      setErr(e.message || String(e))
    }
    setBusy('')
  }

  if (!a) return <section class="card">{err ? <p class="err">{err}</p> : <p class="muted">Loading…</p>}</section>
  const v = a.videos
  return (
    <div class="assets">
      <section class="card">
        <h3>Videos</h3>
        <p class="muted small">
          Clips for the video mode: the picture on the {v.screen[0]}×{v.screen[1]} screen with its sound through the speaker, like Bad Apple.
          Pick one to be the video mode's, or play it now; it plays once and the clock comes back. Black-and-white shadow art reads best at
          this size.
        </p>
        {err && <p class="err">{err}</p>}
        {v.items.length === 0 ? (
          <p class="muted">No videos yet: add one below.</p>
        ) : (
          <table class="assetlist">
            <tbody>
              {v.items.map((it) => (
                <tr class={it.current ? 'current' : ''}>
                  <td>
                    <strong>{it.name}</strong>
                    {it.current && <span class="tag">the video mode's</span>}
                    <div class="muted small">
                      picture {fmtMB(it.frames_bytes)} · {it.audio_bytes ? 'sound ' + fmtMB(it.audio_bytes) : 'no sound'}
                    </div>
                  </td>
                  <td class="actions">
                    {confirmDel === it.name ? (
                      <>
                        <span class="small">Delete {it.name}?</span>
                        <button class="danger" disabled={!!busy} onClick={() => act(it.name, 'delete')}>
                          Delete
                        </button>
                        <button class="ghost" onClick={() => setConfirmDel('')}>
                          Keep
                        </button>
                      </>
                    ) : (
                      <>
                        <button class="primary" disabled={!!busy} onClick={() => act(it.name, 'play')}>
                          Play now
                        </button>
                        {!it.current && (
                          <button disabled={!!busy} onClick={() => act(it.name, 'use')}>
                            Use for the video mode
                          </button>
                        )}
                        <button class="ghost" disabled={!!busy} onClick={() => setConfirmDel(it.name)}>
                          Delete
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {a.free >= 0 && <p class="muted small">{fmtMB(a.free)} free on the speaker.</p>}
      </section>
      <AddVideo peer={peer} base={base} screen={v.screen} free={a.free} onDone={load} />
    </div>
  )
}

function AddVideo(p: { peer: Peer; base: string; screen: [number, number]; free: number; onDone: () => void }) {
  const [mode, setMode] = useState<'convert' | 'files'>('convert')
  const [file, setFile] = useState<File | null>(null)
  const [frames, setFrames] = useState<File | null>(null)
  const [audio, setAudio] = useState<File | null>(null)
  const [name, setName] = useState('')
  const [fps, setFps] = useState(30)
  const [fit, setFit] = useState<'pad' | 'crop'>('pad')
  const [stage, setStage] = useState('')
  const [pct, setPct] = useState(0)
  const [err, setErr] = useState('')
  const [done, setDone] = useState('')
  const running = stage !== ''

  async function upload(what: 'frames' | 'audio', blob: Blob, label: string) {
    setStage(label)
    setPct(0)
    return api.uploadAsset(p.peer.id, p.base, `/api/assets/videos/${name}/${what}`, blob, (n) => setPct(n / blob.size))
  }

  async function go() {
    setErr('')
    setDone('')
    try {
      let fr: Blob, au: Blob | null
      if (mode === 'convert') {
        if (!file) return
        const out = await convertVideo(file, p.screen[0], p.screen[1], fps, fit, (s, f) => {
          setStage(s)
          setPct(f)
        })
        fr = out.frames
        au = out.audio
      } else {
        if (!frames) return
        fr = frames
        au = audio
      }
      if (p.free >= 0 && fr.size + (au?.size || 0) > p.free - KEEP_FREE) throw new Error('not enough room on the speaker')
      const r = await upload('frames', fr, 'Uploading the picture')
      if (au) await upload('audio', au, 'Uploading the sound')
      setDone(`${name}: ${r.frames} frames, ${(r.duration_ms / 1000).toFixed(1)} s${au ? '' : ', no sound'}.`)
      setFile(null)
      setFrames(null)
      setAudio(null)
      p.onDone()
    } catch (e: any) {
      setErr(e.message || String(e))
    }
    setStage('')
  }

  const ready = /^[a-z0-9][a-z0-9_-]{0,39}$/.test(name) && (mode === 'convert' ? !!file : !!frames)
  return (
    <section class="card">
      <h3>Add a video</h3>
      <div class="segmented" role="tablist">
        <button role="tab" aria-selected={mode === 'convert'} class={mode === 'convert' ? 'on' : ''} onClick={() => setMode('convert')}>
          Convert a video file
        </button>
        <button role="tab" aria-selected={mode === 'files'} class={mode === 'files' ? 'on' : ''} onClick={() => setMode('files')}>
          Upload converted files
        </button>
      </div>
      {mode === 'convert' ? (
        <>
          <p class="muted small">
            Any video the browser can play (MP4, WebM). It is turned into {p.screen[0]}×{p.screen[1]} frames and a WAV here in the browser,
            then uploaded. A few minutes of video take a few minutes to convert; keep the tab open.
          </p>
          <div class="row">
            <input
              type="file"
              accept="video/*"
              disabled={running}
              onChange={(e) => {
                const f = e.currentTarget.files?.[0] || null
                setFile(f)
                if (f) setName(slug(f.name))
              }}
            />
          </div>
          <div class="row">
            <label class="inline small">
              Frame rate{' '}
              <select value={String(fps)} disabled={running} onChange={(e) => setFps(+e.currentTarget.value)}>
                {[30, 25, 20, 15].map((n) => (
                  <option value={String(n)}>{n} fps</option>
                ))}
              </select>
            </label>
            <label class="inline small">
              Fit{' '}
              <select value={fit} disabled={running} onChange={(e) => setFit(e.currentTarget.value as any)}>
                <option value="pad">the whole picture (black bars)</option>
                <option value="crop">fill the screen (cut the edges)</option>
              </select>
            </label>
          </div>
        </>
      ) : (
        <>
          <p class="muted small">
            Files made by <code>cmd/videoconv</code> (docs/video-mode.md): the frames (<code>.led</code> or <code>.led.gz</code>, made for a{' '}
            {p.screen[0]}×{p.screen[1]} screen) and, if it has sound, the WAV.
          </p>
          <div class="row">
            <label class="inline small">
              Frames{' '}
              <input
                type="file"
                accept=".led,.gz"
                disabled={running}
                onChange={(e) => {
                  const f = e.currentTarget.files?.[0] || null
                  setFrames(f)
                  if (f) setName(slug(f.name.replace(/\.gz$/, '')))
                }}
              />
            </label>
            <label class="inline small">
              Sound{' '}
              <input type="file" accept=".wav,audio/wav" disabled={running} onChange={(e) => setAudio(e.currentTarget.files?.[0] || null)} />
            </label>
          </div>
        </>
      )}
      <div class="row">
        <label class="inline small">
          Name{' '}
          <input type="text" value={name} disabled={running} maxLength={40} onInput={(e) => setName(e.currentTarget.value.toLowerCase())} />
        </label>
        <button class="primary" disabled={!ready || running} onClick={go}>
          {mode === 'convert' ? 'Convert and upload' : 'Upload'}
        </button>
      </div>
      {running && (
        <div class="progress" role="status">
          <span class="small">
            {stage}… {Math.round(pct * 100)}%
          </span>
          <span class="gauge">
            <i style={{ width: Math.round(pct * 100) + '%' }} />
          </span>
        </div>
      )}
      {err && <p class="err">{err}</p>}
      {done && <p class="small">{done}</p>}
    </section>
  )
}

/** convertVideo turns a video file into the video mode's files: frames in the .led text format
 * (docs/video-mode.md; the same as cmd/videoconv writes), gzipped, and a 48 kHz stereo WAV. The
 * frames come from seeking a <video> frame by frame and area-averaging each picture down to the
 * screen: the video's own (gamma-encoded) levels, as videoconv keeps them. */
export async function convertVideo(
  file: File,
  w: number,
  h: number,
  fps: number,
  fit: 'pad' | 'crop',
  progress: (stage: string, frac: number) => void,
): Promise<{ frames: Blob; audio: Blob | null }> {
  const url = URL.createObjectURL(file)
  try {
    const video = document.createElement('video')
    video.muted = true
    video.preload = 'auto'
    video.src = url
    await new Promise<void>((ok, bad) => {
      video.onloadeddata = () => ok()
      video.onerror = () => bad(new Error('the browser cannot play this file'))
    })
    const K = 8 // supersampling: each screen pixel is the mean of K×K canvas pixels
    const canvas = document.createElement('canvas')
    canvas.width = w * K
    canvas.height = h * K
    const ctx = canvas.getContext('2d', { willReadFrequently: true })!
    ctx.imageSmoothingQuality = 'high'
    const vw = video.videoWidth,
      vh = video.videoHeight
    // Like videoconv's ffmpeg filter: scale to whole screen pixels, then pad or crop by whole
    // pixels (a half-pixel offset would blur every edge into its neighbour).
    const s = fit === 'pad' ? Math.min(w / vw, h / vh) : Math.max(w / vw, h / vh)
    const cw = Math.max(1, Math.round(vw * s)),
      ch = Math.max(1, Math.round(vh * s))
    const dw = cw * K,
      dh = ch * K
    const dx = Math.floor((w - cw) / 2) * K,
      dy = Math.floor((h - ch) / 2) * K
    const n = Math.max(1, Math.floor(video.duration * fps))
    const at = (i: number) => Math.floor((i * 1000 + Math.floor(fps / 2)) / fps) // videoconv's frameMs rounding
    const hex = '0123456789abcdef'
    const lines: string[] = []
    const rgb = new Uint8Array(w * h * 3)
    for (let i = 0; i < n; i++) {
      await new Promise<void>((ok) => {
        video.onseeked = () => ok()
        video.currentTime = Math.min(video.duration, (i + 0.5) / fps)
      })
      ctx.fillStyle = '#000'
      ctx.fillRect(0, 0, w * K, h * K)
      ctx.drawImage(video, dx, dy, dw, dh)
      const px = ctx.getImageData(0, 0, w * K, h * K).data
      for (let y = 0; y < h; y++)
        for (let x = 0; x < w; x++) {
          let r = 0,
            g = 0,
            b = 0
          for (let yy = 0; yy < K; yy++)
            for (let xx = 0; xx < K; xx++) {
              const o = ((y * K + yy) * w * K + x * K + xx) * 4
              r += px[o]
              g += px[o + 1]
              b += px[o + 2]
            }
          const o = (y * w + x) * 3
          rgb[o] = Math.round(r / (K * K))
          rgb[o + 1] = Math.round(g / (K * K))
          rgb[o + 2] = Math.round(b / (K * K))
        }
      let line = ''
      for (let p = 0; p < rgb.length; p++) {
        if (p > 0 && p % 3 === 0) line += ' '
        line += hex[rgb[p] >> 4] + hex[rgb[p] & 15]
      }
      lines.push(line + ' ' + (at(i + 1) - at(i)) + '\n')
      if (i % 10 === 0) progress('Converting the picture', i / n)
    }
    progress('Compressing the picture', 1)
    const frames = await new Response(new Blob(lines).stream().pipeThrough(new CompressionStream('gzip'))).blob()

    progress('Converting the sound', 0)
    let audio: Blob | null = null
    try {
      const decoded = await new OfflineAudioContext(2, 1, 48000).decodeAudioData(await file.arrayBuffer())
      const off = new OfflineAudioContext(2, Math.ceil(decoded.duration * 48000), 48000)
      const src = off.createBufferSource()
      src.buffer = decoded // a mono track is spread to both channels
      src.connect(off.destination)
      src.start()
      audio = wav(await off.startRendering())
    } catch {
      audio = null // no sound track, or one the browser cannot decode: the video plays silently
    }
    progress('Converting the sound', 1)
    return { frames, audio }
  } finally {
    URL.revokeObjectURL(url)
  }
}

/** wav: an AudioBuffer as a 16-bit PCM WAV, the format the speaker plays (aplay). */
function wav(b: AudioBuffer): Blob {
  const ch = b.numberOfChannels,
    n = b.length
  const out = new DataView(new ArrayBuffer(44 + n * ch * 2))
  const str = (o: number, s: string) => [...s].forEach((c, i) => out.setUint8(o + i, c.charCodeAt(0)))
  str(0, 'RIFF')
  out.setUint32(4, 36 + n * ch * 2, true)
  str(8, 'WAVEfmt ')
  out.setUint32(16, 16, true)
  out.setUint16(20, 1, true)
  out.setUint16(22, ch, true)
  out.setUint32(24, b.sampleRate, true)
  out.setUint32(28, b.sampleRate * ch * 2, true)
  out.setUint16(32, ch * 2, true)
  out.setUint16(34, 16, true)
  str(36, 'data')
  out.setUint32(40, n * ch * 2, true)
  const data = [...Array(ch)].map((_, c) => b.getChannelData(c))
  let o = 44
  for (let i = 0; i < n; i++)
    for (let c = 0; c < ch; c++) {
      const v = Math.max(-1, Math.min(1, data[c][i]))
      out.setInt16(o, v < 0 ? v * 0x8000 : v * 0x7fff, true)
      o += 2
    }
  return new Blob([out.buffer], { type: 'audio/wav' })
}
