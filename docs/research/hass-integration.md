# Home Assistant integration — design

> **Historical (2026-10-22).** The original design the CFW grew from, kept for its reasoning and
> for the research docs that cite its sections. Much of it was built differently or later (the
> vendor wake word instead of microWakeWord, no `clockd`, the time zone from a setting, phases long
> past). How it is built now: [../architecture.md](../architecture.md) and the docs of each area.

Design for turning the de-Yandexed **Yandex Station Mini 3 (`bergamot`)** into a first-class,
offline Home Assistant speaker. This is a **design document, not an implementation**. It builds on:

- `../../README.md` — what the CFW is (vendor stack off, internet-less, hardware shell-drivable).
- `device-teardown.md` — neutral hardware/interface reference.
- `../../<ha-config-repo>/docs/` — the **existing** home voice stack this design plugs into (see §0b):
  `voice-stack-refresh-2026-10.md`, `respeaker-voice.md`, `respeaker-wake-words.md` +
  `../../<ha-config-repo>/mww-training/`, `ru-tts-voice-cloning-research.md`,
  `jaina-voice-experiments.md`, `voice-server-f5-wyoming/`.

Do not duplicate those; this doc assumes them and only adds the HASS-integration layer. This design
**plugs into the existing `voice-server`/HASS voice stack — it does not invent a parallel STT/TTS/LLM.**

---

## 0. What was verified on the device (2026-10-22, read-only probe)

Everything below was confirmed live over SSH (`root@<speaker-ip>`), maind off, before designing.
These facts change several "obvious" choices, so they come first.

| Fact | Value | Consequence for the design |
|---|---|---|
| **Userland ABI** | **32-bit ARM hard-float (EABI, `e_machine=0x28`, ELF32)**, glibc **2.30**, `ld-linux-armhf.so.3`. **No `aarch64` loader exists.** | Anything that dynamically links the device's libs (libasound, libdbus…) **must be built `armv7-*-gnueabihf`**, *not* aarch64. **Static** binaries may be either arch — the kernel is 64-bit and runs both (this is why the existing static *aarch64* dropbear works). |
| Kernel | Linux 4.9.68 SMP, ARMv8 (`uname -m`=`aarch64`), 4 cores, NEON/AES/SHA/CRC32. | 64-bit kernel, 32-bit userland. Go/Rust static binaries: target `GOARCH=arm GOARM=7` (safest, matches userland) or `arm64` — both execute. |
| RAM | **238 MB total**, ~170 MB available, **no swap**. | Tight. A small agent (Go ~10–25 MB RSS) is fine. On-device wake-word (tflite/onnx) + AEC is a real memory question — measure, don't assume. |
| Flash | `/bkupgrade` 198 MB, **188 MB free**; rootfs 147 MB, 88 MB free; `/tmp` tmpfs 119 MB. | Plenty of room in `/bkupgrade/cfw/` for binaries + models. Our stuff persists there. |
| Tooling present | `aplay arecord amixer` (alsa-utils), `wget` (busybox, no TLS CA store by default), `ip iptables ifconfig route`, `setsid nohup start-stop-daemon logger flock`, `dbus-send`, `bluealsa bluealsa-aplay bluetoothctl`, `netcat` (busybox; **no `nc` applet**), `alsactl`, full busybox (`awk sed` etc.). | No shell `/dev/tcp`. busybox `netcat` exists but is a poor MQTT transport. **No `scp`, `timeout`, `pkill`/`pgrep`.** |
| **Not present** | **No `python`/`python2`/`python3`**, **no MQTT client** (`mosquitto_pub/sub`), **no compiler** (`gcc`), no `socat`, no `openssl` CLI, no `mpg123`/`mpv`/`ffmpeg`/`gst-launch`. | Rules out "just run wyoming-satellite (Python)" and "shell + mosquitto_pub" as drop-in options. Everything must be **cross-built elsewhere** and copied in. |
| Libraries present (armhf) | `libasound.so.2`, `libdbus-1`, `libbluetooth`, `libssl.so.1.1`/`libcrypto`, `libglib-2.0`/`libgio` (GLib 2.62), `libsndfile`, `libsamplerate`, `libspandsp`, `libsbc`, `libjq`, `libevent`, `libnl-3`, `libstdc++.so.6.0.28`. | A dynamically-linked armhf C program *can* reuse ALSA/BlueZ/OpenSSL. **alsa-lib rate plugins are installed** (`libasound_module_rate_samplerate*`, `speexrate*`) → `plughw:0,0` does format/rate conversion for free. No GStreamer plugins/CLI though. |
| ALSA | card0 `AML-AUGESOUND`: **`hw:0,0`** = TDM→`acm8625` amp (playback + a capture/loopback path), **`hw:0,1`** = `PDM-dummy` mic (capture). | Playback/record exactly as documented. `plughw:0,0` for resampling. |
| Mixer (`amixer -c0`) | `Master`(numid 184), `SPK mute`(183), `main Acm8625 AMP ChL/ChR`(1/2), `AMP Post Gain`(5), `AMP mute status`(4), `AMP Subvolume`(3), `Bluetooth Master`(185); capture side has `PDM Gain`, `Loopback Enable`/`Loopback datain source` (the AEC reference path). | Volume = `Master`. **Unmute recipe across SPK mute / AMP mute / ChL/ChR / Post Gain is still TBD** (open item from project-state) — pin it before Phase-1 audio. The `Loopback *` controls expose a hardware AEC reference for future echo cancellation. |
| LEDs | 52 `leds` nodes; ring = `bergamot:{red,green,blue}:logo{0..5}` trigger default **`ledfb-logo`** (set `none` to drive directly), `max_brightness` 255, mirrored by `/dev/fb0` (`U:6x1p-0`, 24bpp). Clock = `bergamot:white:clock{A..G}{0..3}` + `clock{DH,DL,DM,DEGREE}`, trigger already `none`, max 255. | As documented. One writer per device needed (arbitration, §2.6). |
| Input | `/dev/input/event0` = `gpio-keys` (KEY_MUTE 113 + SW_MUTE_DEVICE 14, the mic-mute hardware switch), `/dev/input/event1` = `aw933xx` touch (VOLUMEUP 115, VOLUMEDOWN 114, VOICECOMMAND 582). 24-byte `input_event`. | Read with evdev; `hardvold` already mirrors mic-mute→mixer. No `timeout` binary — time-box reads with `cmd & p=$!; sleep N; kill $p`. |
| Ambient light | `ltr308als01` on IIO (`/sys/bus/iio/devices/iio:device{0,1}`, plus `meson-axg-saradc`). | Drive clock/ring auto-brightness and expose a lux `sensor`. |
| Running base daemons | `hardvold` (pid), `bluealsa -p a2dp-sink -p a2dp-source -p hfp-ag --sbc-quality=high`, `bluealsa-aplay`, `clockd.sh` (our clock), dbus, wpa_supplicant, dhcpcd, chronyd, `watchdog` (`/dev/watchdog`). maind **dead**. | Design must coexist with `hardvold` (mixer mute) and `clockd` (matrix), and may reuse `bluealsa` for BT audio. |
| LAN / HASS | `ping <ha-host>` (HASS) **OK**. Egress locked (RFC1918 allowed, public DROP). No broker yet on `<ha-host>:1883`. Device clock is UTC; busybox ignores `/etc/TZ`, use `TZ=MSK-3` explicitly. | HASS + a broker on the LAN are reachable under the egress lock with **no internet needed**. |

