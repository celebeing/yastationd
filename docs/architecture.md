# Architecture

How the CFW is put together and why: what runs where, how it talks to Home Assistant and to the
other speakers, and the decisions behind that. Each area has its own doc; this one is the map. The
original design this grew from (2026-10-22, MVP phases and all) is kept as research:
[research/hass-integration.md](research/hass-integration.md). How a new feature fits in:
[adding-a-feature.md](adding-a-feature.md).

## What runs where

```
                 Home Assistant (LAN)                           the other speakers
        MQTT ▲ (control, sensors, options)  ▲ Wyoming (voice)        ▲ MQTT, UDP
             │                              │                        │
  ┌──────────┴──────────────────────────────┴────────────────────────┴──────────┐
  │ yastationd: one static Go binary per speaker (armv7, runs on every board)    │
  │  board profile · display stacks and moments · ALSA via aplay/arecord · keys │
  │  settings · health · time · logs · the web app on :80 · the LED preview      │
  └──┬───────────────┬────────────────┬────────────────┬────────────────────────┘
     │ child processes, supervised (restart with backoff), line protocols
  spotter-host     spotter-host      sendspin-host     hostapd
  serve: VQE,      voice: the        (Music Assistant   (the Wi-Fi setup
  wake word,       voiceprints       player)            mode's access point)
  commands         (voice ID)
     └──── the vendor's libraries (libyandex_evil.so, libtflite.so), loaded by our hosts ────┘
```

- **One daemon** does everything the speaker does: [yastationd/README.md](../yastationd/README.md).
  Static Go, so one binary runs on every board (armhf and aarch64 userlands); started at boot by
  one init hook and kept up by a supervisor that rolls back a crash-looping update
  ([deploying.md](guides/deploying.md)).
- **Model-specific hardware is a board profile**, not code: [boards/README.md](boards/README.md),
  [porting-a-speaker.md](porting-a-speaker.md).
- **The vendor's code runs only in our child processes**: `spotter-host` loads the vendor's voice
  front-end (echo cancellation, noise reduction, the «Алиса» spotter and the command spotter) and,
  in a second process, its voiceprint model; they talk to the daemon over stdin/stdout and a pipe.
  A crash there never takes the daemon down, and the daemon never links a vendor library. The
  vendor's own assistant (`maind`) never runs: a reversible gate keeps it from starting, the
  speaker reaches only the LAN (an egress lock on the speaker and the router), and firmware updates
  are blocked (the safety doctrine in `CLAUDE.md`).
- **Audio goes through the stock ALSA tools** (`aplay` on the firmware's dmix, one `arecord` whose
  frames the daemon fans out), so nothing links `libasound` and the firmware's mixer setup stays.

## Talking to Home Assistant

- **MQTT with discovery** for control and state: the ring and the ambilight as lights, the display,
  volume, sensors, buttons as events, the speaker's options (`internal/mqtt/discovery.go`;
  [yastationd/README.md](../yastationd/README.md), "Entities").
- **A Wyoming satellite** for voice: the speaker streams the request's audio, HA runs STT, the
  agent and TTS, and streams the reply back; announcements and HA's voice timers come the same way
  ([ha-assistant-setup.md](guides/ha-assistant-setup.md), [voice-slow-replies.md](voice-slow-replies.md)).
- **Why these two and not ESPHome:** ESPHome builds firmware for microcontrollers; a station is a
  Linux computer with ALSA, evdev and a filesystem, and ESPHome has no runtime for driving those.
  MQTT discovery is the Linux-native counterpart of ESPHome's API, Wyoming the one HA's voice
  satellites speak.
- **The speaker understands nothing by itself**: no STT, no LLM on the device (there is no room for
  them, and the firmware's own recogniser is the cloud's: [research/stt-decompile.md](research/stt-decompile.md)).
  What it does locally: the wake word, «стоп» / «громче» / «тише» while it plays, voice ID, and
  the reactions to everything (moments).

## Between the speakers

The speakers share the home's MQTT broker and need no leader: arbitration of a wake word several
heard ([wake-arbitration.md](wake-arbitration.md)), settings and options shared on retained topics
([settings.md](settings.md)), the intercom's signalling, with its audio over UDP
([intercom.md](intercom.md)), and voice ID's events to the speaker that keeps the voices and the
household back to all ([voice-id.md](voice-id.md)).

## Inside the daemon

| Area | What | Doc |
|---|---|---|
| Settings | one registry of config keys (parser, pages, API, sync), live reload, a trial restart that rolls back | [settings.md](settings.md) |
| Displays | per-surface stacks of layers with priorities and lifetimes (the front display, the ring, the ambilight) | [display-expressions.md](display-expressions.md) |
| Reactions | moments: named events mapped by the theme stack (packs) to pictures, sounds, ring effects | [display-expressions.md](display-expressions.md), [asset-packs.md](asset-packs.md) |
| Status | booting, first connection, degraded, without HA | [status-indication.md](status-indication.md) |
| Web app | one server per speaker, one app for all of them | [web-ui.md](web-ui.md) |
| Storage, logs | what is written where, the log shipping | [storage.md](storage.md) |

## Time

The clock must be right without the internet. Firmware 0.369 runs `chronyd` against the LAN server
the install sets; firmware 0.6.x has no `chronyd`, and its own sync polls public pools the egress
lock blocks. So the daemon carries a small SNTP client (`internal/timesync`): with `chronyd` or
`ntpd` running it does nothing; otherwise it asks `ntp_server`, then the servers in `chrony_conf`,
then the default gateway, steps the clock when it is off by more than 200 ms and checks every 15
minutes; until the first sync the clock's colon blinks. DHCP option 42 was rejected: few home
routers send it, `dhcpcd` here does not ask for it, and using it means editing the network setup,
which the safety doctrine keeps us out of. A Station 3 keeps time over a power cut on its RTC
([boards/orion.md](boards/orion.md)). The local time zone is the `tz` setting.
