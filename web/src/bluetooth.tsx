import { useEffect, useRef, useState } from 'preact/hooks'
import * as api from './api'
import type { Peer } from './api'

export function BluetoothTab({ peer, base }: { peer: Peer; base: string }) {
  const [state, setState] = useState<api.Bluetooth | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [forget, setForget] = useState<string | null>(null)
  const loadingRequest = useRef(false)
  const setBluetoothState = (next: api.Bluetooth) => setState({ ...next, devices: Array.isArray(next.devices) ? next.devices : [] })
  useEffect(() => {
    let live = true
    const load = async () => {
      if (loadingRequest.current) return
      loadingRequest.current = true
      try {
        const next = await api.bluetooth(peer.id, base)
        if (live) { setBluetoothState(next); setErr('') }
      } catch (e: unknown) {
        if (live) setErr(e instanceof Error ? e.message : String(e))
      } finally {
        loadingRequest.current = false
        if (live) setLoading(false)
      }
    }
    void load()
    const timer = setInterval(() => { void load() }, 1000)
    return () => { live = false; clearInterval(timer) }
  }, [peer.id, base])
  async function run(f: () => Promise<api.Bluetooth>) {
    setErr('')
    try {
      setBluetoothState(await f())
      return true
    } catch (e: unknown) {
      setErr(e instanceof Error ? e.message : String(e))
      return false
    }
  }
  if (!state) return <section class="card"><h3>Bluetooth audio</h3><p class={err ? 'err' : 'muted'}>{err || (loading ? 'Loading Bluetooth state…' : 'Bluetooth state was unavailable. Reload the page to retry.')}</p></section>
  return <div class="cols">
    <section class="card">
      <h3>Bluetooth audio</h3>
      {!state.pairing ? <><p>Pair a phone for Bluetooth audio. The speaker is hidden except during this two-minute pairing window.</p><button onClick={() => run(() => api.startBluetoothPair(peer.id, base))}>Pair phone</button></> : <>
        <p class="ok">Discoverable until {new Date(state.expires! * 1000).toLocaleTimeString()}.</p>
        {state.pending ? <><p>Compare this code with the phone, then confirm.</p><p class="pair-code">{state.code}</p><p class="muted small">{state.device || 'Phone'} is asking to pair.</p><button onClick={() => run(() => api.confirmBluetoothPair(peer.id, base))}>Confirm pairing</button></> : <p class="muted">Choose this speaker on the phone. Its pairing code appears here and on the speaker.</p>}
        <button class="plain" onClick={() => run(() => api.cancelBluetoothPair(peer.id, base))}>Cancel</button>
      </>}
      {state.error && <p class="err">{state.error}</p>}
    </section>
    <section class="card"><h3>Paired phones</h3>
      {state.devices.length === 0 ? <p class="muted">None.</p> : <table class="nets"><tbody>{state.devices.map((d) => <tr><td>{d.name || 'Phone'} {d.connected && <span class="badge ok-badge">connected</span>}</td><td><button class="plain" onClick={() => run(() => api.disconnectBluetooth(peer.id, base, d.id))} disabled={!d.connected}>Disconnect</button><button class="plain" onClick={() => setForget(d.id)}>Forget</button></td></tr>)}</tbody></table>}
      {forget && <p>Forget this phone? <button onClick={async () => { if (await run(() => api.forgetBluetooth(peer.id, base, forget))) setForget(null) }}>Yes</button> <button class="plain" onClick={() => setForget(null)}>No</button></p>}
    </section>
  </div>
}
