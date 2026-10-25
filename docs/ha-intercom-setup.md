# Intercom: setting it up with Home Assistant

How to get 1-to-1 calls between the speakers: the speaker side, what shows up in HA, and the one
HA script that makes «Алиса, позвони в спальню» work. How the intercom works inside:
[intercom.md](intercom.md). Template: [`../hass/intercom/`](../hass/intercom/).

```
«Алиса, позвони в спальню» ─► speaker A ─(Wyoming)─► HA Assist (LLM) ─► script "Позвонить в комнату"
                                                                  └► MQTT <prefix>/intercom/call {"to": B}
speaker A (it just sent the request) ─ invite (MQTT, signed) ─► speaker B: rings
speaker A ◄──────── audio, UDP 10710, AES-GCM ────────► speaker B
```

The phrases are Russian (that is what the speakers speak); adapt them for another language.

## 1. On every speaker

1. `yastationd.conf`:

   ```
   intercom        = true
   intercom_secret = <the same random string on every speaker, at least 16 characters>
   intercom_name   = <the room, e.g. Спальня>      (the name others see; default device_name)
   ```

   Then restart the daemon (or redeploy). The secret signs the calls and keys their encryption:
   a speaker without it can't call or be called.
2. `make vqe-models` (from `yastationd/`, `DEVICE=root@<speaker>`): the VQE's call-mode noise
   reducer, copied from the speaker's own firmware. Firmware 3.21 ships none: pass
   `VQE_MODEL=<model_call.nnet>` from a firmware dump of your own. Without it calls still work,
   on a plainer uplink (more echo and noise).
3. `make sounds`: the vendor's call sounds (ringtone, connected, ended, busy) among the rest.
4. The speakers must reach each other on **UDP 10710**; they already share the MQTT broker.

## 2. What shows up in HA

Per speaker, by MQTT discovery (no YAML):

| Entity | What |
|---|---|
| `sensor` Intercom | `idle` / `calling` / `ringing` / `in_call`; attributes `peer`, `peer_name`, `incoming`, `since`, `ended` (why the last call ended: `missed`, `rejected: declined`, `by voice`, …) |
| `select` Intercom target | the other speakers by their `intercom_name` |
| `button` Intercom call | calls the selected target (answers when ringing) |
| `button` Intercom hang up | hangs up, cancels or declines |

That alone makes calls from a dashboard: pick the target, press "call". A minimal card:

```yaml
type: entities
title: Интерком
entities:
  - sensor.<speaker>_intercom
  - select.<speaker>_intercom_target
  - button.<speaker>_intercom_call
  - button.<speaker>_intercom_hang_up
```

## 3. Calling by voice: the script

«Алиса, позвони в спальню» is a request to HA's conversation agent (an LLM), like «скажи в
спальню, что…». Room names are in no spotter's vocabulary, so this one step needs HA.

1. Take [`hass/intercom/intercom_call.yaml`](../hass/intercom/intercom_call.yaml), replace the
   `<PLACEHOLDERS>` with your rooms, the people they belong to (aliases the LLM can map, «позвони
   маме»), and each room's speaker `device_id`.
2. Add it as a script: the UI's YAML editor, or `POST /api/config/script/config/intercom_call`.
3. **Expose it to Assist** (Settings → Voice assistants → Expose → the script), or over the
   websocket API: `homeassistant/expose_entity` with `assistants: ["conversation"]`.

The script publishes `{"to":"<device_id>"}` on the shared `yastation/intercom/call`. Every
speaker gets it; the one that sent HA a request in the last 60 s places the call (a tool script
doesn't know which satellite asked, so the speakers decide). The LLM's own answer («Звоню.») is
spoken as usual.

## 4. During a call: all on the speaker

Answering, declining, hanging up and the volume are done by the speaker's own spotters; nothing
goes to HA (the vendor command model has no «да»/«нет»):

| When | Say | Or |
|---|---|---|
| ringing | «продолжи» / «играй» — answer | the voice key |
| ringing | «стоп» / «хватит» / «пауза» — decline | the mute switch |
| in a call | «Алиса, стоп / хватит / пауза» — hang up | the voice key, the mute switch |
| in a call | «Алиса, громче / тише» | the volume keys |

In a call «Алиса» opens a 4 s window for one of these commands only; Assist requests wait for
the end of the call.

## 5. Automations (ideas)

- **A missed call** — notify the caller's phone:

  ```yaml
  triggers:
    - trigger: state
      entity_id: sensor.<speaker>_intercom
      to: idle
  conditions:
    - "{{ state_attr(trigger.entity_id, 'ended') == 'no answer' }}"
  actions:
    - action: notify.<phone>
      data:
        message: "{{ state_attr(trigger.entity_id, 'peer_name') }}: нет ответа"
  ```
- **A call from an automation** (e.g. a doorbell to the kitchen): `mqtt.publish` to
  `yastation/<caller device_id>/intercom/set` with `{"action":"call","to":"<callee device_id>"}`.

## Limits

- One call at a time per speaker; a second caller gets "busy".
- No "do not disturb" yet: the mute switch refuses calls meanwhile.
- Checked on two Mini 3 (bergamot); Mini 3 Pro (pickle) and Station 3 (orion) not yet.
