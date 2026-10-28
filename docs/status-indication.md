# Status indication: boot, first connection, degraded

Status (2026-10-24): implemented (`yastationd/internal/app/lifecycle.go`).

## States

| State | When | Ring | Front display |
|---|---|---|---|
| booting | from the daemon's start until the first connection | blue pulse | the `sleep` clip (no clock: the time is unknown) |
| first connection failed | none within `boot_connect_timeout` (default 40 s); it keeps trying | red pulse | `sleep` |
| ready | the first connection is made | — | `eyes` → `wink` once per device boot, then the base layer |
| degraded | after ready, something is missing for 10 s | dim red pulse | unchanged |

"Connected" means the MQTT broker is up (when one is configured) and HA holds the Wyoming satellite
connection (when the satellite is on), i.e. the speaker is usable from HA and by voice. Degraded
also covers the health probes (`probe` lines: broker, pipeline, LAN). The 10 s grace covers HA's
reconnect after a drop of ours (~3 s) and the probe interval (the monitor rechecks at once when the
broker connects or the speaker becomes ready, so a stale probe does not show red after a boot).

- **The clock before the first time sync** shows no digits, only a blinking colon: a stale or zero
  clock reads as a plausible but wrong time. The digits appear once the time is confirmed. A board
  whose RTC keeps time without power (`platform.rtc_backed`, orion) shows the time at once, unless
  the clock is before 2026 (the RTC lost it).
- **Degraded with HA still reachable** (e.g. one probe fails): the dim red pulse shows, but the
  speaker keeps working and answers normally — by design, only a missing HA link refuses voice.
- **A voice attempt with no HA to talk to** (wake word or key, no satellite connection): a bright
  red pulse and the stock phrase `something_went_wrong`, cut by `make sounds` to its first
  sentence «К сожалению, что-то пошло не так» (the second asks to redo the setup): the
  `voice_unavailable` moment. `backend_unavailable` is decoded too, as an alternative.

Every indication on this page is a moment of the default theme (`booting`, `boot_failed`,
`ha_lost`, `voice_unavailable`, `mic_muted`; [asset-packs.md](asset-packs.md)), so a theme or the
board profile (`moment.<name>`) can change it.

## Config

```
boot_connect_timeout = 40   # s, 1..600
```

## Not covered here

The daemon itself being down (crash loop, no binary): the supervisor would have to draw that
from the shell. See the wishlist.
