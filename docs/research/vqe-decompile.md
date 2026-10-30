# VQE decompile: the `bergamot` preset, mic geometry, neural NR, channel wiring

**Scope:** static reverse-engineering of the Yandex VQE (Voice Quality Enhancement) mic-array DSP in
`libyandex_evil.so` (firmware `3.210.369.1.46.4778262544.20261013.117`). It builds on
`docs/research/wakeword-mic-array.md` (the "prior pass"), which said the preset numbers were "compiled in and not
recoverable". **They are recoverable, and this file recovers them.**

Conventions: addresses are virtual addresses in `libyandex_evil.so` (equal to file offsets for the
first LOAD segment, i.e. `.text`/`.rodata`). "Offset +N" means a byte offset inside the 2304-byte
(`0x900`) VQE config struct. Anything not read straight from code or data is marked **(inferred)**.

---

## 0. TL;DR

- **The preset is code, not data.** `"bergamot"` maps to a C++ factory function (`0x1a5668`). It
  default-constructs a `vqe::Config` (`0x19eec0`) and then writes about 90 constant fields (`0x1a5700`). I
  recovered the field names and offsets from the JSON config parser, so every one of those stores has
  a name. The full table is in §3.
- **Mic geometry is confirmed: `micSetup = RIGHT_TRIANGLE_43MM_LEG` (enum 4), 3 mics.** But the
  binary has **no coordinates** for this geometry. Its only geometry-dependent consumer, the
  beamformer, **throws "Beamforming is not implemented for MicSetup::RIGHT_TRIANGLE_43MM_LEG"**.
- **bergamot does not beamform.** `beamformingMode = NONE` (the default, not overridden). The
  directed/`vqe_bf` channel count is 0. The prior pass listed `RELATIVE_TO_OMNI` as a beamforming
  mode; that was wrong. It is a `multimicAecMode` value, and bergamot uses `INDEPENDENT`.
- **What bergamot actually runs:**
  - Per-mic frequency-domain **Kalman AEC** (`aecConfigOmni`, filter = KALMAN, 2 history frames,
    delay 4) against the **HW amp-feedback** reference (`speakerDescr = [{source: HW, weight: 1.0}]`,
    `hwStaticShift = 94`).
  - Picks a "healthiest" omni mic → `vqe` (= `vqe_0`).
  - A separate **2-reference-mic Kalman background-noise canceller** (`bnrConfig.mode =
    STEREO_KALMAN`, with its own Kalman AEC) → `vqe_bnr`.
  - An RC high-pass with a 178 Hz cutoff shift.
  - AGC, NN-NR and gain stages that **depend on the communication mode**.
- **The neural NR (`model_call.nnet`) is a GTCRN** (tensor names say `gtcrn.model_echo_xmb.GtcrnEngine`).
  It is a streaming TFLite model: input `[1,1,4,129]` (3 mic-derived spectral planes + 1 reference plane,
  129 bins = 256-point FFT), output `[1,1,2,129]` mask, 16 recurrent/conv state tensors. **bergamot
  enables it only in `regularCallConfig` (phone/video calls)**, not on the ASR/wake-word path.
- **Verdict:** reproducing the bergamot front-end is now a **known-spec re-implementation**, no longer
  a guess. The topology (no beamformer) is simpler than the prior pass assumed. The remaining unknown
  is the internals of Yandex's Kalman AEC/BNR code, not its tuning. See §7.

---

## 1. Material, tools, limits

### 1.1 Important: the extracted `.so` in `dumps/rootfs/` is truncated

`dumps/rootfs/system/vendor/quasar/libs/libyandex_evil.so` is **4,587,520 bytes (0x460000)**, but its
ELF header places section headers at 0x4aef70, and the tarball lists the file at **4,912,400 bytes**.
`readelf`/`nm` fail on the truncated copy ("missing section headers"). The truncation loses the end of
`.text`, all of `.data.rel.ro`, the GOT and `.data`.

The **complete** file is inside `dumps/rootfs-full.tar`:
- path: `system/vendor/quasar/libs/libyandex_evil.so`
- md5: `2efb61a8d2cdd95d2121936352dcf428`
- its first 0x460000 bytes are identical to the truncated copy

All analysis below uses the complete copy (extracted to the session scratchpad, not into `dumps/`).
`libtflite.so` is intact (same md5 in both).

### 1.2 Tools (static only, nothing vendor-native executed)

- `arm-linux-gnueabihf-objdump` (Ubuntu 22.04 `binutils-arm-linux-gnueabihf 2.38`) for the full
  disassembly (code is ARM-mode ARMv7-A, VFPv3-D16 + NEON).