**Net effect:** the device is a small 32-bit-userland Linux box with ALSA/BlueZ/evdev/sysfs and
no scripting runtime or package manager. The integration must be a **self-contained, cross-built,
few-dependency native program** that talks to HASS over the LAN.

---

## 0b. The existing voice stack we plug into (`<ha-config-repo>` / `voice-server`)

There is already a working, fully-local Russian voice stack at home. **This Station becomes one more
Wyoming satellite into that same stack** — we reuse its STT, TTS, conversation agent, Assist
pipeline, wake-word assets and asset host. We do **not** stand up a parallel brain on the speaker.
Source: `../../<ha-config-repo>/docs/` (esp. `voice-stack-refresh-2026-10.md`, `respeaker-voice.md`).

| Piece | What/where (as of 2026-10-11) | How this design uses it |
|---|---|---|
| **STT** | **GigaAM v3** (`rhasspy/wyoming-whisper:3.8.1`, `gigaam-v3-e2e-rnnt`, onnx-asr, CPU) — Wyoming on **`voice-server:24117`** (`voice-server` = GPU box, `<voice-server-ip>`). | Unchanged. The Station's satellite streams captured audio into the HA Assist pipeline, which routes to this. |
| **TTS** | **Silero** (`navatusein/silero-tts-service:2.0.0`, MaryTTS protocol, voices `xenia`/`baya`/`kseniya`) — **`voice-server:24118`**, wired in HA via `packages/assist.yaml` (`marytts`). `jaina-f5` F5 voice is **parked** (Shtorm_PocketTTS_RU is the CPU path back to it). | Unchanged for live replies. Also the generator for the **pre-rendered voice bank** (§2.5). |
| **Conversation agent** | Local **Ollama** (`qwen3-8b` class) on `voice-server`. | Unchanged. |
| **HASS** | Own LXC **`hass`** (reach via `ssh hass`), HA ~2026.9/2026.10; **Mosquitto**, Zigbee2MQTT, the **ESPHome dashboard** (`:6052`) all live here. In this design HASS + the MQTT broker are **`<ha-host>`**. | Broker for the MQTT agent (§2.1); host of the Assist pipeline + Wyoming integration the satellite joins (§2.2). |
| **Wake word** | Currently **stock "Okay Nabu"**. Custom **Russian microWakeWord** models trained locally (`../../<ha-config-repo>/mww-training/`, Silero positives → TFLite): `кибернерпа`, `Семён Семёныч`, `Винни`, `Господин оператор`; ready-made RU models exist (`Алиса`, `Джарвис`, …). | Reuse the **same `.tflite` assets** for on-device wake word (§2.2, Phase 2+). |
| **Satellites today** | **ReSpeaker Lite** (XMOS XU316 + ESP32-S3) running **formatBCE ESPHome** (parity with HA Voice PE: on-device microWakeWord, mute/buttons, STT/TTS events). | The Station is the **same satellite role, implemented on its own Linux CPU** instead of an ESP32 (§2.2). |
| **Asset host** | **ha-assets nginx** on **`<nas-host>:3017`** (`/media/nas2/Misc/StaticAssets/`), serves the microWakeWord models. | Also the authoritative home for the **pre-rendered voice bank** master (§2.5) and any media the `media_player` fetches. |

**The "Yandex Stations aren't viable" verdict is now OBSOLETE.** `voice-stack-refresh-2026-10.md §5`
concluded that reflashing/repurposing Yandex Stations wasn't practical and that the realistic route
to a pretty case was **transplanting ReSpeaker guts** into the shell. That conclusion predates this
project: we have **turned off the vendor stack, and drive every piece of the Station's hardware directly from
the shell** (speaker, 2-mic PDM array, LED ring, 7-seg matrix, buttons — all confirmed with the
Yandex stack dead). **No hardware transplant is needed — the Station is now a first-class Linux voice
satellite in its own right.** Update `<ha-config-repo>` to cross-reference this when convenient.

