# Yandex SpeechKit "spotter" (wake word) — decompilation pass

**Scope:** static reverse-engineering of the on-device keyword spotter (C++ `Spotter::`, `spotter-type chen14`) in
`libyandex_evil.so`, done to decide whether the shipped «Алиса» / «Яндекс» / "yasmina" models can be reused without the
vendor `maind` stack. This builds on `docs/research/wakeword-mic-array.md` (prior pass) and uses the VQE findings of the sibling
pass in `docs/research/vqe-decompile.md`. Neither of those files is edited here.

**Method, safety, limits:** static only. No vendor code was executed. Model files and TFLite flatbuffers were parsed
as data. Claims are tagged **(observed)** when they come straight from bytes, disassembly or decompiler output, and
**(inferred)** when they are my reading of that evidence. Tools and resource use are in §10.

---

## 0. TL;DR and verdict

- **The engine is self-contained.** `libyandex_evil.so` exports a complete C API (`spotter_conf_read`,
  `spotter_create[_multichannel|_from_multi_conf]`, `spotter_submit_raw_data` / `spotter_feed_data`,
  `spotter_get_next_event`, …) and needs only `libtflite.so`, `libiconv.so` and glibc. `maind` is just a client of this
  API. **(observed)**
- **The models are not really Kaldi.** They use the Kaldi nnet3 *file format*, but Yandex's own runtime ("ynmt",
  `voicetech/spotter/embedded/nnet/ynmt/*`) loads them. They contain components stock Kaldi or Vosk do not have:
  `Q16FixedAffineComponent`, `(Q16)GroupConvCorouComponent`, `<hardswish>`, and a `MultichannelAttention(…)`
  descriptor. **(observed)** Architecturally each model is a small streaming **SVDF/TDNN stack**: affine, depthwise
  temporal conv, ReLU or hard-swish, batchnorm, and a 2-way softmax. There are 0.27–0.88 M weights per model.
- **The feature front-end is plain Kaldi fbank**, with the constants recovered from code: Povey window
  (`pow(0.5−0.5cos(2πn/(N−1)), 0.85)`), 1127·ln(1+f/700) mel, power spectrum, log floor `FLT_EPSILON`, preemph 0.97,
  remove-DC, dither 0.1, int16-scale samples, 25/10 ms, 40 bins, 20 Hz to Nyquist. A fixed global CMVN is baked into
  each model as `cmvn_add`/`cmvn_mul`. There is **no `lda.mat` on the active model.** The `cmn:lda` pipeline belongs
  only to the old fallback alisa model. **(observed + inferred, §4)**
- **The scorer is textbook Chen et al. 2014**, and the decompiled per-frame code matches it. It computes a w-smooth
  moving average of the posteriors, takes the max over a `w-max` window per label, and combines them with a geometric
  mean. The result is compared against `thresholds` (hit) and `subthresholds` (sub-hit), with a peak-picking emit
  rule. **(observed, §5)**
- **Gates around the trigger** are a two-stage event machine (`EVENT_1ST_STAGE_HIT` → `CONFIRMATION`/`REJECTION_2ND_STAGE`)
  plus several checks: a raw-mic "freq-filter", a TTS self-trigger blocker, an RMS blocker, and the TFLite
  **counteractivation** MLP (74 floats in, 1 logit out). The device also sends the activation to the cloud for
  **online validation**. Thresholds were tuned with those later stages in place. **(observed / inferred, §6)**
- **Severability:** the spotter wants 16 kHz int16 PCM. The active bergamot model is **2-channel**: an omni channel
  plus `bnr-channel true`, fused by the `MultichannelAttention` node. The fallback «Алиса»/«Яндекс» models are
  single-channel ("x1", "no-bnr"). According to the sibling VQE pass, `vqe_0` on bergamot is just **one
  Kalman-AEC'd mic** (no beamformer, no NR/AGC in ASR mode). A WebRTC AEC3 stream is therefore a reasonable stand-in.
  Expected risk: level and noise-texture mismatch, not a fundamentally different input. **(inferred, §7)**
- **Verdict:** decompilation **changes the picture from "not worth it" to "a cheap experiment is worth running"**.
  It does not replace the microWakeWord plan:
  - **Path A (cheapest, about 1–2 days):** write a small armhf C host on the device that `dlopen`s the vendor
    `libyandex_evil.so` and feeds it AEC'd 16 kHz PCM. This gives the real Yandex «Алиса» spotter, with no
    reimplementation risk. Costs: a proprietary blob stays on the device, and the API has 4–6 call signatures that
    still need roughly one more day of RE or trial.
  - **Path B (about 1–2 weeks to calibrated):** a clean-room numpy/TFLite reimplementation (nnet3 reader, about 8 op
    types, Kaldi fbank, a chen14 decoder). There are two unresolved details: GroupConvCorou tap order and the exact
    MultichannelAttention math. Without a vendor reference run, validation is behavioural only.
  - **Stock Kaldi/Vosk: no.** They cannot load these graphs, and Kaldi's decoder is the wrong tool anyway.
  - The local thresholds assume cloud re-validation and the 2nd stages. A standalone «Алиса» will show **more false
    accepts** than stock until it is re-thresholded on your own negatives.
  - **Recommendation:** keep microWakeWord as the sovereign default. Run Path A as an A/B, because Yandex's «Алиса»
    model was trained on far more real far-field data than a TTS-trained MWW model is likely to be. Go to Path B
    only if Path A works well enough to justify removing the blob.