- `strings`, `readelf`.
- Small `python3 -I` text-processing scripts:
  - resolve PC-relative literal loads into string/GOT xrefs (`ldr rX,[pc,#imm]` + `add rX,pc,rX`);
  - pattern-match the JSON parser's per-field code into (field name, struct offset, parser callee);
  - list the constant stores in the preset function;
  - decode enum string→value tables.
- `.tflite` inspection used the pure-Python flatbuffer schema package `tflite==2.18.0` (+
  `flatbuffers==24.3.25`, `numpy==1.26.4`, all well over 3 days old) in a scratch venv. It is a schema
  parse only; no interpreter was used and no inference was run.
- No Ghidra or other heavyweight tools.
- Peak RSS observed: about **270 MB** (the Python field mapper); objdump about 9 MB. Host: 23 GiB RAM,
  never below about 14 GiB free.

### 1.3 Method limits (read before trusting the numbers)

- **The bergamot overrides (§3) are reliable.**
  - `0x1a5700` is straight-line code (one entry branch, none in the body) writing immediates into one
    base register (`r4` = the static config object).
  - Every store was checked against the recovered layout.
  - The helper-call arguments (vector assigns) were decoded by hand.
- **Defaults (`0x19eec0`, ~670 instructions) are best-effort.** The constructor re-uses several pointer
  registers, so my linear store tracker may misattribute some stores in the `audioCallConfigs`
  sub-structs (offsets 1668–2244); a few entries there are visibly nonsensical. Defaults are quoted
  only where they are internally consistent.
- **Enum names for the value 0** are often compared as 2–4-char immediates (`"NONE"` = `0x454e4f4e`,
  `"HW"` = `0x5748`, `"RC"` = `0x4352`, `"GEVD"`), not as `.rodata` strings. They were read by hand from
  `movw/movt` + `cmp`.
- Where a field's *meaning* (units, frame vs sample) isn't visible from the config layer, it is labelled
  **(inferred)**. I did not decompile the DSP kernels (Kalman update equations, BNR internals).

---

## 2. How presets are loaded (code, not data)

1. `maind` imports `YandexVqeC_Config_CreateFromPreset` and `YandexVqeC_Config_CreateFromJsonPreset`
   and **no `YandexVqeC_DspClient_*`**, and the rootfs has no `libhifi4rpc*.so`. So on this unit VQE
   runs on the ARM CPU from `libyandex_evil.so`. `quasar.cfg` gives only
   `audiod.audioDevice.vqe = {type:"yandex", preset:"bergamot", defaultModelStoragePath:"${QUASAR}/vqe_models"}`.
2. `YandexVqeC_Config_CreateFromPreset` (`0x198d1c`) does the following:
   - copies the name into a `std::string`;
   - calls the lookup at `0x19cf48`;
   - copies the returned config into a fresh `operator new(0x910)` block (the config is `0x900` bytes
     plus 3 trailing words).
3. Lookup `0x19cf48`:
   - `find()` in a global `unordered_map<string, Config const&(*)()>` at `.bss 0x4d1eb0`;
   - **tail-calls** the factory pointer stored at node+20;
   - on a miss it throws `"Unknown Yandex VQE device preset: "` (`.rodata 0x6224c`, xref `0x19cf98`).
4. The map is filled by the static initializer at `0x19d8b8`:
   - It builds **63** `{std::string name, fn*}` pairs on the stack (loop `cmp r6,#1008` = 63×16 at
     `0x19e8f0`) and inserts them via `0x19d0c8`.
   - Long names are copied from `.rodata` chunks, e.g. `yandexstation_2_rev1`, `magritte_rev2_55in`,
     `plum_LY24092502-2`, `yas_gac_electro`.
   - **Short names (≤10 chars) are built from immediates**, which is why `strings` never finds
     `"bergamot"`. The bergamot entry is:

     ```
     19e334  strb fp(=16), [sp,#668]        ; SSO size byte: len 8
     19e35c  movw r1,#0x6562 ; movt r1,#0x6772   -> "berg"
     19e320  movw r3,#0x6d61 ; movt r3,#0x746f   -> "amot"
     19e370  str r1,[r2,#432] ; 19e378 str r3,[r2,#436]   (r2 = sp+237 -> chars at sp+669..676)
     19e3b8  ldr r0,[pc,#..] ; ldr r0,[pc,r0]   -> GOT 0x4be144 = 0x001a5668  (fn ptr, stored at sp+680)
     ```

     Neighbours built the same way: `"pickle"` → `0x1a5f0c`, `"plum"` → `0x1a5a88`.
