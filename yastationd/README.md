# yastationd

On-device Home Assistant agent for de-Yandexed Yandex Stations. It turns the speaker into a
normal offline MQTT device: LED ring, front clock/display, volume, mic-mute, button reflexes,
announcements and a health/watchdog layer. No cloud, no internet.

It started as Phase 1 of [`docs/yastationd-design.md`](../docs/yastationd-design.md) and has since
grown voice (the Wyoming satellite, the vendor wake word and commands through `spotter-host`),
Music Assistant playback, the intercom, wake-word arbitration, the web app and more — the
top-level [README](../README.md) is the feature list, [CHANGELOG](../CHANGELOG.md) the history.

## Layout

```
cmd/yastationd        main: flags, config, signal handling, tzdata embed
cmd/ringdemo          dev tool: real ring effects (make build-ringdemo / make stage-ringdemo; stop yastationd first)
cmd/fbleddemo         dev tool: front-screen `-clock`/`-uwu` smoke modes (make build-fbleddemo / make stage-fbleddemo; stop yastationd first)
cmd/packtool          host tool: zip a pack directory and check it as a speaker would (make pack PACK=<dir>; docs/asset-packs.md)
internal/board        board profiles: hardware, CFW root, helper architecture, device-tree detection
internal/config       <cfw>/yastationd.conf parser (<cfw> = /bkupgrade/cfw on bergamot/orion, /data/cfw on pickle)
internal/hw           sysfs LEDs (ring, matrix), evdev input, amixer/aplay, IIO lux
internal/display      per-surface priority layer stack + clock/text/ring effects
internal/mqtt         paho client (LWT, backoff), topics, HA MQTT-Discovery
internal/health       liveness state machine (TCP probes + heartbeat)
internal/wifisetup    Wi-Fi setup mode: hostapd handoff, DHCP/DNS, the setup page, wpa_supplicant.conf edits
internal/intercom     1-to-1 calls between speakers: signed signalling, AES-GCM RTP/L16 media, jitter buffer, voice phrases
internal/sendspin     supervises sendspin-host (Music Assistant player) and relays its events
internal/app          wiring: render loop, command handlers, evdev reflexes, the web server's API
internal/web          web login: PBKDF2 password hashes, bearer tokens, login throttling
internal/webui        the embedded web app (built from ../web into dist/app by make build; not committed)
internal/logship      logs to a LAN syslog server, spooled on flash during outages
internal/luxcal       light sensor calibration (reading:lux points, log-log)
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

`<cfw>` is `/bkupgrade/cfw` on bergamot and orion and `/data/cfw` on pickle. The profile records this
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

## Web UI

`http://<speaker-ip>/` — login **admin / yastation** until changed from the app (it writes
`web_user` and a `web_password` hash into the config). The app is embedded in the binary, so a
deploy of `yastationd` updates it; `make build` rebuilds it from `../web` when its source changed,
in the pinned builder container (needs docker). Port and toggles:
`http_addr` (default `:80`, `""` = off), `led_preview`. Details: [docs/web-ui.md](../docs/web-ui.md).

## Deploy

Full guide (first install, HA side, updates, removal, checks):
[`../docs/deploying.md`](../docs/deploying.md). Updating speakers, one or many:
`../tools/deploy.sh` (hosts list in `../tools/deploy.hosts`): it builds in docker, ships the
daemon and its helpers, and checks each speaker. The make targets below are the one-off install
steps and dev tools. Device address, SSH key and `PLATFORM` come from
`local.mk` (gitignored; see `local.mk.example`) or the command line. The platform profile supplies
`DESTDIR` and `HELPER_ARCH`; either can still be overridden explicitly.
For Orion bring-up, copy `configs/orion.conf.example` to a gitignored local file, replace its
placeholders, and install it as `/bkupgrade/cfw/yastationd.conf`.

