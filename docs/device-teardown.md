# Yandex Station Mini 3 (`bergamot`) — device teardown & CFW reference

A neutral, general teardown of the **Yandex Station Mini 3** smart speaker, written as a
reverse-engineering / custom-firmware (CFW) reference. The goal is to document how the
device is built, what runs on it, and where the levers are for repurposing the hardware —
so the platform can be understood and modified by its owner, and the method re-mapped onto
other Yandex Station models.

Facts tagged **[bergamot]** are specific to this model/SoC; **[general]** are patterns
shared across the Yandex Station / "quasar" product family.

---

## 1. Identity & hardware

| | |
|---|---|
| Model | Yandex Station Mini 3, codename **`bergamot`** |
| SoC | **Amlogic** (ARMv8 `aarch64`; drivers prefixed `aml_*`) |
| Kernel | **Linux 4.9.68** SMP PREEMPT (vendor Amlogic BSP) |
| Userland | **BusyBox v1.31.1** (`/bin/sh -> busybox`), glibc |
| Secure world | **OP-TEE** TrustZone (`optee`, `optee_armtz`), `keymaster_svc` + `/dev/tee*`, TEE partition `mtd4` → `/tee` |
| Wi-Fi / BT | SDIO combo (`aml_sdio`, `sdio_bt`, `vlsicomm`); `wpa_supplicant` on `wlan0` |
| Device id | burned in eFuse, read/written with `device_id` (`device_id -r`); case color derived from it (`yandex-getcolor`) |

### Key kernel modules (`lsmod`) [bergamot]
- `snd_soc_acm8625` — speaker smart amplifier
- `aml_pdm` — PDM microphone array
- `leds_yandex_aw21036`, `leds_aw2101x`, `quasar_led_ring` — LEDs / front display (Awinic matrix controllers)
- `key_aw933xx` — Awinic capacitive touch keys
- `ya_pd_controllers` — USB-C Power Delivery
- `ltr308als01` — ambient light sensor
- `optee` / `optee_armtz` — TrustZone secure world
- `aml_sdio` / `sdio_bt` / `vlsicomm` — Wi-Fi / Bluetooth combo
- `aml_info`

---

## 2. Storage & partitions

NAND flash, UBI / UBIFS. `cat /proc/mtd`:

| mtd | size | name | notes |
|-----|------|------|-------|
| mtd0 | 4 MiB | `bootloader` | Amlogic BL2 / boot |
| mtd1 | 6 MiB | `tpl` | bootloader TPL (U-Boot / BL33) |
| mtd2 | 20 MiB | `boot` | kernel + dtb (boot image) |
| mtd3 | 176 MiB | `system` | **rootfs** → `ubi0:rootfs` (UBIFS) |
| mtd4 | 2 MiB | `nv` | NV / calibration |
| mtd5 | 232 MiB | `bkupgrade` | **`/dev/ubi1_0` → `/bkupgrade`** (rw data + OTA staging) |
| mtd6 | 8 MiB | `recovery` | recovery kernel |
| mtd7 | 51 MiB | `recovery_fs` | continuation of the recovery boot image (encrypted ramdisk); later units have one 58.75 MiB `recovery` instead — see [permanent-install.md](permanent-install.md) |

Mount reality:
- **`/` (rootfs) is `ubifs ro`** — mounted read-only. Persisting changes to it needs
  `mount -o remount,rw /` (writable NAND; survives reboot, but OTA would overwrite — see §8).
- **`/bkupgrade` is `ubifs rw`** — the durable writable store (holds `wpa_supplicant.conf`,
  quasar data, OTA staging).
- `/data`, `/tmp`, `/run`, `/dev/shm`, and `/etc/{bluetooth,unbound,wifi}/w1` are **tmpfs**
  (RAM, wiped on reboot). Note: `/data` is **not persistent** on this unit.

Raw backup: `dd if=/dev/mtdN of=...` (or `nanddump` for OOB data). Full filesystem dump:
copy `/system /etc /usr /bin /sbin /lib /var /opt` + `/bkupgrade/data`.