5. Factory `0x1a5668` is a function-local static guard (`__cxa_guard_acquire` pattern at `0x1a5674`):
   - on first call it runs `0x1a5700`, which does `Config::Config()` (`0x19eec0`) followed by the bergamot
     overrides;
   - it then returns the static object.

**So the "preset" is the default config plus about 90 field overrides.** Nothing is stored as JSON.
Field names come from the JSON parser (`0x121968` for the root struct; one parser per sub-struct): each
field is located by name in the JSON object, a path string `"::<name>"` is appended for error messages,
and a type parser is called with `r1 = &config + offset`. Pattern-matching that code gives
**(struct, field, offset, parser)** for all ~320 `::field` references, and the type parser gives the
field type:

| parser  | type     | examples |
|---------|----------|----------|
| `0x10c2a8` | `float`  | `alpha` |
| `0x114868` | `double` | ripple, BNR Kalman values |
| `0x10d430` | `uint32` | rates, frames |
| `0x10c3e8` | `int32`  | shifts, indices |
| `0x110c1c` | `bool`   | |
| other   | enums    | see §3.5 |

Getters cross-check the layout:
- `GetMicsCount`: `ldr r1,[r0,#20]` (= `micSetup`), then a table lookup.
- `GetSpeakersCount`: `(end − begin)/12` of the vector at +112 (= `speakerDescr`).
- `GetInputSamplingRate`: `[r0,#124]`.
- `GetOutputSamplingRate`: `[r0,#132]`.
- `GetProcessingFrame`: `[r0,#136]`.
- `GetInputChunkSize`: `in_rate / proc_rate × processingFrame`.

---

## 3. The `bergamot` preset (decoded from `0x1a5700`)

Format: `field = value` (default in brackets where it is reliable). The store address is the
instruction in `0x1a5700`–`0x1a5a1c`.

### 3.1 Top level / framing

| Offset | Field | bergamot | Default | Evidence |
|---|---|---|---|---|
| +20 | `micSetup` | **4 = `RIGHT_TRIANGLE_43MM_LEG`** | 0 `SINGLE` | `1a5728 str #4,[r4,#20]` |
| +52 | `micChoosingConfig.defaultOmniMicIndex` | **0** | 0 | `1a5724` |
| +48 | `micChoosingConfig.mode` | (default) **1 = `HEALTHIEST`** | 1 | ctor `19efc4` |
| +68 | `micChoosingConfig.micSwitchThreshold` | (default) 0.2 | 0.2 | ctor |
| +112 | `speakerDescr` (vector<{source, feedbackId, weight}>) | **[{source:0=`HW`, feedbackId:0, weight:1.0}]** | empty | `1a574c` call `0x161c74(r4+112, [0,0,1.0f], n=1)` |
| +124 | `inputSamplingRate` | **48000** | 16000 | `1a5790` |
| +128 | `processSamplingRate` | (default) **16000** | 16000 | ctor `19efd4` |
| +132 | `outputSamplingRate` | (default) **16000** | 16000 | ctor |
| +136 | `processingFrame` | **256** | 1024 | `1a578c` |
| +140 | `framesPerWindow` | **8** | 2 | `1a5784` |
| +144 | `shiftsPerFrame` | **1** | −1 (auto) | `1a5760` |
| +148 | `stftWindowFunction` | (default) 2 = `KBD_4` | 2 | ctor |
| +628 | `beamformingMode` | **(default) 0 = `NONE`** | 0 | not written by `0x1a5700` |
| +632 | `multimicAecMode` | (default) 0 = `INDEPENDENT` | 0 | ctor `19f1a8` |
| +620/+624 | `dereverbConfig.{dereverbActive, dereverbDelay}` | (default) false, 2500 | | ctor |
| +540.. | `agcConfig.enabled` (top-level/ASR path) | (default) false | | ctor `19f11c` |
| +812 | `doaWithoutDownsampling` | (default) false | | |
| +784.. | `doaConfig{historyAlpha, rmsThreshold, confidencePower}` | (default) 0.7, 1.5e-5, 4 | | ctor |

Derived:
- input chunk = 48000/16000 × 256 = **768 samples @48 kHz = 16 ms**;
- processing frame = **256 samples @16 kHz = 16 ms**;
- STFT = 256-point → **129 bins** (matches the NN NR, §5).

### 3.2 Echo cancellation (`aecConfigOmni`, base +156, per-mic "omni" AEC)

