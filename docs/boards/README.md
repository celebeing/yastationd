# Boards — bringing up a new Yandex Station model

yastationd is model-independent; everything model-specific is a **board profile**
(`yastationd/internal/board/profiles/<codename>.conf`, embedded in the binary) plus this folder's
per-board notes (`<codename>.md`, from [`_template.md`](_template.md)). Profiles also provide the
host deployment defaults (`platform.cfw_root`, `platform.userland_arch`). The device picks its
runtime profile from the device-tree `compatible` list (`yandex,<codename>`); `board =
<codename>` in `yastationd.conf` forces one, and `<cfw>/board.conf` overrides runtime keys
without a rebuild. Host-side per-unit deployment overrides stay in `local.mk` or
`tools/deploy.hosts`.

| Board | Model | Status |
|---|---|---|
| [bergamot](bergamot.md) | Station Mini 3 | supported, verified on-device |
| [cucumber](cucumber.md) | Station Midi | not profiled: community wiki data only (no unit, no dump) |
| [yandexmidi](yandexmidi.md) | Station 2 (DT `yandex,station-midi`) | profiled from firmware `0.369.1.50` (no device yet) |
| [orion](orion.md) | Station 3 | initial support, installed on a unit: voice + stock wake word (NPU verifier), sound, circular controls, ring, ambilight, 21×12 screen with the stock animations; clock font and call-mode VQE model open |

Known vendor codenames (the VQE presets compiled into `libyandex_evil.so`) — from the
0.369 armhf lib via `strings`, **plus** the 3.21 aarch64 lib where the names are built from
immediates (`mov`/`movk`) and invisible to `strings`: `bergamot`, `cucumber`, `plum`,
`yandexmini`/`mini43`/`mini50`/`mini55`/`mini65`, `yandexmidi`, `pickle`, `orion`, `nebula`,
`potato`, `mango` (+ `rev1..rev5` and 16/48 kHz variants). A model whose codename is in that
list very likely needs **no audio-front-end reversing** — just `wake.vqe_preset = <codename>` and
its own channel layout. (bergamot's sysfs also has a `cucumber:sync:clock-0` LED, i.e. the
kernel/DT is shared across models.)

Yandex's codenames cross over: the DT/quasar name "midi" (`yandexmidi`, `yandex,station-midi`) is
the retail **Station 2**, while the retail **Station Midi** is `cucumber`. Go by SoC, kernel and RAM,
not by the word "Midi".

### Possible ports nobody here is doing

Models we have no unit of and don't plan to port; a contributor with the speaker is welcome to.

- **Station Mini 2** (`yandex,station-mini2`, Amlogic AXG, armhf, kernel 4.9.68): by the community
  wiki ([yandex-station-extended](https://github.com/Marker284/yandex-station-extended),
  `docs/devices/station-mini2.md`) it is close to bergamot — a white 7-segment clock
  (`mini2:white:clock{A..G}{1..4}`, `clockDEGREE`, `clockMINUS`, `clockPLUS1..4`) and a 6-RGB ring
  (`mini2:{red,green,blue}:ring{1..6}`), both per-LED sysfs on IS31FL3236 drivers; LTR-308 light
  sensor; ES8156 DAC + SY6045S amp on `TDM-B`; PDM mics; touch via an AW96103 SAR (`sar0_pad0`), no
  `gpio-keys`; a headphone jack. A profile modelled on `bergamot.conf` (seg7 + `sysfs-rgb` ring) is
  the likely start; the VQE preset `yandexmini_2` is in `libyandex_evil.so`.
- **Station Midi** (`cucumber`): see [cucumber.md](cucumber.md); start from orion's profile.
- **Station 2** (`yandexmidi`): see [yandexmidi.md](yandexmidi.md); needs a front `display.Front`
  for its 89×1 checkerboard panel.

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
