# Boards: the supported models and what is known about others

yastationd is model-independent; everything model-specific is a **board profile**
(`yastationd/internal/board/profiles/<codename>.conf`, embedded in the binary) plus this folder's
per-board notes (`<codename>.md`, from [`_template.md`](_template.md)). Profiles also provide the
host deployment defaults (`platform.cfw_root`, `platform.userland_arch`). The device picks its
runtime profile from the device-tree `compatible` list (`yandex,<codename>`); `board =
<codename>` in `yastationd.conf` forces one, and `<cfw>/board.conf` overrides runtime keys
without a rebuild. Host-side per-unit deployment overrides stay in `local.mk` or
`tools/deploy.hosts`.

The codename is Yandex's own board name: the profile's file name, `board =`, and what the profile
matches in the device tree (`match = yandex,<codename>`). It is not the retail name, and the two
cross over (below). The same table, for users, is in the main [README](../../README.md).

| Codename | Retail model | DT `compatible` | Status |
|---|---|---|---|
| [bergamot](bergamot.md) | Station Mini 3 | `yandex,bergamot` | full support, verified on-device, two units in daily use |
| [pickle](pickle.md) | Station Mini 3 Pro | `yandex,pickle` | full support (a 16-pixel ring, aarch64 userland), verified on-device |
| [orion](orion.md) | Station 3 | `yandex,orion` | full support: voice with the stock wake word (NPU verifier), playback, volume, circular controls, ring, rear ambilight, the 21×12 screen with the firmware's animations and our pixel clock and fonts. The intercom is not yet checked on it |

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

**We don't plan to port these** and have no unit of any; they are not in the table above. A
contributor with the speaker is welcome to start from the notes here.

- **Station Mini 2** (`yandex,station-mini2`, Amlogic AXG, armhf, kernel 4.9.68): by the community
  wiki ([yandex-station-extended](https://github.com/Marker284/yandex-station-extended),
  `docs/devices/station-mini2.md`) it is close to bergamot — a white 7-segment clock
  (`mini2:white:clock{A..G}{1..4}`, `clockDEGREE`, `clockMINUS`, `clockPLUS1..4`) and a 6-RGB ring
  (`mini2:{red,green,blue}:ring{1..6}`), both per-LED sysfs on IS31FL3236 drivers; LTR-308 light
  sensor; ES8156 DAC + SY6045S amp on `TDM-B`; PDM mics; touch via an AW96103 SAR (`sar0_pad0`), no
  `gpio-keys`; a headphone jack. A profile modelled on `bergamot.conf` (seg7 + `sysfs-rgb` ring) is
  the likely start, with two things the profile cannot say today: the LEDs are numbered from 1
  (`clock{seg}{1..4}`, `ring{1..6}`; the profile's `{digit}` / `{i}` count from 0, so a code change
  or an index base key is needed) and the DT compatible is `yandex,station-mini2`, not
  `yandex,<codename>` (`match` takes it as it is). The VQE preset `yandexmini_2` is in
  `libyandex_evil.so` (0.369 armhf). A fork of pulsar has a Mini 2 port to read for hardware hints
  ([ste4ik/pulsar](https://github.com/ste4ik/pulsar)): it lets the vendor stack run and expects the
  internet, which our safety rules forbid, so don't copy its setup.
- **Station Midi** (`cucumber`, DT unknown): community wiki data only, no dump; see
  [cucumber.md](cucumber.md); start from orion's profile.
- **Station 2** (`yandexmidi`, DT `yandex,station-midi`): its firmware `0.369.1.50` studied, no
  profile; see [yandexmidi.md](yandexmidi.md); needs a front `display.Front` for its 89×1
  checkerboard panel.
- pulsar's `AGENTS.md` ([pulsar](https://github.com/Morvex885/pulsar), a C++ daemon for the Midi,
  Lite 2 and Mini 2) has hardware notes for those models; its "Midi" is `cucumber`.

## Porting a new model

How to research a model and bring the CFW up on it — the ground rules (first of all: **no
trial-and-error commands on components you don't know**), what to collect, how each part of the
hardware maps to a profile key, the order to verify in, and when it is done — is
[porting-a-speaker.md](../porting-a-speaker.md). A new board's notes start from
[`_template.md`](_template.md); [bergamot.md](bergamot.md) (armhf) and [pickle.md](pickle.md)
(aarch64, ported from a dump and a survey) are the worked examples.
