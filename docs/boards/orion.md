# orion — Yandex Station 3

Amlogic A5 (a113x2-class, 4x Cortex-A55), aarch64 userland, kernel 5.4, quasar 3.21.
Device tree: `yandex,orion-pvt yandex,orion amlogic, a5`. Sources for this page: the
on-device survey (`dumps/survey/orion-2026-10-23.txt`) and static analysis of the vendor 3.21
tree (maind, libyandex_evil.so, quasar.cfg, asound.conf …; `dumps/orion/quasar/`, its `/usr/lib`
in `dumps/orion/usr_lib/`, on-device audio checks in `dumps/orion/research/`), all local only
and not committed.

Status: playback, volume, mic capture, circular hardware controls, LED ring and the 21×12
front-screen daemon backend **verified on device 2026-10-23/24**. Both `fbleddemo -uwu`
and the production clock renderer remain stable with no flashing; the clock advances normally.
**Installed on the user's unit 2026-10-26 (initial support, v0.8.0):** the voice pipeline with
the stock «Алиса» model and its NPU verifier, the rear ambilight entity, and the screen playing
the firmware's own patterns for the expressions and the call / alarm / boot / setup states (seen
on glass; [display-expressions.md](../display-expressions.md), "Screen patterns") all work; see
"First unit on hand". Since then: our own pixel clock font (v0.8.1) and text fonts (v0.11), the rear ambilight with
DDP effects and colour calibration; open: the intercom is not yet checked on this board.
**Mic retest 2026-10-24 (both tested units; two 30 s pure-capture passes each): all
six channels are healthy.** The earlier "dead capsule" reading from one 4 s clip was a
transient (see "Mic capture"); it does not block on-device wake/VQE work.

## Audio out

- Card 0 `AMLAUGESOUND`. Playback chain `default -> speakers` (plug → dmix → `hw:0,1`
  "TDM-B acm8625-speakers", 6ch S16 @ 48 kHz, period 1920). A separate `us` amp drives
  the **ultrasound transducer** on `hw:0,0` (TDM-A, 96 kHz); `S94alsa` primes it with
  1 s of silence at boot (that materializes the `Ultrasound Playback Volume` softvol).
  quasar.cfg (3.21) ships a complete but **disabled** sonar stack around it
  (`ultrasound.enabled=false`, detector type `debug`): a 38.4 kHz sine generator
  (amplitude 0.05, 4 slots with [0,1] zeroed) and a Doppler detector (2048-sample
  frames, 600 Hz band, thresholdDb −70). libyandex_evil.so exports the detector C API:
  `YandexVqeC_UltrasoundDetector_{Create,AddChannelData,Process,Free}` — spotter-host
  could dlopen it exactly like the VQE. maind names the full pipeline:
  `UltrasoundModule` + `HighLevelUltrasoundDetector`, sine **and impulse** generators,
  `doppler_detection` events over IPC. Verified 2026-10-24 by host-side FFT of the
  verify2 raws (local `dumps/`): the two ultrasound **capture** channels (mroute
  idx 11-12) are dead LSB unless a TDM-A stream is up — they are the `us` codec's own
  TDM-A capture slots, not mics (details in "Mic capture"). Sonar ideas on this dormant
  rig: wishlist.md §"Orion's ultrasound rig".
- Three ACM8625 amps: `fr1`, `fr2`, `wf` (front L/R, rear L/R, woofer) — a 3-way system.
  Each has `ChL/ChR Volume` (−103..0 dB in 0.25 steps), `Post Gain`, `AMP mute status`,
  `Playback Ramp Volume`.
- Vendor volume: `hardvold` maps `Master Playback Volume` (0..255, integer) onto the six
  ChL/ChR controls via volume curves in `/etc/hardvold.config`; the amp mutes
  (`… AMP mute status`, 1=muted) are also under its control, `value_unmuted = 0`.
  We drive `Master Playback Volume` and unmute the three `AMP mute status` controls
  before playback (same approach as bergamot/pickle, ×3 lines).
- `S94alsa` boots amp ramps to −6 dB, `PDM Gain`/`Loopback PDM Gain` to 24, and starts the
  ultrasound silence stream.
- **Verified on stock stack (2026-10-23):** a 1 kHz beep through `default` at
  `Master Playback Volume` = 40 → dmesg shows `acm8625_trigger` start/stop on all three
  amps, hardvold mapping 40 → digital 187 before play and back to stock 168 → 321
  after, `spk_onoff_work: mute=0` during play, `2` after stop, `3` idle. By-ear both
  beeps audible (vol 90 during capture, vol 40 after); the second beep changed volume
  mid-beep — hardvold applies volume **asynchronously (~100 ms)**: cset <100 ms before
  aplay starts the beginning at the old volume. The daemon should apply volume, wait
  ~150 ms, then play — or accept the ramp. Our three unmute csets are the same ones
  hardvold honors; with the stock stack up they are redundant during playback (hardvold
  already unmuted) and ensure function after conversion. The mute readback prints 1
  regardless — dmesg is the truth.

## Mic capture

- The PDM mic array arrives only through the TDM **loopback** (`hw:0,3` LOOPBACK-A, 14ch
  @ 96 kHz): the 'datain' side of the loopback carries the mics, 'datalb' the speaker
  feedback. `Loopback datain source` = `PDM` (verified) — the mic routing into the
  loopback is kernel-side and needs no userspace setup.
