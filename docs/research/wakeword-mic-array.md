# Wake-word & Microphone-array Reverse Engineering Reference

**Device:** Yandex Station Mini 3 — codename `bergamot` (`authd.deviceName` = "Яндекс.Станция Мини 3").
**Firmware under analysis:** quasar `3.210.369.1.46.4778262544.20261013.117` (dump at `dumps/rootfs/system/vendor/quasar/`).
**Status:** the vendor `maind`/quasar stack is **permanently disabled** on our unit; this is a static-analysis reference for rebuilding its audio front-end with sovereign tooling. It is the low-level companion to [docs/research/hass-integration.md](hass-integration.md) (do not edit that file from here).

Everything below is derived primarily from the on-disk dump (configs, model files, and the stripped `maind` + `libyandex_evil.so` binaries); a few points were confirmed read-only against the live device. Where a claim is an inference rather than a direct observation it is labelled **(inferred)**.

---

## 0. TL;DR

- **Mics:** 3 PDM MEMS microphones + 1 hardware echo-reference ("feedback") channel. All arrive on one ALSA capture device `hw:0,1` (kernel `PDM-dummy`), S16_LE, 48 kHz, demuxed by `/etc/asound.conf`.
- **VQE (mic-array DSP):** Yandex's own in-house **"VQE" (Voice Quality Enhancement)** engine, compiled into `libs/libyandex_evil.so` (C++ namespace `vqe::`). It does AEC (Kalman/LMS, NEON-optimised), beamforming (delay-sum / MVDR / relative-to-omni), background-noise reduction (classical + a 525 KB TFLite neural model), dereverb, AGC and a direction-of-arrival / zone tracker. Device-specific tuning comes from a compiled-in preset keyed `"bergamot"`. It can offload to the Amlogic HiFi4 DSP via `libhifi4rpc.so`, falling back to the ARM CPU.
- **Wake word:** Yandex SpeechKit "spotter" — a small-footprint **`chen14` DNN keyword spotter** (Chen/Parada/Heigold 2014), also inside `libyandex_evil.so`. Acoustic models are **Kaldi nnet3** (`acoustic_model.nnet`). Three activation keywords ship: **алиса / яндекс / yasmina**. Supporting spotters: VAD, a 24-command offline spotter, an intonation-interruption spotter, and a TFLite **counteractivation** (false-trigger suppressor).
- **What we lost with `maind` off:** all of the above. Raw capture is now 3 unprocessed PDM channels + an echo reference, with no AEC, no beamforming, no NS/AGC, and no on-device wake word.
- **Sovereign replacement (recommended):** do **not** try to resurrect the Yandex engine. Use `webrtc-audio-processing` (AEC3 + NS + AGC) fed the hardware feedback channel as the AEC reference, optional simple delay-sum beamforming or just pick one mic via ALSA, and **microWakeWord** (TFLite-micro) for the wake word — the user already has a Russian MWW training pipeline in `<ha-config-repo>/mww-training/`.

