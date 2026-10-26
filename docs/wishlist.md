# Wishlist — ideas under consideration

Things we may want, with what is known about them. Nothing here is planned or committed. An item
moves into a design doc (and out of this list) once we decide to build it (so far: the intercom,
[intercom.md](intercom.md)). Several came from
reviewing [pulsar](https://github.com/Morvex885/pulsar) (fork with the Mini 2 port:
[ste4ik/pulsar](https://github.com/ste4ik/pulsar)). pulsar is a C++ daemon for the Station Midi /
Lite 2 / Mini 2 that speaks the ESPHome Native API to HA. It's a useful reference, but it lets the
vendor stack start at every boot and expects internet access (GitHub updates), so its solutions
need checking against our doctrine before reuse. Both projects are MIT.

## Nearest speaker answers (wake-word arbitration)

**Status:** built 2026-10-26 — [wake-arbitration.md](wake-arbitration.md). The notes below are
the background it was designed from.

When several speakers hear the wake word, only the closest should answer. Without arbitration HA's
own `duplicate_wake_up_detected` (first phrase per ~2 s wins across satellites) picks whichever
request arrives first, not the nearest speaker.

pulsar's scheme, for reference:
- On a wake word each speaker broadcasts a UDP claim (`<name> <wake word> <busy> <level dB>`, three
  copies 40 ms apart, since Wi-Fi broadcast has no retries) and decides 250 ms later. A busy speaker
  (talking, announcing, ringing a timer) wins; otherwise the louder claim wins; ties go by name. Mic
  audio is buffered during the window, so nothing said right after the wake word is lost. Losers
  blink and stay silent.
- **The level must come from a VQE output without AGC** (they use `VQE_OMNI`), not from the
  gain-normalized channel the spotter hears, which erases the distance. They average the 8 loudest
  of the last 1.5 s of frames.
- HA's `duplicate_wake_up_detected` error is then handled silently (no error ring or sound).

Refinements from another DIY multi-speaker project (where several speakers on custom firmware
share one apartment), each one found on a real failure:
- **A quiet first claim waits for louder ones.** A fixed window favoured the far speaker: it
  claimed first, and the room it was in got the command (the wrong room's light was switched).
  A quiet claim (low level) now waits up to ~1.5 s for a louder one; a loud claim decides at once.
  For us that is an adaptive window instead of pulsar's fixed 250 ms: short when the claim is
  loud, long when it is quiet.
- **Claims that arrive together are one round,** even if the speakers' timestamps for the phrase
  start differ by seconds. They do differ, because of a different number of segments or an
  utterance opened by loud music. Decided phrases are remembered in a ring for ~1 min, not as a
  single "last" that the next claim overwrites, and a round's timer never closes another round.
  Log every claim (`claim: <speaker> <level> <decision>`); without that, double answers can't be
  explained afterwards.
- **A neighbour's reply is not a new command.** A speaker that hears the next room's spoken
  answer («включил свет на кухне») can take it as a command. Their filter: a phrase is an echo of
  a reply only if it has no long word the reply didn't have. For us this matters less, since we
  need a wake word before every command, but it applies to HA's `continue_conversation` reopening
  the mic.

For us: it is protocol-independent and would fit next to the Wyoming satellite. Open questions: how
much the near/far level gap really is on our units (not measured anywhere), and whether the
on-device firewall and the access point pass LAN broadcast.

## ESPHome Native API instead of MQTT + Wyoming

**Status:** a direction worth keeping open; not a rewrite we're starting.

We have already hit Wyoming's limits in the HA integration. Linux Voice Assistant (LVA, a Linux
voice satellite that speaks the ESPHome protocol to HA) is reported to work much better than
Wyoming. pulsar shows that the protocol itself is practical on these speakers.

What it would give:
- one connection for entities, voice and media, with no MQTT broker;
- mDNS discovery in HA;
- first-class `assist_satellite` and `media_player` entities (the latter also visible to Music
  Assistant);
- device logs in HA;
- Noise encryption (today's broker is anonymous and Wyoming is plaintext).

Note that `hass-integration.md` §2.4 ("Why NOT ESPHome") argues against ESPHome the firmware
builder. That argument does not cover the API protocol. Cost: the whole HA integration layer
(`internal/mqtt`, `internal/wyoming`) would be replaced.

## Voice pipeline robustness

**Status:** note; unclear we need it.

Ideas from pulsar:
- A watchdog per phase: a dialog with no pipeline event for 30 s while listening or 120 s while
  processing is closed with an error indication.
- Pipeline events carry no run id, so events that arrive before the new run's start belong to the
  run it replaced and are dropped.
- With several API clients, the voice pipeline goes back to the previous subscriber when the latest
  one leaves, so a test client never strands HA.

### Slow replies

**Status:** done — see [voice-slow-replies.md](voice-slow-replies.md).

## Music

**Status:** the Sendspin player is built — see [music-sendspin.md](music-sendspin.md). What
remains here is the wider picture.

- The speaker has no internet access, so only LAN sources qualify: Music Assistant or HA serving the
  stream, never a cloud URL fetched by the speaker.
- pulsar plays HTTP streams through the vendor's GStreamer (`libquasar_gstreamer.so`, dlopen'ed
  into its daemon). It is also a [Sendspin](https://www.sendspin-audio.com/) player for Music
  Assistant: FLAC over the LAN, with group sync in the protocol. For us, vendor code would have to
  run in a helper process like `spotter-host`, never inside the agent.
- Local commands for music: pulsar keeps the command spotter on for a minute after music stops, so
  «продолжи» works after a pause.
- This also covers open item 3 in `yastationd/README.md` (remote-URL announcements).
- Music on the front display (phase 5 of [display-expressions.md](display-expressions.md)): the
  stock `pause` / `next` / `previous` clips on transport changes (needs the player's state and the
  controller/metadata roles in sendspin-host); a level meter on seg7 and a spectrum on pixel
  screens from levels sendspin-host computes on the PCM it writes (a `LEVEL` event at ~20 Hz);
  the track title as a marquee on a track change.

## "Someone is asleep here" detection

**Status:** idea (2026-10-25); nothing designed.

A heuristic per speaker: is the room it stands in asleep right now? Exposed to HA (a
`binary_sensor` "Sleeping here", with the inputs as attributes) and used by the speaker itself.

**Inputs, all already on the speaker:**
- **Light:** the IIO light sensor the clock's auto brightness reads (smoothed, already published
  as the Illuminance sensor). Dark for a while is the strongest hint.
- **Noise:** the mic level — the spotter's `LEVEL` lines / the VQE stream — as a slow average:
  a quiet room (no talk, no TV) for some minutes. A level only, never audio leaving the speaker.
- **Time of day:** a user-configured night window; outside it the detector needs stronger
  evidence or stays off.
- Maybe later: no key presses or voice sessions for a while, music not playing.

**Configurable per user** (`yastationd.conf`, some also as HA entities): the night window,
the dark and quiet thresholds and how long each must hold, the hysteresis to wake up (light on
or sustained noise), and an HA override switch (force asleep / awake).

**What could follow it, built-in:** a dimmer or dark clock and ring, quieter replies and
chimes, no announcement chime, intercom calls refused or held as "do not disturb"
([intercom.md](intercom.md)), the volume-key beep off.
**In HA:** automations on the sensor (night scenes, a silent notification instead of an
announcement in that room, the intercom's allow switch).

**Approach (2026-10-25): data first, then the heuristic.**
1. **Collect.** Publish the raw inputs to HA as sensors (recorded by its history), before any
   detector exists: the noise floor (a slow low percentile of the mic level), a short-term
   level / activity, the smoothed light reading (already there), maybe key/voice activity. Cheap,
   and useful on its own.
   **Built (2026-10-26):** `Sound floor`, `Sound level` and `Sound activity` (see
   `internal/app/roomsound.go` and yastationd/README.md); `Illuminance` now reports on a change
   of 10 % or every 15 min instead of every 30 s. Key and voice activity are the `Button` event
   entity, already in HA's history.
2. **After about a week, compare** them against what HA already knows about sleep in those rooms
   (other sensors: lights, presence/motion, phones charging, bed/TV states…) **plus Apple
   Health sleep data** (asleep/in-bed intervals) as the ground truth.
3. **Fit the heuristic** to that: which inputs matter, thresholds and hold times per room —
   then build the detector (on the speaker, with the parameters in config).

Open: thresholds differ per room and per sensor behind the grille (calibrate from the
Illuminance history, like `auto_brightness_curve`); a TV or a fan at night breaks "quiet" —
the light and the time window must carry the decision then.

## Other models

pulsar's `AGENTS.md` has hardware notes for the Midi (`cucumber`: aarch64, glibc 2.36), the Lite 2
(`plum`) and the Mini 2. Their Midi did not match our firmware-only profile (`yandexmidi`: armhf,
glibc 2.30) because those are two models: `yandexmidi` is the retail Station 2 and the retail Midi is
`cucumber` ([boards/README.md](boards/README.md)). We don't port models without a unit in hand:
ports are for contributors who have the speaker (candidates: boards/README.md, "Possible ports").

## The supervisor draws a dead daemon

**Status:** idea, to do separately (higher durability bar than the daemon itself).

When the daemon is down for good (the supervisor's crash loop with no other binary to try, or no
binary at all), nothing on the speaker says so. The supervisor is a busybox shell script and must
stay one, so it would draw a fixed "x_X" face on the clock and a dim red ring straight through
sysfs, board-agnostic: trigger `none` and a low brightness on every `/sys/class/leds/*:red:*`,
the face's segments on `*clock*` (the clip LED order, docs/display-expressions.md). It must never
fight a running daemon over the LEDs, and must keep working when the daemon binary is broken.


## Smoother auto-brightness

**Status:** done 2026-10-26 (yastationd/README.md, item 5; orion dims through its backlight).

## Orion's ultrasound rig

**Status:** idea (2026-10-25); nothing is planned or decided. Hardware exists on orion;
pickle's asound also names an `ultrasound` PCM (boards/pickle.md), so it may share the
rig — unverified. Everything below is orion.

The vendor firmware ships a complete but **disabled** sonar stack around orion's
dedicated ultrasound transducer (`us` amp, `hw:0,0`, TDM-A 96 kHz):

- **Generator** (quasar.cfg `ultrasound.generator`): 38.4 kHz sine, amplitude 0.05
  full-scale, 4 TDM slots with [0,1] zeroed — so transmit presumably rides slots 2-3.
  maind strings also name an **impulse generator**, not just sine.
- **Detector** (quasar.cfg `ultrasound.detector`): 4 channels, 2048-sample frames at
  96 kHz, 600 Hz analysis band, threshold −70 dB. libyandex_evil.so **exports a C API**:
  `YandexVqeC_UltrasoundDetector_{Create,AddChannelData,Process,Free}` (+ an outdoors
  detector), with C++ classes `vqe::BaseDopplerDetector` / `SimpleDopplerDetector`. So
  the vendor detector is reusable from a process that dlopens the blob — the exact
  spotter-host pattern; no need to invent our own DSP from scratch.
- **Capture plumbing**: `hw:0,0` exposes capture too (survey: "playback 1 : capture 1"),
  and mcapt reopens the same `pcm.ultrasound` definition as a capture slave, folding two
  ultrasound RX channels into the 13ch/96k stream (idx 11-12), which we already
  capture. The detector's 4 channels map onto those + the loopback; the exact slot
  wiring needs one on-device look with a live carrier. Measured (verify2 raws,
  2026-10-24): idx 11-12 are dead LSB **while TDM-A is idle** — the S94alsa prime is
  only 1 s of silence and no carrier was playing during those captures; with a live
  carrier on `hw:0,0` they should carry the reflections.
- 38.4 kHz is inside the mics' Nyquist range (96 kHz clocking), so the PDM mics may
  also see the carrier as a bonus RX — bonus, since the primary RX is the transducer
  slots. Music stays far below the carrier band, so sonar can run even mid-song.

**What we could get, roughly in buildability order (none decided):**

1. **Doppler motion alert** — emit the carrier, watch the ±300 Hz band around it,
   publish a binary motion/occupancy sensor. The vendor detector's parameters (600 Hz
   band, −70 dB threshold) map directly onto this. Cheapest useful outcome, and the
   vendor API exists for exactly this recognition.
2. **Echo-signature presence** — rather than Doppler, track how the static echo profile
   (room geometry) drifts over minutes. Fires on sustained change: night trips in a
   household that sleeps, or nobody-home when an automation wants a warm body. Doubles
   as the presence input to the wake state machine (don't run the goodnight scene if
   nobody is home; gate "no one heard you" retries).
3. **Push/pull gesture** — approaching/receding Doppler has a sign, so a hand moving
   toward the speaker could act as a volume/intent gesture. Single-axis only; lateral
   motion mostly invisible; direct path dominates at arm's length, so near-field may
   be the weak spot. A toy unless it proves clean.
4. **Tiny sonar radar** — mic-array phase differences across the 6 mics to localize a
   dominant reflection, ARPA-style. Array response at 38 kHz unknown; a dream, not a
   plan.

**Feasibility experiment to run first** (one session, on the device, stack stopped; only
on the unit the user names — doctrine: the speaker emits energy): generate a 38.4 kHz
sine raw S16 at a few amplitudes (vendor starts at 0.05), **unmute the `us` amp** first
(its `AMP mute status` reads 1 in stock state; same cset family as the speaker amps),
play it to `hw:0,0` through `aplay -D ultrasound`, capture the 13ch/96k `mroute` stream
simultaneously, then FFT host-side: carrier at idx 11-12 with tone vs without, hand
moving vs still, near vs far. Host-side FFT is the same workflow that resolved the
earlier idx-5 anomaly: two 30 s pure captures on **each of two units** showed healthy
channels, so microphone hardware does not block array work (orion.md "Mic capture").
Open questions it settles: slot direction ([0,1] RX vs TX), mic response at 38.4 kHz
(bonus RX), direct-path dominance, and the real noise floor for the −70 dB threshold.

If the experiment shows clean signal, the natural host is **spotter-host itself**: it
already dlopens libyandex_evil.so and already consumes the 13ch/96k stream (idx 11-12
included) — a `-U/--ultrasound` mode emit-via-`hw:0,0` + detector-via-blob add-on would
reuse the capture, VQE, and LED paths. No new daemon, no `mcapt` surgery. One structural
choice: carrier always-on while spotter-host serves wake (duty-cycled in idle windows),
or carrier in bursts; either way it emits energy, so an always-radiating mode is a user
decision, not a default. The Yandex detector via dlopen keeps our DSP cost near zero
(the blob handles the FFT/decision), and the vendor shipped the feature disabled —
evidence they never made it a default either.

Two footnotes. AEC: today mroute idx 11-12 are consumed as two of the seven `raw_spk_*`
AEC refs, so a carrier changes what those channels mean — if yastationd ever emits a
carrier, either the VQE ref set must exclude the carrier-bearing channels (`wake.refs`
remap) or the detector must tolerate the carrier inside the refs. Hardware honesty:
the earlier idx-5 spike was transient; two-pass tests on both units cleared it, so it
does not block array-phase work. The first experiment will still tell us whether
`raw_mic` channels see the carrier at all.

## Orion: a colour sensor for the ambilight?

**Status:** answered 2026-10-26: **no colour sensor** — the ltr308 is the only light sensor in orion's device tree and has one broadband channel ([boards/orion.md](boards/orion.md), "Sensors survey"). Kept for the record; the rest of this entry is the question as it was asked.

If orion senses the colour of the light around it, not only its level, the rear ambilight could
follow the room (warm in the evening, matching a lamp). What we know so far is against it: the one
light sensor found is an ltr308 (`light-sensor-top`, I2C 1-0053; [boards/orion.md](boards/orion.md),
"Lux / power"), a single-channel ambient light sensor with only `in_intensity_*`. Worth checking
before giving up: the rest of the I2C buses and the device tree for an RGB/colour part (e.g. a
TCS34xx, an APDS-99xx, an ltr381), what the vendor's ambilight settings in `quasar.cfg` and maind's
strings depend on, and whether the ltr308 driver exposes more channels than the IIO node shows.

## The mics' frequency response: a speaker as a measurement mic

**Status:** idea (2026-10-26); nothing measured.

With the mics' frequency response known, a speaker could serve as a reference microphone:
measure another speaker's frequency response in the room (a sweep from one, recorded by the
other), find room modes, check what an EQ or the amp's settings do, compare units (the Mini 3
firmwares tune the amp differently, docs/deploying.md §7). What we have and lack:

- **No published response.** The vendor does not publish the mics' parts or curves. The PDM
  mics are probably common MEMS parts with a flat response from ~100 Hz to ~10 kHz and a rise
  near their resonance, but the grille, the gasket and the port in front of them shape it, so a
  datasheet curve would not be enough anyway.
- **The vendor may have one.** Factory tests or the VQE presets may carry per-model mic
  calibration: look in the firmware for calibration tables, an equaliser in the `vqe` presets,
  or the factory-test tools' sweep files.
- **Measure it ourselves.** Put a measurement mic with a calibration file (e.g. a UMIK-1) next
  to the speaker, play a log sweep from a third source and compare the two recordings: the
  speaker's mic response, per channel. Once per model, ideally on two units to see the spread.
  The raw channels are the ones to use (the VQE and its AGC change the level and the spectrum).
- **Limits:** 16-bit PDM capture and its noise floor bound the dynamic range; orion captures
  at 96 kHz, bergamot and pickle at 48 kHz — enough for the audio band. The capture's own
  high-pass in `hw.Mic` (150 Hz) applies only to our 16 kHz path, not to `arecord` itself.

## Logs that survive, and observability from outside

**Status:** partly built 2026-10-26: syslog to a LAN server with a flash spool during outages
(`log_remote`) and a network watchdog ([storage.md](storage.md)). Still open: persistent logs on
the speaker itself, the Prometheus exporter.