**Where this design extends the `<ha-config-repo>` thinking** (rather than just reusing it):
- **Satellite on a Cortex-A Linux CPU, not an ESP32/XMOS.** Wake word, VAD, audio framing and the
  Wyoming client run as native code in `yastationd`, not pymicro-wakeword-on-MCU + ESPHome. No XMOS
  AEC/beamforming (that die isn't here) — see the AEC caveat (§2.2/§5).
- **A much richer indication surface** than the ReSpeaker's few LEDs: a 6-px RGB ring **and** a 4-digit
  7-seg matrix → a proper display/scenario toolkit (§2.6), including timer countdowns and transient
  overlays the ReSpeaker can't show.
- **A pre-rendered voice-response bank on the device** (§2.5) for sub-100 ms acknowledgements and,
  critically, for **speaking up when the pipeline is down** (§2.7) — neither exists in the current stack.
- **A sovereign on-device health/watchdog subsystem** (§2.7) replacing the stock firmware's health
  logic, so the speaker degrades gracefully instead of hanging when the broker/HASS/`voice-server` are down.

---

## 1. Goal & constraints

**Goal:** expose the speaker's audio, mic, LED ring, clock matrix, and buttons to Home Assistant
(`<ha-host>`) so it behaves as a normal offline smart speaker — media/announcements, a
controllable light + status display, physical buttons, mic-mute reporting, and (later) voice
assist and multiroom — with **no cloud and no Yandex code**.

**Hard constraints (all verified §0):**

1. **Offline / LAN-only.** Egress is locked to RFC1918 (`/bkupgrade/cfw/egress.sh lock`) and the
   router drops the speaker's MAC to WAN. The integration must work entirely over the LAN. HASS
   and an MQTT broker on the LAN are reachable under the lock; **no internet required at runtime.**
   Firmware updates must stay blocked forever (an update replaces the CFW and restores the vendor stack) — unchanged by this work.
2. **Lightweight.** 238 MB RAM, no swap, 4× ARMv8 cores. Prefer one small static binary over a
   stack of services.
3. **Cross-build only; no package manager.** Target **`armv7 glibc` (dynamic)** or **any static**
   (Go `GOARCH=arm GOARM=7`, or Rust `armv7-unknown-linux-musleabihf` / `-gnueabihf`). Build on WSL
   or the Pi, stage on a LAN host, copy to `/bkupgrade/cfw/bin/`.
4. **Persist across reboot.** Binaries + config live in `/bkupgrade/cfw/` (persistent UBIFS,
   188 MB free). Start via an `/etc/init.d/S*` hook (rootfs is `ro`; `mount -o remount,rw /` to add
   the hook, exactly like the existing `S42zdropbear`/`S50clockd`/`S99zzegress`). No dm-verity
   (confirmed), so rootfs hooks are safe.
5. **Coexist with `clockd` and `hardvold`** (display arbitration §2.6; mixer/`hardvold` coexistence in §4 Phase 1).

---

## 2. Architecture options (with honest trade-offs)

### 2.1 Control plane: how HASS talks to the device

**Option A — On-device agent speaking MQTT + MQTT Discovery (recommended).**
A single cross-built static daemon (`yastationd`) connects to the HASS Mosquitto broker on the LAN,
publishes **MQTT Discovery** config so entities auto-appear, publishes state, and subscribes to
command topics. It drives sysfs/evdev/ALSA directly (or shells out to `aplay`/`amixer`/`arecord`).

- **+** One TCP connection, one binary, survives HASS restarts, offline-friendly, availability via
  MQTT **LWT** (device shows `unavailable` when it dies). Discovery = zero YAML in HASS.
- **+** Matches the device perfectly: no runtime deps, tiny footprint.
- **−** Needs a broker (HASS Mosquitto add-on — easy; already assumed available).
- **Language/runtime:** **Go, static, `CGO_ENABLED=0 GOARCH=arm GOARM=7`.** Rationale: the device has
  **no Python, no compiler, no MQTT client**, and a **32-bit armhf userland**. A static Go binary
  sidesteps the entire glibc/arch split (no dynamic linking at all), is one file to copy, has
  mature pure-Go MQTT (`eclipse/paho.golang`), evdev, and HTTP; it shells out to the stock
  `aplay`/`arecord`/`amixer` for audio so it never has to link armhf `libasound`. Memory ~10–25 MB.
  (Rust is an equally valid static alternative; Go wins on breadth of ready libraries for this job.)

**Option B — Shell + `mosquitto_pub/sub`.** Cross-build mosquitto clients (armhf, link the present
`libssl.so.1.1`) and write a busybox-sh glue layer.
- **−** `mosquitto_sub` subscribe-loops in sh are fragile (reconnect, LWT, concurrency, binary audio
  payloads). Multiple processes, harder evdev decode in sh, no clean discovery generation.
- Verdict: **fallback only** if a Go toolchain is unavailable. Not recommended.

**Option C — HASS REST/WebSocket from the device.** Agent calls the HASS API directly instead of MQTT.
- **−** No auto-discovery, need a long-lived token on the device, more HASS-version-coupled, state
  push is clumsier than MQTT. Verdict: **no.**

**→ Chosen: Option A (Go static agent, MQTT + Discovery).**

### 2.2 Voice: another Wyoming satellite into the existing `voice-server` pipeline

Expose mic+speaker as a **Wyoming satellite** that joins the **existing** HA Assist pipeline (§0b):
HA orchestrates **GigaAM v3 STT (`voice-server:24117`) → Ollama `qwen3-8b` → Silero TTS
(`voice-server:24118`)**. We add a satellite; **we do not add any STT/TTS/LLM on the speaker.** The
Station becomes a peer of the ReSpeaker Lite, differing only in that the satellite runs as native
Linux code on the Station's own CPU rather than on an ESP32 + XMOS.

- The official `wyoming-satellite` is **Python/asyncio** — **not viable as-is** (no Python, 238 MB
  RAM). The **Wyoming protocol itself is trivial**: newline-delimited JSON event headers + optional
  binary payload over a TCP socket (`audio-start`, `audio-chunk`, `audio-stop`, `detection`,
  `transcript`, `synthesize`, `run-satellite`, …). Re-implement the **satellite client in the same
  Go binary** in a few hundred lines; register it in HA via the **Wyoming** integration so it uses
  the same preferred pipeline as the ReSpeaker.
- **Wake word.** On-device openWakeWord/microWakeWord needs a tflite runtime and tens of MB RAM +
  CPU — borderline here and extra cross-build pain. **MVP = push-to-talk, no wake word:**
  `KEY_VOICECOMMAND` (582) starts a turn; the agent streams `arecord hw:0,1` (downmixed to mono
  16 kHz) as Wyoming `audio-chunk`s; the pipeline does VAD/STT/intent/TTS; the agent plays the reply
  via `aplay plughw:0,0` and drives listening/thinking/speaking animations (§2.6).
- **Hands-free later reuses the existing RU wake-word assets.** The custom Russian microWakeWord
  `.tflite` models already trained in `../../<ha-config-repo>/mww-training/` (and the ready-made `Алиса`
  etc.) are the **same artifacts** we'd run here — but on the ReSpeaker they execute via
  pymicro-wakeword on the ESP32, whereas on the Station they need a **native TFLite-micro / LiteRT
  inference runtime cross-built for armv7** (no Python on the device). That runtime (a small C/Go
  wrapper around the streaming mWW model, fed 16 kHz frames from `arecord`) is the main new piece for
  hands-free; budget it as **Phase 2+ and measure RAM/CPU** on the 238 MB box. The models are hosted
  at `<nas-host>:3017/mww/` already, so provisioning is just a LAN fetch.
- **AEC / beamforming are gone with maind** (proprietary) and — unlike the ReSpeaker — there is **no
  XMOS DSP** to replace them. Raw 2-mic PDM only. Push-to-talk near-field is acceptable; barge-in and
  far-field degrade. The card exposes a **hardware loopback/AEC reference** (`Loopback Enable`/
  `Loopback datain source` mixer controls + the `hw:0,0` capture path), so a future software AEC
  (speexdsp — `libspandsp`/`libsbc` present — or a cross-built webrtc-apm) has a reference to work
  with. Treat AEC as **Phase 2+**.
- **Feasibility:** push-to-talk satellite into the `voice-server` pipeline = clearly feasible now. On-device
  RU wake word = feasible but gated on the armv7 TFLite runtime + a RAM/CPU measurement; defer.
- **Degraded-mode tie-in (critical):** if a turn is triggered (button or wake word) but the pipeline
  (broker/HASS/`voice-server`) is unreachable, the satellite must **not** stream into the void and hang —
  it plays a **pre-rendered "no connection" clip** immediately (§2.5, §2.7).

### 2.3 Audio playback / announcements

- **MVP (recommended): `media_player` over MQTT driving `aplay`.** HASS announces/TTS → publishes a
  media URL (served by HASS on the LAN, reachable under egress lock) → agent fetches it (Go HTTP or
  `wget`) → pipes to **`aplay plughw:0,0`** (plughw auto-resamples via the installed alsa rate
  plugins). **Format caveat:** no MP3/opus decoder on device → prefer **WAV/FLAC** from HASS (Piper
  TTS emits WAV; HASS can serve WAV). For MP3 streams, **embed a pure-Go mp3/flac decoder** in the
  agent (e.g. `go-mp3`) → PCM → `aplay`. Volume via `amixer set Master`. Good enough for
  TTS/notifications/alarms — the common case.
- **Phase 3 — multiroom / streaming:**
  - **Snapcast client (`snapclient`)** — best synchronized multiroom; integrates with HASS; one
    cross-built binary feeding `aplay`. Recommended for multiroom.
  - **Squeezelite** — if the user runs Logitech Media Server; HASS `squeezebox` integration.
  - **DLNA/UPnP renderer** (gmrender-resurrect) — heavier (GStreamer/libupnp; **no GStreamer CLI or
    plugins on device** → more to cross-build). Lower priority.
  - These are **additive**; the MQTT `media_player` already covers HASS-initiated audio.

### 2.4 Why NOT ESPHome

ESPHome compiles a **firmware image for microcontrollers** (ESP32/ESP8266/RP2040/nRF) and flashes
it onto bare metal. `bergamot` is a **full Linux SBC-class device** (4-core ARMv8, 256 MB, Linux
4.9, ALSA/BlueZ/evdev, a real filesystem). ESPHome has no deployment path for "drive ALSA/evdev on
an existing Linux box as a media/voice device" — its Linux/host target is for testing, not a
speaker runtime. The correct Linux-native analogs are exactly what's chosen here: an **MQTT-Discovery
agent** (the role ESPHome's native API plays for MCUs) plus **Wyoming** for voice. Don't reach for
ESPHome.

### 2.5 Pre-rendered voice-response bank (latency + degraded-mode speech)

Round-tripping every spoken reply through the pipeline (Wyoming → Silero synth 0.9–2.5 s + network)
is wasteful for the dozens of **short, fixed or templated** phrases a speaker says constantly, and
it is **impossible** exactly when it matters most — when the pipeline is down. So the agent keeps a
**local bank of pre-rendered TTS clips** and plays them with `aplay plughw:0,0` in **tens of ms**.

- **What's in the bank.**
  - *Fixed acks / errors:* «готово», «включено», «выключено», «не понял», «таймер поставлен»,
    «таймер отменён», «нет связи с сервером», «сейчас не могу, нет связи», «сервер недоступен»,
    a startup chime, an alarm/timer-done tone.
  - *Templated / parameterised:* the common parameter values rendered as **whole phrases** rather than
    glued fragments — e.g. «таймер на 5 минут», «…на 10 минут», for the top-K timer values; numbers
    0–59 / common temperatures for countdowns and climate. Russian number **morphology**
    (минута/минуты/минут, agreement) makes naive concatenation of a number fragment + a noun fragment
    sound wrong and leaves audible prosody seams, so **pre-render full phrases for the frequent
    values** and fall back to live TTS for the long tail (and optionally cache that result).
- **How it's generated.** An **offline batch job on `voice-server`** drives the *same* Silero voice the live
  pipeline uses (so the bank matches live replies), through **RUAccent** for stress, over a phrase
  manifest. (When the home voice moves to Jaina/Shtorm_PocketTTS_RU, re-render the bank with it — see
  invalidation.) This is a build-time step, not a device dependency.
- **Format.** **WAV (or FLAC) at the device's native playback rate** (so `aplay plughw:0,0` plays with
  no decode and no resample cost), mono. A few hundred short clips is only a few MB — trivial against
  the 188 MB free on `/bkupgrade`.
- **Where it lives.** **Master on `<nas-host>:3017`** (the existing ha-assets nginx — shared across
  satellites, survives a speaker reflash), **mirrored to the device** at
  `/bkupgrade/cfw/voicebank/<voice>/<version>/`. The device copy is what guarantees instant, offline,
  pipeline-independent playback. Sync is a plain LAN `wget` of changed files (egress-allowed; no
  internet).
- **Cache key / lookup.** Key = **(phrase-id or normalised text, voice-id, tts-engine-version)** →
  hashed filename, listed in a `manifest.json` with content hashes. The agent's announce path first
  resolves the requested phrase to a bank entry: **hit → `aplay` immediately; miss → request live TTS**
  from the pipeline (and may write the result back into the on-device cache for next time).
- **Invalidation.** The bank directory is **versioned by (voice, engine)**; changing the pipeline
  voice or TTS engine bumps the version and the device syncs the new tree (old one can be pruned).
  Per-entry content hashes catch partial/failed syncs.
- **Latency win.** Live reply ≈ Wyoming round-trip + 0.9–2.5 s synth; bank hit ≈ disk read + `aplay`
  startup (**< 100 ms**). Dramatic for the instant-feedback phrases, and the **only** option in
  degraded mode.
- **Ties into** the `media_player`/announce path (§2.3) and the sovereign watchdog's self-announce
  (§2.7): the connectivity-error clips **must** be in the on-device bank, because they are precisely
  the clips that have to play when everything upstream is unreachable.

### 2.6 Display + LED scenario toolkit (primitives for HASS scenarios *and* local reflexes)

The 6-px RGB ring and the 4-digit 7-seg matrix are the Station's whole UI. `yastationd` must expose
them as **composable primitives** that two kinds of caller can drive: **HASS scenarios/automations**
(semantic, networked) and **local reflexes** (instant, work even in degraded mode). The core model is
a **priority-ordered layer stack with auto-revert**, one stack per surface (ring, matrix).

- **Layer model.** Each surface renders the **top active layer**. Layers carry a **priority** and an
  optional **TTL / clear-condition**; when a layer's TTL expires or its condition clears, it is popped
  and whatever is beneath shows through. Base layer is always present:
  - *Matrix base* = **clock** (the retired `clockd`'s job, now rendered by the agent — §2.6/§4).
  - *Ring base* = the HASS-set `light` state (solid colour/brightness or `off`).
- **Priority order** (high → low): `error/degraded` → `voice-state` (listening/thinking/speaking) →
  `local-transient` (volume bubble, mute indicator) → `HASS-scenario` (timer countdown, notification,
  message text) → `base` (clock / HASS light). This resolves the **local-vs-HASS arbitration**: local
  reflexes own short-lived immediate feedback and always win over a networked scenario; a hard error
  state wins over everything; the clock/light is the quiescent default.
- **Primitives (exposed internally and over MQTT/command topic).**
  - *Matrix:* `show_clock`, `show_number(n)`, `show_time(mm:ss)` / countdown, `show_text(s)` (limited
    7-seg glyph set: digits + a handful of letters, plus the colon/degree dots), `set_brightness`,
    `blank`.
  - *Ring:* `set_solid(rgb)`, `set_brightness`, `fill(k/6)` (level bar), and `effect(name)` from a
    small built-in library rendered by the agent's frame-timer loop — **listening** (cyan pulse),
    **thinking** (rotating comet), **speaking** (amplitude/breathe), **error** (red blink),
    **success** (green flash), **mute** (steady amber), **boot**, **progress**. (Frames can reuse the
    vendor `oknix/*.led` concept: `RRGGBB`×6 + duration per frame — a format already understood from
    the teardown.)
- **Control surfaces — both required.**
  1. **MQTT for HASS scenarios.** Entities: `light` (ring base), `select display_mode`
     (`clock`/`off`/`message`/`timer`/`vu`), `text display_message`, plus a **command topic**
     `yastation/<id>/display/cmd` taking JSON `{surface, op, args, ttl_ms, priority}` so an automation
     can push a one-shot overlay ("flash green", "show countdown 5:00", "show 22°") without a dedicated
     entity each. Button presses are published as HA **`event`** entities so scenarios can react.
  2. **Local reflexes (no HASS round-trip).** The agent, directly on evdev/state changes, pushes the
     matching transient layer: volume key → volume bubble (ring `fill k/6` or matrix number, ~2 s TTL);
     mute switch (`event0`) → steady amber + mute glyph until unmuted; voice turn → listening/thinking/
     speaking (owned by the satellite's own state machine, instant, no HASS needed); pipeline error →
     error pattern + spoken clip (§2.7).
- **Scenario scaffolding (worked examples).**
  - **Timer:** an HA timer/automation publishes `display/cmd {op:"countdown", args:{deadline}}`; the
    agent ticks the remaining `mm:ss` on the matrix locally until it hits zero, then plays the
    timer-done tone + «таймер» clip (bank, §2.5) and flashes the ring — the countdown keeps rendering
    even if HASS drops mid-timer, since the agent holds the deadline.
  - **Volume:** purely local reflex — transient level bar; state still echoed to HASS.
  - **Mute:** local reflex, persistent until the hardware switch clears.
  - **Listening/thinking/speaking:** driven by the satellite locally the instant each phase changes.
- **Coexistence:** single owner (the agent) writes both surfaces; `clockd` is retired/absorbed so
  there is never a second writer (§4 Phase 1 coexist).

### 2.7 Sovereign watchdog / health + self-announce subsystem

The stock firmware had its own health/watchdog logic; with Yandex gone we need a **sovereign
equivalent** so the speaker **degrades gracefully** instead of hanging when the broker, HASS, the
router, `voice-server`/Wyoming, or power misbehave — and **signals a broken state clearly**, by LED/display
**and** by a spoken pre-rendered clip. This is its own subsystem of `yastationd` (plus a tiny
external supervisor).

- **Process supervision (keep the agent alive).** Init hook `/etc/init.d/S52zyastationd` starts
  a busybox-sh supervisor, `<cfw>/init.d/supervise.sh`, under `setsid`. It restarts the agent with
  a backoff, swaps in the previous binary (`yastationd.prev`) after a crash loop, and rotates the
  log (contract in its header and in `../guides/deploying.md`). It lives in `<cfw>`, so changing it needs no
  rootfs write. **Hang detection:** the agent touches a heartbeat file every N s; a watchdog
  checks staleness and `kill`s + restarts a wedged agent. Coexists with the existing `S50clockd`
  (retired — the agent owns the clock) and `hardvold` (left running).
- **Kernel watchdog:** `/dev/watchdog` is already fed by the stock busybox `watchdog`. **Leave it to
  that process; the agent must not open it** (avoid double-open). Optionally the agent can expose its
  heartbeat for a future integration, but it should not be the watchdog feeder.
- **On-device liveness checks (periodic, local).** The agent runs a small health state machine from
  cheap probes: **broker** (MQTT CONNACK / keepalive), **HASS** (TCP/HTTP to `<ha-host>`), **`voice-server`
  pipeline** (TCP to `24117`/`24118`). States: `ONLINE` → `DEGRADED(no-broker)` /
  `DEGRADED(no-pipeline)` → `OFFLINE(no-LAN)`.
- **MQTT availability / LWT + reconnect.** Every entity carries an `availability` topic; the broker
  **LWT** flips the device to `unavailable` in HA the moment the TCP drops. The agent **reconnects
  with exponential backoff + jitter** (cap ~30–60 s) so a broker/HASS **restart is ridden out
  silently**; on reconnect it **re-publishes discovery + current state**.
- **Local degraded mode.** When upstream is down the agent **keeps doing everything local**: clock,
  volume keys, mute reflex, ring/matrix feedback, button handling, and voice-bank playback. It shows a
  **subtle persistent degraded indicator** (e.g. a dim-red ring dot or a small matrix glyph) so the
  state is visible at a glance, and reports it to HA when the broker returns.
- **Self-announce on wake-with-no-pipeline (the critical case).** If the user triggers a voice turn
  (on-device wake word fires, or presses `KEY_VOICECOMMAND`) but broker/HASS/`voice-server` are unreachable,
  the agent must **immediately play a pre-rendered clip** from the local bank — e.g.
  «сейчас не могу, нет связи» — and show the error ring pattern, **instead of streaming into a dead
  socket and hanging silently**. This is the hard requirement that forces the connectivity-error clips
  to live **on the device** (§2.5). (Recovery announcements like «связь восстановлена» are optional /
  opt-in to avoid chattiness.)
- **Health reporting to HASS (diagnostics).** Expose a `sensor` **health state** (`online`/
  `degraded`/`offline`) + a `binary_sensor` **pipeline available**, and optionally last-seen latencies,
  as **diagnostic** entities so HA dashboards/automations can see the satellite's self-assessment.
- **Power loss:** nothing special — everything persists in `/bkupgrade` + the rootfs init hook; the
  box boots back to the same state in ~15 s, chrony re-syncs the clock from the LAN NTP.

---

## 3. Interface → HASS entity mapping

| Device interface | Driven via | HASS entity (MQTT Discovery) | Notes / arbitration |
|---|---|---|---|
| **Speaker** `hw:0,0` + `amixer Master` | agent → `aplay plughw:0,0`, `amixer set Master` | **`media_player`** (play URL / announce / stop / volume) + optional **`number`** for raw volume | WAV/FLAC native; MP3/opus via embedded Go decoder. Unmute recipe TBD (§5). Ducks/pauses Snapcast during announcements (Phase 3). |
| **LED ring** 6×RGB (`…:{r,g,b}:logo0..5` / `/dev/fb0`) | agent sets `trigger=none` then writes `brightness` (or 6×3 B to `/dev/fb0`) | **`light`** (RGB, brightness) = base layer + **`select`** effect + `display/cmd` overlays | Single writer = agent; priority layer stack (§2.6). Voice/error states override the HASS colour via higher-priority layers, then auto-revert. |
| **Clock matrix** 7-seg ×4 (`…:white:clock*`) | agent renders segments (port `clock.sh` logic) | **`select`** `display_mode` (`clock`/`off`/`message`/`timer`/`vu`) + **`text`** `display_message` + `display/cmd` topic | **`clock` is default/fallback.** Agent is the single owner; `clockd` retired/absorbed (§4 Phase 1). Timer countdown, overlays via the layer stack (§2.6). Auto-brightness from ambient lux. Use `TZ=MSK-3`. |
| **Display/LED actions** (ring+matrix) | agent layer stack (§2.6) | command topic **`yastation/<id>/display/cmd`** `{surface,op,args,ttl_ms,priority}` | One topic for one-shot HASS overlays (flash, countdown, show number/text) without an entity per action. Local reflexes use the same primitives. |
| **Mic array** `hw:0,1` | agent → `arecord hw:0,1` | *(not a standalone entity)* consumed by the **Wyoming satellite** → `voice-server` STT; optional **`binary_sensor`** "sound detected" | 2-ch PDM; downmix→mono 16 kHz. No on-device AEC/beamforming, no XMOS DSP (§2.2/§5). |
| **Mic-mute switch** `event0` SW_MUTE_DEVICE(14)+KEY_MUTE(113) | agent reads evdev; `hardvold` applies mute to mixer | **`binary_sensor`** "mic muted" (authoritative state) + optional **`switch`** mirror | Hardware switch is authoritative; agent **observes & reports**, does not fight `hardvold`; gates voice capture. A `switch` can only reflect, not override, the physical latch. |
| **Volume buttons** `event1` VOLUMEUP(115)/VOLUMEDOWN(114) | agent reads evdev → `amixer Master` ±, publishes | folds into `media_player`/`number` volume; optional **`event`** entities | Local reflex: transient volume bubble on ring/matrix (§2.6). Works offline; state echoed to HASS. |
| **Voice button** `event1` VOICECOMMAND(582) | agent reads evdev → starts Wyoming turn | triggers the shared **Assist pipeline** (Phase 2); Phase 1 can expose an **`event`**/**`button`** | Push-to-talk (no wake word needed for MVP). If pipeline down → pre-rendered "no connection" clip (§2.7). |
| **Ambient light** `ltr308als01` (IIO) | agent reads `/sys/bus/iio/.../in_illuminance*` | **`sensor`** (lux) | Also drives clock/ring auto-brightness locally. |
| **Announcements / TTS acks** | agent → voice bank lookup then `aplay`, else live TTS | via `media_player` / `notify` | Bank hit < 100 ms; miss → `voice-server` Silero (§2.5). |
| **Health / self-assessment** | agent liveness state machine (§2.7) | **`sensor`** health (`online`/`degraded`/`offline`) + **`binary_sensor`** pipeline-available (diagnostic) | Reflects broker/HASS/`voice-server` reachability; drives degraded-mode indicator + self-announce. |
| **BT audio** `bluealsa` | existing `bluealsa`/`bluealsa-aplay` | optional **`media_player`**/**`switch`** (BT sink on/off) | Reuse the running stack; later phase. |
| Agent liveness | MQTT **LWT** + backoff reconnect (§2.7) | `availability` on every entity | Entities show `unavailable` if the agent/device drops; auto-recover on reconnect. |

---

## 4. Recommended approach & phased plan

**Recommended architecture:** a single **static Go daemon `yastationd`** on the device, speaking
**MQTT + MQTT Discovery** to the HASS Mosquitto broker for all control/telemetry, **embedding a
Wyoming satellite** for voice, and **shelling out to `aplay`/`arecord`/`amixer`** for audio. It is
the single owner of the LED matrix (absorbing the clock) and LED ring, reads both evdev devices,
coexists with `hardvold` (mixer mute) and `bluealsa` (BT). One file in `/bkupgrade/cfw/bin/`,
started by an init hook, configured by `/bkupgrade/cfw/yastationd.conf`.

### Phase 0 — prerequisites (no device changes beyond staging)
- Use the **existing Mosquitto** on the `hass` LXC (`<ha-host>:1883`, §0b) — add a user/pass for
  the speaker; no new broker needed.
- Set up the **cross toolchain**: Go, `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7` → verify the
  output is ELF32 ARM and runs on the device. (Static ⇒ arch-independent of the armhf/arm64 split.)
- **Staging without internet:** serve build artifacts (and the voice bank) from a **LAN HTTP server**
  — the existing **ha-assets nginx `<nas-host>:3017`** is ideal (same host already serving mWW models) —
  and `wget` them on the device; RFC1918 is allowed under the egress lock, so **no `egress.sh open`
  is needed**. (Internet-only pulls: `egress.sh open` **plus** the router toggle
  `disable [find comment~"yastation"]`, then re-lock and re-enable.)
- **Pin the ALSA unmute recipe** (was open at design time): find the exact
  `SPK mute` / `AMP mute status` / `ChL`/`ChR` / `Post Gain` / `Master` combination that yields
  clean audible output, and bake it into the agent's "audio init". **Blocker for Phase-1 audio.**
- **Batch-render the initial voice bank** on `voice-server` (§2.5): the fixed ack/error clips — including the
  connectivity-error clips the watchdog needs — via the pipeline's Silero voice + RUAccent, at the
  device playback rate; publish to `<nas-host>:3017` and plan the device mirror.
- Respect the supply-chain rule (no dep <3 days old); pin Go module versions.

### Phase 1 — MVP: MQTT agent (control, display toolkit, announcements, watchdog)
**Build:** `yastationd` with:
- MQTT client (paho) + Discovery publisher + per-entity availability (**LWT** + backoff reconnect).
- **Display/LED layer stack (§2.6)** as a core subsystem — not just two entities:
  - **LED ring** → `light` base layer (set `trigger=none`, write brightness per channel / `/dev/fb0`)
    + the effect library + local-reflex overlays.
  - **Clock matrix** → `select display_mode` (default `clock`) + `text display_message`; **port
    `clock.sh`'s segment map into the agent** so there is one writer; countdown/overlay ops.
  - **`display/cmd`** command topic for HASS one-shot overlays; priority stack + auto-revert.
- **Speaker** → `media_player` + `number` volume: `aplay plughw:0,0`, `amixer Master`; fetch WAV/FLAC
  (embedded Go mp3/flac decode for non-WAV).
- **Pre-rendered voice bank (§2.5)**: mirror the bank to `/bkupgrade/cfw/voicebank/…`; announce path
  does bank-lookup → `aplay` (acks + the connectivity-error clips land here even before voice exists).
- **evdev reflexes**: volume keys → `Master` + transient volume bubble; mic-mute `SW_MUTE_DEVICE` →
  `binary_sensor` + amber mute layer; voice button → `event`/`button` placeholder.
- **Ambient lux** → `sensor` + local auto-brightness of clock/ring.
- **Sovereign watchdog/health (§2.7)**: liveness state machine + health `sensor`/`binary_sensor`,
  local degraded mode + degraded indicator, supervisor respawn + heartbeat.

**Cross-compile/install:** one static armv7 binary → `/bkupgrade/cfw/bin/yastationd`; config
`/bkupgrade/cfw/yastationd.conf` (broker host/port/creds, `voice-server`/pipeline endpoints, device name,
voicebank version, default brightness).
**Persist:** add `/etc/init.d/S52zyastationd` (`mount -o remount,rw /`; model it on `S50clockd` —
`start-stop-daemon`/`setsid`, log to `/tmp`) plus the supervisor/respawn (§2.7). Runs after
dbus/wifi/dhcp/hardvold/chrony/bluetooth. **Coexist:**
- **`clockd`:** retire `S50clockd` (move to `disabled-initd/`, as was done for other hooks) and let
  the agent render the clock as the base matrix layer. *Fallback option* if single-owner feels
  risky: keep `clockd` but have the agent `SIGSTOP`/`SIGCONT` it around matrix ownership (two-writer
  caveat). Recommended: **single owner (agent)**, with the supervisor respawn so a crash falls back
  to the clock.
- **`hardvold`:** untouched; it keeps applying the mic-mute to the mixer. The agent uses `Master`
  (and only its own playback mute), never the mic path, and only *reads* `event0` to report state.
  ALSA control device allows concurrent openers.

**Exit criteria:** ring light + display layer stack (clock default, volume bubble, mute, overlays),
volume keys, mic-mute sensor, lux sensor, HASS TTS/announcements (bank + live), and the watchdog
(availability flips, degraded-mode indicator, local reflexes keep working with the broker down) all
work from HASS, offline, and survive a reboot.

### Phase 2 — voice (Wyoming satellite into the `voice-server` pipeline, push-to-talk)
**Build:** extend `yastationd` (or a sibling static binary) with a Wyoming **satellite client** that
registers in HA's **Wyoming** integration so it uses the **same Assist pipeline as the ReSpeaker**
(GigaAM v3 → Ollama → Silero on `voice-server`, §0b/§2.2):
- `KEY_VOICECOMMAND` (582) starts a turn → stream `arecord hw:0,1` downmixed to **mono 16 kHz** as
  Wyoming `audio-chunk`s; receive `transcript`/`synthesize`; play the TTS reply via `aplay plughw:0,0`.
- Drive **listening / thinking / speaking** animations locally via the §2.6 layer stack (the
  satellite's own state machine — no HASS round-trip for the animation).
- **Self-announce (§2.7):** if the pipeline is unreachable at trigger time, play the pre-rendered
  "no connection" clip + error pattern instead of hanging.
**Install/persist:** same binary/hook; add the `voice-server`/Wyoming TCP config.
**Coexist:** mic-mute (`hardvold`/`event0`) gates capture — honour it (don't record when muted).
**Later within Phase 2 (optional, measure RAM/CPU):** **on-device Russian wake word** reusing the
**existing `.tflite` models** from `../../<ha-config-repo>/mww-training/` (hosted at `<nas-host>:3017/mww/`),
run by a **cross-built armv7 TFLite-micro/LiteRT inference** fed 16 kHz frames (the new piece, since
the ReSpeaker runs these on an ESP32 and the device has no Python); and **AEC** (speexdsp or
webrtc-apm) using the hardware loopback reference for barge-in. Both are resource-risky on 238 MB and
there is no XMOS DSP to lean on — prototype and measure before committing.

### Phase 3 — multiroom / streaming audio
- Cross-build **`snapclient`** (armv7, or static) → `/bkupgrade/cfw/bin/`, init hook, HASS Snapcast
  integration for synchronized multiroom. The agent arbitrates the single `hw:0,0` device: pause/duck
  Snapcast during a `media_player` announcement or a voice turn, then resume.
- Alternative: **Squeezelite** (if LMS is in use). DLNA/UPnP renderer is lowest priority (heaviest
  cross-build: GStreamer stack absent).

---

## 5. Risks & open questions

- **ALSA unmute recipe (TBD, Phase-0 blocker).** The exact mixer combination for clean playback
  (`SPK mute`/`AMP mute status`/`ChL`/`ChR`/`Post Gain`/`Master`) is not yet pinned (noted in
  the design notes). No audio feature ships until this is nailed down and codified in the agent.
- **32-bit armhf userland vs 64-bit kernel.** Any dynamically-linked native code must be
  `armv7-gnueabihf` (glibc 2.30) — **not** aarch64. Static builds dodge this. Go shelling out to the
  stock `aplay`/`amixer`/`arecord` avoids linking armhf `libasound` entirely; recommended.
- **Memory (238 MB, no swap).** The MQTT agent is cheap. Wyoming streaming is cheap. **On-device
  wake word + AEC are the real memory/CPU risk** — defer and measure; push-to-talk needs neither.
- **No on-device codecs.** No MP3/opus CLI decoder. Prefer WAV/FLAC from HASS (Piper = WAV); embed a
  pure-Go decoder for MP3; `plughw:0,0` handles resampling via the installed alsa rate plugins.
- **AEC / beamforming absent** (left with maind) and **no XMOS DSP** to replace them (unlike the
  ReSpeaker satellite). Far-field and barge-in degraded. Hardware AEC reference exists (`Loopback *`
  controls + `hw:0,0` capture) for a future software AEC.
- **On-device RU wake word needs a new armv7 TFLite runtime.** The trained `.tflite` models are
  reusable, but the ReSpeaker runs them via pymicro-wakeword on an ESP32; here they need a native
  streaming inference cross-built for the 32-bit userland (no Python) — plus a RAM/CPU budget check
  on 238 MB. Push-to-talk MVP avoids this entirely; it is the main unknown for hands-free.
- **Voice-bank coverage & Russian morphology.** Concatenating number + noun fragments sounds wrong
  (минута/минуты/минут agreement) and leaves prosody seams → pre-render **whole phrases** for frequent
  values and fall back to live TTS for the long tail. The bank must be **re-rendered when the pipeline
  voice/engine changes** (version it); keep the device mirror in sync over the LAN. The
  connectivity-error clips are load-bearing for degraded mode — verify they are always present
  on-device before trusting the self-announce.
- **Dependency on `voice-server` / the shared pipeline.** Voice is only as available as `voice-server`
  (`<voice-server-ip>`, STT `:24117` / TTS `:24118`) + the `hass` LXC. The watchdog's whole job is to make
  this failure graceful (local reflexes + spoken "no connection"), but there is **no on-device STT/LLM
  fallback** by design — if `voice-server` is down, the speaker can't understand commands, only say so.
- **Display arbitration / layer-stack correctness.** A single owner (agent) avoids two-writer fights,
  but the priority stack + auto-revert TTLs must be got right so overlays don't get stuck over the
  clock; the supervisor must restore the clock base layer on restart.
- **Display arbitration.** Two writers to the matrix (agent + `clockd`) will fight. Decision:
  **single owner (agent), `clockd` retired**, with a respawn fallback. Revisit if the agent proves
  less reliable than the tiny `clockd`.
- **Pulling binaries under the egress lock.** Preferred: stage on a **LAN** HTTP host and `wget`
  (RFC1918 allowed → no internet). Only if unavoidable: `egress.sh open` **and** router
  `disable [find comment~"yastation"]`, then re-lock + re-enable. Keep internet blocked by default.
- **Timekeeping.** Device is UTC; busybox ignores `/etc/TZ`. The agent must set `TZ=MSK-3`
  explicitly for the clock (as `clock.sh` does). chrony already syncs to the LAN NTP `<router-ip>`.
- **Watchdog.** `/dev/watchdog` is serviced by the stock busybox `watchdog`. Ensure the agent's init
  hook doesn't interfere; the agent itself should not hold the watchdog.
- **TLS.** No `openssl` CLI, but `libssl.so.1.1` is present and Go has built-in TLS. MQTT can be
  plaintext+auth on the trusted LAN, or TLS via Go stdlib if the broker requires it.
- **Firmware updates stay the existential threat** — the integration changes nothing here; updates
  remain triple-blocked. A factory reset (safe) would wipe `/etc/init.d` hooks but not
  `/bkupgrade/cfw/` — re-adding the rootfs hook is the only recovery step needed.
- **HASS/add-on availability.** Design assumes the **existing** Mosquitto on the `hass` LXC (Phase 1)
  and the **existing** Wyoming integration + Assist pipeline (GigaAM v3 STT `voice-server:24117`, Ollama,
  Silero TTS `voice-server:24118` — §0b) that the ReSpeaker already uses (Phase 2). Nothing new to stand up
  server-side beyond a broker user and registering the new satellite.

---

*Design for the HASS integration of the de-Yandexed Yandex Station Mini 3 (`bergamot`). Grounded in
a read-only device probe on 2026-10-22 (maind off, egress locked). Implementation lives under
`/bkupgrade/cfw/` on the device and in this repo once built.*

---

## Addendum (2026-10-22): decompile outcomes — concrete upgrade shelf

Two follow-up decompiles of `libs/libyandex_evil.so` (details: `vqe-decompile.md`, `spotter-decompile.md`)
turned two "opaque vendor" items into documented, optional upgrades on top of the sovereign MVP.
**The MVP plan is unchanged** (push-to-talk → webrtc-AEC3 on `[mic, HW-feedback]` → single mic → microWakeWord).

- **Wake word — Path A now exists.** The spotter engine is NOT locked in `maind`: `libyandex_evil.so`
  exports a standalone C API (`spotter_conf_read`/`spotter_create*`/`spotter_feed_data`/
  `spotter_get_next_event`), deps only libtflite/libiconv/glibc. A ~1–2-day on-device armhf C host can
  `dlopen` it and feed AEC'd 16 kHz int16 PCM → the **real Yandex «Алиса» spotter** (single-channel
  fallback models fit a mono stream). Run it as an **A/B against microWakeWord** (Yandex's model saw far
  more real far-field data than a TTS-trained MWW). Caveats: proprietary blob stays on device (harmless
  under our egress lock), standalone thresholds false-accept more (tuned assuming cloud re-validation +
  2nd-stage gates), models use a Yandex "ynmt" runtime (stock Kaldi/Vosk can't load them). microWakeWord
  stays the sovereign default; Path B (clean-room reimpl) only if Path A proves worth removing the blob.
- **Front-end — now a known small spec.** `bergamot` does **no beamforming** (code throws "not
  implemented" for its mic geometry) — so our single-mic + AEC3 plan is exactly what Yandex ships, not a
  shortcut. Recovered numbers to seed the AEC3 config: HW-feedback reference delay ≈ `hwStaticShift=94`
  samples, short tail (~32 ms), HPF ~178 Hz, **×50 (~+34 dB) gain on the ASR-mode omni channel** (feed
  MWW a strongly amplified stream). Pick the healthiest mic by RMS.
- **Noise reduction — optional GTCRN drop-in.** The shipped `vqe_models/noise_reducer/model_call.nnet`
  is a standard **GTCRN** streaming TFLite (stock ops, I/O `[1,1,4,129]` log-mag mic+ref → `[1,1,2,129]`
  mask, 16 state tensors) — reusable standalone as a post-AEC mask (Yandex only ran it in call mode).
  Alternative to WebRTC NS if we want stronger suppression later.

Sequencing: ship MVP first; then, as independent experiments, (1) Path A A/B for wake-word far-field,
(2) GTCRN post-filter for NR — each a single controlled variable.
