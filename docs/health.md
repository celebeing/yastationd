# Health: what the speaker watches about itself

Status (2026-10-30): describes what is built. The monitors are spread over several files; this
page is their map, and the patterns a new one (an overheat warning, say) should copy. How each
area works in depth is in the docs linked per row.

## What is watched

| What | Source | How often | What it does | Keys | Code |
|---|---|---|---|---|---|
| Reachability (the health state) | TCP dials of the `probe` lines (`name=host:port@role`, role `broker` / `pipeline` / `lan`), plus the MQTT client's own connection state | every 10 s (dial timeout 2 s); at once on a broker connect and when the speaker becomes ready (`Recheck`) | state `online` / `degraded` / `offline`: `sensor health` and `binary_sensor pipeline_ok` (`connectivity`, ON = online), both diagnostic; an info log line on change; feeds the lifecycle's degraded state and the config trial | `probe` (config, advanced, shared) | `internal/health/health.go`; wiring and `parseProbe` in `internal/app/app.go` (`onHealthChange`) |
| Heartbeat | the health monitor touches a file each round | every 10 s | nothing in the daemon reads it; `diag.sh` reports its age (a stuck monitor shows as a stale file) | `heartbeat_file` (hidden, default `/tmp/yastationd.heartbeat`) | `health.go` (`touchHeartbeat`), `yastationd/scripts/diag.sh` |
| The link to HA (lifecycle) | `connected()`: the broker up (if configured) and HA holding the Wyoming satellite (if on), and the health state | every 500 ms | moments `booting` (held) → `boot_failed` (held, after the deadline) → ready (`startup` once per boot); after ready, missing for 10 s → `ha_lost` (held, dim red pulse) until back → `ha_back`; warn/info log lines | `boot_connect_timeout` (default 40 s) | `internal/app/lifecycle.go` (`statusLoop`, `showLife`, `showDegraded`) |
| Voice with no HA | a wake or key while the satellite has no connection | per attempt | moment `voice_unavailable` (bright red pulse, the stock error phrase) | — | `lifecycle.go` (`voiceUnavailable`) |
| Power supply (USB-C PD contract) | `/sys/class/power_supply/<power.pd>/{voltage_now,current_now,health}` (HUSB238 sink `ya_pd`) | first read 5 s after start, then every 5 min (each read makes the driver re-query and log to dmesg) | `sensor supply_voltage` (V) and `binary_sensor power_problem` (`problem`), both diagnostic, every read; when bad, once per boot: a warning in the log and moment `power_problem` (default: the stock `no_power` phrase). Bad = health not `Good` or voltage below `power.min_mv` | profile `power.pd`, `power.min_mv` (bergamot, pickle 9000; orion 15000: it contracts 20 V) | `internal/app/power.go`, `internal/hw/power.go` |
| Network (gateway) | broker connected, else a ping of the default gateway from `/proc/net/route` | every 30 s (not in Wi-Fi setup mode) | a ladder per outage: log → `wpa_cli -i wlan0 reconfigure` → the board's Wi-Fi controller reset (again every N min) → log the kernel tail, flush the log spool, reboot. Each watchdog reboot doubles the next limit (up to 4 h; count in `<cfw>/netwatch.count`, cleared after an hour up); never reboots a speaker whose uptime is below the limit | `net_watchdog_min` (10, 0 = off), `net_wifi_recovery`, `net_wifi_reconfigure_min` (2), `net_wifi_reset_min` (4), `net_wifi_reset_every_min` (10); profile `platform.wifi_reset` | `internal/app/netwatch.go`; [storage.md](storage.md) "The network watchdog" |
| A config saved from the web app | the health state before the save (in the `config.trial` marker) against the state after | once, 1 min after the restart | worse health → the previous config back and a restart (an error in the log); a crash while on trial → the supervisor restores it | — | `internal/app/settings.go` (`configTrialLoop`), `init.d/supervise.sh`; [web-ui.md](web-ui.md) |
| The daemon itself | its exit status and run time | per exit | restart (1 s after a deliberate exit; after a crash 2 s, doubling to 60 s); 5 crashes in a row each within 5 min of the start → `bin/yastationd.prev` swapped in (bad one kept as `.bad`); none to swap → retry every 5 min. The log is copy-truncated at 1 MB. Nothing is shown on the speaker | env tunables in the script header | `yastationd/init.d/supervise.sh`; [guides/deploying.md](guides/deploying.md) "Supervisor" |
| Vitals | `/proc/uptime`, `/proc/loadavg`, `/proc/meminfo`, `/proc/self/status` (VmRSS), `statfs` of the CFW root, every `/sys/class/thermal/thermal_zone*/{type,temp}` | on demand only: each `GET /api/vitals` (the web card polls every 5 s while open) and each `/metrics` scrape | readings, no reaction: the speaker card's vitals panel; `/metrics` series `yastation_memory_bytes`, `yastation_load`, `yastation_temperature_celsius{zone}`, `yastation_system_uptime_seconds` | — | `internal/app/vitals.go` (`readVitals`, `systemMetrics`); [web-ui.md](web-ui.md), [settings.md](settings.md) "The speaker card" |
| Uptime | the daemon's start time | every 30 s | `sensor uptime` (diagnostic) | — | `app.go` (`telemetryLoop`) |

Every numeric or on/off state the app publishes (`supply_voltage`, `power_problem`, `pipeline_ok`,
…) also shows up in `/metrics` as `yastation_<name>`, and `health` as `yastation_health{state}`,
without extra code: `serveMetrics` walks the state mirror.

