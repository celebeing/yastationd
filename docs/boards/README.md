# Boards — bringing up a new Yandex Station model

yastationd is model-independent; everything model-specific is a **board profile**
(`yastationd/internal/board/profiles/<codename>.conf`, embedded in the binary) plus this folder's
per-board notes (`<codename>.md`, from [`_template.md`](_template.md)). The device picks its profile
from the device-tree `compatible` list (`yandex,<codename>`); `board = <codename>` in
`yastationd.conf` forces one, and `<cfw>/board.conf` on the device (`/bkupgrade/cfw` or `/data/cfw`) overrides single keys
(handy while reversing a new model — no rebuild).

| Board | Model | Status |
|---|---|---|
| [bergamot](bergamot.md) | Station Mini 3 | supported, verified on-device |
| [midi (`yandexmidi`)](midi.md) | Station Midi | profiled from firmware `0.369.1.50` (no device yet) |
| [orion](orion.md) | Station 3 | sound + ring + 21×12 front-screen verified on-device; rear RGBW ambilight entity implemented, daemon smoke pending; wake pending |

Known vendor codenames (the VQE presets compiled into `libyandex_evil.so`) — from the
0.369 armhf lib via `strings`, **plus** the 3.21 aarch64 lib where the names are built from
immediates (`mov`/`movk`) and invisible to `strings`: `bergamot`, `cucumber`, `plum`,
`yandexmini`/`mini43`/`mini50`/`mini55`/`mini65`, `yandexmidi`, `pickle`, `orion`, `nebula`,
`potato`, `mango` (+ `rev1..rev5` and 16/48 kHz variants). A model whose codename is in that
list very likely needs **no audio-front-end reversing** — just `wake.vqe_preset = <codename>` and
its own channel layout. (bergamot's sysfs also has a `cucumber:sync:clock-0` LED, i.e. the
kernel/DT is shared across models.)

## Procedure for a new station

Prerequisite: SSH access to the speaker as root (how it is obtained is out of scope for this repo).
The full agent-oriented guide is [`../porting-a-speaker.md`](../porting-a-speaker.md).

1. **Survey (read-only):**
   `ssh root@<speaker> 'sh -s' < tools/board-survey.sh > dumps/survey/<codename>-<date>.txt` and
   `ssh root@<speaker> 'cd /system/vendor/quasar && tar cf - ledconfigs ledpatterns' > dumps/survey/<codename>-leds.tar`.
   Output has per-device identifiers — it stays in the gitignored `dumps/`.
2. **Write the profile** from the survey (copy `bergamot.conf`), section by section — see the
   checklist below. Start with `wake_enabled = false` in the config and the ring/matrix/inputs.
3. **Verify each subsystem on the device** (in this order — each later one depends on the earlier):
   display (`fbleddemo -clock` or `-uwu` for framebuffer screens), ring
   (`ringdemo -board <codename>`), inputs (log lines on key press), audio (the unmute recipe —
   readback can lie, verify by ear), volume mapping, mic levels (PTT session stats), then the wake
   word.
4. **Record findings** in `docs/boards/<codename>.md` and commit the profile.

## Reversing checklist (per model)

- **LEDs:** names in `/sys/class/leds`, colour order, pixel count/order around the ring, default
  trigger (take it over with `none`), max brightness. A large ring may not be per-pixel sysfs at
  all (framebuffer / `ledfb` / SPI) → needs a new ring backend in `internal/hw`. Vendor
  `ledconfigs/`/`ledpatterns/` describe the geometry.
- **Front display:** 7-segment (pattern + colon/dot/degree) or a dot matrix (new backend) or none.
- **Inputs:** `/proc/bus/input/devices` names; key codes per button (`evtest`-style: watch the
  daemon log at `log_level = debug`); which device carries the mic-mute switch (`B: SW=`).
- **Audio out:** `/etc/asound.conf` chain (prefer the stock dmix `default`), the master volume control
  (often created by `hardvold`, see `/etc/hardvold.config`), the amp's mute/unmute controls — **verify
  by ear, not by `amixer cget`** (on bergamot the readback does not reflect writes).
- **Mic array:** capture device, channel count/rate, which channels are live mics vs copies vs the
  speaker feedback (AEC reference) — capture a few seconds in silence and while playing.
- **Mic gain:** the hardware gain control and a value giving speech around -30..-45 dBFS RMS at 1 m.
- **Voice front-end:** vendor VQE preset name (`strings libyandex_evil.so`), its mic count, and
  whether its outputs look sane (`spotter-host vqe -P <preset> -N .. -M .. -R ..`).
- **Spotter models:** `/data/quasar/data/spotter_model/*` — which dir is the activation model, which
  the commands, and the channel layout each needs (`spotter-host probe <dir>`). Model dir ids are
  per device; tuning thresholds is per model (copy the dir and edit `flags.txt`).
- **Sensors:** IIO devices (ambient light prefix), anything else (temperature, proximity).
- **Extras:** radios (Zigbee/Thread/BLE coprocessors — tty/SPI devices, vendor daemons), extra
  buttons, displays — note them; they're new features, not profile keys.
