import { useEffect, useRef, useState } from 'preact/hooks'
import type { Entity } from './api'

// The controls of an MQTT entity (switch, select, number, text…), shared by the speaker card,
// the controls tab and the settings.

export const name = (e: Entity) => e.config.name || e.object

/** Switch: the app's on/off control (a slider), for every boolean a person flips: a setting, an
 * option, an entity. Use it rather than a checkbox or a pair of buttons (docs/design.md, "Components"). */
export function Switch(p: { checked: boolean; label: string; onChange: (on: boolean) => void; disabled?: boolean }) {
  return (
    <label class="switch">
      <input type="checkbox" aria-label={p.label} checked={p.checked} disabled={p.disabled} onChange={(x) => p.onChange(x.currentTarget.checked)} />
      <span />
    </label>
  )
}

export function Control({ e, send }: { e: Entity; send: (p: string) => void }) {
  const c = e.config
  switch (e.component) {
    case 'switch': {
      const on = e.value === (c.payload_on || 'ON')
      return <Switch label={name(e)} checked={on} onChange={(v) => send(v ? c.payload_on || 'ON' : c.payload_off || 'OFF')} />
    }
    case 'select':
      return (
        <select aria-label={name(e)} value={e.value || ''} onChange={(x) => send(x.currentTarget.value)}>
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
          aria-label={name(e)}
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
        aria-label={name(e)}
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
      <input aria-label={name(e)} value={v} maxLength={e.config.max || 255} onInput={(x) => setV(x.currentTarget.value)} />
      <button type="submit">{e.component === 'notify' ? 'Show' : 'Set'}</button>
    </form>
  )
}

