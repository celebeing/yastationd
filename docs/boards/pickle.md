# pickle — Station Mini 3 Pro

Profile: `yastationd/internal/board/profiles/pickle.conf` · Survey: `dumps/survey/pickle-2026-10-23.txt`
(local only) · Source: **full NAND dump of an owned unit** (`dumps/pickle/`) + that unit on the LAN ·
Status: **supported — ring/clock/keys/audio out, push-to-talk and the «Алиса» wake word via HA verified on-device 2026-10-23.**

## On-device verification (2026-10-23)
State before we started: SSH access, the vendor gate (`/data/cfw/maind_off`), the update block and
the egress lock already in place; maind not running.
- **yastationd**: the stock armv7 static build runs on this aarch64 userland; the profile is
  auto-selected from `yandex,pickle`; MQTT + HA discovery up (`device_id = mini3pro`); installed as
  `/etc/init.d/S52zyastationd` + `/data/cfw/{bin/yastationd,yastationd.conf}`.
- **Reboot**: SSH back ~20 s after `reboot`; yastationd, volume (35 %), `PDM Gain` 24, home Wi-Fi and
  the maind gate all came back on their own. The first daemon start fails (`ring pickle:red:ring0 …
  no such file`): `S52zyastationd` runs before `S93i2ctop` hands the ring bus to Linux; the supervisor
  respawns it 2 s later and it comes up clean. Harmless, but don't read that log line as a fault.
- **HA voice (push-to-talk)**: added as a Wyoming satellite (`<speaker-ip>:10700`), pipeline set like
  bergamot's; the Alice key → STT → answer → TTS on the speaker works.
- **Display and keys**: clock digits and the ring work; +/− change the volume, the Alice key starts a
  voice session (logs "no HA satellite connection" until HA adds the Wyoming satellite), the mute key
  and switch work. Input device names: `aw933xx`, `mute`.
- **Audio out**: `name='speaker Acm8625 AMP mute status' 0` + `aplay -D default` → **audible**. As on
  bergamot, `cget` of the mute flag keeps reading `1`; dmesg shows `acm8625_spk_onoff_work: mute=0` while
  playing. `Master` boots at 255 (+5 dB) — the profile sets `audio.volume_default = 90` (~30 % on the
  curve) until the user picks a volume. `quasar.cfg` has no `platformToAlsaMap`; the profile uses
  bergamot's (same amp and hardvold curve, `volumeDbLevels` 22.3 … 75.7 dB vs 21.9 … 73.9).
- **Mic switch is a hardware cut**: with it on, every capture channel on `hw:0,1` and `hw:0,2` reads exact
  `0` at any gain, while no software is listening to the switch (maind off; the GPIO is only a
  `gpio-keys` input). Off: floor ≈ −59 dBFS on `hw:0,1` at `PDM Gain` 24, ≈ −47 dBFS on `hw:0,2`
  (`Loopback PDM Gain` 24). Both gains boot at 0.