---

## 3. Boot & init chain

```
Amlogic ROM → bootloader(mtd0)/tpl(mtd1) → boot(mtd2): kernel 4.9.68 + dtb
  → BusyBox init (/etc/inittab) → /etc/init.d/rcS → S* scripts:
     S02sysctl S10mdev S20urandom S30dbus S30pd_controller S46hardvold
     S49chrony S49keymaster S50hci0 S51bluetoothd S98pstore-clear → S99quasar
```

- **`/etc/init.d/S99quasar`** → `exec /system/vendor/quasar/onboot.sh "$@"`.
- **`onboot.sh start`** (key boot logic, **[general]**):
  - `echo 1 > /proc/sys/vm/overcommit_memory`
  - wipes old OTA staging: `rm -f /bkupgrade/update_* /bkupgrade/ota/*`
  - sets `LD_LIBRARY_PATH=/system/vendor/quasar{,/libs}`, extends `PATH` with
    `/system/vendor/quasar`, `GST_PLUGIN_PATH=/system/vendor/quasar/libs` (GStreamer media stack)
  - creates tmpfs `/data/quasar/{logs,daemons_logs,data}` (first-run marker `/data/quasar/initialized`)
  - runs `yio_iptables_init.sh`, `set_monotonic_time.sh`
  - `q save-last-monotonic-time` → **`run.sh`** → `touch /data/quasar/initialized`
- **`run.sh`**: `nohup quasar_launcher2 launch.json &` with
  `CA_CERTIFICATES=/system/vendor/quasar/ca-certificates.crt`,
  `LD_LIBRARY_PATH=/system/vendor/quasar/libs`, `ulimit -s 1024`.

### Supporting daemons (from `ps`)
`tee-supplicant`, `syslogd`/`klogd`, `watchdog` (`/dev/watchdog`, `-t 1 -T 5`), `mdev`,
`dbus-daemon`, `wpa_supplicant`, `hardvold` (USB-C PD), `chronyd`, `keymaster_svc`,
`hciattach` + `bluetoothd`, **`bluealsa`** (`-p a2dp-sink -p a2dp-source -p hfp-ag`) +
`bluealsa-aplay`, `just-works-bluez-agent`, `unbound` (local DNS resolver, as `nobody`).

---

## 4. The Yandex userland (`/system/vendor/quasar/`)

The whole product is essentially **one ~22 MB multi-call binary `maind`** plus a supervisor.

- **`quasar_launcher2 launch.json`** — process supervisor; starts `maind` instances:
  - `maind --platformd` (order 1) — platform / hardware + device logic
  - `maind --mediad` (order 3) — media / playback (GStreamer)
  - `maind --updatesd` — OTA updater (see §8)
  - `maind --telemetry`, `maind --fluent-bitd` — metrics / logging (AppMetrica, fluent-bit)
- `maind` is also symlinked as `keymaster_proxy_app`, `quasar_client`, `metrics_collector`,
  `led_util`, `clock_led_util`, `frame_buffer_ledpattern_analyser`, `tcp_alsa_writer`
  (one binary, many roles selected by argv[0] / flag).
- **`quasar.cfg`** (~68 KB) — master config (daemon knobs, spotters, endpoints).
- Other files: `onboot.sh`, `run.sh`, `q` (helper), `launch.json`, `device_ota_update.sh`,
  `ota_script.pub` (OTA signing pubkey), `reboot.sh`, `set_monotonic_time.sh`,
  `spotter_models/`, `vqe_models/`, `bio_lingware/`, `sounds/`, `ledconfigs/`, `ledpatterns/`,
  `unbound/`, `matter/`, `ca-certificates.crt`.

### Vendor helper tools (`/usr/bin/yandex-*`)
Factually, a set of small stock helpers wrapping sysfs / device functions:
- `yandex-indication <state>` — sets `/sys/class/leds/*/brightness` by label regex
  (states: `all boot busy done fail factory off re:<regex>`).
