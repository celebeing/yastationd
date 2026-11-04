# Status indication: boot, first connection, degraded

Status (2026-10-24): implemented (`yastationd/internal/app/lifecycle.go`). Everything else the
speaker watches about itself (the power supply, the network watchdog, vitals, the supervisor):
[health.md](health.md); what to do when something looks wrong: [guides/troubleshooting.md](guides/troubleshooting.md).

## States

| State | When | Ring | Front display |
|---|---|---|---|
| booting | from the daemon's start until the first connection | blue pulse | the `sleep` clip (no clock: the time is unknown) |
| first connection failed | none within `boot_connect_timeout` (default 40 s); it keeps trying | red pulse | `sleep` |
| ready | the first connection is made | — (dark is the normal idle) | the `startup` moment once per device boot (by default eyes, then a wink on the Minis, a blink on Station 3), then the base layer |
| degraded | after ready, something is missing for 10 s | dim red pulse | unchanged |
| mic muted | the mute switch is on | yellow (the Minis' small ring), amber (Station 3) | unchanged |

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

## The rings

The Minis show these on their small ring: a Mini 3's six-pixel logo ring, and the same six-pixel
ring inside a Mini 3 Pro's big one (both the Mini 3's logic: `ring.*` of both profiles). The mute
is one **yellow** there (`mic_muted` on the seg7 boards, `kindTheme`); Station 3 keeps the amber
`mute` ring.

**The Mini 3 Pro's outer ring** (16 pixels, `outer.*`) is extra: every moment's ring repeats there
(the voice stages, a call, a flash), but not the indicators, which stay on the small ring only: the
mute, a lost HA, booting and its failure (`indicator` in the catalogue). A step's `outer` gives the
outer ring its own effect, or `off` ([asset-packs.md](asset-packs.md)). Nothing else lights it: it is
dark at rest, and the ring light in HA is the small ring.

**Brightness** (Configuration → General → Display): what stays lit (the mute, a lost HA, a call, the
setup mode) is capped at `led_held_brightness` (75 % by default: these LEDs warm up at full), what
lights up and goes at `led_moment_brightness` (100 %). With `led_auto_brightness` (on) both follow
the display's brightness, so in a dark room the rings dim with the clock, but less far: the cap
times the square root of the display's level against its brightest (the clock brightness, which
auto-brightness reaches in a bright room). A dim room already puts the display near its bottom, and
an LED near zero loses its colour; with the root a mute in a 2.6 lx room is ~59/255 (it was 19 with
the plain ratio), ~24 in the dark (3), and the cap in a bright room.
These caps are for the moments' rings; the ring light set from HA has its own brightness.

Every indication on this page is a moment of the default theme (`startup`, `booting`,
`boot_failed`, `ha_lost` and `ha_back`, `voice_unavailable`, `mic_muted` and `mic_unmuted`;
[asset-packs.md](asset-packs.md)), so a theme or the board profile (`moment.<name>`) can change it.
A bad power supply has its own moment, `power_problem` ([health.md](health.md)). How the mute
switch reaches `mic_muted`: [input.md](input.md).

## Config

```
boot_connect_timeout = 40   # s, 1..600
```

## Not covered here

The daemon itself being down (crash loop, no binary): nothing shows it on the speaker (the ring
keeps its last frame or goes dark); HA shows the speaker unavailable and the web app does not
answer ([guides/troubleshooting.md](guides/troubleshooting.md)). The supervisor would have to draw
it from the shell: [wishlist.md](wishlist.md), "The supervisor draws a dead daemon".