- **Capture layout**: `hw:0,2` must be opened as **4 channels** = ch0–2 PDM mics + ch3 speaker
  reference (what maind's audiod does; DT loopback `datain_chmask 0x07` + `datalb_chmask 0x01`). Opening
  it as 8 channels interleaves two hardware frames (the first survey's "pairs"). ch3 is digital zero at
  idle; that it carries the playback is still to be confirmed with sound. Silent-room floor at 4ch:
  −68 dBFS with both gains 0, −64 with `PDM Gain`/`Loopback PDM Gain` 24 (both affect it, weakly).
  `hw:0,1` ("mics") is a 4ch mics-only view (ch1 ≡ ch2 there), not used.
- **Wake word**: aarch64 `spotter-host serve -P pickle -N 4 -M 0,1,2 -R 3` with the built-in
  `activation/alisa` + `command` models → «Алиса, сколько времени?» recognised and answered (speech at
  ≈ −43 dBFS RMS, floor −65). No downloaded models or `vqe_models` needed.

> Static analysis only (no vendor code executed). Runtime facts come from the logs that were inside the
> dump's `/data` (one boot with the stock stack running on an internet-less lab Wi-Fi). **[O]** = observed
> in a file/log/string, **[I]** = inferred. Re-verify on the device before relying on a number.

Pickle is **not** a variant of bergamot: different SoC (A4 vs AXG), 64-bit userland, newer kernel,
Buildroot and quasar generation, different recovery scheme. Treat everything bergamot-specific
(paths, binaries, the unmute recipe, armhf builds) as not applying until checked.

## Dump & how to read it
- 11 MTD partitions, sha256 in `dumps/pickle/MANIFEST.txt`. Layout (from dmesg, `meson-partitions`):

  | mtd | name | size | content |
  |---|---|---|---|
  | 0 | bootloader | 4 MiB (separate NAND area) | encrypted U-Boot (BL2/BL3x) |
  | 1–3 | bl2e / bl2x / ddrfip | 2 MiB each | encrypted boot stages / DDR FIP |
  | 4 | tpl | 12 MiB | encrypted TPL |
  | 5 | tee | 2 MiB | jffs2 (`/tee`, ro) — empty (all `0xff`) on this unit |
  | 6 | aml_sysrecovery | 128 MiB | **Amlogic USB-burn package (factory image)** |
  | 7 | recovery | 16 MiB | Android boot img, kernel+ramdisk **encrypted** |
  | 8 | boot | 16 MiB | Android boot img, kernel **encrypted** (entropy 8.0) |
  | 9 | system | 128 MiB | UBI → UBIFS `rootfs` (mounted `ro`) |
  | 10 | data | 188 MiB | UBI → UBIFS `data` (`/data`, rw) |
- `system`: `ubireader_extract_files` works (ubi_reader 0.8.10 in a scratch venv).
- `data` was dumped **while mounted** (index not committed) → ubi_reader finds "no inodes". Recovered
  with an index-free node scanner, [`tools/ubifs-scan`](../../tools/ubifs-scan/README.md) (walk every
  UBIFS node, keep the newest `sqnum` per key, rebuild the tree from dent nodes). Everything came back,
  including the logs.
- **Device tree is readable** even though the boot/dtb partitions are encrypted: the sysrecovery
  package carries `meson1.dtb` = gzip → Amlogic multi-DTB header (`AML_`, 2 KiB) → plain FDT.

## Identity
- DT `model = "Yandex Pickle EVT"`, `compatible = "yandex,pickle-evt", "yandex,pickle", "amlogic, a4"` [O].
  `platform.conf`: `Platform:0x0812 soctype:A4`.
- SoC **Amlogic A4**, 4× Cortex-A53, ~512 MB RAM (455 MB available to Linux) [O].
- Kernel **5.4.283** (Arm GNU Toolchain 12.2), cmdline `ubi.mtd=system root=ubi0:rootfs rootfstype=ubifs
  ubi.mtd=data … androidboot.selinux=enforcing` [O].
- **Userland aarch64** (`ld-linux-aarch64.so.1`, **glibc 2.36**), Buildroot **2023.11.1**, BusyBox 1.36.1,
  `BUILD_TYPE=user`. Kernel has 32-bit EL0 support, so static armhf binaries should run too, but
  anything that `dlopen`s vendor libs (spotter-host → `libyandex_evil.so`) must be **aarch64** [O/I].
- Firmware `3.210.14.1.4.3990575998.20260402.factory-14` (quasar `softwareVersion 0.14.1.4…factory-14`) —
  a **factory build**, the one shipped in `aml_sysrecovery` [O].
- quasar `deviceType: pickle`; hostname `Yandex-Mini-gen3-Pro`; AP name `Yandex-Station-Mini-3-Pro` [O].
- rootfs UBIFS `ro`, **no dm-verity**, "unauthenticated mode" → the bergamot remount recipe should work [I].
- **No `/bkupgrade`** — the persistent rw area is `/data` (`ubi1:data`). `S40wifi` falls back to
  `/data/wpa_supplicant.conf` when `/bkupgrade` is absent [O].
- `S00rw-partitions`: if `/data` isn't mounted at boot it **truncates and recreates** the UBI volume.
  Anything we keep on `/data` dies with a factory reset and with any `/data` mount failure [O].

## Boot chain (`/etc/init.d`)
`S00hostname S00rw-partitions S01rw-dirs S01rw-firmware S01seedrng S01syslogd S02klogd S02sysctl S10mdev
S30dbus S30pd_controller S35iptables S39w1_wifi_ant_config S40bluetoothd S40net.lo S40wifi S41dhcpcd
S42acm8625-settings S46hardvold S48tee-supplicant S49chrony S49sleep S50hci0 S50keymaster-svc S52bluealsa
S53bluealsa-aplay S54default-bluez-agent S61leds-current-coefficients S70unbound S80light-sensor
S90clean-ota S93i2ctop S94alsa S95yandex-debugctl S99quasar`.
- `S99quasar` → `onboot.sh start` (iptables `yio_*` chains, `set_monotonic_time.sh`,
  …) → `run.sh` → `quasar_launcher2 launch.json`. **Stock `run.sh` has no gate.**
- `launch.json` tasks (all the same multicall `maind`, 51 MB aarch64 PIE): `telemetry`, `fluent-bitd`
  (order 0), `maind --platformd` (order 1), `updatesd`, `mediad` (order 3). There is **no separate
  `wifid`** process — Wi-Fi, network, sync, firstrun, etc. are modules inside `platformd` [O].
- `S70unbound`: local **unbound** resolver; maind's `unbound_enabler` rewrites `/data/unbound.conf.head`
  from the backend config (forwarders `77.88.8.{1,8,88,2}` = Yandex DNS) — see Connectivity [O].
- `S49chrony` starts chronyd only if `/data/chrony_is_enabled` exists (maind creates it on first run) [O].
- `etc/monit.d/*` exists but there is no monit binary (dead config) [O].

## Hardware (DT + dmesg)
| Block | Part / bus | Notes |
|---|---|---|
| Speaker amp | **ACM8625** `3-002c` (TDM-A, I²S) | same amp family as bergamot; `S42acm8625-settings` applies `pickle-acm8625-psy02272002.cfg` |
| Mics | **PDM into the SoC** (`snd_pdm`, codec `soc:dummy`) + loopback | no external ADC |
| Logo LEDs | AW21018 `1-0020` | 6 RGB, `pickle:{red,green,blue}:logo{0..5}`, trigger `ledfb-logo` |
| Ring LEDs | AW21012 `1-0021` + AW21036 `1-0034` | **16 RGB**, `pickle:{green,red,blue}:ring{0..15}` (**GRB order** in the DT), trigger via `ring` ledfb |
| Clock | AW21036 `2-0034` | 4-digit 7-seg `pickle:white:clock{A..H}{0..3}` + `clockDL/DH/DM/DEGREE` (note the extra `H` segment vs bergamot) |
| ledfb | `yandex,ledfb` | `logo` (6) and `ring` (16) framebuffers → `/dev/fb0`, `/dev/fb1` + backlight |
| Touch keys | AW93303 `3-0012` (`aw933xx`) | input `aw933xx`: 115 vol+, 114 vol−, 582 Alice (same as bergamot) |
| Mute | `gpio-keys` "mute" | `mute-key` KEY_MUTE (113), `mute-switch` SW 14 (SW_MUTE_DEVICE) — same as bergamot |
| Light sensor | LTR-308ALS `3-0053` | IIO `iio:device1` |
| Accelerometer | QMA6100P `3-0013` | **probe failed -11** on this unit |
| RTC | PCF8563 `3-0051` | `rtc0`, sets the system clock at boot (no battery known) |
| USB-C PD | HUSB238 `3-0008` | `S30pd_controller` negotiated **12 V / 2 A** |
| EEPROMs | 24C02 ×3 (`main`, `bottom`, `clock`) | `pcba_ids` driver → `/sys/devices/platform/pcba_ids/pcba_ids_drv/pcba_ids` (JSON) |
| Wi-Fi / BT | Amlogic **W1** (`vlsicomm`, SDIO) + BT over UART (`hciattach … aml`) | multi-SKU `S40wifi` (Unisoc/Realtek/NXP/Amlogic/W2L) |
| Zigbee | `yandex,zigbee-control` (reset/boot GPIOs), Telink NCP (maind `telink_ncp.cpp`) | idle without maind |
| Watchdog | `meson-a4-wdt` | **fed by the kernel** (`feeding watchdog mode: [kernel]`); nothing opens `/dev/watchdog` |
| Disabled in DT | Ethernet, IR receiver, IR blaster | |

Boot-time detail: the ring/logo bus (`i2c@68000`) starts **owned by the always-on CPU** (pre-boot
animation). `S93i2ctop` applies `/boot/yandex-pickle-i2ctop-overlay.dtb` (`yandex-apply-dtb-overlay`) to
hand it to Linux, then waits for `/dev/fb0` [O].

## Bottom pogo pads — the "cradle" module connector (research 2026-10-23)
The spring-contact pads on the outside of the bottom are the connector for Yandex's snap-on
expansion modules, which replace the stock bottom cover (latches + magnets). Public sources
(reviews, vendor help pages): a **battery module** (YNDX-00651, 2500 mAh) and a **climate module**
(YNDX-00652: CO₂, temperature, humidity). With a module attached the power cable goes into the
**module**, so the pads carry power *into* the speaker as well as data. The pinout below is
inferred, not measured.

What the firmware says:
- **USB host with nothing on it** [O]: two USB 2.0 controllers — `crg21drd`/`crgudc2` (phy1,
  `crg_force_device_mode=1`, the gadget used by `yandex-debugctl`) and `crg2drd` → `xhci-hcd` (phy0,
  host only, bus 1, root hub with **2 ports**, both empty). The case has no USB-A, and the kernel has
  `cdc_acm`, `cp210x`, `usb-storage`, `usbhid`, `snd-usb-audio` built in.
- **Separate bottom board** [O]: `pcba_ids` lists EEPROMs `main` / `bottom` (board_code 2) / `clock`.
- **maind `cradle` subsystem** [O] (`smart_devices/libs/cradle/…`): protobuf request/response
  (`cradle/protos/request.proto`, `response.proto`) — `SyncEndpoint` (device_id, pcba_id),
  `FwVersion`, module firmware OTA (`StartWriteFirmware` / `WriteFimwareChunk` / `ApplyFirmware`,
  `/tmp/cradle_ota/`), button events, animation, brightness, and capabilities `Co2Data` (+ calibration,
  forced value), `TemperatureData` (+ correction), `HumidityData`, plus not-yet-shipped `VocData`,
  `NoxData`, `ParticulatesData` (PM1.0/2.5…), `PowerDeliveryData` (`selected_pdo_current`).
- **`riesling`** = the battery module platform [O]: fuel gauge **CW2217**, charger **IP2366**,
  battery type/temperature limits, even raw `ChargerI2CRead/WriteByte` passthrough.
- **Transport** [O/I]: `HdlcManager` (`smart_devices/libs/hdlc/hdlc_manager.cpp`) — HDLC-framed
  protobuf over a tty (`Hdlc created on port=`, `exception from tty`, `Unknown hdlc response,
  port=`, followed by `ttyACM`). `quasar.cfg` names only `/dev/ttyS2`, which is the Zigbee NCP (with
  `zb-reset`/`zb-boot` on GPIOT_18/19). → The module's MCU most likely enumerates on the xHCI port as a
  **USB CDC-ACM** device (`/dev/ttyACM*`) [I].
- Other UARTs, for completeness: ttyS0 (`fe07a000`, AO debug UART, pins set by the bootloader,
  `console=` empty, no users), ttyS1 (Bluetooth `hciattach`), ttyS2 (Zigbee). Many GPIOs are
  unclaimed (GPIOD_0/1/5–9, GPIOX_7–10/15, GPIOT_12/14/16/20–22, GPIOE_0/1, AO_1, AO_5,
  `GPIO_TEST_N`), but the DT doesn't mark any of them as external.

Likely pads [I]: GND, VIN (the module negotiates PD on its own; the speaker itself takes **12 V**
on USB-C — expect up to 12 V, don't short anything), USB D+/D−, perhaps a detect pin. Unknown whether
the speaker supplies 5 V to the pads by itself (whether a bare USB device would get power from it).

Uses for the CFW:
1. **Climate module → HA**: yastationd opens `/dev/ttyACM*`, speaks the cradle protocol and publishes
   CO₂/temperature/humidity over MQTT. The `.proto` descriptors are compiled into maind and should be
   extractable; the HDLC framing details (escaping, CRC, baud for CDC is irrelevant) still need reversing.
2. **Battery module → HA**: charge level / charging state the same way.
3. **Generic USB host**: a pogo→USB-A adapter would give USB audio, a Zigbee/Thread dongle,
   storage, or a DIY sensor board (any MCU acting as a CDC device).

To verify: attach a module and watch `dmesg` / `lsusb` (bus 1, VID:PID, `/dev/ttyACM*`) over SSH,
with the vendor stack still gated. Without a module: map the pads with a multimeter (GND, VIN, a
D+/D− pair at ~0 V with ~15 kΩ to GND from the host pull-downs). Don't export/drive unclaimed
GPIOs blindly: a pin the bootloader muxed for UART or boot mode could be broken, and pickle has no
physical fallback access.

## Audio
- `/etc/asound.conf`: `default` = asym, playback `speakers` (plug → dmix `ipc_key 1024` → `hw:0,0`, 2ch),
  capture **`loopback`** (`hw:0,2`: mics + speaker reference); `mics` = plug → `hw:0,1` 4ch; there is
  also an `ultrasound` PCM (96 kHz) [O].
- Volume: `S46hardvold` maps `Master Playback Volume` 0..255 → `speaker Acm8625 AMP ChL/ChR Volume`
  −55..+5 dB (same curve as bergamot). **At hardvold start the amp sits at +5 dB (Master=255); maind
  lowers it ~10 s later.** Without maind the speaker stays at max until we set it [O].
- Mute: driver default unmuted (`acm8625_spk_onoff_work: mute=0` on S94's priming `aplay`, before quasar);
  no sticky flag to clear seen [O]. Still verify by ear.
- Mics: the kernel brings up the PDM clock/PLL when a capture device is opened (no clock-holder process
  needed, unlike Midi) [O]. `S94alsa` would set `Loopback PDM Gain` from `/etc/alsa-controls.conf` — that
  file **does not exist**, so the gain is whatever the driver defaults to [O]. Measure it.
- VQE (static RE of the aarch64 `libyandex_evil.so` + maind) [O]:
  - maind calls `YandexVqeC_Config_CreateFromPreset("pickle")` (a preset containing `@` would go to the
    new `CreateFromJsonPreset`). "pickle" is built from immediates in the lib's preset-table static init,
    so `strings` misses it, and its factory pointer is **the same as "bergamot"'s** — identical tuning:
    3 mics, 1 HW speaker reference, 48 kHz in, 16 kHz out, frame 256 (input chunk 768 @48k).
  - Classic `Create/Process` on the CPU. The new `DspClient_*` API (Amlogic HiFi4 offload) is unused —
    its `libhifi4rpc*.so` are not in the rootfs.
  - maind feeds the VQE `float = raw s16` (no 1/32768 scaling); spotter-host's default scale worked too.
  - ABI vs `spotter-host/vqe_api.h`: only `Config_GetOutputChannelCount` differs (`size_t *out`). Outputs:
    `raw_mic` 3, `raw_spk` 1, `vqe_0` 1, `vqe_bnr` 1, `vqe_gain` 1, `vqe_bf` 0.
  - Runtime needs: `libyandex_evil.so` + `libtflite.so` (no libiconv), GLIBC_2.17, plain ARMv8-A.
- Spotter (static RE) [O]:
  - ABI matches `spotter_api.h` except the `opt` slot of `spotter_create*`, which is a `bool` here.
    New multi-conf API (`spotter_multi_conf_*`, `spotter_create_from_multi_conf`) — maind uses only that
    one, with activation + vad + intonationInterruption + counteractivation + command components.
  - Built-in models: `activation/alisa` needs channels {0 omni, 3 bnr, 4 raw-mic} (thresholds
    0.81/0.405), `activation/yandex` {0, 4}, `command` {0} (17 single words + light/TV/BT on/off, like,
    dislike; some per-command thresholds are very low — maind only enables it in context), `vad`,
    `intonationInterruption`, `counteractivation` (TFLite). Only counteractivation calls TFLite.
  - Expect more false accepts than stock: maind's thresholds assume counteractivation + cloud validation.

## Connectivity & Wi-Fi recovery (why it flaps without internet)
Everything below lives in `maind --platformd`. **Nothing in the init layer** (`wpa_supplicant`, `dhcpcd` +
its hooks, `unbound`) checks for internet [O]. Addresses are `maind` ELF vaddrs (aarch64, Ghidra
`-noanalysis` + targeted decompile).

### 1. The internet signal: `WifiEndpoint` generate_204
- URL = `http://` + `common.robotBackendHost` + `/generate_204` → `http://scbh.yandex.net/generate_204`,
  overridable by `wifid.generate204.url` (absent on pickle; bergamot instead had `controlUrls`) [O,
  `0x215029c`]. Other `wifid.generate204` keys: `periodSec`, `failPeriodMs`, `maxFailAttempts`,
  `periodOnLastFailAttemptMs`, `timeoutMs`. An empty URL → "No internet checker, probably url is empty."
  [O, `0x2154ef0`].
- Backend sets `wifiSettings.generate204.periodSec = 300` [O, logged "Apply new generate204 period:300"].
  Failing checks retry much faster (every ~18–38 s in the log). Result → `internetReachable_` OK/FAIL
  (`handleGenerate204Result`, `0x21553c4`) → broadcast as "internet availability".
- The same `WifiWorker` config has its own recovery timers: `rescanTimeoutSec`,
  `waitForInternetAfterConnectSec`, `checkScanBlockedTimeoutSec`, `emptyScanTimeoutMin`,
  `emptyIpTimeoutMin`, `wrongIpTimeoutMin`, `emptyNetworksTimeoutMin` [O]. Defaults not extracted.

### 2. NEW on pickle: `WifiCapability` — account-synced networks + hop on "no internet"
- The backend pushes `set_wifi_configuration_directive` with the **Wi-Fi networks saved in the Yandex
  account** (`account_config.user_wifi_config.wifi_hash`; `TNetworkInfo{Ssid,Psk}`). WifiCapability adds
  them to wpa_supplicant (first `bssid any`, then pinned to the BSSID seen in a scan) and **wpa_supplicant
  saves them to `/data/wpa_supplicant.conf`** (`update_config=1`) — they stay after maind is gone [O].
  In the dumped boot three account networks appeared (the lab AP, the home AP, a laptop hotspot).
- On connect without internet: "No internet connection for network X yet, schedule priority lowering";
  after **`waitConnectionDelayMs` (default 60000)** still FAIL → `handleNoInternet` (`0x21680c0`) lowers
  the current network's priority and calls `reassociateOnNoInternet` (`0x216826c`, "Start reassociation:
  noInternet") or falls back to the previous network → **the station hops to another saved SSID**. In the
  log it hopped between two APs every ~80 s for 6 minutes, until generate_204 turned OK [O].
- **Safety:** this can carry the speaker onto a network that *does* have internet (any saved home AP or
  phone/laptop hotspot) — bypassing the router block → `check_updates` → OTA. Treat every SSID
  in the account as a potential leak.
- Config (`parseSystemConfig`, `0x2171660`, from the backend `system_config.wifi_capability`): `enabled`
  (default **false**), `enabled_v2` (overrides), `waitConnectionDelayMs` 60000,
  `connectPrevNetworkTimeoutMs` 180000, `forcedConnectionDurationMs` 300000, `checkWifiHashPeriod` 3600,
  `extendedLogging*`; a tri-state `forcedEnabled` overrides both [O]. The backend sent
  `{"enabled_v2":true,"reassociateWhenNoInternet":false}` — **`reassociateWhenNoInternet` is not parsed by
  this maind at all** (no such string in the binary), which is why it hopped anyway [O]. Same for the
  backend's `wifiSettings.enableInternetRecovery` / `disableWlan0Monitor` — dead keys on factory-14 [O].
- State persists in `/data/quasar/data/wifi_capability.lmdb` (last directive, manually connected net).

### 3. `WifiReseter` (same family as bergamot's InternetRecovery)
- Config `common.wifiReseter` in `quasar.cfg` (+ backend `system_config.wifiReseter`, `{}` here):
  `minResetIntervalSec 120`, `noInternet {intervalSec 120, method old}`, `noCheckUpdate {intervalSec 30,
  method hard}` [O]. The parser (`0x1d38644`) also knows **`noUniproxy`, `noResolve {method, periodSec,
  hosts}`, `autoReset`, `dailyReset {time}`, rx/tx counter triggers (`minTx`, `minRx`, `maxTxErrors`,
  `maxRxErrors`) and `sixClicksEnabled`** [O]. Each trigger is armed only if present with a non-zero
  interval **and** a known method: `method` → `hard`=2, `old`=1, anything else = 0 = "Trying to reset with
  unspecified method. Ignoring." [O, `0x1d38bac`]. So removing a trigger key (or setting `method:"off"`)
  disables it.
- Actions: `old` → `ip link set wlan0 down/up` / dhcpcd restart path; `hard` → `doHardReset`
  (pickle `wifi_switcher`: `/etc/init.d/S40wifi stop|start`, `S50hci0 stop|start`; generic code also has
  PCI remove/rescan) [O strings, mapping per method I]. Switcher experiments: `experimentalReset`,
  `enableIpLinkReset`, `enableDhcpReset`, `enableReassociateReset`, `enableOldReset` [O, `0x1d33ea4`].
- In the dumped 17-min window no "Reseting wifi" line appeared — WifiCapability's hops kept resetting
  the state first [O/I]. With a single saved network the reseter would be the next thing to fire.

### 4. Other network-touching bits
- `unbound_enabler`: backend `unbound {enable, forwarders 77.88.8.{1,8,88,2}, checkHosts [quasar, uniproxy,
  scbh]}` → writes `/data/unbound.conf.head` and `/data/unbound_enabled`; dhcpcd hook `21-unbound.conf`
  then points `resolv.conf` at `127.0.0.1`. If none of `checkHosts` resolves it removes the flag
  ("Returning to external DNS") and asks networkd to reconfig [O]. With maind off and no flag → plain
  DHCP DNS.
- A `wifiConfigReload` (e.g. on config/`manualSetNetworkGroup` change) does a locally generated deauth
  (reason 3) ~2 s after the first lease [O].
- chrony time-over-HTTP to the same generate_204 URL; uniproxy ping/pong timeouts; ICMP pinger to
  `quasar.yandex.ru` (`monitord`) [O].
- The "169.254 link-local" seen before is **`dummy0`/`p2p0` IPv4LL**, and dhcpcd moves the default route
  to `dummy0` whenever `wlan0` loses carrier — a symptom of the flap, not a separate fault [O].

### How to stop it
1. **Preferred — don't run maind** (next section). No checker remains anywhere; `wpa_supplicant` +
   `dhcpcd` just hold the link. Before that, **prune `/data/wpa_supplicant.conf` to the one intended
   network** (wpa_supplicant alone will otherwise still roam to the account SSIDs if the lab AP drops).
   This is a Wi-Fi change → only with a physical fallback ready.
2. **If maind must run for a while:**
   - satisfy the check on the LAN: the rig answers `HEAD/GET /generate_204` with 204 for
     `scbh.yandex.net` (DNS + port-80 redirect, the bergamot recipe) — with OK internet status
     WifiCapability never hops and WifiReseter's `noInternet` never arms; or set `wifid.generate204.url`
     to a LAN responder in `quasar.cfg`;
   - in `quasar.cfg` (rootfs remount): drop `noInternet`/`noCheckUpdate` from `common.wifiReseter`.
     `noCheckUpdate` can't be satisfied locally (HTTPS `check_updates`) — it must be removed, not faked.
   - remove the extra SSIDs from the Yandex account / `wifi_capability.lmdb`.

## Running without maind
Verdict: **safe to disable outright** — no hardware/boot initialisation depends on quasar [O]:
amp (kernel probe + `S42`), PDM mics (kernel opens the PDM clock on capture open), LEDs/clock (kernel +
`S93i2ctop` + `S61`), touch (kernel loads its own fw), light sensor (`S80`), PD (`S30`), Wi-Fi/BT (`S39–S53`),
watchdog (kernel-fed). No init script waits on quasar. maind's own reboot paths (monitord hanging
services, launcher "apply quasar.cfg patch" reboot, "suicide request") disappear with it.
- **Gate** in `system/vendor/quasar/run.sh` (keep `run.sh.orig`), flag on `/data` (no `/bkupgrade`):
  `[ -f /data/cfw/maind_off ] && { echo "quasar: maind_off gate"; exit 0; }`. Keep `onboot.sh`
  running (it does more than start the stack). A factory reset wipes
  `/data` → the flag goes → stock behaviour returns (fail-safe). Optional second guard: `chmod -x maind`;
  never delete `maind` / `libs/libyandex_evil.so`.
- Prefer the gate over `maind_killafter`: there is no hardware reason to let maind start, and its first
  minute is exactly when the reseter/WifiCapability/updatesd act.
- What maind does touch at runtime (all optional for us) [O]: lowers the volume ~10 s after hardvold
  (dmesg 18.4 s); `AmplifiersTuner` applies backend-downloaded amp profiles
  (`/data/quasar/data/amplifiers/cache/`, empty on this unit); `TouchConfigHandler` writes a
  backend-supplied config to `/sys/module/key_aw933xx/…/3-0012/config` (no `touch_config` stored);
  `yandex-chrony enable` on first run; toggles the Zigbee reset/boot GPIOs and flashes the NCP; a generic
  backend-driven `alsaControls` mechanism exists but no local config feeds it. The first ~40 s of maind's
  own log had rotated out of the dump, so that window is covered by dmesg only.
- Replicate after `S46`/`S94`: `amixer -c0 cset name='Master Playback Volume' <n>` (**else the amp stays at
  +5 dB**), measure and set `Loopback PDM Gain`. Time: chrony only if `/data/chrony_is_enabled`;
  yastationd's SNTP covers it.
- Capture: `-D loopback` (`hw:0,2`, mics + speaker reference) or `-D mics` (`hw:0,1`, 4ch).

## Open questions
- Mic channel mapping (with speech + playback), a measured PDM gain, the speaker-reference channel,
  VQE `pickle` preset resolution, an aarch64 spotter-host build; ring pixel order around the circle.
- Whether an OTA also rewrites `aml_sysrecovery`; what the (encrypted) recovery checks before it
  restores. The package format and the restore takeover: [permanent-install.md](../permanent-install.md).
- Accelerometer probe failure (`-11`) — hardware or timing.
- Bottom pogo pads: confirm the pinout and the CDC-ACM enumeration of a cradle module; extract the
  cradle `.proto` descriptors and the HDLC framing from maind (see "Bottom pogo pads").
