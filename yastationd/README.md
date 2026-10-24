# yastationd

On-device Home Assistant agent for de-Yandexed Yandex Stations. It turns the speaker into a
normal offline MQTT device: LED ring, front clock/display, volume, mic-mute, button reflexes,
announcements and a health/watchdog layer. No cloud, no internet.

This is **Phase 1** of [`docs/yastationd-design.md`](../docs/yastationd-design.md). Voice / Wyoming
satellite, beamforming and the on-device command recogniser are later phases.

## Layout

```
cmd/yastationd        main: flags, config, signal handling, tzdata embed
cmd/ringdemo          dev tool: real ring effects (make build-ringdemo / make stage-ringdemo; stop yastationd first)
cmd/fbleddemo         dev tool: front-screen `-clock`/`-uwu` smoke modes (make build-fbleddemo / make stage-fbleddemo; stop yastationd first)
internal/board        board profiles: hardware, CFW root, helper architecture, device-tree detection
internal/config       <cfw>/yastationd.conf parser (<cfw> = /bkupgrade/cfw on bergamot, /data/cfw on pickle/orion)
internal/hw           sysfs LEDs (ring, matrix), evdev input, amixer/aplay, IIO lux
internal/display      per-surface priority layer stack + clock/text/ring effects
internal/mqtt         paho client (LWT, backoff), topics, HA MQTT-Discovery
internal/health       liveness state machine (TCP probes + heartbeat)
internal/sendspin     supervises sendspin-host (Music Assistant player) and relays its events
internal/app          wiring: render loop, command handlers, evdev reflexes
configs/              generic and Orion-specific yastationd.conf examples
init.d/               S52zyastationd boot hook + supervise.sh (backoff, .prev rollback, log rotation)
```

## Board profiles (multi-model)

All model-specific hardware and deployment facts — CFW root, native helper architecture, LED
ring, front display, input devices and key codes, mixer controls, mic-array layout, vendor VQE
preset and spotter models — live in a **board profile**
(`internal/board/profiles/<codename>.conf`, embedded). The device is matched by its device-tree
`compatible` (`yandex,<codename>`); `board = <codename>` forces one, and `<cfw>/board.conf`
overrides runtime keys without a rebuild. Effects size themselves from the ring's pixel count.
Bringing up a new station: [`../docs/boards/README.md`](../docs/boards/README.md) (survey tool,
procedure, reversing checklist). Supported: `bergamot` (Station Mini 3), `pickle` (Station Mini 3
Pro), `orion` (Station 3 — sound, ring and 21×12 front screen verified on-device).

`<cfw>` is `/bkupgrade/cfw` on bergamot and `/data/cfw` on pickle/orion. The profile records this
for host deployment; the daemon and init hook retain filesystem detection as their bootstrap
fallback.

## Build

Static, CGO-free, armv7 (hard-float userland on a 64-bit kernel — see design §1). The same binary
runs on pickle's aarch64 userland (its kernel has 32-bit EL0 support):

```
make build     # -> build/yastationd (ELF 32-bit ARM, static)
make test      # host unit tests (config, display stack, matrix glyphs, evdev decode, mixer parse)
make vet
```

## Deploy

Full guide (first install, HA side, updates, removal, checks):
[`../docs/deploying.md`](../docs/deploying.md). Device address, SSH key and `PLATFORM` come from
`local.mk` (gitignored; see `local.mk.example`) or the command line. The platform profile supplies
`DESTDIR` and `HELPER_ARCH`; either can still be overridden explicitly.
For Orion bring-up, copy `configs/orion.conf.example` to a gitignored local file, replace its
placeholders, and install it as `/data/cfw/yastationd.conf`.

```
make platform-info   # show selected platform, CFW root and native helper architecture
make stage-tmp       # throwaway yastationd in /tmp for manual testing (gone on reboot)
make build-ringdemo  # -> build/ringdemo (ring dev tool; not part of stage/deploy)
make stage-ringdemo  # throwaway ringdemo in /tmp — cycles the ring effects on the real LEDs
make build-fbleddemo # -> build/fbleddemo (front-screen dev tool; not part of stage/deploy)
make stage-fbleddemo # throwaway fbleddemo in /tmp — shows the clock or UwU
make stage           # first install: binary + init hook copy -> $(DEVICE):$(DESTDIR), no autostart
make install-initd   # copy the init hook into /etc/init.d (remounts / rw then ro)
make deploy          # update a running install: upload aside, stop -> swap -> start, tail the log
make deploy-spotter  # install a spotter-host build (SPOTTER=path)
make sounds          # decode the vendor voice lines into $(DESTDIR)/sounds/*.wav
```

Several speakers at once: `../tools/deploy.sh` (hosts list in `../tools/deploy.hosts`). The init
hook runs `<cfw>/init.d/supervise.sh`: restarts with a backoff, rolls back to `bin/yastationd.prev`
(kept by every deploy) after a crash loop, rotates the log at 1 MB. `touch <cfw>/yastationd_off`
disables it without a rootfs write.

