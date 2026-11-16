# Grafana alerts on the speakers (guide)

What the speakers do with alerts, and why: [alerts.md](../alerts.md). Here: setting it up. You need
Grafana with the MQTT contact point (Grafana 11+) that can reach the speakers' MQTT broker, and Home
Assistant for the alerts said aloud.

## 1. The speakers

In any speaker's web app, **Configuration → Alerts**:

- **Topic**: for example `yastation/alerts`; switch on "All speakers" so every speaker takes it.
  The speaker restarts with it (on trial), and Home Assistant gets its alert entities.
- **Alerts taken here**: on each speaker, the labels of the alerts it should react to; empty takes
  every alert that reaches the topic.
- **Alerts said aloud**: which ones a speaker in the loud mode says; `severity=critical` by default.
- **Alerts** (the mode): `off`, `quiet` or `loud` per speaker. A bedroom speaker: `off` or `quiet`.

## 2. Grafana

**Alerting → Contact points → + Add contact point**, integration **MQTT**:

| Field | Value |
|---|---|
| Broker URL | `tcp://<broker-host>:1883` |
| Topic | the speakers' topic, e.g. `yastation/alerts` |
| Message format | `json` (the default) |
| QoS | At least once |
| Retain | off: a speaker that restarts waits for the next repeat instead of replaying an old group |
| Disable resolved message | off: the resolve ends the speakers' red pulse |

Then a **notification policy** that sends the alerts meant for the speakers to this contact point
(by a label, for example `speakers=yes`), beside the policies you already have. The repeat interval
is how often an alert that stays on is sent again; the speakers ignore repeats unless "React to
repeats" is on, so a short one costs nothing.

In the alert rules:

- the **title** is what a speaker says, unless the rule has a **`speech`** annotation: then that is
  said instead. Keep it short and in the language of the voice («Диск на сервере почти полон»);
- a **`severity`** label of `critical` or `warning` picks the stronger moments (and, by default,
  only `critical` is said aloud).

## 3. Home Assistant: saying an alert

A speaker in the loud mode asks Home Assistant to say an alert by an event of its `Alert to say`
entity. One automation serves every speaker: it speaks on the satellite in the same area as the
speaker that asked, so each speaker needs its area (a speaker without one stays silent).

```yaml
alias: Speakers say Grafana alerts
mode: queued
triggers:
  - trigger: event
    event_type: state_changed
conditions:
  - condition: template
    value_template: >-
      {% set e = trigger.event.data %}
      {{ e.entity_id is match('event\..*_alert_to_say$')
         and e.new_state is not none and e.old_state is not none
         and e.old_state.state != 'unavailable'
         and e.new_state.attributes.event_type == 'say' }}
actions:
  - variables:
      satellite: >-
        {{ area_entities(area_id(trigger.event.data.entity_id))
           | select('match', 'assist_satellite\.') | first | default('') }}
  - condition: template
    value_template: "{{ satellite != '' }}"
  - action: assist_satellite.announce
    target:
      entity_id: "{{ satellite }}"
    data:
      message: "{{ trigger.event.data.new_state.attributes.text }}"
      preannounce: false
```

The voice is the satellite's pipeline's. The `old_state` checks keep a restart of Home Assistant or of
the speaker (the event entity coming back from nothing or from `unavailable`) from saying the last
alert again; `unknown` is let through, it is the state before the entity's first event.

## 4. A schedule (optional)

The mode is `select.<speaker>_alerts`; an automation can set it by time, for example the bedroom
speaker to `off` at night and back to `quiet` in the morning.

## Trying it

Publish a notification by hand to the topic:

```sh
mosquitto_pub -h <broker-host> -t yastation/alerts -m '{"status":"firing","alerts":[{"status":"firing","fingerprint":"test1","labels":{"alertname":"Проверка оповещения","severity":"critical"}}]}'
mosquitto_pub -h <broker-host> -t yastation/alerts -m '{"status":"resolved","alerts":[{"status":"resolved","fingerprint":"test1","labels":{"alertname":"Проверка оповещения","severity":"critical"}}]}'
```

The first fires the critical moment and the red pulse (and, in the loud mode, the phrase); the
second the green flash, and the pulse ends.