- `yandex-getcolor` / `yandex-get-localization` — case color / locale from device id.
- `device_id` — read/write the eFuse device id (`device_id -r`).
- `yandex-debugctl`, `yandex-usb-mode` — debug/USB-mode switching helpers.
- `yandex-apply-dtb-overlay`, `yandex-badblocks`, `yandex-wifireset`,
  `yandex-wifichipmodel`, `yandex-btfwversion`, `yandex-chrony`, `yandex-waitfile`.
- Stock Linux tooling present: `aplay`/`arecord` (ALSA), `wpa_supplicant`/`wpa_cli`,
  `bluetoothctl`/`bluealsa`, `iptables`, `dd`/`nanddump`.

---

## 5. Networking & cloud endpoints

- Uplink `wlan0` (`wpa_supplicant`, config `/bkupgrade/data/wpa_supplicant.conf`).
- Local DNS: **`unbound`** running on the device.
- Cloud endpoints from `quasar.cfg` (**[general]**, the phone-home set):
  - `backendUrl` → **`quasar.yandex.net`** — config / sync / activation
    (`/get_sync_info`, `/check_updates`, `/update_device_state`).
  - `uniProxyUrl` → **`wss://uniproxy.alice.yandex.net/uni.ws`** — the voice assistant
    duplex stream (Alice).
  - `uniProxyQuicUrl` → `uniproxy-quic.alice.yandex.net:8443/uni.ws`.
  - Spotter / wake-word models from `quasar.s3.yandex.net` / `*.svc.cdn.yandex.net`.
  - Metrics: `clck.yandex.net/quasar_metrics`, AppMetrica (`report.appmetrica.yandex.net`),
    `rpc.alice.yandex.net`, `startup.mobile.yandex.net`, `push.yandex.ru`.
  - Time: `ntp.yantp.net`.
- `device_id`, platform, and firmware version are sent as query params on backend calls;
  requests carry `X-Quasar-Alice-App-Id`, `Authorization: OAuth …`, and a request signature
  `x-quasar-signature`.

### TLS trust (**[general]**, factual)
- The device trusts a **public CA bundle** (`/system/vendor/quasar/ca-certificates.crt`),
  not a private pin, for `quasar.yandex.net`.
- Requests to the backend carry an OAuth token and an `x-quasar-signature`
  (`x-quasar-signature-cryptography: keymasterProxy`, version 2) produced by the
  `keymaster_proxy_app` role of `maind` via OP-TEE. This is the device-attestation path.

---

## 6. Hardware interfaces for repurposing

This is the most valuable part for CFW: all the hardware is exposed through **standard
Linux subsystems** — no Yandex API is required to drive it once the vendor stack is stopped.

### Audio — ALSA card 0 `AML-AUGESOUND`
- **Playback**: `hw:0,0` — TDM to the **`acm8625`** smart amplifier (I²C `1-002c`); digital
  volume set via the codec (`acm8625_set_volume`).
- **Capture (mic array)**: `hw:0,1` — **PDM** mic array (`aml_pdm`, device named `PDM-dummy`).
  (`hw:0,0` also exposes a capture path = AEC loopback / reference.)
- **Bluetooth audio**: via **BlueALSA** (`a2dp-sink` / `a2dp-source` / `hfp-ag`).
- Implication: a voice satellite (e.g. Wyoming satellite) or snapcast / squeezelite / MPD
  can use these ALSA devices directly once `maind` is stopped. Note that far-field DSP / AEC
  / beamforming and the wake-word engine live inside `maind` (proprietary); without it you
  get the raw PDM channels and must supply your own processing (speexdsp / webrtc-apm /
  openWakeWord).

### LEDs & front display — `/sys/class/leds/*` (+ `/dev/fb0`)
Driven by Awinic matrix controllers (`leds_yandex_aw21036`, `leds_aw2101x`,
`quasar_led_ring`). Every element is a Linux **LED class** device; write `brightness`
(0–`max_brightness`=255):