- Stock capture "default" → `mroute` (plug) → `mcapt` (multi): 13 channels interleaved at
  96 kHz = loopback ch {0,2,3,4,5,6} **mics**, ch {8,9,11,12,13} speaker refs, + ultrasound
  ch {0,1}. quasar.cfg: `micChannels 6, spkChannels 7, inRate 96000, mainChannel vqe_0`, and
  the channel names `raw_mic_0..5, raw_spk_0..6` (audiosender).
- **Verified on device (2026-10-23, stack stopped, raws + host-side RMS/Fourier):**
  `mroute` opens 13ch/96k and captures 4.0 s exact (9984000 B). The played 1 kHz tone
  appears at exactly **1000 Hz on all six mic channels (idx 0 loudest: 140 RMS in the tone
  window vs 20-40 floor)** and on all five refs (15-31 RMS). Silence floor 20-40 RMS,
  DC ≈ 0 → `mic.voice = 0` (idx 0 = loopback ch 0) confirmed, `Loopback PDM Gain` 24 kept.
  Ref channels are silent (0.0) with the stack stopped - speaker refs only start flowing
  when TDM-B playback is up. A later **pure-capture retest** (2026-10-24: two complete
  30 s `mroute` captures, 150 s apart; 74880000 B each) **cleared mic idx 5** on the
  first tested unit: RMS 27.4 vs 28.1-30.3 on its five peers in capture A and 43.8 vs
  37.8-48.4 in B. The second unit independently passed the same test: idx 5 was 17.2
  vs 17.4-22.0 in A and 41.6 vs 40.4-46.4 in B; B's 196-232 peak-RMS activity was
  shared by every mic, consistent with room noise. The earlier first-unit 4 s idx-5
  spike (RMS 3061, low-frequency junk) was therefore transient — either a local
  acoustic/electrical event or a one-off transport glitch, not a dead capsule. All six
  mic channels are valid VQE inputs; retain raw captures if it recurs.
  The ultrasound pair (idx 11-12) is **dead LSB in every capture** (RMS 0-1 flat,
  no 1 kHz leakage) — those slots are the `us` amp's own TDM-A RX lines; they only
  carry data with a live TDM-A stream. Not captured here because the stock S94alsa
  ultrasound aplay stops after 1 s, so no TDM-A stream was up during the captures.

### Vendor bad-mic detector

`quasar.cfg` enables `audiod.brokenMicDetector` on Orion:

```json
{
  "enabled": true,
  "rmsRatioThreshold": 0.8,
  "rmsRatioExceededCount": 25,
  "sendAudioData": false
}
```

This is a vendor **RMS channel-consistency watchdog**, not an absolute floor or a
calibration measurement. The `0.8` ratio direction, analysis window, 25-observation
cadence/consecutiveness, and post-detection action are not present in the config or
recovered symbols. In particular, do not assume it removes a channel from VQE or emits a
useful event to us. Its temporal count is still the reason a one-off anomalous frame must
not become a hardware diagnosis: compare complete raw captures at separate times first.
The 2026-10-24 two-pass retest did exactly that and cleared the earlier idx-5 anomaly.

Generic detector limits and the bergamot instance: docs/research/wakeword-mic-array.md §2.3
“Vendor bad-mic detector”.