| Offset | Field | bergamot | Default |
|---|---|---|---|
| +156 | `filter` | **2 = `KALMAN`** (`LMS`=1, `OMITTED_KALMAN`=3, 0 = `NONE`) | |
| +212 | `historyFramesCount` (adaptive filter length, frames **(inferred)**) | **2** | −1 |
| +220 | `delay` | **4** | −1 |
| +224 | `mu` | 5.933e-08 (`0x337ed208`) | −1 |
| +228 | `postfilterBeta` | 1.48911e-08 (`0x327fd393`) | −1 |
| +232 | `muOverride` (vector<float>) | **[4.41119e-08]** (`0x333d757f`, call `0x161974`) | [] |
| +244 | `kalmanAlpha` | 0.685803 (`0x3f2f90c4`) | |
| +248 | `kalmanLambda` | 0.999709 (`0x3f7fecf4`) | |
| +252 | `kalmanBaseRmsH` | 3.96687 (`0x407de127`) | |
| +256 | `kalmanRmsRatioForgettingFactor` | 0.96246 (`0x3f7663c7`) | |
| +260 | `kalmanEffectiveShare` | (default) 0.1 | 0.1 |
| +264 | `useNewKalman` | **true** | false |
| +268 | `kalmanLambdaP` | 0.0418358 (`0x3d2b5bfe`) | 0 |
| +272 | `kalmanInitP` | 11.6509 (`0x413a6a13`) | 1.0 |
| +276 | `useNeonKalman` | (default) false | false |
| +280 | `kalmanPostfilterType` | (default) 1 = `GENERIC` (`IDENTITY`=0, `ONE_CHANNEL`=2) | |

The four Kalman values at +244..+256 come from one NEON store of `.text` literal `0x1a5a40`.

`aecConfigDirected` (+284) and `aecConfigHdmi` (+412) are **not touched**. Their `filter` stays 0 =
`NONE`, which is also why the `vqe_bf` channel count is 0 (§6).

### 3.3 Background noise reducer (`bnrConfig`, base +1336), which produces `vqe_bnr`

| Offset | Field | bergamot |
|---|---|---|
| +1336 | `mode` | **3 = `STEREO_KALMAN`** (`DISABLED`=0, `MONO_LMS`=1, `MONO_KALMAN`=2, `OMITTED_KALMAN`=4, `MONO_OMITTED_KALMAN`=5) |
| +1340 | `aecMode` | (default; enum `AEC_LAST`=0 / `AEC_FIRST_ON_ECHO`=1 / `AEC_FIRST_ON_FEEDBACKS`=2) |
| +1344 | `historyFrames` | **3** |
| +1352 | `delayFrames` | **47** |
| +1360 | `mu` (double) | 1.0454424827e-08 (`0x3e4673617e0b0b7e`) |
| +1368 | `muOverride` (vector<double>) | **[3.8217183530e-08]** (`0x3e6484879b03cbeb`, call `0x1a0230`) |
| +1384 | `kalmanAlpha` (double) | 0.9932174867 |
| +1392 | `kalmanLambda` (double) | 0.9999721698 |
| +1400 | `useNewKalman` | true |
| +1404 | `kalmanLambdaP` | 0.0330911 |
| +1408 | `kalmanInitP` | 0.667381 |
| +1416.. | `aecConfigBNR` (nested AEC on the BNR path) | see below |

`aecConfigBNR` values:
- `filter` = KALMAN, `historyFramesCount` = 1, `delay` = 1;
- `mu` = 4.65691e-05, `postfilterBeta` = 44.2766;
- `muOverride` = [1.52337e-08];
- `kalmanAlpha` = 0.981004, `kalmanLambda` = 0.981469, `kalmanBaseRmsH` = 1.44709;
- `kalmanRmsRatioForgettingFactor` = 0.953486;
- `useNewKalman` = true, `kalmanLambdaP` = 0.974821, `kalmanInitP` = 0.067886.

`STEREO_KALMAN` plus the prior-pass guard `bnrRefMicCount <= 2` together mean **(inferred)**: an adaptive
noise canceller that uses the other two mics as noise references (a GSC-like blocking/cancel structure,
not a beamformer), followed by its own Kalman AEC.

### 3.4 Filters, correlators, communication-mode sub-configs

| Field | bergamot |
|---|---|
| `frequenciesFilterConfig` (+872) | `designType` = 0 = **`RC`**, `filterType` = 1 = **`HIGH_PASS`**, `cutoffFrequencyShift` = **178.0** (double), `useEigen` = true |
| `inputDownsampleFilterConfig.useEigen` (+864) | true (the 48→16 kHz decimator; design defaults otherwise, best-effort: `CHEBYSHEV_TYPE_I`, order 4) |
| `correlatorConfig.hwStaticShift` (+964) | **94** (HW feedback ↔ mic alignment; samples, unit **(inferred)**) |
| `correlatorConfig.{hw,sw,hdmi}Correlator.filter.useEigen` | true |
| `correlatorConfig.enableHwCorrelator` | (default) false, so the static shift is used as-is **(inferred)** |