---

## 1. Material and a dump defect

| Item | Path / fact |
|---|---|
| Engine | `system/vendor/quasar/libs/libyandex_evil.so`. **The copy under `dumps/rootfs/` is truncated**: 4,587,520 B (=0x460000) vs the real 4,912,400 B. It loses the last 0x40040 B of the RX LOAD segment plus all of `.data.rel.ro`/`.dynamic`/`.got`, so `nm`/`readelf` fail on it. The intact copy is in `dumps/rootfs-full.tar` (md5 `2efb61a8d2cdd95d2121936352dcf428`); I used a private extract in the scratchpad. **(observed)** |
| Orchestrator | `maind` (22 MB, Thumb-2). It imports the spotter C API through **GOT function pointers** (`R_ARM_GLOB_DAT spotter_*`, 41 symbols) plus 5 PLT slots. It does **not** import `spotter_feed_data`/`spotter_create`; it uses `spotter_conf_read → spotter_multi_conf_create → spotter_multi_conf_attach_spotter_conf → spotter_create_from_multi_conf → spotter_submit_raw_data → spotter_get_next_event`. **(observed)** |
| Active models | `rootfs/data/quasar/data/spotter.json` → `/data/quasar/data/spotter_model/<uuid>/`. Active alisa = `ru-RU-bergamot-alisa-13Nov-x3-bnr-minispeakers-29Jan24` (dir `8cf52356-…`, 1.89 MB nnet, **no lda.mat**). The `/system/.../spotter_models/activation/alisa` model (`…-04Mar22-fused-qd16-ca-no-bnr-no-ca`) is only the built-in fallback. The prior doc's §4.3 table describes the fallback's flags, while §4.2 names the active one. |
| Other models | command (nnet3, 24+ commands, also as TFLite `spotterRecognizer`), vad (`seam-vad-decoder`), intonationInterruption, counteractivation TFLite (§6). |

**Address convention:** Ghidra loaded the PIE at image base 0x10000, so **Ghidra address = ELF vaddr + 0x10000**.
Exported-symbol addresses below are ELF vaddrs as printed by `nm`. Internal `FUN_xxxxxxxx` names are Ghidra
addresses.

---

## 2. Engine structure

### 2.1 Source tree (from assertion paths in `.rodata`, observed)

```
voicetech/spotter/embedded/libdecode/neo/
  spotter_c_wrapper_neo.cpp            C API shims (try/catch -> spotter_error*)
  proxy/{conf,runner}.cpp              conf objects, async runner (TAsyncSpotterWrapper)
  spotter_internal/spotter_conf*.cpp   flags.txt parsing, channel requirements
  spotter/spotter.cpp, spotter_activation_priority.cpp, spotter_stub.hpp
  spotter_task/chen14.cpp              <- the scorer (§5)
  acoustic_model/nnet.cpp              AM wrapper (am-batch-size, am-tune-shifts, priors)
  feat_pipeline/{feat_pipeline,lda}.cpp   fbank/MFCC -> cmn -> splice/lda
  freq_filter/freq_filter.cpp          raw-mic narrow-band gate (§6.2)
  vad/vad.cpp, vad_seam/vad_seam.cpp   VAD + "seamless" (no-pause) activation
  counteractivation/{client_model,counteractivation_feats_computer}.cpp
  spotter_verifier/{spotter_verifier*,rms_blocker_verifier}.cpp
  spotter_npu_model/*                  TFLite ("NPU") acoustic models (spotterRecognizer)
  tts/tts_timings.cpp                  TTS self-activation blocker
  audio_enrollment/*                   voice-print enrollment
  vqe_asr_channel_selector/*           picks which VQE channel goes to ASR
voicetech/spotter/embedded/nnet/ynmt/{reader,config_parser,builder,graph,memory,storage,nnet_ynmt}.cpp
                                       Yandex NN runtime that reads nnet3 files
voicetech/spotter/embedded/util/neo/{kaldi_stream,read-binary,neo_configfile,...}.cpp
voicetech/spotter/counteractivation/library/compress_features/compress_features.cpp
```

### 2.2 C API (exported, observed). Argument counts come from the wrapper disassembly.

Every export is a thin shim. It checks whether the **last argument** (`spotter_error **err`) is NULL, calls the C++
implementation or a vtable slot, and on exception fills `*err` and returns 0. The handle is `{impl*, shared_ptr ctrl}`.
The implementation is reached via `*(*handle)` vtable.

