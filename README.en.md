# yastationd

[Русский](README.md) · English

<p align="center"><img src="docs/media/reel-station3.gif" height="180" alt="Station 3: clock, date, animations, a marquee, Bad Apple">&emsp;&emsp;&emsp;<img src="docs/media/reel-mini3.gif" height="180" alt="Station Mini 3: clock, animations, volume, a marquee, a timer"><br><sub>Station 3 and Station Mini 3 in the simulator: clock, date, animations, a marquee, a timer, Bad Apple.</sub></p>

A custom firmware layer that turns a family of compact smart speakers into **offline Home Assistant
speakers**. The vendor's assistant stack does not run. Its place is taken by a small on-device agent,
`yastationd`, that talks only to Home Assistant on the LAN: MQTT for controls and sensors, Wyoming
for voice. The speaker needs no cloud and no internet, and works the same with the WAN unplugged.

The base Linux of the device (init, Wi-Fi, ALSA, LEDs, input) stays as shipped. The CFW adds files
next to it and drives the hardware directly. The one vendor piece it reuses is the voice front-end
library (echo cancellation, noise reduction and the wake-word engine), which it loads in a separate
helper process, `spotter-host`, so the vendor code never runs inside the agent.

## Highlights

**Made for Home Assistant power users.** Everything on the speaker is an HA entity you can
automate: the ring, the display, volume, the keys as events, the light sensor, power health. Voice
goes into your own Assist pipeline, so you choose the STT, the TTS and the conversation agent, a
local LLM included. The speaker is also a Music Assistant player for whole-home audio. Settings
live in plain config files on the device, with per-unit overrides, and the docs cover the HA side
as well: pipeline setup, entity exposure, prompting.

**The stock wake word, tuned for this hardware.** «Алиса», «стоп», «громче» and the other local
commands run on the speaker's own spotter and voice front-end: the models, echo cancellation,
noise reduction and mic-array processing that the manufacturer tuned for this exact device. It
works phenomenally well and hears the wake word even over the speaker's own music. Open-source
wake word engines don't come close.

**A living front display.** The clock face plays the speaker's own animations, read from its
firmware: eyes, the voice-state bars while it listens, thinks and speaks, a ringing timer, pause
and track changes. A few more faces of ours fill the gaps (happy, sad, angry, wink, sleep). Every change
on the display is smooth: segments cross-fade per element, so animations join without a blank,
digits morph into new ones and the clock fades back in after a face. All of it is scriptable from
HA, from single expressions to timed scenarios across the ring and the display.

## What the speaker does with the CFW

