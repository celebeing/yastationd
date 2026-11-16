# Alerts and notifications on the speakers

Two kinds of message, kept apart:

- an **alert** is a state that lasts — something is wrong until it is fixed. It comes from Grafana,
  fires, stays on (a red pulse on the ring) and resolves; it has a severity;
- a **notification** is a one-off event with a tone — good (the washing is done, the line is back),
  info, or bad (a backup failed) — and no state: nothing stays on, nothing resolves. It comes from
  Home Assistant or from Grafana.

Each has its own mode per speaker (off, quiet, loud), so a bedroom can ignore alerts and still get
quiet notifications.

## Alerts

Grafana's alerts reach the speakers through the MQTT broker they already use: Grafana's MQTT contact
point publishes each notification to one topic, every speaker with that topic subscribes, and each
decides for itself what to do. Home Assistant only switches the speakers' modes and says an alert
aloud for a speaker that asks it to. Setting it up: [guides/grafana-alerts.md](guides/grafana-alerts.md).

Code: `internal/alerts` (Grafana's notification and the label matchers), `internal/app/alerts.go`,
`internal/app/notify.go`.

### What Grafana sends

The contact point's default JSON (Grafana's MQTT integration, "Message format: json"): the
Alertmanager webhook shape, a group of alerts with `status` (`firing` / `resolved`), `labels`,
`annotations` and a `fingerprint`. The speaker reads, per alert:

- the **title**: the `alertname` label (Grafana puts the rule's title there);
- the **severity**: the `severity` label, lower case (`critical`, `warning`, anything else);
- the **speech**: the `speech` annotation if the rule has one, else the title. Only this is said,
  never the summary or the description. A label or annotation `speak=no` keeps the rule silent;
- `kind=notification`: the rule is a notification, not an alert (below);
- the `resolved` annotation: the good news its resolve brings (below).

An alert without a fingerprint gets one from its labels. A payload that is not such JSON is logged
and dropped.

### Which speaker takes what

Grafana's notification policies decide what reaches the topic at all. On the speaker:

- `alerts_topic` (shared): the topic; empty, the speaker takes no alerts and HA shows none of their
  entities;
- `alerts_match` (this speaker's own): label matchers, comma-separated `label=value`,
  `label!=value`, `label=~regex`, `label!~regex`, all of which must hold; a regex matches the whole
  value and a missing label reads as empty, as in Alertmanager. Empty: every alert. An alert that
  does not match is ignored entirely here, its resolve too;
- the **Alerts mode** (this speaker's own, persisted, HA select `alerts_mode` and the web app's
  option): `off` ignores alerts and forgets the ones on; `quiet` shows the moments without their
  sounds; `loud` plays them with their sounds and says the alerts `alerts_speak` picks. A new
  speaker (and a state file from before) is `quiet`.

HA can switch the mode on a schedule with an automation; the speaker keeps no schedule of its own.

### What it does

Per alert that passes `alerts_match`, in a mode other than `off`:

| Event | Reaction |
|---|---|
| firing, not on yet | fires `alert_firing_critical`, `alert_firing_warning` or `alert_firing` by severity (silent in `quiet`); holds `alert_active`; in `loud`, if `alerts_speak` matches, asks HA to say its speech |
| firing, already on (Grafana's repeat) | nothing, unless `alerts_repeat` is on: then as a new one |
| resolved, on here | fires `alert_resolved` (silent in `quiet`); the last one ends `alert_active` |
| resolved, never seen here | nothing (it fired before a restart, or before a filter let it in) |

`alert_active` never plays a sound: it is the standing sign, a red pulse on the ring by default.
The moments are the theme's like any other ([asset-packs.md](asset-packs.md)); their defaults are in
its table.

What is on lives in memory only. After a restart the speaker learns of an alert still on from
Grafana's next repeat notification, and takes it as new (so it does react to it then); until then
nothing shows.

**Acknowledge** (HA button `alerts_ack`): ends `alert_active` on this speaker until a new alert
fires; the alerts stay on, and their resolves still show. Useful when Grafana's contact point has
"Disable resolved message" on, which otherwise leaves the pulse until the speaker restarts.

## Notifications

A notification has a `text`, a `tone` (`good`, `info`, `bad`; anything else is `info`) and may ask
not to be said (`"speak": false`). The speaker fires `notify_good`, `notify_info` or `notify_bad`
(silent in the quiet mode), scrolls the text on the front display (`notify_show_text`, on by
default; for the message duration), and in the loud mode has HA say the text. Nothing stays: there
is no state, no acknowledge, no resolve.

Where they come from:

- **Home Assistant, to one speaker:** its `notify.<name>_notification` entity
  (`notify.send_message`). The message is the payload: plain text is an info; JSON is a notification
  (`{"text": "Стиралка достирала", "tone": "good"}`; `message` is taken for `text`).
- **To every speaker:** the same payload on `notify_topic` (shared; for example `yastation/notify`),
  by `mqtt.publish` or anything else that speaks MQTT.
- **Grafana, as an event:** a rule labelled `kind=notification` (with a `tone` label). Its firing is
  the event, said once: Grafana's repeats are ignored, its resolve only forgets it. `alerts_match`
  applies (it comes on the alerts' topic), `alerts_speak` does not. Grafana's engine is state-based,
  so such an event comes one evaluation interval and the route's group wait after the condition,
  once per time the condition holds — not once per log line.
- **Grafana, the end of an alert:** a rule with a `resolved` annotation ("Основной интернет снова
  жив") gives a good notification with that text when it resolves — on every speaker that takes
  the rule, whatever its alerts mode (the alert itself still fires, stays on and resolves as above).

The mode (`notify_mode`, HA select and the web app's option) is per speaker, `quiet` by default.

## Saying aloud

The speaker has no text-to-speech; HA has, with the pipeline's voice. The speaker fires an event of
its entity `event.<name>_say_aloud` (`event_type: say`, attribute `text`), and an HA automation
([the guide](guides/grafana-alerts.md)) calls `assist_satellite.announce` on the satellite in the
same area as that speaker, with `preannounce: false`. The announcement comes back to the speaker as
any other, but within 30 s of its request it plays without the announce chime: the alert's own
moment came just before. Alerts and notifications share this way out.

`alerts_speak` (shared) picks which alerts are said, with the same matchers; the default is
`severity=critical`; empty says every one.

## HA entities

Every speaker: `select.<name>_notifications` (the notifications' mode), `notify.<name>_notification`,
`event.<name>_say_aloud`. With `alerts_topic` set, also: `select.<name>_alerts` (the alerts' mode),
`sensor.<name>_alerts_on` (the count of alerts on here; attributes: their titles, severities and since
when, and whether they were acknowledged), `button.<name>_acknowledge_alerts`.

## Flash

The modes are part of the state file (`<cfw>/yastationd.state`), written when it changes (as every option, [storage.md](storage.md)).
Nothing else about alerts is written.
