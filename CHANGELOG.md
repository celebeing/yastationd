# Changelog

Versions follow [SemVer](https://semver.org/); before 1.0 a minor version adds features and a
patch version fixes things. How to cut a release: [docs/releasing.md](docs/releasing.md).

## Unreleased

**Features**
- Orion's volume: the exact level in the clock's digits, cross-fading from the old value, over a
  full-width bar whose last pixel is partly lit (1 % moves it); the bar glides, its end pixel
  flashes when pushed past 0 or 100. The firmware's volume patterns were tried and dropped: they
  sweep through the tens only. Other displays keep the number.
- The Wi-Fi setup mode's key combo is now the same on every board: **mic muted + the voice key
  held for 5 s** (it was volume-down + voice + mute, impossible on orion); the ring fills with
  yellow while it is held. Only the voice press needs the mute (orion's held mute flips back by
  itself). Verified on orion: the access point, a phone joining, "Connect now".

## v0.8.1 — 2026-10-26

Orion's visuals polished: a clock of its own, moments, the LED preview and a video party trick.

**Features**
- LED preview ([docs/led-preview.md](docs/led-preview.md)): `frametap_addr` streams the screen,
  ring and ambilight exactly as drawn and takes the daemon's commands over HTTP, with a viewer
  page; `cmd/ledsim` runs the daemon on the host with simulated surfaces and the firmware's screen
  patterns, so scenes can be developed in a browser and compared with a unit frame by frame.
- Orion's clock has its own pixel font: square 4×9 digits without serifs, no leading zero, a
  cross-fade when the minute changes. New display mode `clock_date`, the time over the date, is
  orion's default face (profile key `matrix.default_mode`).
- Moments: the app announces what happened (`startup`, `dialog_end`, …) and a per-platform table
  picks the picture — an expression, a scenario or a native animation, with fallbacks; boards and
  `<cfw>/board.conf` override it with `moment.<name>`. Orion ends a dialog with a blink instead of
  the wink, and starts with eyes and a blink.
- Video mode on pixel screens ([docs/video-mode.md](docs/video-mode.md)): `display_mode` `video`
  plays a converted video on orion's screen with its sound, the picture clocked by the audio, then
  returns to the clock; voice, «стоп», replies, timers, calls and music end it. `make video
  VIDEO=<file>` (`cmd/videoconv`, ffmpeg) converts a local video you supply — the motivating one is
  "Bad Apple!!" — into a gzipped `.led` frames file and a WAV, and uploads them to `<cfw>/media/`.
  The LED preview has a **video** button and `ledsim -media`.

**Fixes and operations**
- MQTT publishes are skipped instead of crashing when the daemon runs without a broker.

## v0.8.0 — 2026-10-26

Initial support for orion (Station 3): installed and running on a unit.

**Features**
- Orion: the voice pipeline wakes on the stock «Алиса» model with its second-stage verifier on
  the NPU. spotter-host now reports the verifier's confirmation as the wake (it used to wait for
  a first-stage confirmation that a verified model never sends), no longer treats the 3.21
  library's warning log as an error, and its `probe` no longer crashes on multi-channel models.
- Orion: the screen renders at the stock patterns' own 20 ms frame time instead of the shared
  15 Hz loop, which dropped two of three frames and turned one-frame details (the wink's
  sparkles) into noise. The ring and seg7 displays keep 15 Hz.
- New board-profile key `platform.stop_initd`: vendor init.d scripts whose leftovers outlive the
  gated vendor stack are stopped at startup. Orion stops `S31animation`, whose boot animation
  otherwise loops on the ring forever.
- The egress lock finds an `ip6tables` link in the bundled iptables, which orion's aarch64
  bundle needs to lock IPv6.

**Fixes and operations**
- Orion's CFW root is `/bkupgrade/cfw` (the firmware's own `/bkupgrade` symlink to `/ota`),
  where every hook, the vendor-stack gate and the install kit already looked; the profile said
  `/data/cfw`.
- Install docs for orion: the aarch64 iptables bundle and its `ip6tables` link, no call-mode VQE
  model in this firmware, the example config with wake on. Open: a pixel clock font.

## v0.7.0 — 2026-10-25

**Features**
- Orion (Station 3): the front screen plays the firmware's own screen animations — the voice
  stages (listening, thinking, speaking), the faces (eyes, wink, joy), pause/next/previous, the
  alarm, incoming and outgoing calls, the boot phase and the Wi-Fi setup mode — instead of
  showing none. The Display faces switch and the expressions now exist on orion too. Built
  against the 3.21 firmware dump and not yet checked on glass: if the animations look too dark
  or washed out, set `matrix.gamma` in `<cfw>/board.conf`
  ([docs/display-expressions.md](docs/display-expressions.md), "Screen patterns").
- The front display is driven through a per-display catalogue of scenes and animations: an
  expression resolves to the display's own animation (a clock clip on seg7, a screen pattern on
  a pixel screen), never a converted one. The clock, text and numbers on orion still use the seg7
  picture drawn as pixels until they get pixel drawings of their own.
- HA `display/cmd` `clip` takes semantic names (`listening`) and, on orion, any screen pattern by
  its path (`media/Like`).

**Fixes and operations**
- The intercom's call timer shows on orion's screen too.

## v0.6.0 — 2026-10-25

**Features**
- Intercom, off by default: 1-to-1 calls between speakers in different rooms
  ([docs/intercom.md](docs/intercom.md), HA setup [docs/ha-intercom-setup.md](docs/ha-intercom-setup.md)).
  - «Алиса, позвони в спальню» through an HA script the LLM calls (template in `hass/intercom/`),
    or the HA target select and call button.
  - Answer with «продолжи / играй» or the voice key, decline with «стоп / хватит / пауза» or the
    mute switch; in a call «Алиса, стоп / хватит» hangs up, «Алиса, громче / тише» sets the
    volume — all on the speaker's own spotters, nothing goes to HA.
  - The uplink is the vendor VQE's call mode (neural noise reduction and AGC; `make vqe-models`
    puts its model on the unit); L16 over UDP sealed with AES-GCM; a jitter buffer that also
    absorbs the two speakers' clock drift; signalling over MQTT, signed with a shared secret.
  - The vendor's call sounds and ringing animation, a call timer on the front display, a blue
    ring while the mic streams to the other room.
  - `intercom-peer` (a stand-in second speaker for tests) and `intercom-replay` (tune the uplink
    on a recorded call).

**Fixes and operations**
- spotter-host runs each spotter in its own thread, the VQE loop never waiting for them; it
  switches the VQE's communication mode at runtime and takes `-m <model dir>`.
- `make sounds` also decodes the vendor's call sounds.

**Docs**
- An orion firmware dump sorted locally; QEMU boot rig for bergamot and pickle; rootfs hardening
  research; wishlist: "someone is asleep here" detection.

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
