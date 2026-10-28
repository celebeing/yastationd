# Wishlist — ideas under consideration

Things we may want, with what is known about them. Nothing here is planned or committed. When an
item is built, it leaves this list: what is worth keeping goes into the doc of its area (built so
far from here: the intercom, wake-word arbitration, slow replies, the music player and its effects,
auto-brightness, the screen's own font, the logs off the speaker and the metrics, settings shared
between speakers). Several ideas came from reviewing [pulsar](https://github.com/Morvex885/pulsar)
(fork with the Mini 2 port: [ste4ik/pulsar](https://github.com/ste4ik/pulsar)), a C++ daemon for the
Station Midi / Lite 2 / Mini 2 that speaks the ESPHome Native API to HA. It's a useful reference,
but it lets the vendor stack start at every boot and expects internet access (GitHub updates), so
its solutions need checking against our doctrine before reuse. Both projects are MIT.

## Voice pipeline robustness

**Status:** note; unclear we need it. (Slow replies are done: [voice-slow-replies.md](voice-slow-replies.md).)

Ideas from pulsar:
- A watchdog per phase: a dialog with no pipeline event for 30 s while listening or 120 s while
  processing is closed with an error indication.
- Pipeline events carry no run id, so events that arrive before the new run's start belong to the
  run it replaced and are dropped.
- With several API clients, the voice pipeline goes back to the previous subscriber when the latest
  one leaves, so a test client never strands HA.

## "Someone is asleep here" detection

**Status:** collecting data since 2026-10-26 (step 1 below: the raw inputs are HA sensors);
the detector waits for about a week of history.

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

## The supervisor draws a dead daemon

**Status:** idea, to do separately (higher durability bar than the daemon itself).

When the daemon is down for good (the supervisor's crash loop with no other binary to try, or no
binary at all), nothing on the speaker says so. The supervisor is a busybox shell script and must
stay one, so it would draw a fixed "x_X" face on the clock and a dim red ring straight through
sysfs, board-agnostic: trigger `none` and a low brightness on every `/sys/class/leds/*:red:*`,
the face's segments on `*clock*` (the clip LED order, docs/display-expressions.md). It must never
fight a running daemon over the LEDs, and must keep working when the daemon binary is broken.

## An activity blinker

**Status:** idea (2026-10-28), a party trick first, a debugging aid second; to discuss.

Something on the speaker that blinks when traffic of a chosen kind goes by, like the lights of an
old modem or a hard drive. Sources to pick from: MQTT messages in and out (a sync, a state change,
an arbitration claim), Wyoming audio to and from HA, the music stream, the logs going out, network
traffic in general, the mic hearing speech. Where it could show: on Station 3 a free pixel row of
the screen (a dot per message, or a running trace), on the Minis the colon or the minus segment,
on any speaker one dim pixel of the ring. Off by default; a setting picks the source and the place.

## Asset kinds and packs

**Status:** idea (2026-10-28); the Assets group exists with one kind, videos ([assets.md](assets.md)).

More kinds of the owner's media, each its own section on the Assets page: **sounds** (the chimes,
the timer's alarm, the wake and error earcons — today fixed files under `<cfw>/sounds`),
**animations** (ring and screen clips the moments can pick, today the firmware's and our embedded
ones), maybe **fonts** for the pixel screen. Then **packs**: one archive with a manifest that
carries several assets (a theme: sounds + animations + moments that use them), installed and
removed as a whole, shareable between speakers of the same display kind (the sync could carry a
pack's name, not its bytes). Needs first: a place for user assets each consumer looks in before its
built-ins, and names that don't collide with the firmware's.

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

## Persistent logs on the speaker

**Status:** idea. Built already: syslog to a LAN server with a flash spool while it is
unreachable (`log_remote`), the network watchdog ([storage.md](storage.md)), and Prometheus
metrics at `/metrics` ([web-ui.md](web-ui.md)).

Logs on the speaker itself are on tmpfs, so a hang that ends in a power cycle loses them when the
log server could not be reached either. orion could keep the full daemon log and syslog on its
unused `/data` (2.6 GB eMMC) with size-capped rotation; the Minis only WARN/ERROR and the
supervisor's lines, appended to a capped file on `<cfw>` every few minutes (negligible NAND wear,
storage.md).

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

## Station 3: the power budget with the white channel on

The firmware keeps the rear strip's framebuffer without white (`ledfb-rgb48`); we switch it to
RGBW at runtime ([ambilight-stream.md](ambilight-stream.md)), and since v0.18.0 the music effects
use the white LEDs by default. If the firmware left white off for power, it shows under load: play
music at full volume with the SoC busy and the strip RGBW at 255, and watch the USB-C PD supply
(`ya_pd`: voltage, the supply sensor in HA), thermal throttling, and whether the sound sags or the
speaker resets. Until that is measured the effects stay dim (brightness 64).

## Wake arbitration vs the context-dependent spotters

Arbitration ([wake-arbitration.md](wake-arbitration.md)) is about the wake word: a wake during an
intercom call is already left out. But the speakers also run context-dependent spotters — the
local commands («стоп», «громче»), the intercom's answer/decline words («продолжи», «хватит») while
ringing or in a call, the commands kept on after music — and two speakers in earshot of each
other may hear the same word in different contexts (one ringing, the other playing music; both in
the call's two rooms). Check where a claim from one could suppress or delay the other's
context-dependent action, and whether such words need their own rule (never arbitrated, or
arbitrated only among speakers in the same context).

## Automated tests: CI and the gaps

State on 2026-10-28: the Go tests (17 packages) pass with `-race`, but nothing runs them on a
push: only `build.sh` and manual runs do (GitHub has the release action only). Coverage is uneven:
`web` 96 %, `ddp` 94 %, `config` 92 % (the settings registry), `intercom` 86 %, `board` 85 %,
`display` 81 %; thin in `app` 48 % (the MQTT command handlers, the voice pipeline; the settings
and their sync are covered), `mqtt` 28 %, `wyoming` 43 %, `wifisetup` 47 %. The web app, the C
helpers (`spotter-host`, `sendspin-host`) and the shell tools have none.
In order of value:

1. **CI on every push:** `go vet`, `go test -race`, a gofmt check, the web app's `tsc` build,
   `shellcheck tools/*.sh`.
2. **The voice pipeline:** tests of `satellite` / `voice` against a fake Wyoming server (the most
   logic with the fewest tests), and the MQTT command parsing.
3. **A smoke test on the simulator:** `ledsim -http`, moments set and played through the API with
   the frame tap recorded (the check done by hand for v0.17.0), as a Go test.
4. **Small web tests** (vitest) of the pure helpers: the exposure-to-lux conversion, the moment
   descriptions.