Today every log is on tmpfs and a hang ends in a power cycle, so the evidence is gone
(the Mini 3 Wi-Fi hang of 2026-10-26, yastationd/README.md item 7). Options, not exclusive:

- **Persistent logs on the speaker.** orion: the full daemon log and syslog on its unused `/data`
  (2.6 GB eMMC) with size-capped rotation. Minis: only WARN/ERROR and the supervisor's lines,
  appended to a capped file on `<cfw>` every few minutes (negligible NAND wear, storage.md).
- **Syslog streamed off the speaker.** busybox `syslogd` can send to a LAN host (`-R host:port`,
  with `-L` to keep the local copy); the daemon would log into syslog too (or send its own lines
  over UDP). Nothing to store on the speaker, and the lines up to the moment the network dies are
  kept elsewhere. LAN only, so the egress lock stays as it is; the receiver (e.g. the homelab's
  Loki) is the user's side.
- **A Prometheus exporter.** A `/metrics` endpoint in yastationd (configurable address, off by
  default, LAN only): uptime, CPU and memory, Wi-Fi signal and bit rate (`/proc/net/wireless`,
  the driver's station info), mic capture restarts, spotter input drops, wake detections, volume,
  health state, flash wear (UBI `max_ec`, eMMC `life_time`), the room sensors. It would have shown
  the rate collapse before the hang from the speaker's side; a scrape gap itself marks the hang.

Open: which of these the homelab already collects best (the router's MKTXP metrics covered the
Wi-Fi side this time), and whether a remote syslog receiver must be set up first.

## Orion: swipe gestures over the top

**Status:** idea (2026-10-26); measured, not built.

The touch top's capacitance sensor (`aw93312`, 12 raw channels in sysfs: ten ring pads, the
centre, the inner area; [boards/orion.md](boards/orion.md), "Sensors survey") also sees a hand
moving above it. Measured: a left/right swipe **1-2 cm over the top** registers every time with
its direction (the signal's centroid over the ten pads moves the swipe's way); at ~5 cm it is
weak and the direction unreliable; at ~10 cm nothing. A front/back swipe is untested (the run
touched the top). A hand held still fades into the chip's baseline in ~2 s, so only movement is
seen, not presence.

Uses considered: switching "home screens" on the front display (clock, clock with date,
weather, the player…) with a swipe; `swipe_left` / `swipe_right` as HA events. The range is
the limit: a swipe has to brush the top, almost a touch, so it is less a gesture in the air than
a contactless flick. How it would work: the daemon polls `diff` (~50 Hz), tells a touch
(hundreds of thousands, plus the input events) from a swipe (5-70k, no touch), and reads the
direction from the centroid's path. Open: false triggers from things put on the speaker or
passing by, whether polling at 50 Hz disturbs the chip's own touch handling, and whether the
chip's sensitivity (its register settings) could be raised for more range without breaking the
ring.

## Orion: text on the screen in our own font

**Status:** done 2026-10-26 ([display-expressions.md](display-expressions.md), "Text on the pixel
screen").