```
make platform-info   # show selected platform, CFW root and native helper architecture
make stage-tmp       # throwaway yastationd in /tmp for manual testing (gone on reboot)
make build-ringdemo  # -> build/ringdemo (ring dev tool; not part of stage/deploy)
make stage-ringdemo  # throwaway ringdemo in /tmp — cycles the ring effects on the real LEDs
make build-fbleddemo # -> build/fbleddemo (front-screen dev tool; not part of stage/deploy)
make stage-fbleddemo # throwaway fbleddemo in /tmp — shows the clock or UwU
make stage           # first install: binary + init hook copy -> $(DEVICE):$(DESTDIR), no autostart
make install-initd   # copy the init hook into /etc/init.d (remounts / rw then ro)
make sounds          # decode the vendor voice lines into $(DESTDIR)/sounds/*.wav
```

The init hook runs `<cfw>/init.d/supervise.sh`: restarts with a backoff, rolls back to `bin/yastationd.prev`
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

Optional `light` ring (`ha_ring_light = true`; with `effect_list`: none/pulse/comet/blink/rainbow
+ voice presets) · optional `light` ambilight (rear RGBW strip, on boards that declare one) ·
`select` display_mode (clock/off/message, clock_date and video on pixel screens —
docs/video-mode.md; long messages scroll) · `text` display_message · `number` volume · `notify`
show_text · `binary_sensor` mic_mute (On/Off) · `sensor` lux (calibrated lux, the raw `reading` as
an attribute)/health/uptime/version · `sensor` sound_floor/sound_level (dB SPL with a mic
calibration, else dBFS)/sound_activity (%) · `sensor` supply_voltage + `binary_sensor`
power_problem · `switch` auto_brightness + `number` clock_brightness · `binary_sensor` pipeline_ok ·
`event` button (volume_up/down, voice_command, panel_gesture, wake_word) · with `intercom = true`:
`sensor` intercom (idle/calling/ringing/in_call; attributes peer, ended) · `select`
intercom_target (the other speakers) · `button` intercom_call (also answers) · `button`
intercom_hangup ([docs/intercom.md](../docs/intercom.md)) · with `voice_id = true`: `sensor` voice
(a named person, guest or unknown; the verdict as attributes) · `sensor` voice_gender ·
`binary_sensor` voice_known · `switch` voice_ignore_guests ([docs/voice-id.md](../docs/voice-id.md)).

**Not on HA's device card** (2026-10-28): the speaker's own settings — `text_duration`,
`animation_duration`, `follow_up` — are in the web app only (still entities of its API;
`mqtt.WebOnly`), and the `ring_animation` and `expression` selects are gone (the LED page plays
both). The Display faces switch is gone (v0.21): what the front display shows is the theme's
([asset-packs.md](../docs/asset-packs.md)). Their MQTT topics stay for automations: `<prefix>/<id>/ring_animation/set` (an
effect name, plays `animation_duration` seconds), `expression/set`, `express`, `ring/set`, and the
capabilities in `display_caps/state`. Discovery publishes an empty config for each, so HA drops
the entities it had.

Timed overlays (show_text, ring_animation, display/cmd) light the ring/matrix over whatever is
underneath — even with the ring light off — and fall back when the time is up, without touching the
light's state. `ring_animation` returns to `off` (not "none": HA's MQTT select reads "None" as unknown).
The ambilight is an independent persisted RGBW light. Ring/status overlays do not take it over.
LED frames streamed over DDP (UDP `ddp_port`, 4048; LedFx, WLED tools) take it over while they
come; without a stream, music effects draw it while music plays (`switch` ambilight_fx, `select`
ambilight_fx_effect, `number` ambilight_fx_brightness)
([`../docs/ambilight-stream.md`](../docs/ambilight-stream.md)).

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
URL ffmpeg can read) plays on the speaker; the ring pulses green meanwhile. The same satellite
carries the voice: the wake word or the voice key streams the mic into the Assist pipeline. HA
also opens a short describe-only connection every ~30 s; connections are concurrent and a stream
belongs to the connection that started it. Raw `yastation/<id>/speaker/set`
(local path / `STOP`) still plays local files for `mqtt.publish` automations.

One-shot overlays go to `yastation/<id>/display/cmd` as JSON
`{surface:"matrix"|"ring", op, args, ttl_ms, priority}` — e.g. `{"surface":"ring","op":"effect","args":{"name":"success"},"ttl_ms":800}`
or `{"surface":"matrix","op":"countdown","args":{"seconds":300}}`.