**Not ours:** the kernel's hardware watchdog (bergamot's `reboot_reason` reads `watchdog_reboot`
after it fires; [guides/deploying.md](guides/deploying.md)), the kernel's thermal trips, and the
voice path's own wedge watchdog ([voice-slow-replies.md](voice-slow-replies.md)). The diagnostics
archive ([guides/diagnostics.md](guides/diagnostics.md)) collects a snapshot of all of the above
(temperatures, reboot reason, heartbeat age, the supervisor's lines) but watches nothing.

## Patterns for a new monitor

| Do | Copy |
|---|---|
| A loop `func (a *App) xLoop(stop <-chan struct{})` that returns at once when the board lacks the hardware (an empty profile key), waits a short first delay, then polls at a fixed period and exits on `stop` | `powerLoop` (`power.go`) |
| Start it with `go a.xLoop(stop)` in the start block of `app.go`, next to `powerLoop` / `luxLoop` / `netWatchLoop`; find the sysfs node once at start (`hw.FindPD(cfg.SysRoot, …)` into `a.pdDir`) | `app.go` |
| Read sysfs through the sysroot (`cfg.SysRoot`, `a.cfg().Path(…)`) so tests run against a fake tree | `vitals.go`, `hw/power.go` |
| A hardware threshold (what counts as bad on this board) is a **profile key** (`power.min_mv`); a preference is a **registry key** ([settings.md](settings.md)); read config through `a.cfg()` at the point of use | `board.go` `PowerSpec`, [porting-a-speaker.md](porting-a-speaker.md) |
| The condition as a diagnostic entity: `binary_sensor` with `device_class: problem`, `entity_category: diagnostic`, declared in `mqtt/discovery.go`; publish through `a.publish(a.topics.State("x"), onOff(bad))` (the web app's mirror and `/metrics` follow) | `power_problem` |
| The value next to it as a `sensor` (unit, `device_class`, `state_class: measurement`, diagnostic) | `supply_voltage` |
| What the owner sees or hears is a **moment**: an entry in `momentCatalog` and `defaultTheme` (`defaulttheme.go`), fired with `a.hook("name")`, listed in [asset-packs.md](asset-packs.md) | `power_problem` (one-shot) |
| A **held** state: the catalogue entry has a `slot` and `prio` and its default is `held(…)`; start with `a.hook("x")`, end with `a.endHook("x")`, fire only on a change (keep the last state in the loop), and fire a `x_back` moment on recovery if it should be heard | `ha_lost` / `ha_back` in `showDegraded` (`lifecycle.go`) |
| Not nagging: `a.firstThisBoot(marker)` with a marker in `/tmp` (survives daemon restarts, not a reboot) | `powerAlertedMarker` (`power.go`), `bootMarker` (`lifecycle.go`) |
| Debounce: a condition must hold for a grace period before it shows (`badSince` + `degradeGrace` 10 s) | `statusLoop` (`lifecycle.go`) |
| Escalation over time, counters in uptime (not wall time: a mini's clock is stale after a reboot), a counter on flash only on the rare event | `netWatch.step`, `netwatch.count` (`netwatch.go`) |
| Logs: `Warn` on the transition, not on every tick; read errors at `Debug` | `power.go`, `netwatch.go` |
| Tests: the decision as a pure function or a small state machine tested alone | `powerProblem`, `netWatch.step` (`netwatch_test.go`), `lifecycle_test.go` |

No monitor has hysteresis on a value (two thresholds): power is checked against one line and
alerts once per boot; the health state flips on a single probe round; the 10 s grace in the
lifecycle is the only time-based debounce, and the light level's `luxGate` (publish on a large
enough change) the only change gate. A temperature monitor needs both (temperatures hover around a
line).

## Thermal

- **What is read:** every `/sys/class/thermal/thermal_zone*`, by its `type` (not its index),
  millidegrees → °C, only on demand (vitals API, `/metrics`). No loop, no HA entity, no moment.
- **Zones:** orion has `thermal_zone0` = `soc_thermal` (38.6 °C idle, 36 °C under the usual load)
  with trips at 85 / 95 / 105 °C ([boards/orion.md](boards/orion.md), sensors survey).
  [web-ui.md](web-ui.md) says `soc_thermal` is present on every board so far; the bergamot and
  pickle board docs do not record their zones or trip points.
- **The web card** shows the hottest zone (all on hover) on a 30–90 °C gauge, amber from 70 °C,
  red from 80 °C (`web/src/app.tsx`, the vitals panel). Those colours are the only reaction.
- **What happens at a trip** is the vendor kernel's business (throttling, a critical shutdown); not
  characterised on our boards. The Station 2 (yandexmidi) firmware has an `S01hot-poweroff` boot
  script for the critical trip ([boards/yandexmidi.md](boards/yandexmidi.md)).
- **No overheat reaction exists**, and the wishlist has no item for one; the only related entry is
  "Station 3: the power budget with the white channel on" ([wishlist.md](wishlist.md)), which asks
  to watch the PD supply and thermal throttling under full load before brightening the ambilight.
  A monitor would follow the patterns above: zone names and trip-relative thresholds per board as
  profile keys, hysteresis, a diagnostic entity, a moment.

## Related

[status-indication.md](status-indication.md) (the boot / degraded indication),
[guides/deploying.md](guides/deploying.md) (the supervisor, "Why did it reboot?"),
[guides/diagnostics.md](guides/diagnostics.md) (the archive), [web-ui.md](web-ui.md) (vitals API,
`/metrics`), [settings.md](settings.md) (the speaker card, the keys), [storage.md](storage.md)
(logs, the network watchdog, what is written to flash), [porting-a-speaker.md](porting-a-speaker.md)
(`power.*`, `platform.wifi_reset`), the boards: [bergamot](boards/bergamot.md),
[pickle](boards/pickle.md), [orion](boards/orion.md).