**Voice (Wyoming satellite in HA's Assist pipeline)**
- Wake word «Алиса», using the device's own front-end and spotter models: 3-mic echo-cancelled,
  noise-reduced audio is streamed to HA's STT, and the answer is played back.
- Push-to-talk on the voice key; dialog follow-up (keeps listening after a question).
- Several speakers in earshot agree which one answers: the nearest (the loudest «Алиса»); the
  others glance aside and stay silent ([docs/wake-arbitration.md](docs/wake-arbitration.md)).
- «Стоп» ends a voice session at once, whether it listens, thinks or speaks.
- Announcements and TTS from HA (`assist_satellite.announce`, any media URL).
- HA voice timers: countdown on the clock, an alarm on finish, any touch key stops it. A paused timer
  blinks its remainder briefly, then the clock comes back.
- Local reflexes while audio plays: «стоп», «громче», «тише» are handled on the device.

**Expressions on the display and the ring**
- The speaker's own clock animations play on the front display: the voice-state bars while the
  assistant listens, thinks and speaks, then a wink as the dialog ends; a greeting (eyes, wink)
  once per boot; `ringing` for a timer or alarm. Our own faces add happy, sad, angry, wink and sleep.
- Smooth per-segment transitions everywhere: back-to-back animations join without a blank, the
  volume digits morph, the clock fades back in after a face.
- From HA: an `Expression` select, `express` and `scenario` topics (built-in or your own timed
  scenarios over the ring and the display), raw frames, and a `Display faces` switch. The speaker
  publishes what it can show, so scripts and an LLM agent can adapt to each model.
  Details: [docs/display-expressions.md](docs/display-expressions.md).

**Controls and sensors (HA MQTT Discovery, one device per speaker)**
- LED ring as an HA `light` with effects (pulse, comet, blink, rainbow, voice-state presets), plus
  timed ring animations that Assist can trigger.
- Station 3's rear strip as an RGBW `light`, and a DDP receiver for it: point
  [LedFx](https://www.ledfx.app/) (or WLED tools) at the speaker as a 12-pixel DDP device and its
  effects play on the strip; without it, our own music effects (rain, VU, spectrum, energy,
  scroll, pulse) follow the music playing on the speaker, chosen and dimmed from HA
  ([docs/ambilight-stream.md](docs/ambilight-stream.md)).
- Front display: clock, a text message (long text scrolls), timed text overlays, countdowns.
- Clock auto-brightness from the ambient-light sensor (dims to barely visible in a dark room).
  Switch and manual level in HA.
- Volume (number + hardware keys with an on-display level), mic-mute switch state, buttons as HA
  events, illuminance in lux (calibrated per speaker), room sound level and activity, uptime,
  health.
- Power self-check: USB-C PD contract voltage and a problem flag. On an unsuitable power supply the
  speaker says so once per boot.

**Music (Music Assistant player)**
- A Sendspin player for Music Assistant (`sendspin-host`): lossless music over the LAN, mixed with
  the assistant on the same speaker. Music ducks while the assistant listens, talks or rings a
  timer; the wake word works over the music; the player volume in MA is the speaker's volume.
- On the display: a new track's title runs once, pause / resume animations, and on Station 3 the
  spectrum under the clock while music plays (each can be switched off: `music_title`,
  `music_clips`, `music_levels`). Details: [docs/music-sendspin.md](docs/music-sendspin.md).

**Operations**
- Logs can go to a syslog server on the LAN, kept on flash while it is unreachable; a network
  watchdog brings back a speaker whose Wi-Fi stopped passing data: it resets the Wi-Fi
  controller the way the stock firmware does, then reboots ([docs/storage.md](docs/storage.md)).
- Prometheus metrics at `/metrics`: memory, load, temperature, sensors.

**Keeps working without HA**
- Clock, volume keys, mute and the display run locally. If the broker or HA goes away, the ring
  shows a dim "degraded" dot, and everything reconnects and re-publishes by itself.
- Time is kept from the LAN NTP server. The clock blinks its colon until the time is confirmed, and a
  stale clock after power-on is corrected right away.

**A web app on every speaker**
- Open `http://<speaker-ip>/` (login **admin / yastation** until you change it from the app):
  every speaker on the LAN in one list, found by itself; per speaker its sensors, the runtime
  controls HA has (display, expressions, brightness, volume…), a live LED preview, the moments
  (what it shows for each event, with an editor), the saved Wi-Fi networks (add one), its logs, a light-sensor
  calibration against a lux meter, the config file with a
  validated save that rolls itself back if the speaker gets worse, the login, and an update check.
  Prometheus metrics at `/metrics`. Details: [docs/web-ui.md](docs/web-ui.md).

**Recovery when the Wi-Fi changes**
- Mute the mic, then hold the voice key for 5 s (the ring fills with yellow): the speaker becomes
  its own access point with
  a setup page that phones open by themselves. On it you list, add, edit and remove the saved Wi-Fi
  networks and set the home settings (broker, health probes, time servers and zone, Music
  Assistant). Nothing about the mode is persistent: leaving it, or a reboot, brings the normal
  Wi-Fi client back. Details: [docs/wifi-setup.md](docs/wifi-setup.md).

**Intercom between rooms** (off by default)
- «Алиса, позвони в спальню»: a live two-way call to the speaker in another room, over the LAN
  only. The speakers use the vendor's own call processing (echo cancellation, neural noise
  reduction, gain control), so double talk works and nothing but the room's voice gets through.
- Answer with «продолжи» or the voice key, decline with «стоп» or mute; in a call «Алиса,
  громче / тише / хватит». All of that is on the speaker's own spotters; HA only starts the call
  (an LLM tool script, or the target select and call button). Calls are signed and encrypted.
  Details: [docs/intercom.md](docs/intercom.md), HA setup: [docs/ha-intercom-setup.md](docs/ha-intercom-setup.md).

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
| `yandexmidi` | Station 2 | profiled from firmware only, no unit yet |
| `orion` | Station 3 | full support: voice with the stock wake word (NPU verifier), playback, volume, circular controls, ring, rear ambilight (with DDP effects), the 21×12 screen with the firmware's own animations, our pixel clock and fonts, music on the screen |

Per-board notes: [docs/boards/](docs/boards/README.md).

## Installing

Installation is manual, over SSH to the speaker. How to get shell access to your own unit is out
of this project's scope: we assume you already have it.

- **Yourself:** building is in [docs/building.md](docs/building.md); installing, updating and
  removing in [docs/deploying.md](docs/deploying.md).
- **With an agent:** give this repository and access to the speaker to your coding agent (Claude
  Code and the like). [docs/deploying.md](docs/deploying.md) is written for it too, and
  [CLAUDE.md](CLAUDE.md) holds the rules for working on a speaker safely. It builds, installs and
  checks everything by itself.

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

## Versions

Releases are `vX.Y.Z` tags; see [CHANGELOG.md](CHANGELOG.md). The running version shows in HA
(device card and the Version sensor). How to cut one: [docs/releasing.md](docs/releasing.md).

## License

MIT — see [LICENSE](LICENSE). Vendor software, firmware and models are not part of this repository
and are not covered by it; the CFW uses the copies already on your own device.