| Export (vaddr) | Args before `err` | Implementation | Meaning (inferred where not obvious) |
|---|---|---|---|
| `spotter_conf_read` 0x281570 | 1 (`const char *dir`) | `FUN_002912c8` → `FUN_00430ae8` | load `flags.txt`/`words.txt`/nnet from a model dir → `conf*` |
| `spotter_conf_free` 0x2817ac | 1 | — | |
| `spotter_conf_native_sample_rate` 0x281850 | 1 (conf) | `FUN_00434a7c` | 16000 for all models |
| `spotter_conf_get_required_channel_count` 0x282894 | 2 (conf, ?) | `FUN_004309c4` | number of input channels the model wants |
| `spotter_required_channel_count_from_dirname` 0x281ea0 | 2 (dir, ?) | `FUN_00291dd0` | same, from a path |
| `spotter_create` 0x283590 | 3 (conf, ?, ?) | `FUN_00293354` → `FUN_00485494` | single-channel spotter |
| `spotter_create_multichannel` 0x28310c | 5 (conf, ?, `n`, `const int *chan_types[n]`, ?) | `FUN_00292e14` → `FUN_00485530` | copies `n` ints of channel types |
| `spotter_multi_conf_create` / `_attach_spotter_conf` / `spotter_create_from_multi_conf` | 2 / 2 / 6 | `FUN_004a1a4c` / `FUN_004a1e24` / `FUN_002970a0` | what `maind` uses (several spotters in one object) |
| `spotter_feed_data` 0x283ccc | 4 | vtable +0x28 | |
| `spotter_submit_raw_data` 0x285a50 | 3 (h, `const void *pcm`, `size_t nbytes`) | vtable +0x18 | interleaved **S16** PCM. `nbytes` must be even and a multiple of the channel count (`"Bad interleaved audio size %d for %d channels"`, `FUN_0041f100`), then it is deinterleaved per channel |
| `spotter_get_next_event` 0x285ec8 | 1 | vtable +0x20 | returns the event enum below |
| `spotter_get_current_phrase[_id]`, `_channel_id`, `_component_id`, `_activation_metainfo` | 1 | vtable +0x38/… | |
| `spotter_finish` / `spotter_destroy` | 2 / 1 | vtable +0x10 / +0x14 | |
| `spotter_get_local_counteractivation_score`, `spotter_update_metainfo` | 3+ | vtable +0x70 / +0x74 | §6.4 |

**Event enum** (string table at `.data.rel.ro` vaddr 0x4a59a8, used by `event_id_to_str`, observed):

```
0 NEED_MORE_DATA_FOR_1ST_STAGE   1 SUBHIT              2 CONFIRMATION_1ST_STAGE
3 1ST_STAGE_HIT                  4 NEED_MORE_DATA_FOR_2ND_STAGE
5 CONFIRMATION_2ND_STAGE         6 REJECTION_2ND_STAGE  7 FINISH
8 ACTIVATION_SEAMLESS            9 NEED_MORE_DATA_FOR_SEAM_VAD
```

`is_hit_or_subhit_event(e)` = `e < 6 && (0x26 >> e) & 1`, i.e. {1, 2, 5}. **(observed)** So a usable "wake" is
`CONFIRMATION_1ST_STAGE` (no 2nd stage configured) or `CONFIRMATION_2ND_STAGE`.

**Minimal host program (inferred, Path A):**
`conf = spotter_conf_read(dir,&e)` → `s = spotter_create(conf, …)` (or `_multichannel(conf, …, 2, {OMNI,BNR}, …)`) →
loop {`spotter_submit_raw_data(s, buf, n*2*ch, &e)`; while `(ev = spotter_get_next_event(s,&e))` is not
`NEED_MORE_DATA_*`: on `is_hit_or_subhit_event(ev)` read `spotter_get_current_phrase`}. The meaning of the `?` args
(probably sample-rate/callback/user-data) still needs to be pinned down. Do this by reading `FUN_00485494`/`FUN_00485530`
or the `maind` call sites.

---

## 3. Acoustic models: format, layers and the components that are not Kaldi

### 3.1 Inventory (parsed with my nnet3 reader, observed)

All models: `input-node name=input dim=40`, one output = softmax.

| Model | Weights | Layers (abridged) | Output | Total `ContextParam` |
|---|---|---|---|---|
| **alisa (active, bergamot x3-bnr)** | 875 k | cmvn → 2×SVDF(158) → `tdnn_8`(40) ‖ `self_attention_aux_9`=Affine 40×40 on *input* → **`MultichannelAttention(tdnn_8, self_attention_aux_9)`** → cmvn → 6×SVDF(681, Q16, batchnorm, ReLU) with 162-d bottlenecks → 2×SVDF(91) → Affine 2 | 2 (filler, алиса) | **16** |
| alisa (fallback 04Mar22) | 276 k | cmvn → 5×SVDF(356, Q16)/tdnn 96 → 2×SVDF(51) → 2 | 2 | 16 |
| yandex (svdf-x1) | 273 k | cmvn → 6×SVDF(311)/tdnn 68 → 2×SVDF(49) → 2 | 2 | 16 |
| yasmina (fallback) | — | 7×SVDF(195)/tdnn 91 → 2×SVDF(180) → 2. No cmvn components | 2 | 16 |
| command | — | cmvn → SVDF(242)… **hardswish** … → `Append(softmax_27[22], softmax_28[6])` | 28 | 16 |
| intonationInterruption | — | 8×SVDF, hardswish | — | 16 |
| vad | — | 5×SVDF(100), ReLU → 2 | 2 (`<speech_started>`/`<speech_finished>` via decoder) | 11 |