`audioCallConfigs` (+1668) holds three 176-byte mode configs, switched by
`YandexVqeC_SetCommunicationMode`:

- **`babyMonitorConfig`** (+1668):
  - `webRtcNoiseReducerConfig.enabled` = true, `level` = 1 = `k12dB`;
  - `webRtcAgcConfig.preAmplifierConfig` = {enabled: true, hardClipSamples: true, gainFactor: 72.0};
  - `webRtcAgcConfig.autoGainControl.enabled` = true;
  - `omniChannelGainCoefficient` = 1.0.
  - The `model_baby_monitor.nnet` name is referenced but **not shipped**, consistent with NN-NR not being
    enabled here.
- **`regularCallConfig`** (+1844), i.e. phone/video calls:
  - `neuralNoiseReducerConfig` = {enabled: true, useFeedbacks: true, type: 1 = `TFLITE`, inputType: 1,
    preprocessorType: 2 = `GTCRN`, preprocessorFeedbackType: 3 = `GTCRN_MAGNITUDE`,
    modelOutputApplierType: 2 = `GTCRN`};
  - `agcConfig` = {enabled: true, preAmplifyGain: 72, targetRmsDb: −18, minGainDb: −12, maxGainDb: 10,
    noiseInitDb: −60, noiseAlphaAttack: 0.01, noiseAlphaRelease: 0.001, noiseDbMin: −72,
    noiseDbMax: −50, hardClip: true, limiterConfig.enabled: true};
  - `omniChannelGainCoefficient` = 1.0.
- **`noCommunicationConfig`** (+2020), the normal ASR / wake-word mode: `omniChannelGainCoefficient` =
  **50.0**. Nothing else is overridden. Defaults here are best-effort, but no NN-NR or AGC enable was
  seen.

### 3.5 Enum tables recovered (from the parser string→value code)

- `MicSetup`:

  | Value | Name |
  |---|---|
  | 0 | `SINGLE` |
  | 1 | `DUAL_D425` |
  | 2 | `DUAL_D1200` |
  | 3 | `DUAL_D1850` |
  | 4 | **`RIGHT_TRIANGLE_43MM_LEG`** |
  | 5 | `SQUARE_S425` |
  | 6 | `CIRC_6_PLUS_1_R425` |
  | 7 | `SIDE_4_FRONT_2_REAR_2` |
  | 8 | `RECT_28MM_18MM` |
  | 9 | `TOP_3_FRONT_4` |
  | 10 | `TOP_3_FRONT_2` |
  | 11 | `TOP_2_FRONT_1` |
  | 12 | `TRAPEZIUM_430MM_103MM_970MM` |
  | 13 | `FRONT_2_MID_2_BACK_2` |

  Mic counts per value from table `0x8a2bc`, in value order: `1,2,2,2,3,4,7,8,4,7,5,3,4,6`, so value 4 → **3 mics**.
- `BeamformingMode` (`0x112b54`): `NONE`=0 (immediate `0x454e4f4e`), `DELAY_SUM`=1, `ADAPTIVE`=2,
  `MVDR`=3, `GEVD`=4 (immediate `0x44564547`), `MUSIC`=5.
- `MultimicAecMode`: `INDEPENDENT`=0, `RELATIVE_TO_OMNI`=1.
- `MicChoosing.mode`: `DEFAULT`=0, `HEALTHIEST`=1, `CHIRON_OMNI6_BNR422`=2, `AVERAGE_AFTER_AEC`=3.
- AEC `filter`: `NONE`=0, `LMS`=1, `KALMAN`=2, `OMITTED_KALMAN`=3.
- Speaker `source`: `HW`=0 (`0x5748`), `SW`=1 (`0x5753`), `HDMI`=2, `SINE`=3.
- Window: `NONE`=0, `HANN`=1, `KBD_4`=2, `SINE`=3, `TORCH_HANN`=4, `TORCH_HANN_SQRT`=5, `TRIANGLE`=6,
  `VORBIS`=7.
- Filter design: `RC`=0, `BUTTERWORTH`=1, `CHEBYSHEV_TYPE_I`=2, `CHEBYSHEV_TYPE_II`=3, `ELLIPTIC`=4.
- Filter type: `LOW_PASS`=0, `HIGH_PASS`=1, `ALL_PASS`=2.
- NN type: `DUMMY`=0, `TFLITE`=1 (`GTCRN`=2 **(inferred)**).
- NN preprocessor: `COMPLEX`=0, `COMPLEX_NORMALIZED`=1, `GTCRN`=2, `GTCRN_MAGNITUDE`=3, `MAGNITUDE`=4,
  `MAGNITUDE_NORMALIZED`=5, `REAL_IMAG`=6.