> **⚠ CORRECTION (2026-10-22, superseded by [vqe-decompile.md](vqe-decompile.md)):** a follow-up
> decompile of the `bergamot` preset shows this device **does NOT beamform** — `beamformingMode = NONE`
> and the beamformer factory literally throws *"Beamforming is not implemented for
> MicSetup::RIGHT_TRIANGLE_43MM_LEG"*. `RELATIVE_TO_OMNI` is a `multimicAecMode`, not a beamforming
> mode (bergamot uses `INDEPENDENT`). So statements below about delay-sum/MVDR being active for
> bergamot are wrong; the real front-end is per-mic Kalman AEC vs the HW feedback reference + pick the
> healthiest omni mic + a 2-ref Kalman BNR (`vqe_bnr`) + HPF ~178 Hz + ×50 ASR-mode gain. The neural NR
> (`model_call.nnet`) is a standard **GTCRN** used **only in call mode**, reusable standalone. Full
> recovered spec + numbers: [vqe-decompile.md](vqe-decompile.md). "Skip beamforming" is therefore exactly what Yandex
> does, not a shortcut.
>
> **⚠ DEVICE RESULTS (2026-10-22, see [voice-phase2-plan.md](voice-phase2-plan.md)):** measured on the unit,
> `hw:0,1` ch0/1/2 are the live mics, ch3 is a copy of ch2, ch4/5 are empty and ch6/7 are −1 when idle (ch6 = amp feedback, playback
> not yet verified). The vendor VQE (`YandexVqeC_*` C API) and the vendor spotter (wake + 27-word command model)
> both run natively from our own host, without maind. The whole pipeline costs about 87 % of one A53 core.
>
> **⚠ WAKE-WORD CORRECTION (2026-10-22, see [spotter-decompile.md](spotter-decompile.md)):** the spotter
> engine is **NOT** locked inside `maind` — `libyandex_evil.so` exports a full standalone C API
> (`spotter_conf_read`/`spotter_create*`/`spotter_feed_data`/`spotter_get_next_event`), deps only
> libtflite/libiconv/glibc. So "don't resurrect the Yandex engine" is now a *choice*, not a necessity:
> **Path A (~1–2 days) = `dlopen` the vendor lib on-device and feed it AEC'd 16 kHz int16 PCM → the real
> «Алиса» spotter** (single-channel fallback models fit a mono stream). microWakeWord stays the
> sovereign default; Path A is a worthwhile A/B (Yandex's model saw far more real far-field data). The
> models use the Kaldi nnet3 *format* but a Yandex "ynmt" runtime with custom ops → stock Kaldi/Vosk
> can't load them. Standalone thresholds will false-accept more (tuned assuming cloud re-validation).

---

## 1. System overview & process model

`quasar_launcher2` reads `launch.json` and spawns the multi-call binary `maind` in several personalities (`--platformd`, `--mediad`, `--updatesd`, `--telemetry`, `--fluent-bitd`). `maind` is a **22 MB, 32-bit ARM EABI5 hard-float, PIE, stripped** ELF (`BuildID fa764f95…`), glibc/`ld-linux-armhf.so.3`. The other vendor "binaries" (`quasar_client`, `tcp_alsa_writer`, `clock_led_util`, `led_util`, `metrics_collector`, …) are **byte-identical copies** of `maind` (same md5 `38bb6ef5…`) — it is a busybox-style multicall engine that branches on `argv[0]`/flags.

`maind` links its heavy lifting out to `libs/`:

| Library | Size | Role |
|---|---|---|
| `libyandex_evil.so` | 4.59 MB | **The catch-all Yandex engine**: SpeechKit spotter (`Spotter::`), VQE DSP (`vqe::`), feature extraction, Amlogic DSP IPC. This is the single most important file for both subsystems. |
| `libtflite.so` | 3.33 MB | TensorFlow Lite runtime (SONAME `libtflite.so`), used by the counteractivation model, the `spotterRecognizer`, and the VQE neural noise reducer. |
| `libquasar_gstreamer.so` | 5.86 MB | GStreamer playback pipeline (not capture). |
| `libglib.so`, `libiconv.so` | — | support. |

`maind`'s `DT_NEEDED`: `libasound.so.2, libiconv.so, libtflite.so, libglib.so, libyandex_evil.so, libavahi-*, libdl, librt, libpthread, libc, libm`; `RUNPATH=/system/vendor/quasar/libs`. Launched by `run.sh` with `LD_LIBRARY_PATH=/system/vendor/quasar/libs`, `MALLOC_ARENA_MAX=2`, `ulimit -s 1024` (1 MB stacks, "over 100 threads").

Inside `maind` the audio front-end is the **`audiod`** component (config key `audiod` in `quasar.cfg`) which owns ALSA capture + VQE, and **`aliced`** (the Alice voice dialog) which owns the spotters and the uplink to `uniproxy.alice.yandex.net`. The `audiosender` component tees named audio channels for logging/debug.

> **Load-path note:** symbol/string evidence for *both* the spotter and the VQE is found in `libyandex_evil.so`, **not** in `maind` directly (`strings maind | grep chen14` → empty). The engine is the library; `maind` is the orchestrator.

---

## 2. Microphone array & capture path

### 2.1 Hardware (confirmed live, read-only)

`arecord -l` / `/proc/asound` on the live device:

```
card 0: AMLAUGESOUND [AML-AUGESOUND]
  device 0: TDM-B-acm8625 acm8625.1-002c-0   (playback 1 / capture 1)   → hw:0,0
  device 1: PDM-dummy dummy-1                 (capture 1)                → hw:0,1
```

- The SoC is **Amlogic** (confirmed by the `vqe::AmlogicIPCClient` / `libhifi4rpc.so` DSP path, §3.5). `acm8625` is the **smart speaker amplifier/codec** on I²C (`1-002c`) wired over TDM-B; it exposes a **capture** stream = the amp's current/voltage **feedback** (real acoustic output), used as the AEC reference.
- Mics are **PDM** (pulse-density-modulation MEMS), captured through the Amlogic PDM block (`hw:0,1`, kernel name `PDM-dummy`). Relevant mixer controls (live): `PDM Gain`, `PDM Filter Mode`, `pdm dclk` (`3.072m` → supports 8/16/32/48 k), `pdm pwr thr low/upper`.
- A hardware **Loopback** feeds the playback TDM back into capture: controls `Loopback Enable`, `Loopback datain source` (`TDMIN_A/B/C` | `PDMIN`), `Loopback tmdin lb source` (`TDMOUT_A/B/C`…). This is how the speaker signal becomes an in-stream AEC reference without an external wire.

### 2.2 ALSA routing (`dumps/rootfs/etc/asound.conf`)

```
pcm.!default { type asym; playback.pcm "speakers"; capture.pcm "multi_capture"; }

pcm.multi_capture {
  type multi
  slaves { loopback { pcm "hw:0,1"; channels 8 } }
  bindings {
    0 { slave loopback; channel 0 } # mic0 (PDM0)
    1 { slave loopback; channel 1 } # mic1 (PDM1)
    2 { slave loopback; channel 2 } # mic1 (PDM2)   ← sic: this is mic2
    3 { slave loopback; channel 6 } # HW feedback (TDMB_IN0)
  }
}
```

So the kernel presents `hw:0,1` as an **8-channel** stream; the vendor picks **4**: PDM channels 0/1/2 = the three mics, channel 6 = the amplifier feedback (TDMB_IN0) = AEC reference. The default capture PCM delivered to `audiod` is therefore **4ch: `[mic0, mic1, mic2, spk_ref]`**.

Playback path (for completeness / AEC-reference context): `pcm.speakers → dmix_speakers → hw:0,0`, 2ch, **48000 Hz**, period 960 / buffer 7680. A Bluetooth softvol variant exists in `asound-bluetooth.conf`.

### 2.3 Capture format & channel naming (`quasar.cfg → audiod`)

```json
"audiod": {
  "audioDevice": {
    "deviceName": "default",
    "inRate": 48000,          // capture at 48 kHz
    "outRate": 16000,         // VQE emits 16 kHz (spotter/ASR rate)
    "micChannels": 3,
    "spkChannels": 1,         // one speaker/echo reference
    "mainChannel": "vqe_0",   // the enhanced channel everyone consumes
    "periodSize": 256, "sampleSize": 2,   // S16
    "vqe": { "type": "yandex", "preset": "bergamot",
             "defaultModelStoragePath": "${QUASAR}/vqe_models" }
  },
  "brokenMicDetector": { "enabled": true, "rmsRatioThreshold": 0.8, "rmsRatioExceededCount": 25 }
}
```

#### Vendor bad-mic detector

`audiod.brokenMicDetector` is a **channel-consistency health detector**, not a calibration
or absolute noise-floor test. The recoverable contract is deliberately narrow:

- `rmsRatioThreshold = 0.8` compares RMS levels between microphone channels, but the
  config does not expose the ratio direction, the aggregation window, or whether it
  compares each channel with a mean, median, or peer.
- `rmsRatioExceededCount = 25` adds temporal accumulation before it declares a result;
  the cadence and whether the observations must be consecutive are unrecovered.
- Orion also sets `sendAudioData = false`: the detector is enabled without asking the
  audiod configuration to include diagnostic audio in its result path.

Do not turn these fields into an absolute “bad mic” verdict in our daemon. Static
configuration does not show whether the vendor reports an event, mutes a channel, or
changes the VQE input after a detection. Use a repeated raw-capture comparison first:
a physical fault remains disproportionately noisy or quiet across captures, while a
single noisy interval can be room activity, electrical interference, or a transport
glitch. The Orion retest in [../boards/orion.md](../boards/orion.md) is the concrete example.

The `audiosender` component enumerates the **named channels** that exist inside the pipeline:

```
availableChannels / dumpingChannels:
  raw_mic_0, raw_mic_1, raw_mic_2,   // 3 raw PDM mics
  raw_spk_0,                         // raw speaker / echo reference
  vqe_0,                             // MAIN VQE output (enhanced, single channel)
  vqe_bnr                            // secondary "background noise reducer" output
playerChannelName   = vqe_0
spotterChannelName  = vqe_0         // ← the wake-word spotter listens to vqe_0, not raw mics
speechkitLogChannels = [ vqe_0, vqe_bnr ]   (aliced)
```

The binary's internal channel-type enum (`AUDIO_INPUT_CHANNEL_TYPE_*` in `libyandex_evil.so`) is richer and shows the full data model:

```
Raw inputs : RAW_MIC, RAW_SPEAKER, RAW_MAIN_MIC_SYNC, RAW_AUXILIARY_MIC_SYNC, RAW_FEEDBACK_SYNC
VQE outputs: VQE_OMNI, VQE_OMNI_WITH_GAIN, VQE_BEAMFORMING, VQE_BACKGROUND_NOISE_REDUCER, VQE_ZONE, VQE_STATE
```

**Interpretation:** the 3 mics + feedback go in as `RAW_MIC`/`RAW_FEEDBACK`; VQE produces a beamformed/denoised main stream (`vqe_0`, type `VQE_BEAMFORMING` or `VQE_OMNI`) plus a `VQE_BACKGROUND_NOISE_REDUCER` stream (`vqe_bnr`). The spotter and the cloud uplink consume `vqe_0` at **16 kHz mono**.

### 2.4 Mic geometry

`libyandex_evil.so` carries a `MicSetup` enum (device array geometries). Observed members:
`SINGLE`, `DUAL_D425`, `RIGHT_TRIANGLE_43MM_LEG`, `FRONT_2_MID_2_BACK_2`, `SIDE_4_FRONT_2_REAR_2`, `TOP_2_FRONT_1`, `TOP_3_FRONT_2`, `TOP_3_FRONT_4`, `TRAPEZIUM_430MM_103MM_970MM`. The **`bergamot` preset is 3-mic** (`micChannels:3`; spotter model name `…-x3-…`). The exact enum member bergamot maps to is **(inferred)** `RIGHT_TRIANGLE_43MM_LEG` — a 3-mic right-triangle with a 43 mm leg is the only 3-mic *planar* geometry in the list, consistent with a small round "Mini" puck; the `TOP_3_*` variants also exist. The physical mic coordinates are inside the compiled `"bergamot"` preset, which is **not shipped as JSON** in the dump (see §3.4), so the spacing is not directly recoverable from these files.

---

## 3. VQE (Voice Quality Enhancement) — the mic-array DSP

### 3.1 What it is

VQE is **Yandex's own in-house DSP engine**, not a licensed third-party stack (no Alango/2mic/DSP-Concepts strings; everything lives under the Yandex source tree `voicetech/vqe/…`). Config says `"type": "yandex"`. Initialisation strings: `Initialized YandexVqe (preset hash = …)`, `Initialized new VqeDspClient for device preset "…"`, `YandexVqeC_Config_CreateFromPreset`, `YandexVqeC_Config_CreateFromJsonPreset`, `Failed parsing Yandex VQE JSON config`. The VQE version string on the shipped neural model (`vqe_models/noise_reducer/model_call_meta.json`) is:

```
VQE-1982_2025_12_23_tuning_random_preset_xmb_4_log_mag_ref_2
```

The processing graph is assembled from modular C++ classes (all `N3vqe…` mangled symbols). The pipeline stages, with concrete evidence:

### 3.2 Echo cancellation (AEC) — present, central

Multiple interchangeable AEC implementations exist:

```
vqe::LmsEchoCanceler            vqe::KalmanEchoCanceller
vqe::NeonKalmanEchoCanceller<0/1>   (ARM NEON-optimised)
vqe::StereoKalmanEchoCanceller  vqe::CrossOmittedKalmanEchoCanceller
vqe::IAdaptiveEchoCanceler / vqe::IStereoAdaptiveEchoCanceler / vqe::EchoCancelerNone
```

Config knobs (member strings): `aecConfig`, `aecConfigOmni`, `aecConfigDirected`, `aecConfigBNR`, `aecConfigHdmi`, `aecMode`, `aecMu`, `multimicAecMode`, `disableAec`, `reduceNoiseAfterAec`, `useAecOmniAsDirected`, `kalman*` (`kalmanAlpha/Lambda/InitP/PostfilterType/…`), `useNeonKalman`, `useNewKalman`. The reference comes from the feedback channel (`RAW_FEEDBACK_SYNC`, `raw_spk_0`, `useFeedbacks`, `feedbackGain`, `feedbackStaticShift`, `usePeakFeedbackRms`). There are hardware/software/HDMI correlators (`enableHwCorrelator`, `enableSwCorrelator`, `hwStaticShift`, `swStaticShift`) that time-align the speaker reference to the mic stream — i.e. the HW loopback (§2.1) is consumed here. Guard string: `Invalid use of non-stereo AEC with stereo feedback`.

### 3.3 Beamforming & direction / zones — present

```
vqe::  adaptiveBeamformingConfig, beamformingMode ∈ { DELAY_SUM, MVDR, RELATIVE_TO_OMNI, DUMMY },
       mvdrConfig, doaConfig (direction-of-arrival), zoneTrackerConfig, micZoneMap,
       ZoneTracker / MaxRmsZoneTracker, defaultOmniMicIndex, omniMicWhitelist
Runtime strings: "DOA has been changed, new DOA: …", "Switched YandexVqe omni microphone index from …"
```

So VQE picks a steering direction (DOA), can run **delay-sum** or **MVDR** beamforming or fall back to an **omni** mic, and tracks acoustic "zones" (`VQE_ZONE` output). `Beamforming is impossible for MicSetup::SINGLE`; several geometries are marked "not implemented" (the implemented set for 3-mic bergamot uses at least delay-sum + omni; whether full MVDR is enabled for bergamot lives in the compiled preset). The shipped alisa activation model is named `…-x3-bnr-…` confirming 3-channel beamformed/BNR features feed the spotter.

### 3.4 Noise reduction — classical + neural (the only shipped VQE model)

```
Classical/stat: vqe::BackgroundNoiseReducer (BNR), Mono/Stereo/Neon variants over
                vqe::KalmanFilter | vqe::LmsFilter;  webRtcNoiseReducerConfig
                ("Initializing WebRtc noise reducer with level: …")
Neural        : vqe::TFLiteNoiseReducer / vqe::INeuralNoiseReducer, neuralNoiseReducerConfig,
                omniNeuralPostfilterConfig, vqe::NoiseReducerVqeDspPostprocessor
```

The **one** model shipped under `vqe_models/` is the neural noise reducer:

- `vqe_models/noise_reducer/model_call.nnet` — **despite the `.nnet` extension this is a TFLite file** (magic `TFL3` at offset 4; tensors `streaming_model_0…9`, a streaming/stateful model), **525,264 bytes**.
- `model_call_meta.json` → `{"version":"VQE-1982_2025_12_23_tuning_random_preset_xmb_4_log_mag_ref_2"}`. "log_mag_ref" ⇒ it consumes log-magnitude spectra of mic + reference → it's an AEC-residual / noise **post-filter** mask net, runs on `libtflite.so` (`NOISE_REDUCER_FFT_SIZE`, `micChunk`/`spkChunk`/`chunk` must equal FFT size). `bnrRefMicCount <= 2`.

The `vqe_bnr` named channel is the BNR output; `vqe_0` is the fully-processed main channel (AEC→beamform→NR→AGC).

### 3.5 AGC, dereverb, equalizer, Doppler, DSP offload

- **AGC:** `vqe::webRtcAgcConfig` / `agcConfig` (`agcAttackFrames`, `agcReleaseFrames`, `maxGainDb`, `targetRmsDb`, `compressionGainDb`, `kFixedDigital`) — WebRTC-style AGC plus a limiter (`limiterConfig`, `hardClip`).
- **Dereverb:** `dereverbConfig/Active/Delay`, `gevdConfig` (generalised-eigenvalue dereverb), `reverbTimeDbThreshold`.
- **Equalizer / room correction:** `equalizerConfig`, SmartEqualizer, `Room Correction preset`, `targetTransferFunctionDb` — speaker-side tuning.
- **Doppler / presence:** `vqe::BaseDopplerDetector` / `SimpleDopplerDetector` (ultrasound-based presence/gesture) and `outdoorsDetectorConfig`, `babyMonitorConfig`.
- **Communication modes:** `YandexVqeC_SetCommunicationMode` ∈ `{ON_NO_COMMUNICATION, ON_REGULAR_CALL, ON_ANY_COMMUNICATION, ON_BABY_MONITOR}` — different tunings for calls vs. ASR.
- **DSP offload:** `vqe::AmlogicIPCClient` loads `libhifi4rpc.so` / `libhifi4rpc_client.so` to run VQE on the **Amlogic HiFi4 DSP core** via shared memory (`voicetech/vqe/libs/amlogic/…`). Fallback string: *"After testing all DSP cores not a single one is suitable for VQE IPC calls"* ⇒ it reverts to the ARM CPU (NEON) path. On our unit this DSP path is irrelevant once `maind` is gone.

### 3.6 Device preset

`audiod.vqe.preset = "bergamot"`, `type = "yandex"`. The preset is selected by device codename and resolved from a **compiled-in table** inside `libyandex_evil.so` (`CreateFromPreset`, `Unknown Yandex VQE device preset: …`). It defines the full config tree whose field names are enumerated above (`micSetup`, `beamformingMode`, `aecConfig*`, `bnrConfig`, `agcConfig`, `doaConfig`, `micZoneMap`, `processingFrameSize`, `hopsPerFrame`, `inputSamplingRate`, …). **The preset JSON is not present on disk** — only the neural NR model ships in `vqe_models/`. Consequently the exact numeric tuning (mic coordinates, beam angles, AEC taps, gains) is **not recoverable** from the dump without decompiling the preset table out of the binary. The ~190 `::`-prefixed config member names in `libyandex_evil.so` (reproduced in Appendix A) are the full surface area of what *could* be tuned.

---

## 4. Wake-word / keyword-spotter subsystem

### 4.1 Engine

A single Yandex **SpeechKit "spotter"** engine (namespace `Spotter::` in `libyandex_evil.so`) runs *all* keyword/command/VAD models. Architecture tag in every model's `flags.txt`: `spotter-type chen14` — the small-footprint DNN KWS of **G. Chen, C. Parada, G. Heigold, "Small-footprint keyword spotting using deep neural networks," ICASSP 2014**: frame-level acoustic DNN over log-mel features, posterior smoothing, and a confidence = geometric mean of a sliding window of target-word posteriors, fired when it crosses a threshold at a local peak.

Common feature/scoring flags (from the shipped `flags.txt` files):
- Features: `frame-size 25`, `frame-shift 10`, `sample-rate 16000`, `mel-num-bins 40`, `feat-frontend log-mel-fbank` (alisa additionally uses a Kaldi `feat-pipeline cmn:lda` with its `lda.mat`; the yandex model uses raw log-mel + SVDF).
- Scoring: `metric geometric`, `hit-at-peak subhit`, `w-smooth` (posterior smoothing window), `w-max 60`, `thresholds` / `subthresholds` per word, `skip-rate 1`, `am-batch-size 6`, `nnet-representation float`, `use-vad false`.

The engine supports async wrappers, multi-component "single spotters", logging, and an online "spotter verifier" (`SpotterVerifier`) that re-scores against a cloud check (`onlineSpotterValidationDisabledTypes: ["noise-detection","vad"]`, `spotterSubscribePointShiftMs: -80`). Decode/config load classes seen: `Spotter::Features::IFeatureOptions`, `TConfigFile`, `IConfigStorage`, `MfccOpts`, `FeatPipelineConf`, `TAsyncSpotterWrapper`.

### 4.2 Model load path

1. `quasar.cfg → aliced.audioInput.model_storage` defines `defaultModelsPath = ${QUASAR}/spotter_models` (the files in the dump), `customModelsPath = ${DATA}/data/spotter_model` (OTA-downloaded), and `customModelsConfigPath = ${DATA}/data/spotter.json`.
2. **`dumps/rootfs/data/quasar/data/spotter.json`** is the runtime manifest: it lists each active model by `type`, a `crc`, a local unpacked `path` (UUID dir), and the CDN `url` of the zip it came from. Active set on our unit: `activation/alisa`, `command`, `counteractivation`, `intonationInterruption`, `vad` (×2 instances).
3. `quasar.cfg` carries **fallback** copies of every model URL (`fallbackModels`) + CRCs, so the device can re-provision if the custom store is wiped. The `${QUASAR}/spotter_models` tree in the dump is the built-in default/fallback.
4. Each model dir = `{acoustic_model.nnet, flags.txt, words.txt [, lda.mat][, tts-timings-info/]}`.

The **active alisa model** is `ru-RU-bergamot-alisa-13Nov-x3-bnr-minispeakers-29Jan24` (note `bergamot`, `x3`, `bnr`, `minispeakers` — trained on this device's 3-mic beamformed+BNR front-end). The spotter reads `vqe_0` (`audiosender.spotterChannelName`), i.e. it scores the **VQE-enhanced** stream, not raw mic.

### 4.3 The models (as data)

| Model | Format | Size | `version` string | Keyword(s) / outputs |
|---|---|---|---|---|
| `activation/alisa` | Kaldi **nnet3** + `lda.mat` | 583 KB + 15 KB | `ru-RU-quasar-alisa-04Mar22-fused-qd16-ca-no-bnr-no-ca` | **«алиса»** (`string-representations алиса`); phone model a/л/и/с/А. `thresholds 0.44`, `subthresholds 0.22`, `feat-pipeline cmn:lda` |
| `activation/yandex` | Kaldi **nnet3** (SVDF) | 588 KB | `…-yandex-svdf-x1-02Dec2022-…` | **«яндекс»**. `thresholds 0.93`, `subthresholds 0.46`. `x1` = single-channel, no BNR. Header shows `svdf_features/svdf_times` nodes |
| `activation/yasmina` | Kaldi **nnet3** | 626 KB | `ar-SA-quasar-yasmina-22Nov24-x3-…` | **"yasmina"** (Arabic wake word; present but ru device uses alisa) |
| `command` | Kaldi **nnet3** | 773 KB | `ru-RU-quasar-commands-10Feb25-…-dqmm` | **24 offline commands** (see below). `dynamically-quantized-matmul true` |
| `spotterRecognizer` | **TFLite** (`TFL3`) | 912 KB | `…commands-10Feb25-…-fp16-v3-no-likes` | Same command vocab, NPU/TFLite variant: `spotter-npu-model-path model.tflite`, `spotter-npu-use-streaming true`, `spotter-tflite-resolver-tag SPOTTER_ACTIVATION` |
| `vad` | Kaldi **nnet3** | 100 KB | `vad-human-detector-20Oct23-50k-rt-split` | `spotter-type seam-vad-decoder`; outputs `<speech_started>`/`<speech_finished>`, `threshold 0.9` |
| `intonationInterruption` | Kaldi **nnet3** | 1.55 MB | `…intonation-interruption-23Mar26-…-singlechannel-…` | fires on **«...?» question intonation** to let you interrupt Alice (`<intonation>`, `is-intonation-spotter true`, `thresholds 0.47`) |
| `counteractivation` | **TFLite** (`TFL3`) | 55 KB | `counteractivation-local-ab1-tiny-husky-53-bnr` | false-trigger suppressor, see §4.5 |

**Offline command vocabulary** (`command/words.txt`, 24 real commands; useful for offline reflexes): дальше, назад, выше, ниже, вправо, влево, вверх, вниз, домой, продолжи, играй, стоп, хватит, пауза, следующий, громче, тише, включи, выключи, свет, телевизор, блютуз, лайк, дизлайк. Multi-word combos encoded via `commands 18,20:18,21:…` (e.g. «включи свет», «включи телевизор», «выключи блютуз»). Each command has its own threshold (`thresholds`/`subthresholds` vectors in `flags.txt`). `interval-after-last-activation 25`.

### 4.4 Roles of the auxiliary spotters

- **`vad`** — "human voice" VAD (`seam-vad-decoder`), gates/segments speech. Config `vadEnabled:false` at the `aliced` top level but the model is loaded (two instances) and used by the pipeline; `audio-length-model-threshold 20:20`.
- **`command` / `spotterRecognizer`** — an **always-on, fully offline** 24-command spotter: lets the device obey navigation/volume/light/playback commands with no wake word and no cloud. `spotterRecognizer` is the TFLite/"NPU" port of the same vocab. **Conceptually worth copying** (offline reflexes).
- **`intonationInterruption`** — detects rising question intonation so the user can barge-in on Alice; resets the activation/command spotters (`reset-after-other-spotters-activation activation:commands`, `suppress-spotter-for-ms 200`).
- **`counteractivation`** — anti-false-trigger (§4.5).

### 4.5 Counteractivation (false-trigger suppressor) — notable design

A second TFLite model that vetoes a wake-word hit using multi-channel acoustic evidence + device context. `spotter_models/counteractivation/…/model_list.json` is the decisive artefact:

- Model `local-ab1-tiny-husky-53`, `type: tflite_nn`, resource `local-ab1-tiny-husky-53.tflite` (55 KB).
- **Acoustic features** from three channels (`ca-channels omni,raw-mic,bnr`, `frame-size 20`): `fbankFeatureIndices { omni:[0,23], raw:[23,46] }` → it compares the **omni** mic vs. **raw** mic vs. **BNR** spectra. Rationale: a true near-field voice looks different across beamformed/omni/raw than TV/speaker playback does.
- **Per-device bias** feature index: `"Yandex bergamot": 57` (each device model has its own slot; also a `"Yandex bergamot": 0.940427236557007` scalar embedded in the binary's preset).
- **Context features** (`mmFeatureIndices`): `isPlayerStopCommand`, `hasScreenRelatedAnswer`, `isActiveCall`, `currentlyPlayingRelated`, `secondsSinceLastPlayback…`, `isDeviceOn…`, `preferBetterAcoustics` — i.e. the suppressor knows whether the speaker is currently playing, on a call, etc. and raises the bar accordingly.
- `counterActivationPresetName: "Yandex bergamot"` in `quasar.cfg`. Note the primary activation flags set `counteractivation-features-enabled false` inside the *acoustic* model — counteractivation runs as a **separate stage**, gated by the top-level `multiflags.txt` (`counteractivation-features-enabled true`, `version counteractivation-local-ab1-tiny-husky-53-bnr`).

### 4.6 Reusability of the Yandex wake-word models

- The acoustic models are **standard Kaldi nnet3** (`<Nnet3>` text header, `input-node dim=40`, `cmvn_add/cmvn_mul/svdf_*` components) — in principle loadable by open **Kaldi**/**Vosk** if you reconstruct the decode graph (`words.txt` + `flags.txt` give the symbol table and feature config; alisa also needs its `lda.mat`). The scoring is a plain `chen14` posterior-smoothing decoder, reimplementable in ~100 lines.
- **But** the feature frontend is Yandex-specific (their `cmn:lda` + beamformed `x3-bnr` input expectation), the confidence metric/threshold calibration assumes the VQE front-end, and the `.tflite` variants use a custom op resolver tag (`SPOTTER_ACTIVATION`) that stock TFLite-micro won't have. Resurrecting the full Yandex KWS is **high-effort, low-payoff** given the models only know Russian/Arabic Yandex wake words and are tuned for a pipeline we just removed.

---

## 5. What we lost by disabling `maind`, and the sovereign replacement

### 5.1 What is gone

With `maind`/`audiod` off (confirmed live: no `maind` process; `hw:0,1` idle/`closed`), we lose the **entire** front-end:

| Lost capability | Was provided by |
|---|---|
| On-device wake word («алиса») | `Spotter::` chen14 + `activation/alisa` nnet3 on `vqe_0` |
| Offline 24-command reflexes | `command` / `spotterRecognizer` spotters |
| False-trigger suppression | `counteractivation` TFLite |
| **AEC** (barge-in while music plays) | `vqe::*EchoCanceller` + HW feedback ref |
| **Beamforming / DOA** | `vqe` delay-sum/MVDR + zone tracker |
| **Noise reduction** (classical + neural) | `vqe::BackgroundNoiseReducer` + `TFLiteNoiseReducer` |
| **AGC / limiter / dereverb / EQ** | `vqe::webRtcAgc`, `dereverb`, `equalizer` |

What **remains** for us: the raw ALSA capture — 3 PDM mics + 1 amp-feedback reference on `hw:0,1`, 48 kHz S16_LE (demuxed by `asound.conf` into 4 channels). That is a *good* starting point: we still have the AEC reference channel in hardware.

### 5.2 Recommended sovereign stack (armhf, ~238 MB RAM, 4 cores)

Design the HASS/Wyoming-satellite front-end as a short ALSA→DSP→wake-word chain. The device is **32-bit ARM hard-float, glibc ≥2.30**; everything below has armhf builds.

**(1) Capture / channel mapping — ALSA only, free.**
Keep the existing `asound.conf` idea but expose named PCMs for a sovereign consumer:
- One mic for a first cut: `plughw`/`dsnoop` picking PDM channel 0 → mono 16 kHz. Simplest; works today.
- Full array: capture the 4ch device (`mic0,mic1,mic2,spk_ref`) and feed a DSP. The amp feedback (channel 6 → binding 3) is your **AEC reference** — this is the single biggest asset; most DIY satellites have no reference and can't do AEC.
- Verify channel order empirically with `arecord -c4 -r48000 -fS16_LE` + per-channel RMS while playing a tone (the ref channel tracks playback).

**(2) AEC + NS + AGC — `webrtc-audio-processing` (APM / AEC3).** Recommended primary.
- Feed it `near = mic`, `far = spk_ref` (we have a true hardware reference → AEC3 works well). Enable NS + AGC2 + high-pass. This directly replaces `vqe` AEC+NS+AGC with a well-maintained open implementation. CPU cost on 4× Cortex-A at 16 kHz mono is small.
- Alternative / lighter: **SpeexDSP** (`speex_echo_cancellation` + `speex_preprocess` for NS/AGC) — lower quality AEC but tiny and trivial to build on armhf. Good fallback if webrtc-apm is painful to cross-compile.
- Integration options: a small ALSA plugin, a PipeWire/PulseAudio `module-echo-cancel` (wraps webrtc-apm) if you run PipeWire on the device, or do it inside the Wyoming satellite process.

**(3) Beamforming — optional, do last.**
- Cheapest useful win: **delay-sum** of the 3 mics (fixed broadside), or just **pick the loudest mic** per utterance (mirrors VQE's omni-mic fallback / `MaxRmsZoneTracker`). A full MVDR/adaptive beamformer is not worth it initially; the VQE evidence shows even Yandex often falls back to omni.
- If wanted later: `webrtc-audio-processing` had an experimental beamformer; or a small custom frequency-domain delay-sum using the `RIGHT_TRIANGLE_43MM_LEG`-style geometry once measured. Mostly skip — single mic + good AEC/NS gets ~90% of the benefit for a near-field room speaker.

**(4) Wake word — microWakeWord (TFLite-micro). Recommended.** *(Superseded: the vendor spotter shipped instead of microWakeWord ([wake-word.md](../wake-word.md#decision-record-the-vendor-spotter-not-microwakeword)).)*
- Do **not** port the Yandex Kaldi KWS. Use **microWakeWord**, which the user already trains for Russian: pipeline in `<ha-config-repo>/mww-training/` (Silero-TTS positives → `make_features.py` → GPU train → `stream_state_internal_quant.tflite` + ESPHome v2 manifest), documented in `<ha-config-repo>/docs/respeaker-wake-words.md`. MWW runs as a Wyoming wake service (`wyoming-microwakeword` / openWakeWord) on the device or on the HASS host.
- Feed the wake-word model the **post-AEC/NS mono 16 kHz** stream (mirrors Yandex scoring `vqe_0`, not raw mic) — this materially improves robustness when the speaker is playing.
- Concepts worth copying from Yandex conceptually: a lightweight **counteractivation/verification** second stage (openWakeWord already supports a VAD + a "melspectrogram diff" style gate; you can add a simple "is the speaker currently playing?" suppressor in the satellite), the **VAD gate** before uplink, and the **offline command spotter** idea (a second tiny MWW/openWakeWord model set for «стоп», «громче», «тише», «пауза» giving no-cloud reflexes).

**Feasibility:** all of this fits the box. `webrtc-audio-processing` + one MWW model at 16 kHz mono is a few % of one core and a few MB RAM — far below the `maind` footprint (100+ threads, which is why `ulimit -s 1024` was needed). The hardware AEC reference is already wired, so we are better positioned than a typical ReSpeaker satellite.

---

## 6. Open questions / not recoverable from the dump

1. **Exact `bergamot` VQE preset numbers** (mic coordinates, beam angles, AEC tap counts, gains, which `beamformingMode` is active). Compiled into `libyandex_evil.so` as a preset table; not shipped as JSON. Would require decompiling the `CreateFromPreset` table. Appendix A lists every tunable *name*, not its value.
2. **Precise `MicSetup` member for bergamot** and physical mic spacing — inferred 3-mic (likely `RIGHT_TRIANGLE_43MM_LEG`); confirm by measuring the PCB or cross-correlating the 3 mic channels against a moving source.
3. **Physical channel order** of `hw:0,1` (the `asound.conf` comment has an obvious typo "mic1 (PDM2)"). Confirm channel→mic and which index is the reference by RMS test (§5.2-1).
4. **VQE neural NR exact I/O** — `model_call.nnet` is TFLite; its input tensor shapes (`streaming_model_*`, FFT size) can be dumped with a TFLite inspector if we ever want to reuse it, but it expects VQE-internal log-mag mic+ref features, so reuse is unlikely.
5. **`spotterRecognizer` custom op** `SPOTTER_ACTIVATION` / resolver tag `SPOTTER_ACTIVATION` — a Yandex custom TFLite op; stock runtimes can't execute that model as-is.
6. Whether the HiFi4 **DSP firmware** (`libhifi4rpc.so`) is still loadable/usable independently of `maind` (probably not worth it; CPU path is plenty).

---

## Appendix A — VQE configuration surface (member names in `libyandex_evil.so`)

Full set of `::`-prefixed VQE config fields (what the `bergamot` preset can set; values not in dump). Grouped:

- **Top/graph:** `micSetup`, `micCount`, `micChannels`, `processingFrameSize`, `processingFrames`, `hopSize`, `hopsPerFrame`, `inputSamplingRate`, `outputSamplingRate`, `processSamplingRate`, `processingSamplingRate`, `fftWindowFunction`, `stftTransform`, `stftWindowFunction`, `zeroPaddingSize`, `shiftsPerFrame`, `preprocessorType`, `preprocessorFeedbackType`.
- **AEC:** `aecConfig`, `aecConfigOmni`, `aecConfigDirected`, `aecConfigBNR`, `aecConfigHdmi`, `oppositeAecConfig`, `aecMode`, `aecMu`, `multimicAecMode`, `disableAec`, `disableDuringAEC`, `reduceNoiseAfterAec`, `useAecOmniAsDirected`, `kalmanAlpha/BaseRmsH/EffectiveShare/InitP/Lambda/LambdaP/PostfilterType/RmsRatioForgettingFactor`, `useNeonKalman`, `useNewKalman`, `mu`, `muOverride`, `historyFrames(Count)`, `postfilterBeta`, `usePostfilter`.
- **Feedback/correlators:** `useFeedbacks`, `feedbackGain`, `feedbackId`, `feedbackStaticShift`, `feedbackThreshold`, `feedbackAmpSilenceThreshold`, `silenceFeedbackThreshold`, `usePeakFeedbackRms`, `enableHw/Sw/HdmiCorrelator`, `hw/sw/hdmiStaticShift`, `runCorrelators`, `swSyncQuasar`, `exportFeedbackSyncChannels`, `exportFeedbacksAsSum`, `exportMicSyncChannels`.
- **Beamforming/DOA/zones:** `beamformingMode`, `adaptiveBeamformingConfig`, `mvdrConfig`, `doaConfig`, `doaWithoutDownsampling`, `downsampleFactor`, `nDirections`, `anglesOverride`, `angleOffset`, `nMicsToUse`, `halfMics`, `micZoneMap`, `zoneTrackerConfig`, `defaultOmniMicIndex`, `omniMicId`, `omniMicWhitelist`, `omniChannelGainCoefficient`, `micChoosingConfig`, `safetyMicChoosingConfigs`, `micSwitchDelay`, `micSwitchThreshold`, `nPeaks`, `minSureMaxCorr`, `minSureMedian`, `correlatorConfig`, `covarianceAlpha`, `gevdConfig/Delay`.
- **Noise reduction:** `neuralNoiseReducerConfig`, `omniNoiseReducerConfig`, `omniNeuralPostfilterConfig`, `webRtcNoiseReducerConfig`, `bnrConfig`, `bnrMicWhitelist`, `noiseAlphaAttack/Release`, `noiseDbMin/Max`, `noiseInitDb`, `noiseLevel`, `speechMarginDb`, `muteMicsThresholdDb`, `modelOutputApplierType`, `useNnApi`, `useXnnPack`, `resolverTag`.
- **AGC/limiter/EQ/dereverb:** `agcConfig`, `webRtcAgcConfig`, `autoGainControl`, `agcAttackFrames`, `agcReleaseFrames`, `attackFrames`, `releaseFrames`, `compressionGainDb`, `targetRmsDb`, `maxGain(Db)`, `minGain(Db)`, `customMaxGain`, `gainFactor`, `preAmplifierConfig`, `preAmplifyGain`, `limiterConfig`, `hardClip(Samples)`, `kFixedDigital`, `equalizerConfig`, `equalizerScalingConfig`, `smartEqualizer`/`targetTransferFunctionDb`, `transferFunction`, `bandSettings`, `dereverbConfig/Active/Delay`, `reverbTimeDbThreshold`, `reverbWindowSizeSec`, `reverbScaleMultiplicatorFix`.
- **Modes/misc:** `communitationModePreset` (sic), `audioCallConfigs`, `regularCallConfig`, `noCommunicationConfig`, `forceOnCommunication`, `babyMonitorConfig`, `outdoorsDetectorConfig`, `volumePresets`, `supportVolumePresets`, `speakerCommunicationModeAmplifier`, `speakerVolumeAmplifier`, `spkDescriptions`, `speakerDescr/Index`, `telemetryReportingPeriodSec`, `loggingConfig`, `logLevel`.

## Appendix B — key file map

```
dumps/rootfs/system/vendor/quasar/
  maind                              22MB armhf multicall engine (orchestrator)
  libs/libyandex_evil.so             4.6MB  Spotter:: + vqe::  (THE engine)
  libs/libtflite.so                  3.3MB  TFLite runtime
  quasar.cfg                         master config (audiod/aliced/audiosender keys)
  vqe_models/noise_reducer/
     model_call.nnet                 525KB  *** actually TFLite (TFL3) *** neural NR
     model_call_meta.json            VQE-1982_2025_12_23_tuning_random_preset_xmb_4_log_mag_ref_2
  spotter_models/
     activation/{alisa,yandex,yasmina}/  Kaldi nnet3 acoustic_model.nnet + flags/words[/lda.mat]
     command/                        24-command offline spotter (nnet3)
     spotterRecognizer/model.tflite  TFLite command spotter (custom op SPOTTER_ACTIVATION)
     counteractivation/…tflite       55KB false-trigger suppressor + model_list.json
     vad/                            seam-vad-decoder (nnet3)
     intonationInterruption/         question-intonation barge-in (nnet3)
dumps/rootfs/etc/asound.conf         mic demux (3 PDM mics + ch6 feedback) & speaker dmix
dumps/rootfs/data/quasar/data/spotter.json   runtime model manifest (type/crc/path/url)
<ha-config-repo>/mww-training/       our Russian microWakeWord training pipeline (replacement)
<ha-config-repo>/docs/respeaker-wake-words.md
```