Component types seen: `FixedBiasComponent`, `FixedScaleComponent`, `FixedAffineComponent`, `RectifiedLinearComponent`,
`SoftmaxComponent` (stock Kaldi), plus **`Q16FixedAffineComponent`** (`<S>` float scale + `SIM` = int16 matrix),
**`GroupConvCorouComponent` / `Q16GroupConvCorouComponent`** (`<LinearParams> [D×K]`, `<BiasParams> [D]`,
`<ContextParam> c`), **`<hardswish>`**, and the descriptor **`MultichannelAttention(a,b)`**. The ynmt reader also knows
`Q8FixedAffineComponent`, `Q8GroupConvCorouComponent`, `Pcen`, `GetChannel`, `LstmComponent`, `GruComponent`,
`Conv1dComponent`, `MaxPool1d`, `Rotary*` and more (tag strings `<q16fixedaffinecomponent>`, `<groupconvcorou>`,
`<multichannelattention>`, `<getchannel>`, `<pcen>` …). The dispatcher is `FUN_002c5540`. `FUN_002cc4f0`/`002ccbec`/
`002cd34c` read `<ContextParam>` and rebuild the component as a generic ynmt conv node with tensors `conv_mat` and `bias`.

### 3.2 Semantics (observed where stated, otherwise inferred)

- **Q16 affine:** `W = int16 · S` (one per-matrix scale `<S>`, e.g. 2.9e-4). The bias is float. You can dequantize to
  float without loss of meaning. The int16 kernel only adds quantisation noise. **(inferred)**
- **GroupConvCorou = SVDF time filter:** a per-channel (depthwise) 1-D convolution over **K = cols** frames
  (D×K matrix, one K-tap filter per dimension, plus bias). Evidence: the TFLite port of the command spotter
  (`spotterRecognizer/model.tflite`) implements the same block as `CONCAT(state[K−1 frames], new) → DEPTHWISE_CONV_2D
  (K taps)`. Its state tensors are exactly K−1 frames: 44/39/37/40/44 for K = 45/40/38/41/45. **(observed)**
- **`ContextParam` = per-layer right context (look-ahead frames).** For **every** activation/command/intonation model
  the ContextParams sum to **16**, and the TFLite port carries metadata `right_context = 0x10 = 16`. **(observed)**
  So a kernel spans `[t−(K−1−c), t+c]`, and in streaming each layer delays by `c`. The total spotter latency from
  look-ahead is 160 ms. **(inferred, strong)**
- **Tap order (unresolved):** whether `LinearParams[:,0]` is the oldest or the newest frame. The TFLite and nnet
  command models are different trainings (244 vs 242 channels), so their weights cannot be cross-checked. Flag
  `tf-tdnn true` (active alisa, yasmina) probably selects a TF-style vs Kaldi-style convention. **(inferred)** This
  is a 2-way ambiguity per model, testable empirically.
- **MultichannelAttention(a, b)** (`FUN_0041b9c0` parses the descriptor; error strings "Expected 2 terms…", "Expected 2
  input specs for multichannel attention node"). ynmt tensors are `(batch, n_channels, n_features)` ("Expected input
  specification to contain 3 dimensions (batch_size, n_channels, n_features)"). In the bergamot model `a` = SVDF branch
  → 40, and `b` = Affine(input) → 40, both computed per channel. My reading **(inferred, plausible not proven)**:
  `out[f] = Σ_ch softmax_ch(a[ch,f]) · b[ch,f]`, which fuses the omni and BNR channels into one 40-d stream that then
  goes through the rest of the network. I did not decompile the kernel itself.
- **Hard-swish:** standard `x·relu6(x+3)/6`. The TFLite port uses the builtin `HARD_SWISH`. **(observed in TFLite)**

### 3.3 TFLite spotter (`spotterRecognizer/model.tflite`, observed)

Inputs: `frames [1,1,6,40]` plus 5 streaming states `[1,1,{44,39,37,40,44},{244,244,244,244,400}]`. Outputs:
`logits [1,1,6,28]` (two softmaxes, 22+6) plus the new states. Ops: `DEPTHWISE_CONV_2D, CONCATENATION, STRIDED_SLICE,
HARD_SWISH, SOFTMAX, MUL` and the custom op **`LINEAR_FP16`** (an FC with fp16 weights). The custom op is registered by
Yandex's patched `libtflite.so` under resolver tag `SPOTTER_ACTIVATION` (strings `SPOTTER_ACTIVATION`,
`COUNTERACTIVATION`, `DW_CONV_1D_Q8`, `voicetech/common/tflite_backend/lib/resolvers/custom_ops/…` in
`libtflite.so`). *Correction to the prior doc:* `SPOTTER_ACTIVATION` is the resolver tag. The missing custom op is
`LINEAR_FP16`, and it is trivial to replace with a builtin `FULLY_CONNECTED` after an fp16→fp32 conversion.

---

## 4. Feature front-end (recovered constants)