- NN output applier: `COMPLEX_MASK`=0, `IDENTITY`=1, `GTCRN`=2, `MAGNITUDE_MASK`=3, `REAL_IMAG`=4.
- WebRTC NS level: `k12dB`=1, `k18dB`=2, `k21dB`=3.

---

## 4. Mic geometry: what is and isn't in the binary

- **Confirmed by code:** `micSetup = RIGHT_TRIANGLE_43MM_LEG` (`1a5728`), 3 mics (`0x8a2bc[4] = 3`).
  The geometry claim is in the enum name only: a right triangle with 43 mm legs, so the hypotenuse is
  ≈ 60.8 mm **(inferred from the name)**.
- **No coordinates exist for it.** The beamformer factory `0x26789c` switches on `micSetup` (jump table at
  `0x267994`):
  - setups 2, 3, 5, 6 and 8 are implemented;
  - setup 4 jumps to `0x267a80`, which throws `"Beamforming is not implemented for
    MicSetup::RIGHT_TRIANGLE_43MM_LEG"` (`.rodata 0x76ea1`, xref `0x267a90`);
  - setups 0, 1, 7 and 9–13 throw similarly.
- The only geometry literal block found is the hexagon for `CIRC_6_PLUS_1_R425` at `0x22f210`
  (±0.02125/±0.03681/±0.0425 m, radius 42.5 mm).
- A search of the whole file for 0.043 / 0.0215 / 0.0608 as float or double, and for `movw/movt` pairs
  building them, returns nothing.
- Because DOA (`doaConfig`) and mic choosing work from correlation and RMS (`micAnalyzerConfig.algorithm`
  default 1 = `RMS`), bergamot never needs coordinates.
- **Implication:** the triangle shape is known, but its orientation relative to the PCB and the
  channel→vertex mapping are **not in the firmware**. If you ever want a beamformer, measure them
  (PCB photo or TDOA test).

---

## 5. Neural noise reducer `vqe_models/noise_reducer/model_call.nnet` (GTCRN, TFLite)

### 5.1 The file

- `TFL3`, schema v3, `"MLIR Converted."`.
- 1 subgraph, 601 tensors, 405 ops, all builtin: CONV_2D, DEPTHWISE_CONV_2D, FULLY_CONNECTED (GRU),
  LOGISTIC, TANH, SOFTMAX, BATCH_MATMUL, MEAN/RSQRT (norm), RESIZE_NEAREST_NEIGHBOR, …
- **No custom ops**, so it is runnable on stock TFLite.
- Signature `serving_default`.
- Loader strings: `model_call.nnet`, `model_call_meta.json`, `tflite_noise_reducer.cpp`, `VQE_GTCRN`.
  Meta version: `VQE-1982_2025_12_23_tuning_random_preset_xmb_4_log_mag_ref_2`.

### 5.2 I/O

| Tensor | Shape | Role |
|---|---|---|
| `input` | `[1,1,4,129]` f32 | one STFT frame: planes 0–2 = mic features, plane 3 = reference (feedback) |
| `state_0..15` | `[1,1,32]` ×9, `[1,2,33,16]` ×2, `[1,4,33,16]` ×2, `[1,10,33,16]` ×2, `[1,33,16]` ×2 | streaming state (GRU hidden 32; conv caches for dilations 1/2/5 with kernel 3 → 2/4/10 frames) |
| `output` | `[1,1,2,129]` | mask planes |
| `streaming_model_1..16` | same shapes as the states | next state: feed back into `state_k` (matching by shape/name order **(inferred)**) |

### 5.3 What the graph does at the edges (ops 0–13 and 395–404)

1. Slices planes 0–2 (mic) and plane 3 (ref).
2. Scales mic ×**50.0** (`arith.constant176`) and ref ×**2.0** (`arith.constant173`).
3. GATHER_ND index **0**: only mic plane 0 is used.
4. Computes `log(x + 1e-8)` for both, concatenates to `[1,1,129,2]`, and runs
   Conv2d(16,(1,7)) → … (129→65→33 bins).
5. Encoder/GRU/decoder stages.
6. Upsample to 130, Conv2d → GLU → slice 129 → **real mask in plane 0**; plane 1 is a constant **0.0**
   (`arith.constant172`, all zeros).