- **Front clock (7-segment style, 33 white segments)** [bergamot]:
  `bergamot:white:clock{A,B,C,D,E,F,G}{0..3}` (4 digits × 7 segments) plus `clockDH`,
  `clockDL`, `clockDM` (colon dots) and `clockDEGREE`. Trigger `none` → direct `brightness`
  control. The clock face is drawn by lighting these segments.
- **RGB logo bar (6 pixels)** [bergamot]: `bergamot:{red,green,blue}:logo{0..5}` — 6 positions
  × RGB. These use trigger **`ledfb-logo`** and are mirrored by **`/dev/fb0`** (reported
  geometry `logo 6×1`, `bpp=24`): writing 6×3 bytes to `fb0` sets the 6 RGB pixels.
  `bergamot:logo:sync` (`ledfb-logo-sync`) and `cucumber:sync:clock-0` are latch / sync
  pseudo-LEDs.
- **Animation patterns** live in `/system/vendor/quasar/ledpatterns/`:
  - `oknix/*.led` — **text frames**, one line per frame: `RRGGBB` ×6 + `<duration>`, with a
    leading `loop` keyword (e.g. `alice_listening`/`speaking`/`thinking`, `bluetooth_*`,
    `block`). These drive the status animations on the 6-pixel bar.
  - `clock/*.binscled.zst` — zstd-compressed binary clock / status frames
    (`alice_listening`/`speaking`/`thinking`, `blink`, `boot`, `configuring`, …).
  - Rendered by `maind` as `frame_buffer_ledpattern_analyser` / `clock_led_util`.
  - `ledconfigs/<color>.json` — per-case-color LED calibration.

### Buttons & sensors (evdev + sysfs)
- `/dev/input/event0` = `gpio-keys` (physical buttons).
- `/dev/input/event1` = `aw933xx` (Awinic capacitive **touch** keys, `key_aw933xx`).
- Ambient light sensor `ltr308als01` (iio / sysfs).
- USB-C PD: `ya_pd_controllers` (`S30pd_controller`, `hardvold`).

---

## 7. OTA update mechanism (factual)

- Daemon **`maind --updatesd`**. Flow: `check_updates` on `quasar.yandex.net` → if an update
  exists, download image → **`/system/vendor/quasar/device_ota_update.sh <ota-file>`**:
  `killall quasar_launcher2`, `killall -9 maind`, `drop_caches`, `compact_memory`,
  `apply_packed_ota <file> /bkupgrade/ota`, **`reboot`**. References a **"Linkplay OTA"**
  mechanism. Images are staged under `/bkupgrade/ota`, `/bkupgrade/update_*` (wiped at boot).
- The OTA image is signed; the verification pubkey is `/system/vendor/quasar/ota_script.pub`.
- CFW relevance: OTA will overwrite rootfs modifications. Any persistent CFW must account for
  the updater (disable `updatesd`, remove/neuter `device_ota_update.sh`, or otherwise prevent
  updates from applying). See §9 for a keep/replace framing.

---

## 9. What a CFW would need to replace vs. keep

**Keep (the reusable Linux base):**
- Kernel 4.9.68 + Amlogic BSP and all device drivers (audio `acm8625`/`aml_pdm`, Awinic LEDs,
  touch, ALS, USB-C PD, SDIO Wi-Fi/BT) — this is what makes the hardware usable.
- BusyBox userland, `wpa_supplicant`, BlueALSA/bluetoothd, ALSA, `unbound` (optional).
- The `/sys/class/leds` + `/dev/fb0` + evdev interfaces for indication and input.
- `recovery` partitions for safety.

**Replace / remove (the Yandex-specific brain):**
- `quasar_launcher2` + `maind` (platformd/mediad/updatesd/telemetry) — the proprietary
  assistant, media, OTA, and telemetry stack.
- The cloud endpoints and OAuth/keymaster attestation (§5) — not reusable off-cloud.
- OTA updater (§8) so CFW survives.

