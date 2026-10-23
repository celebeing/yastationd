# bergamot — Station Mini 3

Profile: `yastationd/internal/board/profiles/bergamot.conf` · Survey: `dumps/survey/bergamot-2026-10-22.txt`
(local only) · Status: **supported** (everything below verified on-device 2026-10-22).

## Identity
- device-tree model `Yandex Bergamot EVT1`, compatible `yandex,bergamot-evt1 yandex,bergamot amlogic, axg`
- Amlogic AXG, 4x Cortex-A53 1.2 GHz, ~240 MB RAM; **armhf (32-bit) userland, glibc 2.30, arm64 kernel 4.9**
- quasar `deviceType: bergamot`; hostname `Yandex-Mini3`

## LED ring
- 6 RGB pixels, per-pixel sysfs: `bergamot:{red,green,blue}:logo{0..5}`, max 255, default trigger
  `ledfb-logo` (taken over with `none`). Also `bergamot:logo:sync` and `cucumber:sync:clock-0`.
- Physically ~12 mm across: fine for colour/animation, useless as a level bar (volume is shown on
  the matrix instead). All effects (pulse, comet, blink, rainbow, dot) are distinguishable.

## Front display
- 4-digit 7-segment: `bergamot:white:clock{A..G}{0..3}`, colon = `clockDH`+`clockDL`, dot `clockDM`,
  degree `clockDEGREE`, max 255. Change-only writes → no flicker.

## Inputs
| Device (`N: Name=`) | Code | Physical control | Action |
|---|---|---|---|
| aw933xx (capacitive) | 115 KEY_VOLUMEUP | + | volume_up |
| aw933xx | 114 KEY_VOLUMEDOWN | − | volume_down |
| aw933xx | 582 KEY_VOICECOMMAND | Alice key | voice (push-to-talk) |
| gpio-keys | SW 14 SW_MUTE_DEVICE | mic-mute switch | mic_mute |
| gpio-keys | 113 KEY_MUTE | (with the switch) | mute_key → re-read the switch |

`input_event` size auto-detected (both layouts handled).

## Audio out
- `/etc/asound.conf`: `default` = plug → dmix (`ipc_key 1024`) → `hw:0,0`, 48 kHz stereo. Use it;
  `plughw:0,0` played mono 48 kHz at ~2x speed.
- Volume: `Master Playback Volume` 0..255 is **hardvold's** user control (recreated at boot with its
  default — the daemon restores the saved value), mapped onto `main Acm8625 AMP ChL/ChR Volume` along
  -55 dB (0) … +5 dB (255), linear in dB. The stock curve (`platformToAlsaMap` `0:0, 1:33, 20:255`)
  is the profile's `audio.volume_map`: 5 % steps, the first jumps to raw 33 (-47 dB, everything
  below is inaudible), then ~2.75 dB per step. `volumeDbLevels` in `quasar.cfg` (the stock loudness
  per Alice level 0..10: 21.9 … 73.9 dB) matches that: ~5.3 dB per Alice level = 2 of our steps.
  Raw 0 is -55 dB, not a hardware mute.
- Unmute: `name='main Acm8625 AMP mute status' 0`. The ACM8625 driver ORs this sticky flag into its
  per-stream speaker on/off (dmesg `acm8625_spk_onoff_work`: start → `mute=flag`, stop →
  `mute=2|flag`). **cget readback does not reflect writes**; `SPK mute` has no audible effect.
- **Never SIGKILL a playing aplay**: it can leave dmix's semaphore held and wedge all later playback
  (recovery: `ipcrm -M 0x400 -M 0x401 -S 0x400`). yastationd uses SIGTERM.

## Mic array & voice front-end
- `hw:0,1`: 8ch S16 48 kHz (exclusive, no dsnoop). ch0–2 = PDM mics (equivalent, floor ~-61 dBFS at
  gain 0), ch3 = copy of ch2, ch4/5 empty, ch6/7 = −1 when idle (ch6 = amp feedback per asound.conf;
  **not yet seen carrying playback** → AEC unverified). ~1.5 s LF transient after open.