Option parsing: `FUN_002afc9c` (frontend), `FUN_002a4ba8` (frame options), `FUN_002b0a9c` (mel). Compute: window
`FUN_002a4e50`, framing + power spectrum `FUN_002a50d8`, mel banks `FUN_002b0e74`, log `FUN_002b08ac`, DCT `FUN_002b00d0`.

| Item | Value | Evidence |
|---|---|---|
| Input | int16 samples converted to float **without scaling** (Kaldi convention) | `FUN_002a50d8`: `short → float` |
| Frame | `frame-size 25` ms, `frame-shift 10` ms, `sample-rate 16000` | flags |
| `remove-dc-offset` | default **true** | `FUN_002a4ba8` |
| `dither` | default **0.1** (flags also 0.1) | `0x3dcccccd` |
| `preemph-coeff` | default **0.97** | `0x3f7851ec` |
| Window | **Povey**: `powf(0.5 − 0.5·cos(2πn/(N−1)), 0.85)` | `FUN_002a4e50`, consts 2π (double) and 0.85 |
| Spectrum | power `re²+im²` | `FUN_002a50d8` |
| Mel | `1127·ln(1 + f/700)`, `mel-num-bins` default 23 (flags: **40**), `mel-low-freq` default **20 Hz**, `mel-high-freq` default **0** (= Nyquist) | `FUN_002b0e74`/`FUN_002b0a9c` |
| Log | `log(max(x, 1.1920929e-07))` | `FUN_002b08ac` |
| Frontends | `feat-frontend` ∈ {`mfcc` (default, `num-ceps` 13, `cepstral-lifter` 22), `mel-fbank`, `log-mel-fbank`, one 11-char variant} | `FUN_002afc9c` |

This is **Kaldi `compute-fbank-feats` behaviour** (the Kaldi `MelBanks`/`FeatureWindowFunction`/`ComputeDctMatrix`
math, probably vendored). `kaldi-native-fbank` or `torchaudio.compliance.kaldi.fbank` with `window_type=povey,
dither=0.1, num_mel_bins=40, low_freq=20, high_freq=0, use_energy=False` should reproduce it to within dither noise.
FFT size is presumably 512 (Kaldi round-to-power-of-two). **(inferred, not separately verified)**

**Normalisation:** the active alisa, yandex and command models start with `cmvn_add_0`/`cmvn_mul_1` holding **identical**
vectors. Bias begins `−5.77, −7.89, −7.19, −6.75, −7.36, −7.71, −7.98, −8.06` (every 5th bin) and scale `0.43…0.59`
**(observed)**. That is a fleet-wide fixed global mean/variance, not a per-device or online CMN. The new models have no
`lda.mat`. Note that the absolute log-energy matters, so **input gain matters** (§7.3).

**Fallback alisa (04Mar22) only:** `feat-pipeline cmn:lda`, `cmn-window 600`, `cmn-latency 50` and `lda.mat` = Kaldi
binary `FM` **40×91** (observed). Its flags omit `feat-frontend`, so the default is `mfcc` with 13 ceps, and
13 × 7 (splice ±3) = 91. Pipeline: MFCC13 → sliding CMN (600 frames) → splice ±3 → LDA → 40 → nnet. **(inferred)**

---

## 5. chen14 scorer (decompiled, `chen14.cpp` unit, Ghidra 0x450000–0x458000)

| Function | Role |
|---|---|
| `FUN_0045245c` | config: reads `hit-at-peak`, `w-max` (stored **divided by `skip-rate`**), `w-smooth`, `metric` (`geometric`→0, a 7-char alternative→1), thresholds, `fire-at-*` options |
| `FUN_00453548` | parses per-phrase `w-smooth` (`"There is no w-smooth attribute…"`, `"Skip-rate must be not higher than w-smooth"`) |
| `FUN_00454470` | **posterior smoothing.** Per label it keeps a running sum over the last `w-smooth` frames (add new, subtract leaving, ring buffer), divides by `min(t, w_smooth)`, clamps at ≥ 0 (and counts values < −1e-6 as numeric errors), and stores into a `w-max` ring buffer |
| `FUN_00453fcc` | **confidence.** For each phrase and each label in it, takes the max of the smoothed posterior over the `w-max` ring. `metric 0` (geometric) multiplies the maxima and takes the `n`-th root (`sqrtf` when n=2, `powf(x,1/n)` otherwise) |
| `FUN_00453a64` | **decision.** Per phrase it classifies `conf > threshold` → 2 (hit) and `conf > subthreshold` → 1 (sub-hit), and picks the phrase with the best `conf/threshold` ratio. Returns `idx+1` for a hit and `~idx` (negative) for a sub-hit. An optional single override threshold exists (`+0x58/+0x5c`) |
| `FUN_00454fa0` | per-frame driver: runs the AM output through smoothing, confidence and decision, then applies the `hit-at-peak` mode (0/1/2). In mode 2 ("subhit") the event is emitted when the confidence stops rising, using the previous frame's result. It also handles `interval-after-last-activation`, `zeroing-confidence-buffer-after-hit` and reset |

