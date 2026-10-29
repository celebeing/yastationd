# Porting the CFW to a new speaker (agent guide)

How to research a speaker model we don't support yet and make `yastationd` (and the voice host)
work on it. Scope: **the CFW's functionality on the device**: hardware, audio, voice, sensors. How
the CFW gets onto a device is out of scope here. This guide assumes you already have a root shell on
the unit (SSH) and, ideally, a full firmware dump.

Read first: [boards/README.md](boards/README.md) (profile mechanics + checklist),
[boards/_template.md](boards/_template.md), and the two worked examples:
[boards/bergamot.md](boards/bergamot.md) (armhf, the reference) and
[boards/pickle.md](boards/pickle.md) (aarch64; a full port done from a dump + survey).

## Ground rules while porting

- **Read-only first.** Surveys, sysfs reads, `arecord` and mixer *reads* are safe. Mixer writes,
  LED writes and the gain controls are reversible (gone on reboot) but are still changes. Never touch
  Wi-Fi, the firewall, init/boot, or the vendor gate while porting the CFW features.
- **Sound only with the user's OK, and only on the unit they name.** Playing a tone, TTS or an alarm
  is audible in someone's room. Ask which speaker may make sound. Visual tests (LEDs, display) are
  fine, but say what will light up.
- **Don't start the vendor stack** to "see what it does". Static analysis of its binaries and its
  own logs (found on `/data` of a used unit or in a dump) is how we learn what it initialises.
- **Record as you go** in `docs/boards/<codename>.md`, marking every claim **observed** or
  **inferred**. Never commit dumps, surveys or per-device identifiers (MACs, serials, SSIDs).

## 1. Collect the inputs

| Input | How | Gives you |
|---|---|---|
| Board survey | `ssh … 'sh -s' < tools/board-survey.sh > dumps/survey/<codename>-<date>.txt` | DT model/compatible, LEDs, `/proc/bus/input/devices`, ALSA cards/PCMs from `/proc/asound` (no mixer dump: see below), `asound.conf`, IIO, power_supply, init scripts, vendor files |
| LED patterns | `ssh … 'cd /system/vendor/quasar && tar cf - ledconfigs ledpatterns' > dumps/survey/<codename>-leds.tar` | ring/display geometry hints |
| Firmware dump (if available) | `cat /dev/mtdN` per partition → `dumps/<codename>/` | everything offline: rootfs, configs, vendor binaries, the device tree |
| A boot log | `dmesg`, `/var/log/messages*`; on a used unit also the vendor logs under `/data` | who initialises which hardware, timing, PD contract, codec probe |

Unpacking a dump (see [boards/pickle.md](boards/pickle.md) "Dump & how to read it"):
- UBI/UBIFS: `ubi_reader` (`ubireader_extract_images`, `ubireader_extract_files`) in a scratch venv.
  A `/data` image taken while mounted has a stale index, so use the index-free
  [`tools/ubifs-scan`](../tools/ubifs-scan/README.md) to recover it, logs included.
- Device tree: boot/DTB partitions may be encrypted. An Amlogic recovery/burn package
  (`0x27b51956` header) can still carry a plain `meson1.dtb` (gzip → `AML_` multi-DTB → FDT at
  +2 KiB) → `dtc -I dtb -O dts`.
- The vendor's audio config (`system/vendor/quasar/quasar.cfg`, JSON) states the capture device,
  mic/speaker channel counts, the front-end preset name and the spotter model paths. It is the
  single most useful file for the voice port.

## 2. Map the hardware to a board profile

Copy `yastationd/internal/board/profiles/bergamot.conf` to `<codename>.conf`. Fill it section by
section. While iterating on a unit, put override keys in `<cfw>/board.conf` (no rebuild needed),
then fold them into the profile.

