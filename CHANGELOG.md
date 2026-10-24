# Changelog

Versions follow [SemVer](https://semver.org/); before 1.0 a minor version adds features and a
patch version fixes things. How to cut a release: [docs/releasing.md](docs/releasing.md).

## v0.5.1 — 2026-10-24

**Docs**
- The README lists the Wi-Fi setup mode.
- Research on making the CFW survive a factory reset: how the reset works per model, pickle's
  replaceable restore package, why bergamot's is not ([docs/permanent-install.md](docs/permanent-install.md)).

## v0.5.0 — 2026-10-24

**Operations**
- Setup page: "Home settings" for the network-dependent config (broker, health probes, time servers
  and zone, Music Assistant, speaker name, the setup network's own password), checked with the
  daemon's own parser before it is written; the agent restarts with them on leaving setup mode.

## v0.4.0 — 2026-10-24

**Operations**
- Wi-Fi setup mode: hold volume-down + voice + mute for 5 s and the speaker becomes an open access
  point with a page for listing, adding, editing and removing its saved networks (captive portal,
  networks seen nearby). Never persistent: leaving it, the page or a reboot brings the Wi-Fi client
  back. Ships its own static `hostapd` (the firmware's wpa_supplicant has no AP mode).

## v0.3.0 — 2026-10-24

The first tagged release; it covers everything up to now.

**Voice**
- Wake word «Алиса» on the speaker's own front-end and spotter, a Wyoming satellite in HA's
  Assist pipeline; push-to-talk, dialog follow-up, announcements and TTS, HA voice timers, local
  «стоп» / «громче» / «тише».
- TTS jitter buffer: a slow link or a TTS pausing between sentences gives one pause, not a stutter.
- Slow replies: our own reply deadline (HA has none), «стоп» cancels a pending reply, a wake word
  interrupts one without wedging HA's satellite, a wedge watchdog.

**Display and ring**
- The speaker's own clock animations plus our faces; expressions mapped onto the ring and the front
  display; timed scenarios; smooth per-segment transitions.
- Status indication: boot, first connection (and its failure), degraded; no clock digits before
  the first time sync; a red pulse and a spoken error when the voice finds no HA.

**Music**
- A Music Assistant (Sendspin) player (`sendspin-host`): music mixed with the assistant on the
  same speaker, ducking under the assistant, linked volume, the wake word over the music.

**Hardware and HA**
- Board profiles: bergamot (Station Mini 3), pickle (Station Mini 3 Pro), orion (Station 3:
  ring, 21×12 screen, rear ambilight, circular controls; contributed).
- HA MQTT Discovery: ring and ambilight lights, display, volume on the stock curve with a key tick,
  auto-brightness, power self-check, buttons, health; the build version in the device card and a
  Version sensor.

**Operations**
- A crash-loop supervisor with rollback to the previous binary and log rotation; per-platform
  deployment (`make deploy`, `tools/deploy.sh`).