So for a phrase with labels L, smoothed posteriors p̄ and window W = w-max, the confidence is
**conf(t) = (∏_{l∈L} max_{t−W<τ≤t} p̄_l(τ))^{1/|L|}**. Fire when conf > `thresholds` (sub-hit when > `subthresholds`),
at the peak. For the 2-class alisa model, L = {label 1 "алиса"}, so conf is simply the max of the smoothed
"алиса" posterior over `w-max` frames.

**Flags to behaviour (active bergamot alisa, observed values):** `w-smooth 4`, `w-max 20`, `skip-rate 1`,
`metric geometric`, `hit-at-peak subhit`, **`thresholds 0.928`, `subthresholds 0.355`**, `commands 1`,
`am-batch-size 6` (6 frames per NN call, the same as the TFLite `[1,1,6,40]`). Fallback alisa: 0.44/0.22, w-smooth 16,
w-max 60. Yandex: 0.93/0.46. `spotter.json` holds no thresholds; it is only a manifest (type/crc/path/url). Thresholds
live in each model's `flags.txt`.

---

## 6. Gates: second stage, counteractivation, VAD, intonation

### 6.1 Two-stage event machine (observed)

States `PROCESSING_LOGITS`, `PULL_OUTPUTS`, `SUBTHRESHOLD_ACTIVATION`, `REJECTION_2ND_STAGE` (assert strings). Second-stage
components that can reject a 1st-stage hit: `SpotterVerifier` (`verifier-enabled`, `verifier-model`), `rms-blocker`,
the TTS blocker (`use-tts-blocker true` on all activation models; it blocks self-activation from Alice's own TTS using
`tts-timings-info/`), and `freq-filter`.

### 6.2 freq-filter (on the **raw mic**, not vqe_0)

Flags on every activation model: `freq-filter-enabled true`, `freq-filter-audio-channel raw-mic`, frequencies
`3501:2089:814:1355:638:4510:2635:486 Hz`, `lower/higher-freq-ratio 0.68/1.45`, `threshold −3.6`, `frame 252 / shift 177`,
`left-context 979`, inside/outside smoothing with `mean`/`median`. Code: `FUN_002a0d2c`, `FUN_002a11c8`, `FUN_002a1418`,
`FUN_002a308c`. Reading **(inferred)**: it compares band energy at each listed frequency with its neighbourhood
(0.68–1.45×) over ~1 s before the hit. A strong narrow-band signature (an electronic sound source, or the device's own
playback) rejects the hit. It needs a raw-mic channel, so in a standalone host you either provide one or disable it.

### 6.3 Channels the spotter consumes (observed: `FUN_0029ee50` channel-descriptor parser)

| Flag | Default | Meaning |
|---|---|---|
| `omni-channel` | **true** | main (VQE omni) channel |
| `bnr-channel` | false | VQE background-noise-reducer channel |
| `beamforming-channels` | 0 | N beamformed channels |
| `feedback-channel` | false | speaker reference |
| `raw-mic-am-channels` / `raw-mic-channels` | 0 | raw mics into the AM / for freq-filter |

The active bergamot alisa sets `bnr-channel true`, so the AM gets **omni + BNR** (2 channels), fused by
`MultichannelAttention`. The fallback alisa/yandex use omni only. Asserts "Counteractivation am needs omni channel",
"vad am needs omni channel", `ChanDesc.ChanTypeToIndices(CHANNEL_OMNI).size() == 1` (`FUN_0048355c`). On bergamot,
`vqe_0`→omni and `vqe_bnr`→BNR (matches `speechkitLogChannels`). **(inferred mapping)**

### 6.4 Counteractivation (TFLite, observed I/O)