## Music Assistant player

With `sendspin = true`, the daemon runs `../sendspin-host` (`internal/sendspin`, `internal/app/music.go`):
it restarts it with backoff, ducks the music by `music_duck_db` while the assistant is busy (a
voice session, a reply pending, HA audio, our own playback, a ringing timer; back 1.5 s after),
and with `sendspin_volume = linked` maps the player volume in MA onto the speaker's volume and
reports local changes back. Volume key ticks are skipped while music plays. On the front display
(`internal/app/musicdisplay.go`): a new track's title, pause/resume animations, and on pixel
screens the spectrum under the clock (`music_title`, `music_clips`, `music_levels`). Design and
findings: [`../docs/music-sendspin.md`](../docs/music-sendspin.md).

## Local reflexes (work with the broker down)

- Volume keys → `amixer Master` ± and a transient 6-pixel level bar.
- Mic-mute switch → steady amber ring + `binary_sensor`.
- Mic muted + the voice key held 5 s (the ring fills with yellow) → Wi-Fi setup mode: the speaker
  becomes an open access point
  with a page for its saved networks (yellow comet, `O  O°`); the same again, the page or a reboot
  leaves it. See [docs/wifi-setup.md](../docs/wifi-setup.md).
- Broker/LAN down → health state machine drops to degraded/offline, shows a dim-red ring dot, keeps
  the clock and all local reflexes running, and re-publishes discovery + state on reconnect.

## Open items (from the design)

1. ~~ALSA unmute recipe~~ — **pinned on-device 2026-10-22**: `AMP mute status = 0` (built-in default),
   play via `default` (dmix). See design §2 Audio.
2. ~~Mosquitto broker~~ — exists: HA's Mosquitto on `<ha-host>:1883`, anonymous.
3. The raw `speaker/set` topic plays local files only (no downloader); a remote URL is logged and
   dropped. Remote media goes through the Wyoming satellite instead (`assist_satellite.announce`,
   HA converts it).
4. The `input_event` record size is auto-detected at runtime (16 vs 24 bytes); verify on-device.
5. ~~Auto-brightness~~ — **done 2026-10-23**: the ltr308 is set to gain 18 / 0.4 s (stock reads
   ~2 counts indoors behind the grille), read every second, smoothed on log(lux) (EMA τ 4 s) and
   mapped through `auto_brightness_curve` (default `0.04:4, 0.55:128, 10:255`, calibrated on the
   black bergamot against a lux meter: 1 lx → 0.03, 37 lx → 0.55, office light → 80; 4 is the
   dimmest still readable in the dark, 128 comfortable in normal light), 8 % hysteresis. HA: switch
   `Auto brightness`, number `Clock brightness` (the fixed level, and since 2026-10-28 the brightest
   auto goes; setting it no longer turns auto off); `Illuminance` now
   publishes the smoothed reading. **Sensors differ a lot between units** (same daylight: 0.48 /
   10.8 / 18.7 on the three speakers) — set a per-unit `auto_brightness_curve` if a speaker
   doesn't dim at night; calibrate from its `Illuminance` history.
   **Smoother since 2026-10-26:** a 5-reading median drops spikes (a hand over the sensor), the
   EMA is τ 10 s, the curve interpolates log(level) on log(lux) (even for the eye between its
   points), and the shown level glides to the target in log space (τ 1 s, no faster than a
   doubling per 1.5 s) instead of jumping; the hysteresis is 8 % (with a whole step only where
   levels are whole steps). The curve's darkest point defaults to the board's
   `matrix.min_brightness` (bergamot, pickle 4; orion 2), which also floors the manual level.
   **Range since 2026-10-28:** auto runs from the dimmest (`auto_brightness_min`, else the board's
   minimum) to the clock brightness: the curve's darkest level maps to the first, 255 to the second.
   A state file from before with auto on gets clock brightness 255, so nothing changes until it is
   set.
   On orion the level goes to the screen's backlight (`matrix.backlight`), with the frame at
   2/3..full scale making up the fraction: a dimmed frame lost its half-tones at the dark end,
   a dim backlight keeps them (A/B on glass).
   **Calibrated in lux since 2026-10-26** ([sensor-calibration.md](../docs/sensor-calibration.md)):
   the reading goes through the board's `lux.calibration` or the unit's `lux_calibration`;
   `Illuminance` is lux (the raw `reading` and the calibration as attributes) and
   `auto_brightness_curve` is in lux (default `0.7:<min>, 9:128, 150:255`).
