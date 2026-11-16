# Alerts and notifications on the speakers (guide)

What the speakers do with alerts (states, from Grafana) and notifications (one-off events, from HA or
Grafana), and why: [alerts.md](../alerts.md). Here: setting it up. You need Grafana with the MQTT
contact point (Grafana 11+) that can reach the speakers' MQTT broker, and Home Assistant for
anything said aloud.

## 1. The speakers

In any speaker's web app, **Configuration → Alerts**:

- **Topic**: for example `yastation/alerts`; switch on "All speakers" so every speaker takes it.
  The speaker restarts with it (on trial), and Home Assistant gets its alert entities.
- **Alerts taken here**: on each speaker, the labels of the alerts it should react to; empty takes
  every alert that reaches the topic.
- **Alerts said aloud**: which ones a speaker in the loud mode says; `severity=critical` by default.
- **Alerts** (the mode): `off`, `quiet` or `loud` per speaker. A bedroom speaker: `off` or `quiet`.
- **Notifications**: their own mode, the same three; and **Topic for every speaker** (for example
  `yastation/notify`) if something should reach all speakers at once.

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
  only `critical` is said aloud);
- `speak=no` (a label or an annotation) keeps a rule silent;
- a **`resolved`** annotation is good news said when the alert resolves: «Основной интернет снова
  жив, можно переключаться» on a "main line down" rule;
- **`kind=notification`** with a **`tone`** label (`good`, `info`, `bad`) makes the rule an event, not
  an alert: its firing is said once, nothing stays on (a "backup failed" rule over the logs). Give the
  speakers' route a short group wait if the delay matters.

## 3. Home Assistant: saying an alert or a notification

A speaker in the loud mode asks Home Assistant to say something by an event of its `Say aloud`
entity. One automation serves every speaker: it speaks on the satellite in the same area as the
speaker that asked, so each speaker needs its area (a speaker without one stays silent).

```yaml
alias: Speakers say alerts and notifications
mode: queued
triggers:
  - trigger: event
    event_type: state_changed
conditions:
  - condition: template
    value_template: >-
      {% set e = trigger.event.data %}
      {{ e.entity_id is match('event\..*_say_aloud$')
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

## 4. Notifications from Home Assistant

To one speaker, its Notification entity:

```yaml
action: notify.send_message
target:
  entity_id: notify.<speaker>_notification
data:
  message: '{"text": "Стиралка достирала", "tone": "good"}'
```

Plain text works too (an info). To every speaker, publish the same payload to the notifications'
topic:

```yaml
action: mqtt.publish
data:
  topic: yastation/notify
  payload: '{"text": "Курьер у двери"}'
```

## 5. A schedule (optional)

The modes are `select.<speaker>_alerts` and `select.<speaker>_notifications`; an automation can set them by time, for example the bedroom
speaker to `off` at night and back to `quiet` in the morning.

## Trying it

Publish a notification by hand to the topic:

```sh
mosquitto_pub -h <broker-host> -t yastation/alerts -m '{"status":"firing","alerts":[{"status":"firing","fingerprint":"test1","labels":{"alertname":"Проверка оповещения","severity":"critical"}}]}'
mosquitto_pub -h <broker-host> -t yastation/alerts -m '{"status":"resolved","alerts":[{"status":"resolved","fingerprint":"test1","labels":{"alertname":"Проверка оповещения","severity":"critical"}}]}'
```

The first fires the critical moment and the red pulse (and, in the loud mode, the phrase); the
second the green flash, and the pulse ends.
