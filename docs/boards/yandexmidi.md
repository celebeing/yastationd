# yandexmidi — Station 2 (DT `yandex,station-midi`)

Profile: _not written yet_ (`yastationd/internal/board/profiles/yandexmidi.conf`) ·
Source: **firmware dump only**, `dumps/fw-midi/` (SWU `0.369.1.50`, local only; the directory name
predates the correction below), plus the community wiki's live-unit data where marked [wiki] ·
Status: **profiled from firmware — no device in hand, nothing verified on-device.**

> **Which model this is.** Yandex's internal name "midi" belongs to the retail **Station 2**: quasar
> `deviceType: yandexmidi`, DT `yandex,station-midi` (model "Yandex Station Midi"), hostname
> `…-gen2`. The retail **Station Midi** is a different device, codename `cucumber` (Amlogic A5,
> kernel 5.4, eMMC, aarch64): [cucumber.md](cucumber.md). This page was first written as "Station
> Midi"; everything in the dump (AXG, kernel 4.9.68, NAND/UBIFS, armhf, the gowin panel, SY6045S)
> matches the Station 2 as described by the community wiki
> ([yandex-station-extended](https://github.com/Marker284/yandex-station-extended), `docs/devices/station-2.md`), not the Midi.

> Everything here is read out of the firmware image (`rootfs.ubifs`, init scripts, configs, the
> vendor blobs' strings). Boot/DT/U-Boot are AES-encrypted blobs — not inspectable. Treat all of
> it as a bring-up starting point, not confirmed behaviour; re-verify on hardware per the
> [boards README](README.md) procedure before trusting any number.

## Firmware package (`dumps/fw-midi/`)
- Delivered as a **SWUpdate `.swu`** container (ASCII cpio, SVR4-CRC; the `…369….zip` was that cpio,
  and `midi.7z` carried byte-identical members). Extracted members live in `dumps/fw-midi/images/`,
  the unpacked root in `dumps/fw-midi/rootfs/`; `PROVENANCE.txt` has the sha256 of every member.
- `sw-description` (libconfig) + `sw-description.sig` (RSA, verified against `ota_script.pub`) drive
  the install: `rootfs.ubifs` → UBI volume `rootfs`; `boot.img.encrypt` → `/dev/mtd2`;
  `dt.img.encrypt` → `/dev/dtb`; `u-boot.bin.encrypt.usb.{bl2,tpl}` flashed to `/dev/mtd0` / `/dev/mtd1`
  at several offsets by `flash-nand-copies.sh` (keeps redundant U-Boot copies — a swupdate-2019.11
  workaround). `hardware-compatibility = "encrypt"`.
- `boot.img` is an Android boot image (kernel load `0x1080000`, page 2048) but the kernel payload is
  encrypted (entropy ≈ 8.0); `dt.img`, both U-Boot parts likewise. Amlogic secure-boot style — the
  kernel/DTB are not readable from this dump.
- rootfs is **UBIFS, mounted `ro`, no dm-verity** (fstab `/dev/ubi0_0 / ubifs ro`; no verity line) —
  so the bergamot editing recipe (`mount -o remount,rw /` … `sync; remount,ro`) should apply here too.

## Identity
- quasar `deviceType: yandexmidi`, VQE `preset: yandexmidi`; hostname `Yandex-Station-gen2`.
- Firmware `3.210.369.1.50.4794649087.20261021.295`, Buildroot 2020.02.1, kernel **4.9.68**.
- SoC **Amlogic** (`aml_sdio`, `meson-*` controls, `optee`/`optee_armtz` TEE).
- **Userland armhf (32-bit, `ld-linux-armhf.so.3`, glibc 2.30)**; `lib32/` present. `maind` is a
  32-bit ARM PIE. Same ABI split as bergamot (static aarch64 binaries run on the 64-bit kernel; any
  dynamically-linked helper must be `armv7-gnueabihf`).
- DT `compatible` not readable (encrypted). The `yandex,yandexmidi` form is the assumption; set
  `board = yandexmidi` in config or `/bkupgrade/cfw/board.conf` until the DT string is confirmed on a
  device.

## Hardware facts from a live unit [wiki]
- AXG, 4× Cortex-A53 @ 1.2 GHz, ~490 MB RAM, **512 MB Micron SLC NAND** (page 4096, erase 256 KB).
  MTD: `bootloader` 4 MB, `tpl` 6 MB, `boot` 20 MB, `system` 176 MB (`ubi0:rootfs`, ubifs ro),
  `nv` 1 MB (`/tee`, vfat ro), `bkupgrade` 233 MB (`ubi1`, rw, `/bkupgrade`), `recovery` 60 MB.
- I²C: `0-0x08` HUSB238 (USB-C PD), `0-0x53` LTR-308 (light), `1-0x18` **TLV320ADC3101** (the mic
  ADC; ALSA `TDM-C-tlv320adc3101-adc`), `1-0x2a` SY6045S (`TDM-B-sy6045s-hifi`), EEPROMs.
- Inputs: `gpio-keys` (physical keys) and `midi_touch` (the touch panel).
- Wi-Fi on that unit: Marvell/NXP (`mlan` + `moal`), BT over `hci_uart` (the firmware is multi-SKU,
  see "Power, radios" below).
- USB host mode works at runtime on this SoC (the same PHY registers as the Station Lite), not
  something we plan to use.

## Front display — framebuffer dot-matrix (NOT per-pixel sysfs)
- Driver `gowin_led_device` (**fbdev**, `drivers/video/fbdev/gowin_led_device.ko`), loaded by
  `S29led_panel_midi` (`modprobe gowin_led_device`). The panel is `/dev/fb0`; brightness via
  **`/sys/class/backlight/fb0/brightness`** (`maind` strings: `maxBrightness=`, `/dev/fb0`,
  `/sys/class/backlight/fb0/brightness`). There are **no `/sys/class/leds/*` entries for the panel.**
- Animations are `.led` pattern files blitted to the framebuffer: `ledpatterns/panel/` and
  `ledpatterns/oknix/` (`alice_listening.led`, `alice_speaking.led`, `alice_thinking.led`,
  `bluetooth_connected.led`, `block.led`, …), plus gradient files `blink.grad`, `tts.grad`.
  Vendor tools: `led-play`, `led_util`, `frame_buffer_ledpattern_analyser`.
- **Panel geometry [wiki]:** `/dev/fb0` "YNDX LED screen", logical **89×1**, 32 bpp (4 bytes a
  pixel, 356-byte frame). 88 LEDs (indices 0–87; 88 never lights) under a diffuser, laid out in a
  **checkerboard**: one frame pixel = one LED, so the 2-D picture needs an index map (the wiki has
  it as an image: 0–83 the main field, 84–87 four centre LEDs). Channel order within a pixel is
  unverified. The name is the same as orion's screen ("YNDX LED screen"), but orion's is a true
  21×12 grid.
- **yastationd implication:** `hw.DotMatrix` already discovers an "LED screen" fb and reads its
  geometry by ioctl, but this panel needs a pixel→LED map and its own `display.Front` (the seg7
  fallback cannot draw on 89×1). Its animations are `.led` files in `ledpatterns/oknix/`.

## Audio out
- `/etc/asound.conf`: `default` playback = `dmix_spk_plug` → dmix (`ipc_key 1024`, `0666`) →
  **`hw:0,0`, 48 kHz stereo** (period 960 / buffer 7680). A `softvol` "Master Playback Volume" and an
  `equal` plugin (`/data/alsaequal_state.bin`) sit in front. Prefer `default`, as on bergamot.
- Amplifier: **Silergy `SY6045S`** (codec `snd-soc-sy6045s`, I²C `1-002a`) — `S47amplifiers_fw`
  symlinks its `settings_file`/`default_settings`. Alternate board stuffing is **TI `TAS5825M`**
  (`tas5825m` at the same I²C `1-002a`; `S45tas58xx` is a stub on current PCBs). Vendor tuning blobs:
  `amplifiers_configs/{3nod_burst_20,sb_burst_23}`.
- Volume: `Master Playback Volume` 0..255 is **hardvold's** control (`/etc/hardvold.config`), curve
  **−51 dB (0) … 0 dB (255)** onto `SY6045S Master Playback Volume`. Codec also exposes
  `Ch1/Ch2 Playback Volume/Switch`, `Master Playback Switch`. `set_volume.sh` = `amixer set 'Master' N%`.
- **Unmute recipe unknown** — the SY6045S mute control name and whether `cget` readback lies (as it
  does on bergamot's ACM8625) must be found by ear on a device.

## Mic array & voice front-end
- 4-mic array. `etc/alsa-audio-diagnostics/devices/yandexmidi.json`: capture = `raw_mic_0..3`
  (microphones) + `raw_spk_0,1` (feedback, fault-injectable); playback = `playback_left/right`.
  48 kHz, S16_LE, chunk 1536.
- ALSA topology differs from bergamot: **mics on `hw:0,2`** (`asound.conf` treats it as up to 16ch),
  **`hw:0,1` is the ADC/loopback readout**. `S40adc` enables the HW loopback mix
  (`amixer sset "Loopback …"`, PGA 13 dB) and starts a dummy `arecord -D hw:0,1 … /dev/null` so TDM
  data flows and the mics appear on `hw:0,2` (ch 8/9). `multi_capture` binds `hw:0,2` ch{0,2,4,7} +
  `hw:0,1` ch{0,1} + `hw:0,2` ch6 → a 7-channel VQE feed; `hw_feedback_and_mics` wraps it with
  fault-inject + `audio_metrics` taps.
- VQE preset **`yandexmidi`** is compiled into `libyandex_evil.so` (its `strings` show
  `bergamot`, `cucumber`, `plum`, `yandexmidi`, `yandexmini_2`) → per the boards README this model
  **likely needs no audio-front-end reversing** — just `wake.vqe_preset = yandexmidi` and the right
  mic/feedback channel count (4 mics + feedback). Confirm channel mapping with `spotter-host vqe`.
- Spotter models in `spotter_models/`: activation (`alisa`→«алиса», `yandex`→«яндекс»,
  `yasmina`→«yasmina»), plus `vad`, `command`, `counteractivation`, `intonationInterruption`,
  `spotterRecognizer` (`model.tflite`). `libtflite.so` present. Same shape as bergamot; thresholds
  are per-model and per-device, tune on hardware.

## Inputs
- Not determinable from the dump (device-tree `gpio-keys` / touch controller codes are in the
  encrypted DT). `mdev.conf` only has the generic `input/event[0-9]+` rule. Must be read on a device
  (`/proc/bus/input/devices`, watch codes at `log_level = debug`), incl. which carries the mic-mute
  switch. Expect a layout similar to bergamot (volume ±, Alice/voice key, mute switch).

## Power, radios & smart-home extras
- **USB-C PD input**: `S30pd_controller` drives a **HUSB238** sink (`husb238_pd`, sysfs
  `/sys/class/power_supply/ya_pd`); it negotiates the highest available PDO among **15 V / 12 V / 9 V /
  5 V**. So the Station 2 is USB-C-powered and wants a PD supply (≥15 V preferred) — a plain 5 V port may
  brown it out.
- **Zigbee**: Silicon-Labs NCP (`zigbee/yandexmidi_zigbee_ncp.signed.gbl` + `gateway.ybl`), reset/boot
  on `GPIOX_16`/`GPIOX_17` (`S30zigbee`). **Matter** controller present (`matter/` with a large PAA
  trust store + `http`). These are Yandex-stack features (a hub), not needed for a HASS speaker — but
  note the device has a Zigbee radio if it's ever wanted.
- **Bluetooth**: multi-vendor `S50hci0` (Realtek `rtk_hciattach`, NXP, Amlogic `hciattach … aml`,
  Unisoc `hciattach_sprd`) on a UART; BlueALSA stack (`S52bluealsa`, `S53bluealsa-aplay`) as on bergamot.
- **Wi-Fi**: `S40wifi` picks the chip at boot via `yandex-wifichipmodel` → Unisoc / Realtek / NXP /
  **Amlogic** (`modprobe vlsicomm`); multi-SKU. `wpa_supplicant` config at
  `/bkupgrade/data/wpa_supplicant.conf`, owned by init (same as bergamot — killing `maind` won't drop
  the network).
- Thermal: `S01hot-poweroff` (critical-trip handler that shows a pattern then powers off).

## Vendor stack & updates
- The vendor stack starts the same way as on bergamot (`S99quasar → onboot.sh → run.sh →
  quasar_launcher2 → maind`), so the same `run.sh` gate keeps it from running. The update path
  (`device_ota_update.sh`, `apply_packed_ota`, `ota_script.pub`) is shaped like bergamot's, and the
  same update block applies.

## Open questions (need a device)
- The panel's channel order and backlight range (geometry: see above [wiki]).
- Input device names + key codes; which device has the mic-mute switch.
- SY6045S/TAS5825M unmute control + whether `amixer cget` readback reflects writes.
- Exact mic/feedback channel mapping into the `yandexmidi` VQE preset; mic gain control + a good value.
- DT `compatible` string (to auto-select the profile) and the real `/proc/mtd` layout.