6. **Demo / self-test mode** (future): triggerable from HA and after first setup — cycles ring
   effects and matrix, plays test sounds, reacts distinctly to each button, so every function can be
   checked end-to-end. `cmd/ringdemo` is the seed of it.
7. **Wi-Fi sluggish after reboot (noted 2026-10-22, not investigated):** ping to the speaker jumped
   6–220 ms and a 6 MB ssh upload took minutes (it took seconds before the reboot); signal -54 dBm,
   CPU 76 % idle. Suspect the wifi chip's power save re-enabled at boot. Only look into it if it
   causes real problems (voice is unaffected so far).
   **A Wi-Fi hang, 2026-10-26** (a bergamot, 14 h after boot, v0.8.3): the speaker vanished from
   the LAN (no ping, no ARP reply, MQTT's last will in HA) and stayed so until it was power-cycled.
   The router's view: the signal fell from -51 to -59 dBm a few minutes before (something moved
   near it); then over ~4 min both rates fell from 72/21 Mbps to the 1 Mbps floor and the byte
   counters stopped, while the station stayed associated for 2 h more. busybox `watchdog -T 5`
   on `/dev/watchdog` did not reboot it, so the kernel and userland were alive: the Wi-Fi
   (bcmdhd, `dhd` in dmesg) stopped passing data. Logs are lost (syslog and the daemon's log
   are on tmpfs). If it repeats: a LAN self-check (gateway unreachable for minutes → reload the
   Wi-Fi interface, never touching wpa_supplicant.conf), and a log kept across reboots.
   It hung again the same afternoon (the rates fell to 1 Mbps over a few minutes, still
   associated, signal fine). **Since then:** logs go to a LAN syslog server with a flash spool
   while it is unreachable (`log_remote`), and a network watchdog resets the Wi-Fi controller
   (the firmware's own `yandex-wifireset`) after 4 min without the gateway and reboots after 10,
   backing off after each such reboot (`net_watchdog_min`; [storage.md](../docs/storage.md)).
   The black Mini's chip hung the same way on 2026-10-27 ("fw recovery not finish").
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
11. **The wake word in the STT transcript** («Алиса, …») — fixed 2026-10-28: the stream starts
    `preroll_ms` before the WAKE's own sample in vqe_0 (spotter-host's `AT` line), not before "now",
    and the value is the board's, measured from real recordings (bergamot 150 ms, orion 220; pickle 300 until
    measured). `docs/wake-word-boundary.md`.
12. **Room sound sensors — added 2026-10-26** (the inputs for "someone is asleep here",
    docs/wishlist.md): `Sound floor`, `Sound level` (dBFS) and `Sound activity` (%), once a
    minute from the raw voice mic channel (before the VQE and its AGC; 150 Hz high-pass, 20 ms
    blocks, short enough for a click or a key press to lift one): the floor is the 10th
    percentile of the minute's blocks, the level their energy average, the activity the share
    more than 6 dB above the floor. Blocks while the speaker plays or the mic is muted are left
    out; a minute that is mostly those reads unknown. Values are re-sent only on a change (1 dB,
    1 %) or every 15 min. Only levels leave the speaker. With a `mic.spl_offset` (the board's; from
    a watch's noise meter, rough) or `mic_spl_offset` (the unit's) the levels are dB SPL
    ([sensor-calibration.md](../docs/sensor-calibration.md)). `room_sound = false` turns them off
    (they keep the mic open even with the wake word off). `Illuminance` is now sent on a 10 %
    change or every 15 min.

Installed and autostarting on the device since 2026-10-22 (see the top-level README).