**Decide case-by-case:**
- Far-field DSP / AEC / beamforming and wake-word: proprietary inside `maind`. A CFW must
  bring its own (speexdsp / webrtc-apm / openWakeWord) or accept raw PDM capture.
- LED animation rendering: reimplement a small sysfs daemon mapping state → `/sys/class/leds`
  (clock segments + 6-pixel logo bar) and read buttons from evdev.

Open question for any rootfs modification: whether a secure-boot / dm-verity-style check
(OP-TEE is present) rejects a modified rootfs on boot. Verify on the specific unit before
relying on persistent rootfs edits; keep backups and recovery partitions.

---

## 10. Applying to other Yandex Station models [general]

The overall shape is shared across the family — re-verify the specifics per model/firmware.

**Common across the family:**
- The **boot → init → `/system/vendor/quasar/{onboot,run}.sh` → `quasar_launcher2` → `maind`**
  structure.
- The multi-call `maind` binary + symlinks pattern.
- Public-CA TLS trust and the OAuth + `x-quasar-signature` request attestation.
- OTA via `check_updates` + `device_ota_update.sh`.

**Changes per model:**
- **SoC** — Amlogic here; older Mini/Lite are Amlogic A113x, some are other vendors.
- **LED/display chips & `/sys/class/leds` labels** — here Awinic AW21036/AW2101x with
  `bergamot:*` labels, a 7-segment clock + a 6-pixel logo bar; other models have rings /
  full matrices / no display.
- **ALSA card layout** and codecs.
- **Partition sizes** and layout.

**Method to re-map on a new unit** (run these and compare to §§2, 6):
```
cat /proc/mtd                     # partition table
mount                             # rootfs ro/rw, bkupgrade, tmpfs mounts
ls /sys/class/leds                # LED class device labels (clock/logo/ring)
aplay -l ; arecord -l             # ALSA playback/capture devices
cat /proc/bus/input/devices       # evdev: gpio-keys, touch
lsmod                             # driver set (codec, mic, LEDs, touch, ALS, PD, wifi/BT)
```
Then read the vendor stack:
```
/system/vendor/quasar/onboot.sh
/system/vendor/quasar/run.sh
/system/vendor/quasar/launch.json
/system/vendor/quasar/quasar.cfg
```

## 11. Confirmed runtime facts

Verified against the raw dumps under `../dumps/` (`rootfs-full.tar` with perms/symlinks,
the extracted `extra/init.d/`, and the file-level `rootfs/`):

- **`/data` is a symlink to `/bkupgrade/data`.** `/bkupgrade` is the persistent rw UBIFS
  volume (`/dev/ubi1_0`, per `fstab`), whereas `/` is mounted `ubifs ro`. So everything
  under `/data/…` is persistent UBIFS, not tmpfs — a naive `mount` reading can suggest
  otherwise.

- **WiFi + DHCP are brought up by init, not by the Yandex `maind`.** `/etc/init.d/S40wifi`
  starts `wpa_supplicant` (`/usr/sbin/wpa_supplicant`, config `/bkupgrade/data/wpa_supplicant.conf`)
  and `/etc/init.d/S41dhcpcd` starts `dhcpcd` via `start-stop-daemon`. `wpa_supplicant` runs
  as a child of PID 1 (init). The Linux base therefore has working networking independent of
  the vendor userland.

- **`rcS` runs `/etc/init.d/S??*` in sorted (glob) order,** each as `script start`, piping
  stdout to syslog via `/run/logfifo` (`logger -t init.d`). `S99quasar` is effectively last
  (it starts the Yandex stack); the audio/led/network init scripts (`S40`–`S90`) run before
  it. `/bkupgrade` is already mounted at `sysinit` (`mount -a` in `inittab`) before `rcS`.

---

*General device teardown for the Yandex Station Mini 3 (`bergamot`). Hardware/software facts
observed on an owned unit on firmware `0.369.1.46.*`. Raw device dumps accompany this doc
under `../dumps/`.*
