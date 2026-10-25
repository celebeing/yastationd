# `yastationd` — implementation spec (Phase 1 MVP)

Focused build spec for the on-device daemon. Big-picture rationale + later phases live in
[hass-integration.md](hass-integration.md); hardware facts in [device-teardown.md](device-teardown.md)
and [wakeword-mic-array.md](wakeword-mic-array.md).

## 0. Scope
One static daemon on the speaker (`root@<speaker-ip>`) that makes it a normal offline HASS device over
**MQTT + MQTT Discovery**. **Phase 1 (this spec):** LED ring, optional rear ambilight,
front clock matrix, volume, mic-mute state, local button reflexes, WAV/announcement
playback, health + watchdog. **Explicit non-goals here (Phase 2+):** voice / Wyoming
satellite / wake word, beamforming, GTCRN NR. No cloud, no internet.

## 1. Build & deploy
- **Go, static: `CGO_ENABLED=0 GOARCH=arm GOARM=7 go build`.** Static sidesteps the device's armhf
  glibc-2.30 coupling and the arm64-kernel/armhf-userland split (same trick as the static dropbear).
- Cross-build on the dev host; stage the binary to `/bkupgrade/cfw/bin/yastationd` (writable, persistent).
- Shell out to `aplay`/`arecord`/`amixer` (present) — do NOT link armhf libasound.
- Config: `/bkupgrade/cfw/yastationd.conf` (broker host/port/user/pass, device-id, default brightness, topic prefix).
- Autostart: `/etc/init.d/S52zyastationd` (see §6). Supervisor respawn loop like the existing hooks. rootfs edit needs `mount -o remount,rw /` … `sync; mount -o remount,ro /`.

## 2. Device interfaces (verified constants)
### LED ring — 6 RGB px
`/sys/class/leds/bergamot:{red,green,blue}:logo{0..5}/brightness` (0–255). **Set `trigger`=`none` once**
(`echo none > …/trigger`; default is `ledfb-logo`) before writing brightness. (Also a 6×1 24bpp
framebuffer `/dev/fb0` if you prefer one write.)
### Front clock matrix — 7-seg × 4 digits
`/sys/class/leds/bergamot:white:clock{A..G}{0..3}/brightness` + `clock{DH,DL,DM,DEGREE}`. **Verified map:**
position 0 = leftmost; standard 7-seg A=top,B=TR,C=BR,D=bottom,E=BL,F=TL,G=mid; colon = `DH`+`DL`.
Digit→segments: 0=ABCDEF 1=BC 2=ABDEG 3=ABCDG 4=BCFG 5=ACDFG 6=ACDEFG 7=ABC 8=ABCDEFG 9=ABCDFG.
**Flicker-free rule: write a segment only if its value changed** (read current, compare) — no blank-all pass.
Brightness source `/bkupgrade/cfw/clock_brightness` (currently 128). Reference impl: `clock.sh`.

### Orion front screen — 21×12 fbdev dot matrix
`/dev/fb1`, name `YNDX LED screen`, gowin FPGA. `matrix.kind = dotmatrix-fb` discovers it
by name (or `matrix.dev` pins the path) and reads geometry, row stride and RGB byte
offsets via fbdev ioctls — no hard-coded packing. The panel latches one write behind:
the 15 Hz display loop continuously re-sends the current frame, making it stable with no
visible flash; shutdown writes blank then flushes once. The front stack's layers come from
a per-display catalogue (`display.Front`, [display-expressions.md](display-expressions.md)
"Front displays"): animations are the firmware's own screen patterns, while clock,
countdown, messages and marquee are still the seg7 picture drawn as a compact 3×5 font
doubled vertically to 3×10 (the fallback until they get pixel drawings).

### Orion rear ambilight — 12 RGBW positions
`orion:{red,green,blue,white}:ambilight{i}{a,b}` exposes two co-located dies for each
logical position, ordered top-to-bottom. `ambilight.kind = sysfs-rgbw` takes every
channel off its `ledfb-ambilight` trigger and writes both dies identically. The strip is
an independent persisted HA MQTT `light` with brightness and `rgbw` color mode; it does
not participate in ring voice/status overlays.

### Buttons
Profiles map stable kernel input-device names and event codes rather than probe-order `eventN`
paths. Bergamot's `gpio-keys`/`aw933xx` expose ordinary mute, volume and voice keys. Orion's
`aw933xx` is a circular capacitive panel: the center emits `KEY_VOICECOMMAND`, motion inside it
emits a user-assignable `panel_gesture`, and the outer groove emits a wrapped `ABS_X` angle that
the daemon converts to stock 5% volume levels. The physical mic-mute switch remains authoritative.
`input_event` records may have 16- or 24-byte timeval layouts; the reader detects either.
`hardvold` already applies the mute to the ALSA mixer — do NOT fight it; just reflect the state.