- arecord needs `--buffer-time=1000000 --period-time=20000` (the default buffer overruns).
- `PDM Gain` 0..48 boots at 0; floor gain +10 dB at 24, +23 dB at 48. At 24: speech ≈ -46 dBFS RMS
  at 1 m, -49 at 3 m (raw ch0); ≈ -40..-55 through the VQE.
- VQE: preset `bergamot` (3 mics + 1 speaker), 768-frame float chunks → 16 kHz outputs
  (`vqe_0`, `vqe_bnr`, gain, raw). 42 % of one core.
- Spotter: activation «Алиса» `8cf52356…` (layout 0,3,4 = omni, BNR, raw), commands `a23e7ada…`
  (27 phrases). VQE + both spotters ≈ 87–90 % of one core, ~20 MB.
- Real voice: «Алиса» 10/10 at 1 and 3 m, 0 false confirmations in the test; commands
  громче/тише/стоп hit, хватит/дальше/включи свет weaker.

## Sensors & extras
- IIO `ltr308als01` ambient light. Stock gain 3 / 0.1 s gives raw ≈ 2 indoors (0.2 steps); yastationd
  sets gain 18 / 0.4 s (the driver normalises `in_intensity_input`, so the scale stays). Against a lux
  meter next to the black unit: 1 lx → 0.033, 25 lx → 0.53, 37 lx → 0.57, office light (200 lx at the
  meter, shining onto the top) → 81 — direction/spectrum matter more than lux. Drives the clock
  auto-brightness (see yastationd README, open item 5). `meson-axg-saradc` (ADC).
- Wi-Fi + BT (bluealsa present). No Zigbee.
- **No RTC** (no `/dev/rtc*`, no `/sys/class/rtc`): the clock starts from scratch after a power loss (unit #2 came up 4 h 20 min behind). The vendor `set_monotonic_time.sh` only keeps it from going backwards (saved stamp in `/data/quasar/data/monotonic_time`); real time comes from chronyd or yastationd's SNTP once the network is up. (The Mini 3 Pro has a PCF8563 — see [pickle](pickle.md).)

## Firmware variants
Two units run the same profile (hardware identical: LEDs, inputs, mixer controls, mic layout).
| | fw `0.369` (unit #1) | factory fw `0.6.1.4` (unit #2, kernel built Nov 2024) |
|---|---|---|
| RAM | ~240 MB | ~490 MB |
| iptables | present | absent (we ship a static armhf bundle for the egress lock) |
| `libyandex_evil.so` | 4.9 MB, needs `libtflite.so`; `spotter_error_*` API | 3.4 MB, **older API**: no `spotter_error_*`, no error out-param (errors print and exit), `spotter_create_multichannel` takes 7 args |
| time sync | `chronyd` → LAN server | no chronyd; vendor `ntp_sync.sh` → public pools (blocked) → yastationd's SNTP |
| spotter models | downloaded to `/data/quasar/data/spotter_model/<uuid>` | none downloaded; built-in multi-stage `/system/vendor/quasar/spotter_models/activation/alisa/{0,1}` |

spotter-host is built against the 0.369 API, so on a 0.6.x unit we don't port it: we ship the
0.369 `libyandex_evil.so` + `libtflite.so` + `libiconv.so` (deps beyond that: glibc ≥ 2.16 only)
and the two 0.369 models to `/bkupgrade/cfw/vendor/{libs,spotter_model}` and point
`spotter_libs` / `wake_model` / `command_model` at them in `yastationd.conf`. Vendor blobs, so
they're not in git: copy them from a 0.369 unit or a firmware dump. Verified on unit #2: probe
OK (layout 0,3,4), `serve` READY.

## Open questions
- Does ch6 carry the amp feedback during playback → does the vendor AEC work (barge-in on «Алиса»)?
- Better lux resolution via `hardwaregain`/`integration_time`; auto-brightness curve.
- Wi-Fi latency after reboot (power save?).
