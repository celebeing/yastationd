# yastationd

On-device Home Assistant agent for the de-Yandexed **Yandex Station Mini 3** (`bergamot`). It turns
the speaker into a normal offline MQTT device: LED ring, front 7-segment clock, volume, mic-mute,
button reflexes, announcements and a health/watchdog layer. No cloud, no internet.

This is **Phase 1** of [`docs/yastationd-design.md`](../docs/yastationd-design.md). Voice / Wyoming
satellite, beamforming and the on-device command recogniser are later phases.

## Layout

```
cmd/yastationd        main: flags, config, signal handling, tzdata embed
cmd/ringdemo          dev tool: cycles the ring effects on the real LEDs (stop yastationd first; -board)
internal/board        board profiles: per-model hardware description + device-tree detection
internal/config       <cfw>/yastationd.conf parser (<cfw> = /bkupgrade/cfw, or /data/cfw on pickle)
internal/hw           sysfs LEDs (ring, matrix), evdev input, amixer/aplay, IIO lux
internal/display      per-surface priority layer stack + clock/text/ring effects
internal/mqtt         paho client (LWT, backoff), topics, HA MQTT-Discovery
internal/health       liveness state machine (TCP probes + heartbeat)
internal/app          wiring: render loop, command handlers, evdev reflexes
configs/              yastationd.conf.example
init.d/               S52zyastationd supervisor hook
```

## Board profiles (multi-model)

All model-specific hardware — LED ring (pixel count, LED names), front display, input devices and
key codes, mixer controls, mic-array layout, vendor VQE preset and spotter models — is a **board
profile** (`internal/board/profiles/<codename>.conf`, embedded). The device is matched by its
device-tree `compatible` (`yandex,<codename>`); `board = <codename>` forces one, and
`<cfw>/board.conf` overrides single keys without a rebuild. Effects size themselves from
the ring's pixel count. Bringing up a new station: [`../docs/boards/README.md`](../docs/boards/README.md)
(survey tool, procedure, reversing checklist). Supported: `bergamot` (Station Mini 3), `pickle` (Station Mini 3 Pro).

`<cfw>` is the CFW root on the device: `/bkupgrade/cfw` where `/bkupgrade` exists, else `/data/cfw`
(pickle has no `/bkupgrade` and must not get one — its `S40wifi` would then look for the Wi-Fi config
under `/bkupgrade/data`). The daemon, the init hook and the `run.sh` gate all use that rule.

## Build

Static, CGO-free, armv7 (hard-float userland on a 64-bit kernel — see design §1). The same binary
runs on pickle's aarch64 userland (its kernel has 32-bit EL0 support):

```
make build     # -> build/yastationd (ELF 32-bit ARM, static)
make test      # host unit tests (config, display stack, matrix glyphs, evdev decode, mixer parse)
make vet
```

## Deploy

Full guide (first install, HA side, updates, removal, checks): [`../docs/deploying.md`](../docs/deploying.md).
Device address, ssh key and `DESTDIR` (the device's `<cfw>`) come from `local.mk` (gitignored, see
`local.mk.example`) or the command line.

```
make stage-tmp       # throwaway binary in /tmp for manual testing (gone on reboot)
make stage           # first install: binary + init hook copy -> $(DEVICE):$(DESTDIR), no autostart
make install-initd   # copy the init hook into /etc/init.d (remounts / rw then ro)
make deploy          # update a running install: upload aside, stop -> swap -> start, tail the log
make deploy-spotter  # install a spotter-host build (SPOTTER=path)
make sounds          # decode the vendor voice lines into $(DESTDIR)/sounds/*.wav
```

Several speakers at once: `../tools/deploy.sh` (hosts list in `../tools/deploy.hosts`). The init
hook runs a respawn supervisor; `touch <cfw>/yastationd_off` disables it without a rootfs write.

## Entities (HA MQTT Discovery)

`light` ring (with `effect_list`: none/pulse/comet/blink/rainbow + voice presets) · `select` display_mode
(clock/off/message; long messages scroll) · `text` display_message · `number` volume ·
`notify` show_text + `number` text_duration (s) · `select` ring_animation + `number` animation_duration (s) ·
`binary_sensor` mic_mute · `sensor` lux/health/uptime · `binary_sensor` pipeline_ok ·
`event` button (volume_up/down, voice_command).

Timed overlays (show_text, ring_animation, display/cmd) light the ring/matrix over whatever is
underneath — even with the ring light off — and fall back when the time is up, without touching the
light's state. `ring_animation` returns to `off` (not "none": HA's MQTT select reads "None" as unknown).

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

Installed and autostarting on the device since 2026-10-22 (see the top-level README).