## TTS playback (Wyoming)

HA's audio chunks go through a jitter buffer before aplay (`internal/app/satellite.go`, `pump`).
Playback starts once 0.5 s is queued, the stream ends, or 2 s pass. When the written audio runs out
mid-stream, the pump holds again until 0.5 s is queued. A Wi-Fi stall or a TTS that pauses between
sentences then gives one pause rather than a stutter (aplay underruns every few tens of ms
otherwise, seen on-device). The ring follows what is actually heard: a green pulse while audio
plays, the white "thinking" comet while it waits for audio, and nothing once aplay is gone (a
«стоп», or a newer stream), even if HA is still sending. `wyoming: audio stop` logs `wall_secs`,
`max_gap_ms` and `rebuffers` to tell slow delivery from a playback problem.

## Entities (HA MQTT Discovery)

`light` ring (with `effect_list`: none/pulse/comet/blink/rainbow + voice presets) · optional
`light` ambilight (rear RGBW strip, on boards that declare one) · `select` display_mode
(clock/off/message; long messages scroll) · `text` display_message · `number` volume ·
`notify` show_text + `number` text_duration (s) · `select` ring_animation + `number`
animation_duration (s) · `binary_sensor` mic_mute · `sensor` lux/health/uptime ·
`binary_sensor` pipeline_ok · `event` button (volume_up/down, voice_command, panel_gesture) ·
`select` expression (attributes: the display capabilities) · `switch` display_faces (seg7 fronts).

Timed overlays (show_text, ring_animation, display/cmd) light the ring/matrix over whatever is
underneath — even with the ring light off — and fall back when the time is up, without touching the
light's state. `ring_animation` returns to `off` (not "none": HA's MQTT select reads "None" as unknown).
The ambilight is an independent persisted RGBW light. Ring/status overlays do not take it over.

### Expressions and scenarios

Design: [`../docs/display-expressions.md`](../docs/display-expressions.md) (`internal/app/express*.go`,
`scenario.go`, `internal/display/clip.go`). Topics under `yastation/<id>/`:

| Topic | Payload |
|---|---|
| `expression/set` | the `Expression` select: one name, shown for `animation_duration`, then `off` |
| `express/set` | a name, or `{"name": "happy", "ttl_ms": 5000}`; `off` clears |
| `scenario/set` | a name (built-in `hello`, `celebrate`, `goodnight`, `startup`, or `<cfw>/scenarios/<name>.json`), an inline `{"steps": [...]}`, or `{"stop": true}` |
| `display/cmd` | also `{"surface":"matrix","op":"clip","args":{"name":"ringing","loop":true},"ttl_ms":5000}` and `"op":"frames"` with `[{"seg":["ABFG","","","ABFG"],"ms":300}, {"text":"12:34","ms":500}]` |
| `display_caps/state` | what this speaker can show: front kind, ring size, expressions, clips, scenarios |

A scenario step is `{"express": …, "ring": <effect>, "matrix": <clip>, "text": …, "ms": …}`; the
fields of one step run in parallel, steps run in sequence. The front display runs through a
per-segment smoother: a layer on top fades in in 80 ms, a lower one returning in 400 ms, a plain
picture changing within a layer morphs in 120 ms, frames of one clip keep their own timing.

### HA-side scripts (Assist tools)

`script.yastation_play_animation(effect, seconds=10, color?)` and `script.yastation_show_text(text,
seconds=10)` publish display/cmd overlays; they're exposed to Assist so the voice agent can call them.
Source: [`../hass/scripts.json`](../hass/scripts.json) — created through HA's config API
(`POST /api/config/script/config/<id>`, i.e. stored like UI-made scripts, no package files), then the
entity ids renamed to `script.yastation_*` and exposed via the websocket API.

## Audio from HA: Wyoming satellite

HA has no MQTT `media_player`, so announcements/TTS come in over **Wyoming**: yastationd listens on
`wyoming_port` (10700) and HA's Wyoming integration (add host `<speaker-ip>`, port `10700`) creates an
`assist_satellite` entity. `assist_satellite.announce` with `message` (pipeline TTS) or `media_id` (any
URL ffmpeg can read) plays on the speaker; the ring pulses green meanwhile. Playback only for now —
mic/wake word are Phase 2. HA also opens a short describe-only connection every ~30 s; connections are
concurrent and a stream belongs to the connection that started it. Raw `yastation/<id>/speaker/set`
(local path / `STOP`) still plays local files for `mqtt.publish` automations.

One-shot overlays go to `yastation/<id>/display/cmd` as JSON
`{surface:"matrix"|"ring", op, args, ttl_ms, priority}` — e.g. `{"surface":"ring","op":"effect","args":{"name":"success"},"ttl_ms":800}`
or `{"surface":"matrix","op":"countdown","args":{"seconds":300}}`.