| Section / keys | Where the answer comes from | Watch out for |
|---|---|---|
| `name`, `model`, `match` | `/proc/device-tree/compatible` | `match` is one compatible string (`yandex,<codename>`), used for auto-detection |
| `platform.cfw_root`, `platform.userland_arch` | writable persistent mounts; `file` on a vendor executable | CFW root is `/bkupgrade/cfw` or `/data/cfw`; never create a missing `/bkupgrade`. Helper arch is the userland ABI (`armhf` or `aarch64`), not necessarily the kernel architecture |
| `platform.wifi_reset` | the firmware's `/usr/bin/yandex-wifireset` (what its `WifiWatcher` runs; `strings maind`) | the Wi-Fi controller reset the network watchdog runs before rebooting ([storage.md](storage.md)); empty = none |
| `platform.rtc_backed` | after a power cut of some minutes, `dmesg \| grep rtc` at boot ("setting system clock to …" right to the second, no low-voltage warning) | `true`: the clock shows the time before the SNTP check (orion) |
| `platform.stop_initd` | `ps w` with the vendor stack gated: vendor processes that still draw on a surface (boot-animation players) | init.d scripts whose own `stop` the daemon runs at startup (orion: `S31animation`) |
| `moment.<name>` | what the board's displays can show (its expressions and stock patterns), what it should sound like | per-board choice for a CFW moment (pictures, `sound:`/`phrase:`/`ring:` references; empty = nothing), overriding the display kind's defaults and the default theme ([display-expressions.md](display-expressions.md), "Moments"; [asset-packs.md](asset-packs.md)). The stock sounds are played by name from `<cfw>/sounds` (`make sounds`), so a board needs no sound keys |
| `matrix.backlight` | `/sys/class/backlight/*` whose device is the screen | a sysfs brightness (0..255) that dims the whole screen: the display level goes there and frames keep their half-tones (orion: `fb1`) |
| `matrix.min_brightness` | the dimmest level still readable in a dark room, on glass | the dimmest auto brightness unless `auto_brightness_min` is set, the default curve's darkest point, and the manual level's floor (bergamot, pickle 4; orion 2) |
| `matrix.default_mode` | — | the front display mode of a fresh state: `clock` (default), `clock_date`, `off` |
| `ring.kind/pixels/led/colors` | `/sys/class/leds` names, DT `led-controller` nodes and their labels | `{color}`/`{i}` pattern. Pixel index = DT label number; physical order around the ring may differ (check visually). A model may have a second small "logo" ring; pick the main one. Only `sysfs-rgb` exists today (see §5 for new kinds) |
| `ambilight.kind/pixels/led/colors/dies` | `/sys/class/leds` names and a visual index probe | Optional rear RGBW strip. `sysfs-rgbw` expands `{color}`/`{i}`/`{die}`; all listed dies at one logical position receive the same value. Verify physical index direction and whether dies are co-located |
| `ambilight.fb` | `/sys/class/graphics/fb*/name` | Optional: the ledfb framebuffer that takes the strip in one write (orion: `ambilight`). Worth it for effects: [ambilight-stream.md](ambilight-stream.md) |
| `ambilight.primaries`, `ambilight.response` | a measurement ([ambilight-color.md](ambilight-color.md)) | Optional colour calibration: R, G, B, W at full as `x y Y` (Y relative to W), and the drive level → light table. Without it the HA light's colours go out uncorrected |
| `ambilight.fb_format`, `ambilight.fb_compatible` | the fb's `bits_per_pixel`, its device-tree node's `compatible`, the driver's `of_match` strings | `rgb48` (B,G,R 16-bit LE; W on sysfs; default) or `rgbw64` (B,G,R,W). With `fb_compatible`, a runtime device-tree overlay sets the node's compatible and the driver is rebound (orion: `yandex,ledfb-rgbw64`); needs the kernel's configfs overlays |
| `matrix.*` | `/sys/class/leds/*clock*` or `/sys/class/graphics/fb*/name` | `seg7` = 4 digits × segments A–G (`{seg}{digit}`), colon LEDs, dot, degree. `dotmatrix-fb` = framebuffer whose name contains `LED screen`; geometry/stride/RGB offsets come from fbdev ioctls. Its animations are the firmware's screen patterns (`clips.dir` → `ledpatterns/screen`, mapped to expressions in `pixelAnims`, `internal/display/front.go`; `matrix.gamma` = the screen gamma from quasar.cfg `leds`); scenes without a pixel drawing fall back to the seg7 picture via `Seg7ToPixels`. `matrix.dev` optionally pins `/dev/fbN`. Extra seg7 segments (pickle has an `H`) can stay unused |
| `input.device`, `key.N`, `switch.N`, `abs.N`, `abs_range.N` | `/proc/bus/input/devices` (`N: Name=`, `B: KEY=`/`SW=`/`ABS=` bitmaps), `EVIOCGABS`, and isolated evdev captures | Names are matched **exactly**. Decode bitmaps without pressing anything, then capture each physical control with the vendor stack stopped. Key actions: `volume_up`, `volume_down`, `voice`, `mute_key`, `panel_gesture`; switch: `mic_mute`; absolute action: `volume_ring`. `volume_ring` normally obtains its range with `EVIOCGABS`; if captured values prove that metadata wrong (Orion reports 0..3 but emits 0..1000), pin the verified `min,max` with `abs_range.N` |
| `audio.playback/card/volume/volume_max` | `asound.conf`, named controls (`amixer -c0 cget name=…`; the names from the firmware dump's configs and `dmesg`), `/etc/hardvold.config` | **Never `amixer contents`** (or `controls`/`scontents`, or a loop over every control): a full dump froze a bergamot's kernel in its codec driver ([diagnostics.md](diagnostics.md)); `tools/orion-verify.sh` takes one only with `FULL_MIXER=1`. Prefer the stock dmix `default`. The master control is often created by a vendor volume daemon from its own config |
| `audio.unmute` | codec controls, `dmesg` of the amp driver while playing | **`cget` readback can lie** (it does on the ACM8625: it reads `1` while the driver logs `mute=0`). Verify by ear, with the user's OK |
| `audio.volume_map` | `quasar.cfg` `volume_capability.volume_manager_settings.platformToAlsaMap` | Stock curve, `level:raw` points, 20 levels = 100 % (`changeStepPercent` 5). HA's percent, the keys (one level each) and the display all follow it. Without it the curve is a straight line over the raw range, which leaves the inaudible bottom as a dead zone. No map in the config: compare `volumeDbLevels` with a model that has one |
| `audio.volume_default` | what the master boots at | pickle boots its amp at the maximum (+5 dB). Set a sane first-start level |
| `audio.mic_gain`, `mic_gain_value` | PDM/ADC gain controls | Gains often boot at 0. A hardware mic switch can cut the mics entirely (exact zeros); check the switch before concluding that capture is broken |
| `mic.device/channels/rate/voice` | the vendor capture device (§3) | Open the device with the channel count the hardware frame really has |
| `wake.*` | §4 | |
| `power.pd/min_mv` | `/sys/class/power_supply/*` | Each PD read makes the driver re-query (and log), so poll rarely. Below `min_mv` the `power_problem` moment plays the stock `no_power` line (vendor sounds may be opus even when named `.wav`; `make sounds` decodes them) |
| `lux.iio_prefix/gain/integration_time` | `/sys/bus/iio/devices/*/name`, `*_available` | Behind a grille the stock gain gives a couple of counts; raise gain/integration |
| `lux.calibration` | a lux meter next to a unit, [sensor-calibration.md](sensor-calibration.md) | `reading:lux` points: the board's base light calibration (a unit may override with `lux_calibration`) |
| `mic.spl_offset` | a sound level meter next to a unit (the firmware's `quasar.cfg` `spl.splCalibShift` is a hint, not a value to copy: its dBFS need not be ours) | dBFS → dB SPL for the room sound sensors |

Verify in this order (each step depends on the previous one): display
(`cmd/fbleddemo -clock` or `-uwu`, when framebuffer-backed), ring
(`cmd/ringdemo -board <codename>`), keys (the daemon log at `log_level = debug`), audio out,
volume mapping, mic levels, then the wake word.

Code paths that are profile-agnostic but have bitten new boards:
- **LEDs appearing late.** pickle's ring bus is handed from a co-processor to Linux by a late init
  script, so the daemon's first start can fail and the supervisor respawns it. That's harmless, but
  check that the second start succeeds.
- **CFW root.** `<cfw>` is `/bkupgrade/cfw` if `/bkupgrade` exists, else `/data/cfw` (daemon, init
  hook and the vendor gate all use this rule). Don't create `/bkupgrade` on a model that lacks it:
  stock scripts branch on its existence.
- **Architecture.** `yastationd` is static armv7 and runs on 64-bit userlands whose kernels have
  32-bit EL0 support (`CPU features: detected: 32-bit EL0 Support`). Anything that links vendor
  libraries must match the userland (`file /system/vendor/quasar/maind`).

## 3. The audio path

How the CFW uses audio (independent of the model):
- **Out:** `aplay -D <audio.playback>` per sound (local files, the alarm, Wyoming TTS streams). The
  unmute lines run before each playback.
- **In:** exactly one `arecord -D <mic.device> -c <mic.channels> -r <mic.rate> -f S16_LE`
  while anyone needs audio. It runs with a large buffer (1 s buffer / 20 ms periods; smaller
  ones overran). Its raw frames are fanned out to the voice host (§4) and to the raw-voice
  fallback (`mic.voice` channel, decimated to 16 kHz). `mic.rate` is per board — any
  1..6 multiple of 16 kHz (48 kHz on bergamot/pickle; 96 kHz on orion via the stock 13ch
  `mroute`), decimated in-process by `hw.Decimator`.

Finding the capture layout for a new model:
1. Read the vendor audio config: the capture device name, `micChannels`, `spkChannels`, rates,
   period size, and the channel names it expects (`raw_mic_N`, `raw_spk_N`).
2. Resolve the device through `asound.conf`. The vendor often opens `default`, whose capture side is
   a raw `hw:` PCM, and that's the one to use.
3. Count channels per hardware frame. On Amlogic, the DT loopback node's `datain_chmask` (mics) +
   `datalb_chmask` (playback loopback = speaker reference) give it: pickle is 0x07 + 0x01 = 4
   channels. **Opening a PCM with more channels than the frame interleaves several frames**, which
   shows up as channel "pairs" with correlation ~0.9. That was a false lead on pickle.
4. The vendor's channel splitter order is `[mics | unknown mics (skipped) | speaker refs | vad]`
   (static RE of pickle's `ChannelSplitter`). Confirm the reference by playing a tone (with the
   user's OK): the reference channel must follow the playback, and it reads digital zero when idle.
5. Measure with `arecord` + per-channel RMS/correlation (silence, speech at 1 m and 3 m), with the mic
   switch off. Pick the gain from speech levels (target around −30..−45 dBFS RMS at 1 m), not from the
   noise floor.

## 4. The voice front-end (wake word, echo cancellation)

`spotter-host` `dlopen`s the vendor front-end library from the device (never copied into the repo)
and runs:
- a **VQE** (Kalman echo canceller against the speaker reference + noise reduction), built from a
  named preset, taking the mic channels plus the reference;
- one or two **spotters** (activation model, command model) fed from VQE outputs.

### Contract with yastationd (any replacement must keep it)

| Channel | Content |
|---|---|
| stdin | raw interleaved S16LE device frames at the preset's input rate (`-N` channels; 48 kHz, orion 96 kHz) |
| stdout | lines `READY`; `WAKE <ms> <phrase>`; `CMD <ms> <phrase>`; `SUBHIT <WAKE\|CMD> <ms> <phrase>` (near miss); optional `LEVEL …` |
| fd 3 | the clean voice stream, 16 kHz mono S16LE, streamed to HA's STT during a session (with pre-roll) |
| args | `serve -P <preset> -N <channels> -M <mic channels> -R <ref channel> \| -Q <ref channels> -a <activation model> [-k <command model>]`, built from the profile's `wake.*` + `mic.channels` |

So **plugging in a different audio stack** (another model's front-end library, or an open one such
as webrtc AEC3 + an open wake-word engine) means a new host binary that speaks this contract. Point
`spotter_bin` at it. The daemon doesn't change.

### Porting the vendor front-end to a new model

1. **Architecture and build.** `tools/build.sh spotter:armhf|aarch64` with `SPOTTER_SYSROOT_<ARCH>=<rootfs>` (`tools/build.env`; runs `spotter-host/build.sh` in its container). armhf
   links against the device's own glibc; aarch64 is a plain dynamic link with RPATH
   (`--disable-new-dtags`, so the library's own dependencies such as `libtflite.so` resolve).
   Add a new `ARCH` case if a model needs one.
2. **ABI check, never assume.** The library drifts between firmware builds. Diff the exports
   (`nm -D --defined-only`) against what `spotter_api.h`/`vqe_api.h` declare, and statically check
   argument counts and out-param widths for each function the host calls. Deltas found so far:
   - an older build had no `spotter_error_*` and a 7-arg `spotter_create_multichannel`;
   - on aarch64, `YandexVqeC_Config_GetOutputChannelCount` writes a `size_t`;
   - the `opt` slot of `spotter_create*` is a `bool`.
   Record deltas in the headers' comments.
3. **Preset.** Find how the vendor resolves the preset name from its audio config. On pickle it is
   compiled in (built from immediates, so `strings` misses it) and shares the bergamot factory. A
   name containing `@` goes through `CreateFromJsonPreset` instead. Get mic count, speaker count,
   rates and frame size from the preset and make `wake.mics`/`wake.ref` match — multi-ref
   presets (speakers > 1, e.g. orion) take `wake.refs`, all channels in VQE order.
4. **Models.** Use the device's own models: downloaded ones under `/data/quasar/data/spotter_model/`
   or built-in ones under `system/vendor/quasar/spotter_models/`. `spotter-host probe <dir>` prints
   the channel layout each model needs (e.g. activation `0,3,4` = VQE omni, VQE BNR, raw mic;
   commands `0`).
5. **Run and verify** with `spotter-host listen -v` on live capture before wiring it into the
   daemon. Check recall at 1 m and 3 m, then false accepts over hours of normal room audio. Stock
   thresholds assume extra vendor stages we don't run (counteractivation, server re-validation),
   so expect more false accepts. Tune by copying a model dir and editing `thresholds` in `flags.txt`.
6. **Where «Алиса» ends** (`wake.preroll_ms`): each model fires its own time after the word ends.
   Turn `wake_capture` on, say «Алиса, сколько времени?» a dozen times at different speeds, label
   the captures (Vosk, see [wake-word-boundary.md](wake-word-boundary.md)), and set the profile's
   `wake.preroll_ms` a little above the median gap between the word's end and the WAKE (bergamot:
   ~110 ms → 150; orion: ~170 ms → 220). Without it the CFW's default (300 ms) applies, which leaves the end of the wake
   word in HA's transcript.

## 5. When the profile is not enough

- **New ring/display hardware** (a framebuffer LED panel, an SPI strip, a dot matrix): add a
  backend in `internal/hw`, a new `ring.kind`/`matrix.kind` in `internal/board` (+ `Validate`), and
  make `internal/display` effects size from it, and a `display.Front` for a new kind of front
  display. The Station 2's 89×1 checkerboard panel needs this ([boards/yandexmidi.md](boards/yandexmidi.md)).
- **New sensor or radio:** a new profile section + an `internal/hw` reader + an HA entity in
  `internal/mqtt/discovery.go`. Model it on `power.*` / `lux.*`.
- Keep every model difference in the profile; code should only learn *kinds* of hardware.

## 6. Definition of done

- `docs/boards/<codename>.md` from the template: identity, each subsystem with observed/inferred
  marks, the verification results, open questions. Added to [boards/README.md](boards/README.md) and
  [index.md](index.md).
- The profile is committed, `go test ./...` passes (it parses every built-in profile), and the board
  is auto-detected on the device.
- Verified on the unit: display, ring, every key, audio out (by ear), volume, mic levels; for voice,
  push-to-talk through HA, then the wake word.
- Known gaps listed rather than hidden (e.g. "speaker reference not yet confirmed with playback").