### Audio
Playback: `aplay -D default` — the stock `/etc/asound.conf` chain plug→dmix→`hw:0,0` (48 kHz stereo).
**Not `plughw:0,0`**: on-device it played a mono 48 kHz WAV at ~2x speed. Mixer `amixer -c0`:
`Master Playback Volume`(0–255), `SPK mute`, `main Acm8625 AMP ChL/ChR Volume`(0–511), `AMP Post Gain`(0–31),
`AMP mute status`. `Master` is a user control created by **`hardvold`**, which maps it onto AMP ChL/ChR
along a linear-dB curve −55 dB (0) … +5 dB (255) (`/etc/hardvold.config`) — so it works for any PCM.
**Unmute recipe (verified by ear, 2026-10-22): `cset name='main Acm8625 AMP mute status' 0`.** The ACM8625
driver ORs that sticky flag into its per-stream speaker on/off (dmesg `acm8625_spk_onoff_work`: stream
start → `mute=flag`, stop → `mute=2|flag`); 1 silences everything, 0 lets streams through, and it persists
across streams and idle. **Readback lies** — `cget` of `AMP mute status`/`SPK mute` doesn't reflect the
write; judge by ear/dmesg. `SPK mute` has no audible effect. Master 35% is clearly audible in a room.
Mic (`hw:0,1`) is Phase 2, not needed here.

## 3. MQTT + Discovery (entities)
Connect to the HASS broker (Phase-0 prereq: stand up Mosquitto on `<ha-host>`). Publish Home
Assistant MQTT-Discovery configs under `homeassistant/<component>/yastation_<id>/<obj>/config`; state/cmd
under `yastation/<id>/…`. Availability via **LWT** (`.../availability` online/offline).
MVP entities: `light` (ring: on/off/brightness/rgb), optional `light` (rear ambilight:
on/off/brightness/rgbw), `select` `display_mode` (default **clock**), `text` `message`
(matrix text/number), and `number` `volume` (`amixer Master`). **No `media_player`** —
HA has no MQTT media_player platform (it silently ignores the config); announcements/TTS
arrive via the **Wyoming satellite** role instead (`assist_satellite.announce`, see
yastationd/README "Audio from HA") · `binary_sensor` `mic_mute` (from event0) ·
`sensor`s (uptime/health; `lux`+auto-bright later) · a
`yastation/<id>/display/cmd` JSON topic for one-shot HASS overlays (see §4).

## 4. Display layer-stack (core model)
Each surface (matrix, ring) is a **priority-ordered stack of layers with per-layer TTL + auto-revert**:
- matrix base = **clock**; ring base = **HASS light state**.
- overlays (higher priority, short TTL): volume bubble, mute indicator, listening/thinking/speaking/
  error/success effects. Highest active layer wins; on TTL expiry it pops and the surface reverts.
- Two drivers: MQTT entities/overlay-topic (HASS) **and** local reflexes that work with no broker
  (volume button → bubble; mute → indicator). Arbitration = the priority order (local reflexes and HASS
  overlays share the stack).
The rear ambilight is intentionally outside this stack: it is a stable ambient light, not a
status surface, so ring/matrix overlays never replace the user's ambilight state.

## 5. Watchdog / health (MVP slice)
Supervisor respawn (init.d loop). On-device liveness: broker reachable? → `binary_sensor pipeline_ok`.
MQTT reconnect with backoff + re-publish discovery. Local **degraded mode** indicator (ring/matrix
pattern) when the broker is down. (Full self-announce via the pre-rendered voice bank is Phase 2 — see
hass-integration.md §2.5–2.7.)

## 6. Coexistence / cutover
- **The matrix has ONE writer: yastationd.** A clean device has no other clock. (`S50clockd`/`clockd.sh`
  on our unit is a throwaway from the exploration phase — retire it on that device, don't design
  around it.) If yastationd dies, the supervisor respawns it; `/bkupgrade/cfw/yastationd_off` disables it.
- Leave `hardvold` (mute→mixer) and `bluealsa` alone.
- **Clock sync (`internal/timesync`).** fw 0.369 runs `chronyd` (the install points
  `/etc/chrony.conf` at the LAN server); fw 0.6.x has no chronyd, and its vendor `ntp_sync.sh` polls
  `*.ru.pool.ntp.org`, which the egress lock blocks, so the clock never syncs (unit #2 drifted 6 min).
  yastationd therefore carries a minimal SNTP client: if `chronyd`/`ntpd` runs it does nothing;
  otherwise it tries `ntp_server` (config) → the servers in `chrony_conf` → the default gateway, steps
  the clock when off by >200 ms, re-syncs every 15 min (retry 30 s → 10 min). Until synced the clock's
  colon blinks. DHCP option 42 was rejected: few home routers send it, dhcpcd here doesn't request it,
  and using it means editing `dhcpcd.conf` (network path — see the safety doctrine).
  Local time comes from `tz` in the config.

## 7. Open items before/at implementation
1. ~~ALSA clean-unmute recipe~~ — resolved on-device (§2 Audio).
2. ~~Mosquitto broker~~ — HA's Mosquitto on <ha-host>:1883, anonymous.
3. ~~Confirm `input_event` record size~~ — autodetect works on-device (both evdev nodes).
4. Go armv7 static cross-toolchain on the dev host; stage artifacts over the LAN (no egress-open needed).
