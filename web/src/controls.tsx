import { useEffect, useRef, useState } from 'preact/hooks'
import type { Entity } from './api'

// The controls of an MQTT entity (switch, select, number, text…), shared by the speaker card,
// the controls tab and the settings.

export const name = (e: Entity) => e.config.name || e.object

export function Control({ e, send }: { e: Entity; send: (p: string) => void }) {
  const c = e.config
  switch (e.component) {
    case 'switch': {
      const on = e.value === (c.payload_on || 'ON')
      return (
        <label class="switch">
          <input type="checkbox" checked={on} onChange={(x) => send(x.currentTarget.checked ? c.payload_on || 'ON' : c.payload_off || 'OFF')} />
          <span />
        </label>
      )
    }
    case 'select':
      return (
        <select value={e.value || ''} onChange={(x) => send(x.currentTarget.value)}>
          {(c.options || []).map((o: string) => (
            <option value={o}>{o}</option>
          ))}
        </select>
      )
    case 'number':
      return <NumberControl e={e} send={send} />
    case 'button':
      return <button onClick={() => send(c.payload_press || 'PRESS')}>Press</button>
    case 'text':
    case 'notify':
      return <TextControl e={e} send={send} />
  }
  return <span class="muted">{e.value}</span>
}

function NumberControl({ e, send }: { e: Entity; send: (p: string) => void }) {
  const c = e.config
  const [v, setV] = useState(e.value || '')
  const editing = useRef(false)
  useEffect(() => {
    if (!editing.current) setV(e.value || '')
  }, [e.value])
  const commit = () => {
    editing.current = false
    if (v !== e.value) send(v)
  }
  const slider = c.mode !== 'box' && c.max - c.min <= 1000
  return (
    <span class="num">
      {slider && (
        <input
          type="range"
          min={c.min}
          max={c.max}
          step={c.step || 1}
          value={v}
          onInput={(x) => {
            editing.current = true
            setV(x.currentTarget.value)
          }}
          onChange={commit}
        />
      )}
      <input
        type="number"
        min={c.min}
        max={c.max}
        step={c.step || 1}
        value={v}
        onInput={(x) => {
          editing.current = true
          setV(x.currentTarget.value)
        }}
        onChange={commit}
      />
      {c.unit_of_measurement && <span class="muted small">{c.unit_of_measurement}</span>}
    </span>
  )
}

function TextControl({ e, send }: { e: Entity; send: (p: string) => void }) {
  const [v, setV] = useState(e.component === 'text' ? e.value || '' : '')
  return (
    <form
      class="text"
      onSubmit={(x) => {
        x.preventDefault()
        send(v)
        if (e.component === 'notify') setV('')
      }}
    >
      <input value={v} maxLength={e.config.max || 255} onInput={(x) => setV(x.currentTarget.value)} />
      <button type="submit">{e.component === 'notify' ? 'Show' : 'Set'}</button>
    </form>
  )
}

