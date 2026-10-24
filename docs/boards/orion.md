# orion — Yandex Station 3

Amlogic A5 (a113x2-class, 4x Cortex-A55), aarch64 userland, kernel 5.4, quasar 3.21.
Device tree: `yandex,orion-pvt yandex,orion amlogic, a5`. Sources for this page: the
on-device survey and static analysis of the vendor 3.21 tree (maind, libyandex_evil.so,
quasar.cfg, asound.conf …), both kept locally and not committed.

Status: playback, volume, mic capture, LED ring and the 21×12 front-screen daemon
backend **verified on device 2026-10-23/24**. Both `fbleddemo -uwu` and the production
clock renderer remain stable with no flashing; the clock advances normally. Rear
ambilight hardware is verified and its daemon entity is implemented but not yet smoke-tested
on-device. Wake is verified offline; on-device spotter remains open.

## Audio out

- Card 0 `AMLAUGESOUND`. Playback chain `default -> speakers` (plug → dmix → `hw:0,1`
  "TDM-B acm8625-speakers", 6ch S16 @ 48 kHz, period 1920). A third amp `us` drives the
  **ultrasound transducer** on `hw:0,0` (TDM-A, 96 kHz) — vendor keeps it fed with silence
  via `aplay -D ultrasound`; we do not touch it.
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
  when TDM-B playback is up.
- CPU cost of the stock 96 kHz plug chain: arecord via `mroute` 13ch **110 jiffies / 5 s
  ≈ 2.2 %** of one core vs **60 jiffies ≈ 1.2 %** at 48 kHz (measured; =13 % of a CPU on
  the 4×A55). On top, in-process 6× decimation costs ~0.2-0.3 % per the Go Decimator's
  48 kHz numbers — well within budget.
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
  4 s of tone/silence. On device, the pristine model with the NPU verifier is the real
  test. Fallback remains: strip the verifier (re-threshold; more false accepts) -
  bergamot §6.4 tradeoff in docs/spotter-decompile.md.
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

- `mute` (gpio-keys, event2): KEY 113 + SW 14 (mic-mute hardware switch — cuts the mics).
- `aw933xx` touch controller (event3): KEY 582 (Alice) + 581; **no 114/115** — the volume
  strip is ABS-only, so hardware volume is restricted to the app's MQTT/API … and the
  Bluetooth remote (`input_btrcu`: KEY 116 + 133).
- `vad_keypad`: Amlogic VAD engine, vendor-internal; ignore.

## Lux / power

- Persistent writable storage: `/data`, approximately 2 GB free on the tested unit. The
  Orion CFW root is `/data/cfw`; do not deploy it under `/bkupgrade/cfw`.

- Ambient light: ltr308 (I2C 1-0053) registered as IIO `light-sensor-top`, `in_intensity_*`
  only; the stock S80light-sensor sets gain 18 / 0.4 s — max values, nothing to override.
  Profile prefix: `light-sensor`.
- USB-C PD: HUSB238 (I2C 2-0008, `ya_pd`) as bergamot/pickle. **Measured on device
  (2026-10-23): contracts a 20 V / 3.25 A PD (voltage_now 20000 mV, current_now 3250,
  status "Good")** — orion is a 65 W-class supply, not bergamot's 12 V. Profile
  `min_mv 15000` distinguishes a real 20 V PD source from 9/12 V fallbacks; nothing in
  the vendor tree (quasar.cfg, scripts) carries a textual threshold, so 15000 is our own
  alert line, tuned to the measured contract.

## LEDs

Three surfaces, every LED a Linux LED class device each mirrored by a framebuffer
(`ledfb` triggers) — same architecture as bergamot's logo bar, ×3 chips:

| Surface | sysfs | fb | geometry | chips |
| --- | --- | --- | --- | --- |
| Ring | `orion:{red,green,blue}:ring{0..35}` | `fb2` "ring" 36×1 | 36 RGB px | 3 × AW21036 @ I2C 2-0034/2-0035 (12 px each; expected A=0..11, B=12..23, C=24..35) |
| Ambilight | `orion:{red,green,blue,white}:ambilight{i}{a,b}` (i=0..11) | `fb0` "ambilight" 12×1 | 12 positions × 2 dies; **RGBW** (the only white channel) | own `ambilight{A,B,C}:sync` latch |
| Screen | — (fb only) | `fb1` "YNDX LED screen" 21×12 **(ioctl-confirmed)**, gowin FPGA | 252 px dot-matrix; glass lags one write behind — needs periodic re-send | plain `write()` works (verified 2026-10-24); no `S29gowin`/ipc needed |

- Latch pseudo-LEDs `orion:{ring,ambilight}{A,B,C}:sync` (trigger `ledfb-*-sync`): one
  per AW21036 in each chain. bergamot's `logo:sync` is never poked by our sysfs writes and
  the ring works — the same is expected here; `tools/orion-led-probe.sh` verifies it
  before the daemon relies on it.
- **Vendor pattern files** (`/system/vendor/quasar/ledpatterns/`, dumped): plain text,
  one frame per line — `RRGGBB` per pixel + trailing frame duration in **ms** (usually
  `20`; `ring/block.led` holds `10000`). A global first line may be `loop`,
  `smooth <N>` (interpolation on) or `no_size_optimization`; `ring/` = 36 px + duration,
  `ambilight/` = 12 px + duration, `screen/` = 252 px (21×12 row-major) + duration.
  No gamma in the files — the surfaces' gamma (ambilight 2.0, screen 2.2) comes from
  quasar.cfg's `leds` block.
- `ledconfigs/{red,blue}.json`: per-case-colour 3×3 **channel current coefficient
  matrices** — which one applies is decided by the case colour, and
  `S61leds-current-coefficients` programs it at boot. Orthogonal to our sysfs brightness
  writes; we keep it running.
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
  channel). `ambilight.kind = sysfs-rgbw` exposes it as a separate persisted HA MQTT
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

## Boot / egress

- FW 3.21 keeps the same init.d chain as 3.x: S94alsa, S95hardvold, S97quasar (starts
  maind via run.sh), plus S80light-sensor and S61leds-* on orion. Our maind_off gate and
  egress firewall carry over unchanged.