So the effective function is a **real-valued per-bin gain mask** computed from log|mic| and log|ref|.
Planes 1–2 (probably the mic's real/imag parts, from preprocessor `GTCRN`) are ignored. The names
`log_mag_ref` and `model_echo_xmb` fit: an echo- and noise-suppression post-filter.

### 5.4 Standalone reuse recipe

Mostly **(inferred)** from config plus graph.

1. Signal: 16 kHz mono mic and ref (amp-feedback), 256-point STFT. The VQE frame is 256 samples with a
   `KBD_4` window by default. Hop is `processingFrame / shiftsPerFrame` = 256, but `framesPerWindow = 8`
   in the root config suggests overlap inside the STFT stage. Confirm by listening; try hop 128 or 256.
2. Each frame: build `input[0,0,0,:] = |Mic(f)|` and `input[0,0,3,:] = |Ref(f)|` (planes 1–2 can be 0).
   Run the model, carry the 16 states forward, and multiply `Mic(f)` by `output[0,0,0,:]`.
3. Use it after a linear AEC: in Yandex's chain it is applied to the post-AEC omni channel
   (`modelOutputApplierType = GTCRN`).
4. Caveat: magnitude normalisation (`|X|` vs `|X|²`, FFT scaling) is not visible in the graph. The ×50 and
   ×2 scalers expect Yandex's internal scaling, so calibrate once by checking the mask stays in
   a sensible 0..1 range on speech.

---

## 6. Channel / graph wiring: from code

VQE output-channel types and their names come from the pointer table at `.data.rel.ro 0x4b5188`. The
per-type channel count is `0x19c580`, which switches on the type (index 1–9):

| Index | Name | Count rule (address) | bergamot |
|---|---|---|---|
| 1 | `raw_mic` | `micCountTable[micSetup]` (`19c5c4`) | **3** |
| 2 | `raw_sync_mic_main` | `exportMicSyncChannels != 0` (`19c65c`) | 0 (default) |
| 3 | `raw_sync_mic_aux` | `exportMicSyncChannels == 2` (`19c608`) | 0 |
| 4 | `raw_spk` | `speakerDescr.size()` (`19c630`) | **1** (HW feedback) |
| 5 | `raw_spk_sync` | `exportFeedbackSyncChannels` (`19c5e4`) | 0 |
| 6 | `vqe` | always 1 (`19c690`) | **1 → `vqe_0`** |
| 7 | `vqe_bf` | 0 unless `aecConfigDirected.filter \| beamformingMode` ≠ 0 (`19c6b8`) | **0** |
| 8 | `vqe_bnr` | `bnrConfig.mode != 0` (`19c64c`) | **1** |
| 9 | `vqe_gain` | any `omniChannelGainCoefficient` engaged (`+1744`/`+1920`/`+2096`) | **1** |

Index 0 is `unknown`; 10 and 11 (`vqe_state`, `vqe_zone`) are not counted here.

This matches `quasar.cfg audiosender.availableChannels = [raw_mic_0..2, raw_spk_0, vqe_0, vqe_bnr]` and
`spotterChannelName = vqe_0`. The capture chain is therefore established from code plus config, not
inferred:

```
hw:0,1 (8ch PDM/TDM) --asound.conf multi--> 4ch S16 @48k [mic0, mic1, mic2, HW-feedback(ch6)]
   -> VQE input chunk 768 frames @48k (inputSamplingRate 48000, processingFrame 256 @16k)
   -> 48->16 kHz decimation (inputDownsampleFilterConfig) + RC HPF (178 cutoff shift)
   -> per-mic Kalman AEC vs HW feedback (aecConfigOmni, multimicAecMode INDEPENDENT, hwStaticShift 94)
   -> mic choosing (HEALTHIEST, default index 0)  ---------------------> vqe   (= vqe_0, 16 kHz mono)
   -> STEREO_KALMAN BNR (2 ref mics) + its Kalman AEC ------------------> vqe_bnr
   -> x omniChannelGainCoefficient (50 in ASR mode, 1 in calls) --------> vqe_gain
   [call mode only: GTCRN TFLite NR + AGC/limiter on the omni path]
```

The order of the stages inside the omni path (for example, HPF before or after the AEC) is
**(inferred)**. The set of stages and their parameters is not.

---

## 7. Verdict: how much did the decompile change?

**Before:** "preset not recoverable; probably delay-sum/MVDR beamforming over a ~43 mm triangle; neural
NR of unknown I/O; re-implement everything from scratch."

**Now:**
1. **Every bergamot tuning value is recovered**: about 90 overrides on top of a decoded default config,
   all mapped to names, plus 14 enum tables.
2. **There is no beamformer to reproduce.** For bergamot, Yandex's front-end is per-mic Kalman AEC
   with a hardware reference, a picked mic, a 2-reference adaptive noise canceller, an HPF, and
   mode-dependent gain/AGC. That is much less than assumed, and the "best" Yandex quality on this
   device comes from good AEC on a true HW reference, not from array processing.
3. **The neural NR is a standard GTCRN with clean I/O** and no custom ops, so it can be reused
   standalone (§5.4) with modest calibration effort. It is the *call* model, though. Yandex does not
   run it on the wake-word/ASR path.

**What still blocks a bit-exact clone:** the DSP kernels themselves. I did not decompile the internals of
the Kalman AEC/BNR (`NeonKalmanEchoCanceller`, `StereoKalman…`, update rules, postfilter), the
decimator design, or the correlator. The parameters are known, but the algorithms they feed are
Yandex-specific. Re-implementing those kernels from disassembly would be a multi-week project (NEON-heavy
code; Ghidra would be needed), and it is not worth it.

**Practical recommendation (updates prior-pass §5.2):**
- **AEC:** keep `webrtc-audio-processing` AEC3 (or SpeexDSP) on `[mic_k, HW feedback]`. Use the
  recovered facts: a reference delay around `hwStaticShift = 94` samples, a short AEC tail (bergamot uses 2
  history frames of 256 @16 k, roughly 32 ms, **(inferred)**), and a high-pass near 178 Hz.
- **Arrays:** skip beamforming entirely, as Yandex does. Pick the best mic (RMS/health), and optionally
  add a 2-ref-mic adaptive noise canceller if you want a `vqe_bnr` equivalent.
- **NR:** optionally drop in the GTCRN model as a post-AEC mask, or use WebRTC NS.
- **Gain:** ASR-mode gain is ×50 (about +34 dB) on the omni channel, so feed MWW a strongly amplified
  stream.
- **Wake word:** stays microWakeWord.

So VQE moves from "opaque, rewrite blind" to **"rewrite to a known spec"**, and the spec turns out to
be small. A full clone of the vendor kernels is still not realistic or useful. Reusing the GTCRN model
and copying the topology and constants is.

---

## Appendix: key addresses

| What | Address |
|---|---|
| `YandexVqeC_Config_CreateFromPreset` / `…FromJsonPreset` | `0x198d1c` / `0x198ed4` |
| Preset lookup (map find + tail call) / map object | `0x19cf48` / `.bss 0x4d1eb0` |
| Preset-table static init (63 entries) / insert | `0x19d8b8` / `0x19d0c8` |
| bergamot name build / GOT slot / factory / body | `0x19e320..0x19e3c0` / `0x4be144` / `0x1a5668` / `0x1a5700` |
| `Config::Config()` defaults | `0x19eec0` |
| Root JSON parser (48 top fields) | `0x121968` |
| AEC struct parser / BNR struct parser | `0x10ef74` / `0x118cd0` |
| MicSetup / BeamformingMode enum parsers | `0x10b444` / `0x112b54` |
| Mic-count table | `.rodata 0x8a2bc` |
| Beamformer geometry switch (throws for RIGHT_TRIANGLE) | `0x26789c`, throw xref `0x267a90` |
| Hex-array coordinates (other devices) | `0x22f210` |
| Output-channel count per type / channel-name table | `0x19c580` / `0x4b5188` |
| Other presets of interest | `"plum"` → `0x1a5a88`, `"pickle"` → `0x1a5f0c` |

---

## Summary

The `bergamot` VQE preset is a compiled factory function, not a data table. Decompiling it together with
the JSON config parser's field map recovers every tuned value:
- `micSetup = RIGHT_TRIANGLE_43MM_LEG` (3 mics, no coordinates anywhere in the binary);
- **beamforming NONE**, which the code says is "not implemented" for this geometry;
- per-mic Kalman AEC against the HW amp-feedback reference (numeric Kalman/μ values recovered,
  `hwStaticShift` 94);
- 48→16 kHz decimation, 256-sample frames, an RC high-pass (178);
- a 2-reference-mic Kalman noise canceller feeding `vqe_bnr`;
- ×50 omni gain in ASR mode;
- GTCRN neural NR plus AGC only in call mode.

The shipped `model_call.nnet` is a stock-op streaming GTCRN TFLite model: 1×4×129 log-magnitude input
(mic plus reference), a real 129-bin mask output, and 16 state tensors. It is reusable standalone.

Reproducing the bergamot front-end is now a small, fully specified re-implementation with open tools
(AEC3 + mic picking + optional GTCRN). A bit-exact clone of Yandex's NEON Kalman kernels remains
unrecovered and not worth the effort.