## Music Assistant player

With `sendspin = true`, the daemon runs `../sendspin-host` (`internal/sendspin`, `internal/app/music.go`):
it restarts it with backoff, ducks the music by `music_duck_db` while the assistant is busy (a
voice session, a reply pending, HA audio, our own playback, a ringing timer; back 1.5 s after),
and with `sendspin_volume = linked` maps the player volume in MA onto the speaker's volume and
reports local changes back. Volume key ticks are skipped while music plays. Design and findings:
[`../docs/music-sendspin.md`](../docs/music-sendspin.md).

## Local reflexes (work with the broker down)

- Volume keys → `amixer Master` ± and a transient 6-pixel level bar.
- Mic-mute switch → steady amber ring + `binary_sensor`.
- Broker/LAN down → health state machine drops to degraded/offline, shows a dim-red ring dot, keeps
  the clock and all local reflexes running, and re-publishes discovery + state on reconnect.

## Open items (from the design)

1. ~~ALSA unmute recipe~~ — **pinned on-device 2026-10-22**: `AMP mute status = 0` (built-in default),
   play via `default` (dmix). See design §2 Audio.
2. ~~Mosquitto broker~~ — exists: HA's Mosquitto on `<ha-host>:1883`, anonymous.
3. Remote-URL announcements aren't wired yet (no downloader); only local files play. The pre-rendered
   voice bank (Phase 2) will live under `/bkupgrade/cfw/voicebank/`.
4. The `input_event` record size is auto-detected at runtime (16 vs 24 bytes); verify on-device.
5. ~~Auto-brightness~~ — **done 2026-10-23**: the ltr308 is set to gain 18 / 0.4 s (stock reads
   ~2 counts indoors behind the grille), read every second, smoothed on log(lux) (EMA τ 4 s) and
   mapped through `auto_brightness_curve` (default `0.04:4, 0.55:128, 10:255`, calibrated on the
   black bergamot against a lux meter: 1 lx → 0.03, 37 lx → 0.55, office light → 80; 4 is the
   dimmest still readable in the dark, 128 comfortable in normal light), 8 % hysteresis. HA: switch
   `Auto brightness`, number `Clock brightness` (setting it turns auto off); `Illuminance` now
   publishes the smoothed reading. **Sensors differ a lot between units** (same daylight: 0.48 /
   10.8 / 18.7 on the three speakers) — set a per-unit `auto_brightness_curve` if a speaker
   doesn't dim at night; calibrate from its `Illuminance` history.
6. **Demo / self-test mode** (future): triggerable from HA and after first setup — cycles ring
   effects and matrix, plays test sounds, reacts distinctly to each button, so every function can be
   checked end-to-end. `cmd/ringdemo` is the seed of it.
7. **Wi-Fi sluggish after reboot (noted 2026-10-22, not investigated):** ping to the speaker jumped
   6–220 ms and a 6 MB ssh upload took minutes (it took seconds before the reboot); signal -54 dBm,
   CPU 76 % idle. Suspect the wifi chip's power save re-enabled at boot. Only look into it if it
   causes real problems (voice is unaffected so far).
8. **Multi-speaker timers:** HA's timers are bound to the device the request came from
   (`HassStartTimer` has no target slot), so a timer rings only on that speaker. Ringing on several
   rooms needs our own Assist tool (script → per-speaker MQTT countdown + local alarm).
9. ~~Power self-diagnosis~~ — **done 2026-10-23**: `ya_pd` (HUSB238) is read at start and every
   5 min (each read makes the driver re-query and log, so not more often) → HA `Supply voltage` +
   `Power supply problem` (no `Good` contract ≥ `power.min_mv`). On a problem the stock Alice line
   plays once per boot. It's opus on every firmware (pickle/0.6.x name the Ogg file `.wav`) and
   aplay can't decode it: `make sounds DEVICE=… DESTDIR=…` decodes it into `<cfw>/sounds/`.
10. ~~Clock wrong after power-on~~ — **fixed 2026-10-23**: with chronyd running the clock counts
    as synced only after our own SNTP query agrees (an offset > 5 s — a stale boot clock — is
    stepped right away instead of waiting for chronyd's slew, seen taking ~11 min on bergamot),
    or, with no server answering, while the kernel's `STA_UNSYNC` is clear. Until then the colon
    blinks. The display re-renders every frame, so the step shows at once.
11. **The wake word lands in the STT transcript** («Алиса, …»): the session streams `preroll_ms`
    (300 ms) of VQE audio from before the detection, and the detection fires late enough that the
    word is still in it. HA's built-in matcher then misses plain commands and they go to the slower
    LLM (see `docs/ha-assistant-setup.md` §8 for the HA-side workarounds). Fix here: start the stream
    at the spotter's end-of-phrase position (the event carries a stream offset) instead of
    `now - preroll`, keeping only a few tens of ms before it.

Installed and autostarting on the device since 2026-10-22 (see the top-level README).
