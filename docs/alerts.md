# Grafana alerts on the speakers

Grafana's alerts reach the speakers through the MQTT broker they already use: Grafana's MQTT contact
point publishes each notification to one topic, every speaker with that topic subscribes, and each
decides for itself what to do. Home Assistant only switches the speakers' modes and says an alert
aloud for a speaker that asks it to. Setting it up: [guides/grafana-alerts.md](guides/grafana-alerts.md).

Code: `internal/alerts` (the notification and the label matchers), `internal/app/alerts.go`.

## The notification

The contact point's default JSON (Grafana's MQTT integration, "Message format: json"): the
Alertmanager webhook shape, a group of alerts with `status` (`firing` / `resolved`), `labels`,
`annotations` and a `fingerprint`. The speaker reads, per alert:

- the **title**: the `alertname` label (Grafana puts the rule's title there);
- the **severity**: the `severity` label, lower case (`critical`, `warning`, anything else);
- the **speech**: the `speech` annotation if the rule has one, else the title. Only this is said,
  never the summary or the description.

An alert without a fingerprint gets one from its labels. A payload that is not such JSON is logged
and dropped.

## Which speaker takes what

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

## What it does

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

## Saying an alert aloud

The speaker has no text-to-speech; HA has, with the pipeline's voice. The speaker fires an event of
its entity `event.<name>_alert_to_say` (`event_type: say`, attribute `text`), and an HA automation
([the guide](guides/grafana-alerts.md)) calls `assist_satellite.announce` on the satellite in the
same area as that speaker, with `preannounce: false`. The announcement comes back to the speaker as
any other, but within 30 s of its request it plays without the announce chime: the alert's own
moment came just before.

`alerts_speak` (shared) picks which alerts are said, with the same matchers; the default is
`severity=critical`; empty says every one.

## HA entities

With `alerts_topic` set: `select.<name>_alerts` (the mode, a config entity), `sensor.<name>_alerts_on`
(the count of alerts on here; attributes: their titles, severities and since when, and whether they
were acknowledged), `button.<name>_acknowledge_alerts`, `event.<name>_alert_to_say`.

## Flash

The mode is part of the state file (`<cfw>/yastationd.state`), written when it changes (as every option, [storage.md](storage.md)).
Nothing else about alerts is written.
