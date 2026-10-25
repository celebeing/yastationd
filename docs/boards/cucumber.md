# cucumber — Station Midi (DT `yandex,cucumber`)

Profile: _none_ · Source: **no unit, no firmware dump** — the community wiki's live-unit snapshot
([yandex-station-extended](https://github.com/Marker284/yandex-station-extended),
`docs/devices/station-midi.md`) and pulsar's hardware notes (see [wishlist.md](../wishlist.md)) ·
Status: **not profiled by us; a pointer for whoever ports it.**

Not to be confused with the Station 2, whose internal name is "midi" ([yandexmidi.md](yandexmidi.md)).

## Platform [wiki]
- **Amlogic A5** (DT `Yandex Cucumber DVT2`, `yandex,cucumber-dvt2` / `yandex,cucumber`), 4×
  Cortex-A55 up to 2.016 GHz, ~980 MB RAM, kernel **5.4.180** aarch64, Buildroot 2023.11.1, quasar
  3.21. Userland **aarch64, glibc 2.36** (pulsar). Storage **eMMC** (`/proc/mtd` empty, no UBI) —
  so the rootfs editing recipe and the CFW root must be checked anew (orion, the other A5, uses
  `/data/cfw`).
- The same generation as [orion](orion.md): A5, kernel 5.4, AW21036 LED drivers, `ya_pd_controllers`
  USB-C PD, `optee`. A port would start from orion's profile, not bergamot's.

## Peripherals [wiki]

| Bus:addr | Chip | Role |
|---|---|---|
| `0-0x08` | HUSB238 (`husb238_pd`) | USB-C PD |
| `1-0x12` | AW96105 (`awinic_sar`) | capacitive SAR (touch / proximity) |
| `1-0x20` | AW21018 | LED driver |
| `1-0x34`, `1-0x35` | AW21036 | LED drivers |
| `1-0x51` | PT7C4563 | RTC |
| `2-0x68`, `2-0x6a` | 2× FS2105 | stereo class-D amps |
| `3-0x21` | AW21012 | LED driver |
| `3-0x37` | AW21036 | LED driver |
| `3-0x53` | LTR-308 (`ltr308als01`) | light sensor |

## Indication [wiki]
All in `/sys/class/leds/` — per-LED sysfs, the backend bergamot and orion already use:
- logo: `cucumber:{red,green,blue}:logo0..5` (6 RGB);
- ring: `cucumber:{red,green,blue}:ring0..23` (24 RGB);
- a white **7-segment clock**: `cucumber:white:clock-*` (`clock-a1..4` … plus `clock-deg`,
  `clock-h/l/m/p/q…`) — a `seg7` matrix profile;
- latch nodes `cucumber:logo:sync`, `cucumber:ringA:sync`, `cucumber:ringB:sync`,
  `cucumber:sync:clock-0/1` (bergamot's sysfs also carries a `cucumber:sync:clock-0`: the kernel/DT
  is shared across models).

## Audio and inputs [wiki]
- ALSA card 0 `AML-AUGESOUND`: `TDM-A-fs2105-speakers` (playback + capture), `PDM-dummy-mics`,
  `LOOPBACK-A` (the AEC reference — as on orion, refs ride a hardware loopback).
- VQE preset `cucumber` is compiled into `libyandex_evil.so` (see [README.md](README.md)), so the
  voice front-end most likely needs no reversing — only the channel layout.
- Inputs: `awinic_sar`, `vad_keypad` (keys), `mute` (hardware mic switch), `input_btrcu` (BT remote).
- Zigbee radio (`zigbee_control`), like orion; not needed for a HA speaker.

## Open (needs a unit)
The usual [porting checklist](../porting-a-speaker.md): eMMC partitions and where the CFW root can
live, the vendor-stack gate, key codes, the clock's exact segment names, the unmute recipe, the
VQE channel map.
