# yastationd

A custom firmware layer that turns a family of compact smart speakers into **offline Home Assistant
speakers**. The vendor's assistant stack does not run. Its place is taken by a small on-device agent,
`yastationd`, that talks only to Home Assistant on the LAN: MQTT for controls and sensors, Wyoming
for voice. The speaker needs no cloud and no internet, and works the same with the WAN unplugged.

The base Linux of the device (init, Wi-Fi, ALSA, LEDs, input) stays as shipped. The CFW adds files
next to it and drives the hardware directly. The one vendor piece it reuses is the voice front-end
library (echo cancellation, noise reduction and the wake-word engine), which it loads in a separate
helper process, `spotter-host`, so the vendor code never runs inside the agent.

## What the speaker does with the CFW

**Voice (Wyoming satellite in HA's Assist pipeline)**
- Wake word «Алиса», using the device's own front-end and spotter models: 3-mic echo-cancelled,
  noise-reduced audio is streamed to HA's STT, and the answer is played back.
- Push-to-talk on the voice key; dialog follow-up (keeps listening after a question).
- Announcements and TTS from HA (`assist_satellite.announce`, any media URL).
- HA voice timers: countdown on the clock, an alarm on finish, any touch key stops it. A paused timer
  blinks its remainder briefly, then the clock comes back.
- Local reflexes while audio plays: «стоп», «громче», «тише» are handled on the device.

**Controls and sensors (HA MQTT Discovery, one device per speaker)**
- LED ring as an HA `light` with effects (pulse, comet, blink, rainbow, voice-state presets), plus
  timed ring animations that Assist can trigger.
- Front display: clock, a text message (long text scrolls), timed text overlays, countdowns.
- Clock auto-brightness from the ambient-light sensor (dims to barely visible in a dark room).
  Switch and manual level in HA.
- Volume (number + hardware keys with an on-display level), mic-mute switch state, buttons as HA
  events, illuminance, uptime, health.
- Power self-check: USB-C PD contract voltage and a problem flag. On an unsuitable power supply the
  speaker says so once per boot.

**Music (Music Assistant player)**
- A Sendspin player for Music Assistant (`sendspin-host`): lossless music over the LAN, mixed with
  the assistant on the same speaker. Music ducks while the assistant listens, talks or rings a
  timer; the wake word works over the music; the player volume in MA is the speaker's volume.
  Details: [docs/music-sendspin.md](docs/music-sendspin.md).

**Keeps working without HA**
- Clock, volume keys, mute and the display run locally. If the broker or HA goes away, the ring
  shows a dim "degraded" dot, and everything reconnects and re-publishes by itself.
- Time is kept from the LAN NTP server. The clock blinks its colon until the time is confirmed, and a
  stale clock after power-on is corrected right away.

## How it is built

```
             Home Assistant (LAN)
        MQTT ▲            ▲ Wyoming (voice in/out)
             │            │
  ┌──────────┴────────────┴─────────────────────────────┐
  │ yastationd  (static Go binary, one per speaker)     │
  │  board profile ─ LEDs/display (sysfs) ─ keys (evdev)│
  │  ALSA: aplay (dmix) out, one arecord in, fanned out │
  │  timers · health · time sync · power · lux          │
  └───────┬──────────────────────────────▲──────────────┘
   raw mic│frames (stdin)   events (stdout)│ + clean 16 kHz voice (fd 3)
  ┌───────▼──────────────────────────────┴──────────────┐
  │ spotter-host  (C, native arch, dlopens the vendor   │
  │ front-end lib: AEC/NR + wake word + commands)       │
  └─────────────────────────────────────────────────────┘
```

- **One binary, many models.** Everything model-specific (LED names and count, display, keys,
  mixer controls, mic layout, front-end preset and models, sensors, power) lives in a **board
  profile** embedded in the binary and picked from the device tree. A new speaker is a new profile,
  plus a backend only if it has genuinely new hardware (for example a framebuffer display).
- **Persistent state** (ring, display mode, volume, brightness and so on) lives on the device's
  writable partition under `<cfw>`: `/bkupgrade/cfw` on some models, `/data/cfw` on others.
- The agent is a static armv7 binary, which also runs on models with a 64-bit userland.
  `spotter-host` must match the userland's architecture, because it links the vendor library.

## Supported speakers

| Board | Model | Status |
|---|---|---|
| `bergamot` | Station Mini 3 | full support, two units in daily use |
| `pickle` | Station Mini 3 Pro | full support (16-pixel ring, aarch64 userland) |
| `yandexmidi` | Station Midi | profiled from firmware only, no unit yet |

Per-board notes: [docs/boards/](docs/boards/README.md).

## Repository

| Path | What |
|---|---|
| [`yastationd/`](yastationd/README.md) | The on-device agent: entities, config, build. Board profiles: `internal/board/profiles/` |
| [`spotter-host/`](spotter-host/README.md) | Voice front-end host (wake word, echo cancellation), per-arch build |
| [`sendspin-host/`](sendspin-host/README.md) | Music Assistant (Sendspin) player, a native helper on sendspin-cpp, per-arch build (zig) |
| [`hass/`](hass/) | HA-side scripts the Assist agent can call (ring animation, show text) |
| [`tools/`](tools/) | Read-only board survey, multi-speaker `deploy.sh`, firmware-dump helpers |
| [`docs/`](docs/index.md) | Design docs, hardware notes, reverse-engineering findings — start at the index |
| [`docs/ha-assistant-setup.md`](docs/ha-assistant-setup.md) | Setting up the voice assistant in Home Assistant (STT, TTS, agent, pipeline) |
| [`docs/deploying.md`](docs/deploying.md) | **For agents:** installing, updating and removing the CFW on a speaker |
| [`docs/porting-a-speaker.md`](docs/porting-a-speaker.md) | **For agents:** how to research a new speaker and add support for it |

## Repo hygiene

`dumps/` (raw firmware and device dumps) and `*.local.md` hold per-device secrets and personal
details. They are gitignored and must never be committed or shared. Committed files use placeholders
(`<speaker-ip>`, `<ha-host>`, …) instead of deployment values.

## License

MIT — see [LICENSE](LICENSE). Vendor software, firmware and models are not part of this repository
and are not covered by it; the CFW uses the copies already on your own device.