`counteractivation-features/local-ab1-tiny-husky-53.tflite` (55 KB): **input `serving_default_x:0` FLOAT32 [1,74]** →
FC 74→64 → ELU → FC 64→64 → ELU → FC 64→64 → ELU → FC 64→1 → **output FLOAT32 [1,1]** (a logit; ONNX→TF converted,
builtin ops only, so stock TFLite runs it). The feature layout comes from `model_list.json`: `[0,23)` omni fbank,
`[23,46)` raw-mic fbank (23 = the default `mel-num-bins`, since the CA `flags.txt` doesn't set it; `frame-size 20`),
`[46,62)` one-hot device (`Yandex bergamot` = 57), and `[62,74)` "MM" (Megamind/cloud dialog) context:
`isPlayerStopCommand, hasScreenRelatedAnswer, isActiveCall, currentlyPlayingRelated, secondsSinceLastPlayback…,
isDeviceOn…`. The MM fields are set through `spotter_update_metainfo` and the score is read with
`spotter_get_local_counteractivation_score`. The features are also serialised (`Counteractivation.TCounteractivationAcousticFeatures`
protobuf, `"counteractivation_features"` JSON) for **server-side** counteractivation. The acoustic part is a
windowed/compressed fbank over the activation segment (`ca-window-features true`, `matrixFbanksDownsampleFactor`,
`compress_features.cpp`); I did not recover the exact pooling. **(inferred)** The decision threshold is applied by
the client and was not found in the cfg.

### 6.5 Online validation

`quasar.cfg`: `onlineSpotterValidationDisabledTypes: ["noise-detection","vad"]`, `spotterSubscribePointShiftMs: −80`,
API `spotter_get_current_activation_info_for_online_validation`. **Activation spotters are re-validated by the cloud.**
The local threshold therefore trades toward recall, with cloud and counteractivation cleaning up. **(inferred from
config + API)** This matters for standalone FAR.

### 6.6 VAD and intonation

- `vad`: `spotter-type seam-vad-decoder`, 5×SVDF(100) → 2-way softmax, `threshold 0.9`, `w-smooth 1`,
  `audio-length-model-threshold 20:20`. It drives `<speech_started>/<speech_finished>` and "seamless" activation
  (wake word followed by the command with no pause, `EVENT_ACTIVATION_SEAMLESS`, `speech-length-for-seamless`).
  `aliced.vadEnabled=false`, but two instances are loaded.
- `intonationInterruption`: a chen14 1-label model (`<intonation>`, threshold 0.47, `w-max 1`, `w-smooth 8`). It resets
  and is reset by `activation:commands`.

---

## 7. Severability from the Yandex front-end

### 7.1 What the spotter actually needs

16 kHz, S16, interleaved for multichannel. Kaldi fbank-40 is computed internally. The bergamot alisa needs 2 streams
(omni + BNR), the fallback alisa/yandex 1 stream (omni). A raw-mic stream is optional and only used by freq-filter
and counteractivation.

### 7.2 What `vqe_0` is (from the sibling VQE pass, `docs/research/vqe-decompile.md` §6)

On bergamot there is **no beamforming**. The chain is 48→16 kHz decimation and an RC high-pass (~178), then a
**per-mic Kalman AEC** against the hardware amp-feedback reference, then the "healthiest" mic (default index 0) →
`vqe_0`. A 2-reference-mic Kalman noise canceller produces `vqe_bnr`. NN-NR and AGC run only in call mode. The ×50
gain goes to `vqe_gain`, and it is not established that `vqe_0` gets it.

### 7.3 Can a WebRTC-AEC3 16 kHz mono stream substitute? (inferred)

- **Structurally yes.** An AEC'd single mic is what the model was trained on. AEC3 plus a ~150–180 Hz HPF on mic0 with
  the HW feedback reference matches the *kind* of signal.
- **Turn WebRTC NS/AGC off or keep them mild** for the spotter tap. Yandex runs neither on the ASR/wake path, and NS
  artefacts and AGC pumping are exactly the mismatch that would hurt.
- **Level is the main knob.** The global CMVN means (log power per mel bin) sit at 5.8–8.1. My Kaldi-fbank reference
  (`kfbank.py`) gives white noise of σ = 1 LSB as −1.2…8.7 and σ = 2 LSB as 0.1…10.1 across bins 0…35, so the
  training input is near native int16 mic scale (a quiet floor of about 1 LSB at HF, more at LF). It is **not** a
  +34 dB-amplified stream. Calibrate by measuring the mean fbank of your front-end over a mixed quiet/speech recording
  and adjusting a fixed gain until it matches `−cmvn_add` within about 1 nat. **(inferred)**
- **The BNR channel for the bergamot model:** you have no 2-ref noise canceller. Options: (a) feed the same AEC'd
  stream as both omni and BNR (the attention then sees two identical channels, which is probably close to
  "omni-only"), or (b) use the single-channel fallback alisa (`04Mar22`, raw-mic-channels 1, MFCC+CMN+LDA front-end)
  or yandex-x1. (a) is the first thing to try.
- **Expected accuracy hit:** unknown without testing. Spotters like this are trained with heavy augmentation, so I
  would expect a moderate FRR increase rather than collapse, and a larger FAR increase from losing the cloud and
  counteractivation stages. **This must be measured.** Suggested protocol: about 200 «Алиса» utterances at 1/3/5 m
  with and without music playback, plus 24 h of household audio/TV for FA/h, sweeping the threshold from 0.6 to 0.95.

---

## 8. Standalone paths and effort

| Path | What | Effort (focused) | Risk |
|---|---|---|---|
| **A. Vendor-lib host on the device** | ~200 lines of armhf C: `dlopen libyandex_evil.so` (+ `libtflite.so`, `libiconv.so` already in `/system/vendor/quasar/libs`) and drive the C API from §2.2. Output a Wyoming wake event or a local socket | **1–2 days**: about 0.5–1 day of RE to pin the 4–6 unknown args (`FUN_00485494`/`00485530`, or `maind` call sites; the remaining `spotter_create` args), the rest is coding and test | Proprietary blob stays; no Thumb/ABI issue (lib is ARM, armhf, glibc). Untested on our unit with `maind` off, but the lib has no `maind` dependency. RAM: about 10 MB |
| **B. Clean-room reimplementation** | nnet3 reader (prototype done: `nnet3dump.py`), dequantize Q16, ops {affine, bias, scale, depthwise-time-conv w/ right context, ReLU, hard-swish, softmax, MultichannelAttention}, Kaldi fbank (off-the-shelf), chen14 decoder (~80 lines). Optionally export to a streaming TFLite like `spotterRecognizer` | **3–4 days to first posteriors, 1–2 weeks to calibrated** | Two unresolved details (tap order, attention math). No reference outputs unless Path A is run on the device once to capture posteriors. Compute ~90 MMAC/s for the bergamot model, ~28 MMAC/s for x1 models, fine on Cortex-A |
| C. Stock Kaldi / Vosk | load the nnet3 files directly | **not viable** | Kaldi has no Q16*/GroupConvCorou/hardswish/MultichannelAttention. Converting to stock nnet3 needs per-layer rewriting (TimeHeightConvolution or Append/Offset tricks), and Kaldi's HCLG decoders don't do chen14 anyway. B is strictly easier |
| D. microWakeWord in place of the Yandex frontend | — | n/a | MWW uses the TFLM micro-frontend (PCAN, different features), so the Yandex models cannot be dropped into MWW. They need their own Wyoming wrapper |

---

## 9. Verdict, quantified

1. **Did decompilation change the recommendation?** Partly. The prior pass guessed "high-effort, low-payoff;
   Yandex-specific frontend; custom TFLite op". The facts are simpler: Kaldi fbank, a fixed global CMVN, a tiny SVDF
   network, a textbook chen14 decoder, an AEC'd single mic upstream, and a self-contained C API. **Reuse is
   realistic.** Path A costs 1–2 days; Path B costs 1–2 weeks.
2. **What remains true:** the models know only «Алиса», «Яндекс», "yasmina" and the 24 commands. They are proprietary.
   Their thresholds were tuned for a pipeline that included cloud re-validation and counteractivation, which used
   cloud context we won't have. Standalone FA/h will be higher at the stock 0.928 threshold, and re-thresholding on
   our own negative audio is mandatory.
3. **Recommendation:** keep **microWakeWord via Wyoming** as the sovereign baseline, since it is trainable for any
   phrase and blob-free. Add **Path A as a time-boxed A/B (≤ 2 days)**. If Yandex «Алиса» clearly beats our MWW model
   in FRR at equal FA/h on the same AEC3 stream, keep it, and later port it with Path B to drop the blob. The offline
   **command** spotter (24 commands, the same engine) comes almost free with either path.

---

## 10. Tools, limits and resource use

- Tools: Python 3.10 (`capstone` 5.0.7, `pyelftools`, numpy), my own nnet3 reader and TFLite flatbuffer reader,
  `readelf`/`nm`/`strings`, and **Ghidra 12.1.4 headless** (one import+analysis, 122 s, then read-only
  `-noanalysis` decompile batches by string xref, address and range).
- Limits: Ghidra ran under `systemd-run --user --scope -p MemoryMax=6G -p MemorySwapMax=0`, `GHIDRA_HEADLESS_MAXMEM=4G`,
  `nice 19 ionice -c3`, `-max-cpu 4`. `ulimit -v` breaks the JVM, so a cgroup cap was used instead. Observed system
  "used" memory rose from ~3.2 GiB to ~4.3 GiB during analysis, i.e. **peak ≈ 1.1–1.5 GB** for this work, with no swap.
- Not done: execution or emulation of vendor code, and decompiling the ynmt compute kernels (GroupConvCorou tap order,
  MultichannelAttention math, Q16 int kernels), the counteractivation pooling, and the freq-filter decision details.
- Scratch artefacts (not part of the repo): `<session-scratchpad>`
  → `nnet3dump.py`, `tflinfo.py`, `kfbank.py`, `armdis.py`, `gscripts/DecompByStrings.java`, `decomp*/` (Ghidra C
  output), `all_nnets.txt`, `strings.txt`.

## Appendix: address map (Ghidra address = vaddr + 0x10000)

| Ghidra addr | What |
|---|---|
| 0x2912c8 / 0x430ae8 | conf read |
| 0x292e14 / 0x485530 | create multichannel |
| 0x293354 / 0x485494 | create |
| 0x29ee50 | channel-descriptor flags (omni/bnr/beamforming/feedback/raw-mic-am) |
| 0x29ff14 | "%d channel count doesn't match (%d in spotter conf, %d in api call)" |
| 0x41f100 | interleaved S16 → per-channel split |
| 0x2a4ba8 / 0x2afc9c / 0x2b0a9c | frame / frontend / mel option parsing |
| 0x2a4e50 / 0x2a50d8 / 0x2b0e74 / 0x2b08ac / 0x2b00d0 | Povey window / power spectrum / mel banks / log / DCT |
| 0x2b1b9c / 0x2b8f84 / 0x2b69e4 | feat pipeline / lda / cmn-window |
| 0x2c5540 / 0x2cc4f0 | ynmt component dispatcher / GroupConvCorou reader |
| 0x41b9c0 | MultichannelAttention descriptor parser |
| 0x45245c, 0x453548, 0x454470, 0x453fcc, 0x453a64, 0x454fa0 | chen14 config, w-smooth, smoothing, confidence, decision, frame driver |
| 0x2a0d2c, 0x2a11c8, 0x2a1418, 0x2a308c | freq-filter |
| 0x4773d0, 0x47a35c, 0x4a03c4, 0x49f86c | counteractivation config, fbank features, feature getter, client model |
| 0x48355c, 0x480d80 | vad_seam, vad |
| 0x4368a4 | tts-blocker config |