- CPU cost of the stock 96 kHz plug chain: arecord via `mroute` 13ch **110 jiffies / 5 s
  ≈ 22 %** of one core vs **60 jiffies ≈ 12 %** at 48 kHz (measured; re-measured 2026-10-28:
  23 % of a core, against 13 % for the Mini's arecord). Reading the hw device directly instead of
  `mroute` would save ~5 % of the whole CPU; not needed. On top, in-process 6× decimation costs ~0.2-0.3 % per the Go Decimator's
  48 kHz numbers — well within budget.
- **The load average sits 1.0 higher than the CPU use** (2026-10-28: LA ~2.4 against ~1.1 cores
  busy; a Mini's LA ≈ its busy cores): the stock kernel's `vmap_thread` (pid 36) waits in
  `msleep` in the D state for good, and Linux counts D tasks in the load. It used ~0.03 % CPU in
  9 h. Nothing to fix (the kernel stays); read load as CPU% from `/proc/stat`, not LA. The real
  load matches a Mini's: spotter-host ~0.75 core, arecord 0.23, yastationd 0.12; 70 % idle,
  SoC 36 °C.
- hw:0,2 "PDM-dummy-mics" is **not dead, only exclusive**: the A5's audio DDR manager
  offers a single PDM `tddrs[0]` slot, and the loopback (fed from PDM) claims it whenever
  the vendor audiod runs - direct hw:0,2 then fails ENXIO (`snd_pdm: failed to claim
  irq 0 / claim to ddr / can't open component -6`). With the stack stopped it opens fine
  (6ch × 96 kHz, verified in run 2; dmesg `tddrs[0] registered by … pdm`). Not used
  anyway: its data carries DC ≈ +60 LSB and a much hotter noise floor than the loopback
  copy - the vendor's capture was right.

## VQE / wake (in progress)

- The preset names are compiled into the aarch64 `libyandex_evil.so` from immediates
  (mov/movk clusters - `strings` shows nothing): rev1..rev5, orion, nebula, mini43/50/55/65,
  cucumber, bergamot, pickle, plum, potato, mango, found via the static init chain at
  0x34cec8-0x34dc90.
- **Confirmed by running the 3.21 blob in an arm64 container** (OrbStack + debian;
  `YandexVqeC_Config_CreateFromPreset` via dlsym - no device needed):
  orion = **mics 6, spk 7, in_rate 96000, out_rate 16000, proc_frame 512, chunk 3072**
  (= 32 ms per Process() at 96 kHz). The VQE takes the raw 13ch mroute stream natively.
  Tension with earlier static RE (the orion factory at 0x34e694 passed 16000 to the
  config init): the runtime contract is authoritative — 96 kHz in, 16 kHz out; the
  static read was likely an internal 16 kHz sub-config, not the kernel-facing rate.
  Same-generation cucumber: 4/4, 96→16 kHz, frame 512. The fruit presets
  (bergamot/pickle/plum) stay 48→16 kHz, frame 256.
- **Spotter models** (3.21 `spotter_models/`, probed via `spotter_conf_read` in container):
  `activation/alisa` version `ru-RU-cucumber-alisa-02Aug24-bnr-verifier-no-ca` - wants
  **omni + BNR + raw-mic @ 16 kHz** (type0/type3/type4 = 1 each), like bergamot's active
  model; `command` = single omni channel. Three load-time facts, pinned by strace/gdb:
  - the full conf read fails NOT at `verifier-model` parse but inside `libnnsdk.so`
    (`verifier-lib /usr/lib/libnnsdk.so`, `model.nb` = `VPMN` int8 net, 16.9 MB):
    nnsdk is an **Amlogic NPU library** — it calls `aml_read_chip_info()` at init, which
    needs the real `/dev` NPU device. Off-device it segfaults; on the speaker it should
    **load. The 2nd-stage «Алиса» verifier is NPU-accelerated hardware.**
  - the *error slot* the 3.21 blob fills is the library's WARN-log buffer (plain C
    string), NOT an error object: `spotter_conf_read` returning NULL is the real failure
    signal; the buffer carries `WARN : Key "am-tune-shifts"/"tf-tdnn" is deprecated`
    notices. With those two flags REMOVED from flags.txt, the conf reads clean -
    the warnings alone were polluting the slot.
  - `vad-features/lingware/` is probed ENOENT-benign (pristine model dir doesn't ship it
    either on this fw).
  Verdict: vendor alisa CAN run 1st-stage in container (verifier stripped locally +
  deprecated flags dropped) - full listen verified, LEVEL lines healthy, no false hits on
  4 s of tone/silence. On device (2026-10-26) the pristine model, NPU verifier included,
  loads and creates as a 3-channel spotter; two spotter-host fixes were needed for it: the
  3.21 WARN-log buffer is no longer treated as an error (the call's NULL return is), and
  `probe` no longer calls the single-channel `spotter_create`, which segfaults in this blob
  for a multi-channel model. A third one for the daemon: with a verifier the spotter reports
  `1ST_STAGE_HIT` and then the verifier's `CONFIRMATION_2ND_STAGE` (or `REJECTION_2ND_STAGE`),
  never `CONFIRMATION_1ST_STAGE`, so `serve` now emits `WAKE` on either confirmation. A live
  60 s test in the user's room: 14 of 14 spoken «Алиса» confirmed, one verifier rejection, no
  false wakes in the silent half; room-noise floor raw ≈ −70 dBFS, speech raw −61…−65 dBFS. Fallback remains: strip the verifier (re-threshold; more false accepts) -
  bergamot §6.4 tradeoff in docs/research/spotter-decompile.md.
- **Runtime noise-event spotter (stock unit, captured 2026-10-25):** `spotter.json` declares one
  downloaded model with `type = noise_detection`; its UUID, CDN URL and binary stay only in
  gitignored `dumps/`. `words.txt` maps labels `1..8` to clapping, snapping, crying, alarm, glass,
  dog, cat and water. It is a 1.8 MB float `nnet` (not the NPU verifier): 16 kHz, 25 ms frames,
  10 ms shift, 40-bin log-mel fbank, no VAD, with per-class thresholds
  `0.316:0.238:0.957:0.345:0.0953:0.452:0.97:0.833`. This makes local sound-event detection
  viable through the vendor spotter API. An arm64 container probe of the preserved model loads it
  successfully at 16 kHz with exactly one channel of type 0 and no other channel types;
  `spotter_create` and plain layout `0` both succeed. It can therefore consume VQE omni directly.

- **3.21 blob drops the `spotter_error_*` C helpers** (0.369 bergamot has them; 3.21
  exports 41 spotter symbols, none of them error getters). spotter-host declares them as weak
  references, so one source links both: they resolve to the library on 0.369 and to NULL on
  3.21, where an error prints the WARN-log buffer from the slot instead. (Weak *definitions*
  would not work: the executable's own copy wins over the library's, breaking 0.369.)
- **Wake chain verified offline in the container with real run-2 audio (2026-10-23):**
  `spotter-host vqe -P orion -N 13 -M 0,1,2,3,4,5 -Q 6,7,8,9,10,11,12 < cap-tone.raw`
  → 4.00 s × 16 kHz × 4ch out, 1000 Hz tone present in vqe_0 (Goertzel), raw_mic RMS 75;
  config out_channels: raw-mic 6, sync-main 1, raw-spk 7, vqe_0 1 (+bnr/gain at runtime).
  `listen … -k command` runs the full chain with the command spotter (no verifier needed)
  and exits clean. The speaker refs (hw:0,3 ch 6..12) are live even with the vendor stack
  stopped — refs ride the loopback capture, so AEC works even mid-song on our stack.
- Profile wake keys are wired (verified offline; turned on by `wake_enabled = true` in
  yastationd.conf): `wake.vqe_preset = orion`, `wake.mics = 0,1,2,3,4,5`,
  `wake.refs = 6,7,8,9,10,11,12` (multi-ref boards; bergamot/pickle keep `wake.ref`), and
  yastationd builds spotter-host serve with `-P orion -N 13 -M 0,…,5 -Q 6,…,12`
  instead of the old `-P -N -M -R`.

## Inputs

- The top surface has two concentric capacitive areas driven by `aw933xx` (event3); only the
  separate mic-mute control is a physical switch.
- A standalone central glyph tap emits KEY 582 (`KEY_VOICECOMMAND`) press/release → local voice
  turn / HA `voice_command`. A horizontal inner movement is indistinguishable: it also emits only
  a standalone 582 press/release.
- A standalone inner arc or circle emits KEY 581 (`KEY_SCREENSAVER`) press/release, regardless of
  direction. It is exposed as the debounced HA `panel_gesture` event; no ambilight or other action
  is hardcoded.
- **Vertical inner gestures (captured on device 2026-10-25):** direction is encoded by the order
  of the first distinct key-down events, not by an axis: up = 582 then 581; down = 581 then 582.
  The opposite key arrives in 60–380 ms in six fast/normal samples of each direction. The driver
  can duplicate a same-code key-down, emit releases between the two presses, and accompany the
  sequence with `ABS_MT_SLOT` / `ABS_MT_POSITION_X`; those records carry no usable direction.
  A future recognizer must defer standalone 581/582 actions for a 500 ms pairing window, ignore
  duplicate same-code downs, and suppress both constituent actions after recognizing a pair. It is
  not implemented yet.
- Deeper outer groove: `ABS_X` wrapped angular position, verified with the vendor stack stopped.
  Clockwise values increase and counterclockwise values decrease, crossing zero near 1000. The
  kernel incorrectly reports this axis as 0..3 through `EVIOCGABS`, while captured events span
  0..992; the profile therefore pins `abs_range.0 = 0,1000`. yastationd accumulates wrapped
  deltas. **The ring has about ten pads per turn** (captured 2026-10-26): it reports the finger
  mostly at the pads' centres (100, 200, …), with only ±20 of interpolation around them, and
  `ABS_MT_SLOT`/`ABS_MT_POSITION_X` alongside — there is no finer position. So a step is one pad
  crossing, and its size follows the speed like a mouse wheel's acceleration: ≥350 ms since the
  previous crossing 1 %, ≥200 ms 2 %, ≥110 ms 3 %, faster 5 %. (It was one stock 5 % level per
  1/20 turn: through hardvold's dB-linear Master and the stock curve that is ~4 dB a step
  mid-scale, and a quick turn jumped loud; a plain 1 % per 1/100 turn then snapped to the pads.)
- `mute` (event2) is a physical mic-mute switch and remains authoritative. **Direct capture
  2026-10-25:** switching on emits KEY 113 down, SW 14 = 1, KEY 113 up; switching off emits the
  same key press/release around SW 14 = 0. The switch cuts the microphones.
- `input_btrcu`: Bluetooth remote KEY 116 + 133. `vad_keypad` is vendor-internal and ignored.

## Bluetooth / BLE

- **Stock unit baseline, 2026-10-25:** the standard BlueZ controller is powered, neither
  discoverable nor pairable, and supports both central and peripheral roles plus five advertising
  instances. It advertises A2DP source/sink, AVRCP controller/target, and standard GAP/GATT/device
  information UUIDs. No BLE advertisement is active.
- The reusable Linux base processes are `hciattach -s 115200 /dev/ttyS1 aml`,
  `bluetoothd -n`, and `bluealsa -p a2dp-sink -p a2dp-source -p hfp-ag --sbc-quality=high`.
  `bluealsa-aplay -D bluetooth -M disable -S` owns the stock audio route. `bluetooth` is an ALSA
  `softvol` PCM over `speakers`, controlled by card-0 `Bluetooth Master Playback Volume`; thus
  A2DP reaches the stock speaker routing without `maind`. `bluealsa-aplay` has no device-address
  restriction, so it accepts audio from any Bluetooth device that BlueZ has connected. `aplay -L`
  has no statically named `bluealsa` PCM before a source connects.
- `ble_initd`'s vendor GATT setup service is not reusable: it accepts Wi-Fi and token writes.
  Keep Bluetooth disabled/discoverable-off by default under CFW; pairing must be a deliberate,
  local action.

## Zigbee

- **Stock unit probe, 2026-10-25:** the kernel registers a dedicated `zb_control` platform driver
  for `/sys/devices/platform/zigbee`; this is physical Zigbee control hardware, not only a cloud
  capability. The `/zigbee` device-tree node is `yandex,zigbee-control` and declares
  `zb-boot-gpios` and `zb-reset-gpios`; it declares no NCP transport. Both use GPIO-controller
  phandle `0x0f` with active-low flag: boot is pin 84 and reset is pin 82.
- **NCP boundary:** stock `quasar.cfg` identifies a Telink NCP on `/dev/ttyS2`, with vendor
  firmware `${QUASAR}/zigbee/gateway.ybl` (217,692 bytes) and `zbControl = true`. `ttyS1` remains
  Bluetooth. The serial protocol is the proprietary W2L/Telink protocol implemented inside
  `maind`, not a standard EZSP or ZNP adapter. With `maind` stopped, `ttyS2` is unowned.
  Preserve the stock firmware and private network state; do not bind, unbind, reset, flash, pair,
  or open the network while investigating. A CFW coordinator would require a clean W2L host
  implementation or an explicitly approved, reversible firmware replacement.

## Lux / power

- Persistent writable storage: two ext4 `rw` partitions, `/data` (2.6 GB) and `/ota` (1.4 GB).
  The firmware ships `/bkupgrade` as a symlink to `/ota` (dated with the firmware build), so the
  CFW root is `/bkupgrade/cfw`, like bergamot: every hook, `run.sh`'s gate, `egress.sh` and the
  daemon's fallback probe `/bkupgrade/cfw` first, and the install kit puts its files there. `/ota`
  is the OTA staging partition; with updates blocked nothing rewrites it. What a factory reset
  does to `/ota` and `/data` is not characterised yet.

- Ambient light: ltr308 (I2C 0-0053) registered as IIO `light-sensor-top`, `in_intensity_*`
  only; the stock S80light-sensor sets gain 18 / 0.4 s — max values, nothing to override.
  **Measured on device 2026-10-25:** gain 18, 0.4 s integration, 40 Hz sampling, zero offset,
  and `in_intensity_input = in_intensity_raw × 2.25`; dark readings were 4–10 raw (9–22.5
  input), while a room light adjusted through brightness and colour-temperature changes produced
  1006–3493 raw (2263.5–7859.25 input). These are intensity units, not calibrated lux. The
  physical aperture is the lower-left of the four top holes when facing the display: an opaque
  phone cover drives the reading to 0–1 raw. Finger-dependent high readings at the other holes
  were not reproducible and are treated as optical reflections, not additional sensors. Profile
  prefix: `light-sensor`.
- USB-C PD: HUSB238 (I2C 2-0008, `ya_pd`) as bergamot/pickle. **Measured on device
  (2026-10-23): contracts a 20 V / 3.25 A PD (voltage_now 20000 mV, current_now 3250,
  status "Good")** — orion is a 65 W-class supply, not bergamot's 12 V. Profile
  `min_mv 15000` distinguishes a real 20 V PD source from 9/12 V fallbacks; nothing in
  the vendor tree (quasar.cfg, scripts) carries a textual threshold, so 15000 is our own
  alert line, tuned to the measured contract.

## Sensors survey (2026-10-26)

A read-only pass over I2C, IIO, input, thermal and the device tree, for sensors worth exposing.

| Part | Where | What it gives |
|---|---|---|
| `ltr308als01` ambient light | I2C 0-0053, IIO `light-sensor-top` | one broadband channel (`in_intensity_*`); **no colour**. The device tree has no other light sensor (`light-sensor@53` is the only one), so an ambilight that follows the room's colour is not possible with this hardware. The vendor's "ambient lighting colour" features are voice commands, not sensor-driven; maind has a `darkeningLuxThreshold`. |
| `aw93312` capacitive sensor (`aw933xx` driver) | I2C 0-0012, input event3 | the touch top. Besides the input events, sysfs exposes **12 channels of raw capacitance** (map, captured 2026-10-26: **ch0-ch9 are the ring's ten pads in clockwise order**, ch10 the centre glyph, ch11 the inner area; a touch reads ~250k on a pad and ~700k on the centre, the noise ~±500, ch7/ch8 about ten times noisier; a shell loop reads `diff` at ~90-160 Hz; a hand swiped 3-5 cm over the top shows as 5-30k blips whose centroid over the ten pads moves the swipe's way, left→right ≈ 255° → 90° with pad 0 at 0°, but not every swipe registers and a front-back swipe did not; a slowly approaching hand is partly absorbed by the chip's own baseline tracking, with an undershoot when it leaves. A second run by height: **1-2 cm over the top, every left/right swipe registered (12 in 12 s, 7-70k against a ~1.7k noise p99) with its direction readable from the centroid's path**; ~5 cm, weak (3-12k) and the direction only sometimes; ~10 cm, nothing. A palm held still ~3 cm above reads ~50k and fades into the baseline in ~2 s, so the sensor sees a hand arriving, not a hand staying): `diff` (live deltas per channel, `DIFF_CHn = …`), `offset` (per-channel offset in pF), `mode_operation` (`Active`), `chip_info`. ~10 channels are the ring's pads; the others are the inner area. Unused so far: interpolating `diff` between neighbouring pads may give the ring a finer position than the input events' ten pads, and the deltas may show a hand approaching (proximity) before it touches. **Read only `diff`, `offset`, `mode_operation`, `chip_info`:** reading `reg` overflows the sysfs page in the vendor driver (4099 bytes; a kernel `WARNING` in `vsnprintf`, harmless otherwise), and `soft_rst`, `update`, `aot` (auto offset tuning) and `awrw` are actions, not values. |
| `pcf8563` RTC | I2C 0-0051, `/dev/rtc0` | a real-time clock, readable with `hwclock -r`; `rtc0` (the vendor kernel's sysfs has no `date`/`time` attributes). The kernel has `RTC_HCTOSYS=y` on `rtc0`, so the system clock comes from it at boot: after the reboot of 2026-10-25 the daemon's first lines ran with the network still unreachable (`time sync failed … network is unreachable`) and the clock already right (the SNTP check 30 s later: offset -1 ms). **It keeps time without power** (tested 2026-10-26): after ~10 min unplugged the kernel set the clock from it 2.9 s into the boot (`rtc-pcf8563 0-0051: setting system clock to …`) right to the second, with no low-voltage warning. A backup cell or a supercap, not opened to see; a supercap would last hours to days, so an outage longer than that is untested. So orion could show the right time before the network is up, where our clock now blinks its colon until the SNTP check. How the test was done: unplug for a known time (minutes for a supercap, hours for a cell), plug in, and read the head of `/tmp/yastationd.log` (its first lines are stamped before the network is up) and, within the first hour, `dmesg \| grep -iE 'rtc\|pcf8563\|hctosys'` (a lost clock shows as pcf8563's "low voltage detected, date/time is not reliable", or a 1970/2000 date). The vendor's own fallback, `set_monotonic_time.sh` (the last saved time or maind's build date), runs only with the vendor stack. |
| SoC temperature | `/sys/class/thermal/thermal_zone0` (`soc_thermal`) | millidegrees (38.6 °C idle); trips at 85 / 95 / 105 °C. A cheap diagnostic sensor for HA. |
| SAR ADC, 8 channels | IIO `meson-c2-saradc` | ch1 ≈ 768 mV, ch5 ≈ 468 mV, the rest near 0-85 mV. **No user found:** the device-tree node has no phandle (nothing references it), and in the firmware's rootfs the only file naming the channels is the ADC driver itself — no kernel module asks for an IIO channel, no binary or script reads `in_voltage*`. Board identification is the EEPROMs' job (below). The bootloader image is encrypted, so a U-Boot use (a recovery key, board straps) cannot be ruled out. To learn what ch1/ch5 are: log them against the SoC temperature and while touching, muting, replugging. |
| Hardware VAD | `/dev/vad`, `/sys/class/vad`, input `vad_keypad` | the SoC's low-power voice activity detector (Amlogic audio VAD); vendor-internal, unused by us. |
| 4 × `24c02` EEPROM | I2C 0-0052, 1-0052, 2-0051, 3-0050 | one per PCB, named in the device tree's `pcba_ids` node: `main`, `top`, `display`, `ambient`. An ASCII record with a part number, a revision, an assembly variant, a date and a production serial (not calibration data); the `pcba_ids` driver serves them as JSON at `/sys/devices/platform/pcba_ids/pcba_ids_drv/pcba_ids` (also `/usr/bin/pcba_id`). Stock `S94alsa` picks the PDM mic gain by the first digit of the `top` board's assembly (`ALSA_PDM_GAIN_<n>`, default 24). Treat the records as private (serials). |
| `husb238` USB-C PD | I2C 2-0008, `ya_pd` | already used (Supply voltage, Power supply problem). |
| `acm8625s` amps ×4 | I2C 2-002c, 3-002c/2d/2e | only `settings_file` in sysfs: no temperature or fault status exposed. |

Also: `/proc/net/wireless` is all zeros (the Wi-Fi driver does not fill it), so Wi-Fi signal and
rate must come from the driver's own tools. The Zigbee radio is in "Zigbee" above.

## LEDs

Three surfaces, every LED a Linux LED class device each mirrored by a framebuffer
(`ledfb` triggers) — same architecture as bergamot's logo bar, ×3 chips:

| Surface | sysfs | fb | geometry | chips |
| --- | --- | --- | --- | --- |
| Ring | `orion:{red,green,blue}:ring{0..35}` | `fb2` "ring" 36×1 | 36 RGB px | 3 × AW21036 @ I2C 2-0034/2-0035 (12 px each; expected A=0..11, B=12..23, C=24..35) |
| Ambilight | `orion:{red,green,blue,white}:ambilight{i}{a,b}` (i=0..11) | `fb0` "ambilight" 12×1 | 12 positions × 2 dies; **RGBW** (the only white channel) | own `ambilight{A,B,C}:sync` latch |
| Screen | — (fb only) | `fb1` "YNDX LED screen" 21×12 **(ioctl-confirmed)**, gowin FPGA | 252 px dot-matrix; glass lags one write behind — needs periodic re-send | plain `write()` works (verified 2026-10-24); no `S29gowin`/ipc needed |

- **Ambilight framebuffer** (`fb0`, 12×1, 48 bpp, stride 72; probed 2026-10-26): per position
  B, G, R as 16-bit little-endian, the LED level = value/2 (510 = 255), both dies following,
  for the LEDs whose trigger is `ledfb-ambilight`. No white channel — by configuration, not by
  hardware: the driver (`quasar_led_ring.ko`) has `ledfb-rgb48`, `ledfb-rgbw32` and
  `ledfb-rgbw64` formats, and the device tree's `ambilight-framebuffer` node says
  `compatible = "yandex,ledfb-rgb48"`; a white LED put on the trigger logs "ledfb: Using white
  color with RGB framebuffer. Check device tree configuration." and stays dark. The format cannot
  be changed at runtime (`FBIOPUT_VSCREENINFO` with 32 or 64 bpp: `EINVAL`, 2026-10-27), and the
  device tree is part of the boot chain we do not touch. But orion applies **runtime** overlays
  (configfs `device-tree/overlays`, `dtbocfg`): an overlay setting the node's compatible to
  `yandex,ledfb-rgbw64` plus an unbind/bind of `ledfb` gives a 64 bpp framebuffer, B, G, R, W
  16-bit LE, both dies (verified 2026-10-27, by eye and by readback; gone on reboot). yastationd
  does that at start (`ambilight.fb_format = rgbw64`, [ambilight-stream.md](../ambilight-stream.md)). One frame write = 3.5 ms
  against 25–34 ms for 96 sysfs writes; `ambilight.fb` uses it
  ([ambilight-stream.md](../ambilight-stream.md)).
- Latch pseudo-LEDs `orion:{ring,ambilight}{A,B,C}:sync` (trigger `ledfb-*-sync`): one
  per AW21036 in each chain. bergamot's `logo:sync` is never poked by our sysfs writes and
  the ring works — the same is expected here; `tools/orion-led-probe.sh` verifies it
  before the daemon relies on it.
- **Vendor pattern files** (`/system/vendor/quasar/ledpatterns/`, dumped): plain text,
  one frame per line — `RRGGBB` per pixel + trailing frame duration in **ms** (usually
  `20`; `ring/block.led` holds `10000`). Directive lines: `loop` marks where the
  repeating part starts — on the first line the whole file loops, further down the
  frames above it are a one-shot intro (`screen/call_incoming.led` line 51,
  `configure.led` line 368); `smooth <N>` (interpolation on, only the factory `qc`
  pattern on the screen); `no_size_optimization`. `ring/` = 36 px + duration,
  `ambilight/` = 12 px + duration, `screen/` = 252 px (21×12 row-major) + duration.
  No gamma in the files — the surfaces' gamma (ambilight 2.0, screen 2.2) comes from
  quasar.cfg's `leds` block.
- `ledconfigs/{red,blue}.json`: per-case-colour 3×3 colour matrices, read by maind
  (`maind.ledConfigsPath`), so not applied with maind gated; the case colour is a character of
  `device_id -r`. Both our units are `K` (black), which has no file. `S61leds-current-coefficients`
  is something else: it writes `current_coefs` of the clock's LED drivers (3-0021, 3-0037) for
  the `Y` and `G` cases only (`/etc/leds-current-coefficients.conf`). The ambilight's own
  calibration is ours: [ambilight-color.md](../ambilight-color.md).
- The profile wires the ring exactly like bergamot's bar (`sysfs-rgb`, trigger → `none`);
  the 36 px work with the existing stack/effect sizes — **no new backend needed, verified
  on device 2026-10-24** (`tools/orion-led-probe.sh`, log in the local `dumps/`):
  - sysfs brightness writes reach the glass with no `:sync` poke (sync LEDs stay at 0,
    never written);
  - `ring{i}` indices walk **clockwise** starting at the vendor's 0 — the effect stack's
    rotations read as-is, no `ring.reverse` knob; all 36 px live, none dead;
  - the 0/12/24 chip-third pixels look equally bright;
  - `fb2` accepts plain writes and the ring follows them (byte order RGB) — but we drive
    sysfs, not the fb, as the single-writer-per-surface rule needs.
- **Ambilight hardware probed on device 2026-10-24** — a 12-position strip on the
  **back** of the speaker, top-to-bottom in index order (0→11); the `a`/`b` suffixes
  are two dies of one position and are driven identically. It is RGBW (the only white
  channel): the white is a real emitter, visibly different from R+G+B at the same level, and
  RGBW together differs from both (checked by eye over DDP 2026-10-27). `ambilight.kind = sysfs-rgbw` exposes it as a separate persisted HA MQTT
  `light` with brightness and `rgbw` color mode; no voice/music overlays use it. The
  daemon path is host-tested but still needs an on-device smoke test.
- **Screen backend verified end-to-end on device 2026-10-24:** `hw.DotMatrix`
  discovers fb1 by the "LED screen" name and reads geometry / stride / RGB channel order
  from fbdev ioctls (21×12 confirmed). The glass lags one write behind, so a lone write
  does not replace the vendor's stale clock; the next one latches it. `matrix.kind =
  dotmatrix-fb` makes the daemon's existing 15 Hz display loop the continuous writer.
  `display.Seg7ToPixels` converts the board-independent `MatrixFrame` to a 3×5 pixel
  font doubled vertically (3×10 per digit), so clock, countdown, message text, marquee,
  brightness and overlays keep their existing semantics. On shutdown: blank → `Flush`
  so the stale frame does not remain. `fbleddemo -clock` verified that exact renderer
  and cadence on glass: correct orientation, stable image, live clock updates; `-uwu`
  remains the lower-level framebuffer diagnostic.
- **Screen animations (2026-10-25, blind):** the daemon's `PixelFront` resolves every
  expression to a pattern under `ledpatterns/screen/` (`clips.dir`), gamma-corrected by
  `matrix.gamma = 2.2`; the clock, text and numbers stay on the seg7 fallback above.
  The screen has its own 20 ms render tick (the patterns' frame time); at the shared 15 Hz
  loop two of three frames were dropped, and the wink's one-frame sparkles around the left
  eye (deliberate: a glint in the eye and particles flying off it, every frame of the file
  checked) were held three times too long and read as noise. On glass, check: the gamma (too dark or washed out → `matrix.gamma` in
  `<cfw>/board.conf`, e.g. `1`), smoothness at the 15 Hz render loop, the voice stages
  (`express/set listening`, `thinking`, `speaking`) and the call/alarm patterns.

## Screen work list (from the first days on glass, 2026-10-26)

- **Clock:** done — our own pixel font, plus the two-line `clock_date` mode
  ([display-expressions.md](../display-expressions.md), "The pixel clock").
- **Volume:** done — our number over a bar (`display/volume.go`). The stock `volume.led` was tried
  first: it is a continuous sweep through the tens (~20 frames of a clean "10", ~30 of scrolling to
  "20"), so a 5 % level lands mid-scroll, and `rcu_volume/volume_edge/10` shows "10" (the remote's
  0..10 scale) at 100 %.
- **Low brightness:** done — the level goes to the screen's backlight
  (`/sys/class/backlight/fb1/brightness`, 0..255, `matrix.backlight`). A/B on glass
  (2026-10-26), four bands of 100/50/25/10 % at 16 levels from 255 to 2: dimming the frame (gamma
  2.2, then the level, both in 8 bits) lost almost every band by the end; a full-scale frame under
  a dim backlight kept them faintly distinct down to 2. The backlight takes the level rounded up,
  the frame the rest (2/3..full scale), and is written one frame late (the screen shows a write
  late). The vendor never goes below 127 on it (quasar.cfg `brightness.minBrightness`); we go
  down to `matrix.min_brightness` 2. The frame tap shows frames scaled by the backlight.
- **Tooling:** the browser preview and the host simulator, [led-preview.md](../led-preview.md).
- **Volume curve:** done — the stock `platformToAlsaMap` in the profile's `audio.volume_map`
  (plus a 5 % point), on top of hardvold's own curves.
- **Light sensor:** calibrate the intensity units against real lux (later).

## Boot / egress

- FW 3.21 keeps the same init.d chain as 3.x: S94alsa, S95hardvold, S97quasar (starts
  maind via run.sh), plus S80light-sensor and S61leds-* on orion. Our maind_off gate
  carries over unchanged (orion's `run.sh` is byte-identical to bergamot's and pickle's).
- **No iptables or nft in the firmware** (busybox has no applet either). The install kit
  ships a Debian bookworm iptables-legacy 1.8.9 aarch64 bundle in `<cfw>/iptables/`; its
  `bin/iptables` is the multi-call binary and picks the applet by its own name, so IPv6
  needs a `bin/ip6tables -> iptables` link (verified: run under that name it lists the
  IPv6 tables), which `egress.sh` then uses. Without the link `egress.sh` falls back to
  switching IPv6 off on the interfaces. On the first unit the bundle directory also
  carried leftovers of the armhf bundle (an armhf `xtables-legacy-multi`, `.so.0` libs),
  which cannot run here; reinstall the directory from the aarch64 bundle alone.
- **Vendor leftovers.** `S31animation` starts `led-play -r` loops on the ring framebuffer
  (and, on a first boot, on the screen); maind stops them once the stack is up. With the
  stack gated they loop forever and overwrite whatever we put on the ring. The profile's
  `platform.stop_initd = S31animation` makes the daemon run the script's own `stop` at
  startup (USR1: led-play finishes the current cycle, ~2 s, and exits; verified on device
  2026-10-26).

## First unit on hand (2026-10-26)

Recon of the user's own unit after the install kit's Tier 1 (rooting and de-Yandexing are
the other project's scope):

- Firmware `0.13.1.36.3322101745.20250823.factory-13`, the same build as the dump: every
  vendor file imported from the dump is byte-identical on the unit, which also supplied the
  `/lib` sysroot the dump lacked.
- Kit state: `<cfw>` = `/bkupgrade/cfw` with dropbear, `maind_off`, the kit's own
  (IPv4-only) `egress.sh` locked, `run.sh` gated, `device_ota_update.sh` stubbed,
  `--updatesd` stripped from `launch.json`, a foreign `ota_script.pub` (each with an
  `.orig`). No maind process; network ADB `:5555` stays up.
- `/dev/galcore` (the NPU) is present. spotter-host's `probe` on the stock models:
  `activation/alisa` (omni + BNR + raw-mic, NPU verifier) creates fine as a 3-channel
  spotter, `yandex` and `yasmina` as 2-channel (omni + raw-mic), `command` as 1-channel.
  The pristine alisa model, verifier included, therefore loads on device; detection
  quality is still to be tested with sound.
- Install (same day): `make deploy-egress` (IPv4 and IPv6 locked), `stage`, the helpers
  (`spotter-host`, `sendspin-host`), `sounds`, `install-initd`; the reboot brings everything back on its own
  (no maind, no led-play). Wake on with the stock alisa model: `wake: spotter ready`. A TTS
  announcement plays cleanly. `make vqe-models` finds no call-mode model: this firmware has only
  `vqe_models/noise_reducer/model.nnet` (no `model_call.nnet`). Later found: that is the name the
  library itself loads in the call mode, so the daemon uses the firmware's directory as it is and
  nothing needs copying ([intercom.md](../intercom.md)); the call mode itself is not yet tried here.
- **Wi-Fi gotcha:** the install kit leaves its lab network (the provisioning AP) in
  `wpa_supplicant.conf` with a lower priority than the home network; after a reboot the unit
  joined the lab AP once while it was up. Remove it, or keep the lab AP off, once provisioning is
  done.
- The aarch64 spotter-host built against pickle's sysroot runs here unchanged (both
  firmwares have glibc 2.36), so `build/aarch64/` serves both boards.
