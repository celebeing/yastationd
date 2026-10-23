# Yandex Station Mini 3 (`bergamot`): how speech-to-text works — decompilation pass

**Question.** Does any part of STT/ASR run on the device (models, decoder, offline recogniser, or a feature
front-end that feeds the cloud)? If so, how far can it be reused outside the vendor stack? If not, what does the
device send to the cloud, and how?

**Builds on:** `docs/spotter-decompile.md` (spotter engine, chen14, nnet3/ynmt, fbank),
`docs/vqe-decompile.md` (VQE / `vqe_0`), `docs/wakeword-mic-array.md`, `docs/hass-integration.md`, and the sibling
`docs/tts-decompile.md`. Facts already established there are referenced, not repeated. No existing doc was edited.

**Tags.** **(observed)** = read directly from bytes, strings, relocations, disassembly or decompiler output, or
measured by running my own code on the vendor model files. **(inferred)** = my reading of that evidence.
**Addresses** are `maind` ELF vaddrs (Thumb-2 PIE). Ghidra address = vaddr + 0x10000. Ghidra function names
`FUN_00xxxxxx` in my scratch output are therefore 0x10000 higher than the vaddrs quoted here.

---

## 0. TL;DR and verdict

- **There is no on-device general ASR.** No acoustic or language model for dictation exists anywhere in the
  rootfs or `/data`. No ASR decoder exists in any binary: no CTC/RNN-T/WFST/lattice/LM code, and no ASR TFLite
  resolver. `libyandex_evil.so` exports only `spotter_*` and `YandexVqeC_*`. The `voicetech/asr/...` code in `maind`
  is three utility headers. **(observed, §2)**
- **STT is cloud-side, through Yandex uniproxy** (`wss://uniproxy.alice.yandex.net/uni.ws`, with QUIC as an
  alternative). The messages are protobuf (`useProtoAliceApi: true`), zstd-compressed with a shipped dictionary.
  The device sends:
  - a `VoiceInput`/`AsrRecognize` request with topic **`quasar-general`** and lang `ru-RU`;
  - then **Ogg-Opus** (`audio/opus`) audio: **16 kHz mono from `vqe_0`**, 24 kbit/s, wideband, complexity 0,
    VOIP application, in chunks of up to 100 ms;
  - the activation audio with pre-roll, for cloud re-validation of the spotter.

  End-of-utterance, text, normalisation and partials all come back from the server. **(observed: enum, ctor
  defaults, encoder ctls, config keys; inferred: exact wire framing, §3)**
- **What does run locally:**
  1. the VQE front-end (`docs/vqe-decompile.md`);
  2. the wake-word spotter plus a spotter-VAD gate that decides when to open the stream;
  3. on-device voice **biometry** (ECAPA-TDNN, age, gender). This is speaker ID, not STT;
  4. a **"hybrid" local path**. A **`SpotterRecognizer`** wraps the **offline command spotter**
     (`spotterRecognizer/model.tflite`, 25 fixed phrases) behind the same `Recognizer` interface as the cloud
     ASR, so the device can obey «стоп / громче / включи блютуз…» when the backend is unavailable. Next to it, a
     text-level `FastCommandClassifier` maps (server-partial or local) text to fast commands. **(observed, §4)**
- **The only reusable client-side "STT" is that closed-vocabulary command recogniser.** I took it apart fully
  (§5). I decoded the one missing piece, the `LINEAR_FP16` custom-op weight layout, from the bytes. A ~150-line
  numpy reimplementation (Kaldi fbank → TFLite graph → chen14) gives sane posteriors. On a vendor prompt that
  contains the spoken phrase «Включи Bluetooth», `включи блютуз` scores 0.95× its threshold at the right moment
  (t ≈ 7.5 s), against ≤ 0.01× on all other prompts. **(observed, §5.6)**
- **Verdict:**
  - Reusable client-side STT: **none** (nothing to decompile).
  - Reusable offline command recogniser: **yes, about 2–4 days** to a calibrated on-device service. It is
    blob-free, since the vendor `.so` is not needed, but the model itself is proprietary.
  - For real STT, use the server: **GigaAM v3 / faster-whisper / Vosk via Wyoming on `voice-server`**, as already
    planned in `hass-integration.md`. Running ASR on the device is the wrong place.
  - The command recogniser is worth keeping **only as an offline-reflex layer** (stop / louder / quieter / pause),
    gated by context. §7 has the details.

---

## 1. Material, integrity and method

| Item | Fact |
|---|---|
| `maind` | 22,003,764 B, the same size as in `rootfs-full.tar`, md5 `38bb6ef5…1831` in both copies. Thumb-2 PIE, stripped. Imports `libyandex_evil.so`, `libtflite.so`, `libglib`, `libasound`, `libavahi*`. `led_util`, `quasar_client`, `metrics_collector` etc. are the same multicall binary **(observed)** |
| `libyandex_evil.so` | Truncated in `dumps/rootfs` (4,587,520 B vs 4,912,400 B). Intact copy extracted from the tar (md5 `2efb61a8…`) **(observed)** |
| `libtflite.so`, `libquasar_gstreamer.so`, `libglib.so`, `libiconv.so`, `quasar.cfg` | Intact (sizes and md5 match the tar) **(observed)** |
| `/data/quasar/data/spotter.json` | Exactly 5 downloaded models: activation/alisa, command, counteractivation, intonationInterruption, vad. **No ASR entry** **(observed)** |

Method: strings → a custom **Thumb-2 PC-relative xref scanner** (`ldr rX,[pc,#]`/`movw+movt` … `add rX,pc`), giving
70,745 resolved targets in 0.8 s → a string trace per function → **Ghidra 12.1.4 headless with `-noanalysis`**, then
targeted disassembly, function creation and decompile of about 15 functions. No vendor code was executed. §8 has
the details.

---

## 2. Negative proof: no on-device ASR

### 2.1 Every model file on the device (observed)

The largest model of any kind is 1.89 MB (the active alisa spotter). The complete set:

| Path | Size | What it is |
|---|---|---|
| `spotter_models/activation/{alisa,yandex,yasmina}` + `/data/.../8cf52356…` | 0.58–1.89 MB nnet3 | wake word (2-class) |
| `spotter_models/command` + `/data/.../a23e7ada…` | 773 KB nnet3 | 25-command spotter |
| `spotter_models/spotterRecognizer/model.tflite` | 912 KB | the same commands, TFLite fp16 (§5) |
| `spotter_models/{vad,intonationInterruption,counteractivation}` | 0.1–1.5 MB | gates |
| `vqe_models/noise_reducer/model_call.nnet` | 525 KB | call-mode NR |
| `bio_lingware/tflite-online-ecapa-tdnn-frontend/{body,head}.tflite` + age/gender classifiers | 1.4 MB + small | voice biometry |

There is no lexicon, no G2P, no FST/HCLG, no LM, no BPE/token vocabulary, no `*.onnx`, and no Whisper/Conformer
weights. A usable Russian dictation model is ≥ 40 MB (Vosk small) and usually hundreds of MB. Nothing of that
order exists on the device, compressed or not. The tar's only files over 2 MB are libraries and `maind`.
**(observed)**

### 2.2 Binaries (observed)

- `libyandex_evil.so` exports 83 `YandexVqeC_*`, 56 `spotter_*`, and 4 helpers. It has **no ASR API.**
- `libtflite.so` registers resolvers `TFullResolver`, `TBioEcapaResolver`, `TBioNextResolver`,
  `TBioRedimnet2Resolver`, `TBioClassifierResolver`, `TSpotterActivationResolver`, `TVqeGtcrnResolver`,
  `TVqeUdResolver` and `TCounteractivationResolver`. **None is for ASR.**
- `maind` source paths under `voicetech/asr/` are only `core/lib/util/{frame_data_view.h, memory_guard.h,
  io/pcm_stream_converter/pcm_stream_converter.cpp}`. These are a PCM format converter and helpers, not a
  recogniser. All other `voicetech/` paths are `bio/*`, `common/kaldi_features_lib/*` (fbank for biometry) and
  `common/tflite_backend/*`.
- Searching every binary for `lattice|ctc|rnnt|transducer|joiner|wfst|hclg|lexicon|language model|n-best` finds
  only `lexicon_g2p_path` and `custom_spotter_ctc_head_model_path` in `libyandex_evil.so`. Those belong to a
  **custom (user-defined) wake word** feature that is **compiled out** in this build. The string
  `Calling CreateCustomActivation() without correct compilation flag` is present, and no such model exists.
- The `ynmt` runtime in `libyandex_evil.so` contains generic transformer kernels (rotary embeddings, MoE,
  `ShadowKVGatherTokens`). That is shared Yandex NN-runtime code. No model on the device uses it. **(inferred)**
- `maind` strings like "Ondevice recognizer enabled" refer to the **biometry** `TRequestProcessor`
  (`voicetech/bio/ondevice/...`), not to speech recognition.

### 2.3 Config (observed)

`quasar.cfg` → `aliced`: `withLocalVins: false`, `shortRecognizerModel: "quasar-general"` (a cloud ASR topic),
`uniProxyUrl`/`uniProxyQuicUrl`, `voiceDialogSettings.useProtoAliceApi: true`, `wsUseZstd: true`,
`quicZstdDictFile: ${QUASAR}/zstd.dict`, and `hybrid_request_factory.hybrid_voice_inputs_when_backend_unavailable:
true` (§4.3).

---

## 3. The cloud STT path, reconstructed

### 3.1 Transport and protocol

- **Endpoint:** `wss://uniproxy.alice.yandex.net/uni.ws`. It is also the hard default in the `RecognizerSettings`
  constructor (vaddr 0xa6cef8). The QUIC alternative is `uniproxy-quic.alice.yandex.net:8443/uni.ws`. Keys
  `uniProxyPreferQuic` and `quicIdleTimeoutSec` are parsed in the VoiceDialogSettings builder at 0xcd04d8.
  **(observed)**
- **Encoding:** protobuf messages in namespace `NAlice.NAliceApi` (`TClientMessage`/`TServerMessage` with
  `TEvent`, `TEventHeader{MessageId, RefMessageId, …}`), zstd-compressed with the 48 KB `zstd.dict`
  (`wsUseZstd`). The protobuf is the lite runtime, so there are no embedded descriptors. Message and field names
  survive only as UTF-8-check strings and RTTI. **(observed)**
- **Messages relevant to ASR (observed names; field semantics inferred):**
  - Client → server: `TVoiceInput` (with `TClientOptions.TOpenStreamPolicy.TVadGate`) and `TAsrRecognize`
    (string fields `Topic`, `Lang`, `Format`, plus `TAdvancedAsrOptions` and `TMusicRequest` for music
    recognition). Audio goes as stream chunks: `TChunkHeader`, `TClientChunkMeta.TChunkTimings`, `TChunkAck`
    back. Also `TStreamControl{TActionClose, TActionSpotterEnd, TActionLazyTTS}` and `TSpotterValidation`.
  - Server → client: `TAsrResult`/`TAsrRecognition{Normalized, ParentModel, TWord.Value}`, `TSpeechControl{TBegin,
    TFinish, TRecognition.Text, TResult}`, and `TAsrMusicResult`.
  - The legacy JSON event names `Vins.VoiceInput` / `ASR.Result` are still in the binary ("start
    Vins.VoiceInput", 0xb96f7e; "ASR.Result.responseCode=").
- **The server does the ASR work.** The Megamind request builder at 0xa47b38 copies server-provided fields:
  `asr_result`, `hypothesis_number`, `end_of_utterance`, `original_zero_asr_hypothesis_index`, `asr_core_debug`,
  `asr_whisper`, `asr_duration_processed_audio`, `hybrid_asr_partial`, `pre_eou_info`, `biometry_scoring`,
  `biometry_classification`. **End-of-utterance is server-side** (`enableE2EEou`, `use_uniproxy_eou_hint`,
  `TPreEouInfo`). The client only enforces timeouts. **(observed strings; role inferred)**

### 3.2 What audio is sent: format, codec and settings

**SoundFormat enum** (`NSpeechKit::SoundFormat`, string table at vaddr 0x154349c, built at 0xa6d748, parsed by
`fromString` at 0xa6d5ec, observed):

| Value | Name | MIME |
|---|---|---|
| 1 | `pcm` | `audio/x-pcm` (`;bit=…;rate=…` suffix built by `toString`) |
| **2** | **`opus`** | **`audio/opus`** |
| 3 | `octet` | `application/octet-stream` |
| 7 | `flac` | `audio/flac` |
| (others) | `headerless_opus_stream`, `headerless_opus_stream_byte_size`, `aac_lc`, `unknown` | |

**`RecognizerSettings` constructor defaults** (vaddr 0xa6cef8, decompiled; config overrides parsed at 0xcd00f0 from
`aliced.voiceDialogSettings.recognizer`, none of which are set in `quasar.cfg` except
`spotterSubscribePointShiftMs`):

| Offset | Field (config key) | Default | Notes |
|---|---|---|---|
| +0x18 | soundFormat | **2 = OPUS** | `SoundFormat(2)` (ctor `0x8ecdbc` is a plain store) **(observed)** |
| +0x1c | `encodingBitrate` | **24000** | bps **(observed)** |
| +0x20 | `encodingComplexity` | **0** | **(observed)** |
| +0x24 | `encodingApplication` | `"voip"` → 0 | `"audio"` is the alternative **(observed key + default; mapping to `OPUS_APPLICATION_VOIP` inferred)** |
| +0x58 / +0x60 / +0x70 / +0x88 | `recordingTimeout` / `startingSilenceTimeout` / `waitForResultTimeout` / `forceReconnectTimeout` | 20000 / 5000 / 12000 / 1000 ms | **(observed)** |
| +0xb8 / +0xbc | `packSoundBuffer` / `maxPackDurationMs` | false / **100** | audio packed into chunks of ≤ 100 ms **(observed; packing semantics inferred)** |
| +0x92 / +0x91 | `enablePunctuation` / `enableCapitalization` | true / false | server-side text formatting flags |
| +0xe0 / +0xe2 | `allowMultiUtt` / `enableE2EEou` | unset (optional) | |
| +0xd8 | `spotterSubscribePointShiftMs` | 0 (cfg: **−80**) | where the ASR stream starts relative to the spotter hit |
| +0x0c | model/topic | from `shortRecognizerModel` = **`quasar-general`** (fallback constant `dialog-general`) | reader 0xccea28 **(observed)** |
| lang | | `ru-RU` (SpeechKit language table at 0xade7a4 also has en-EN, tr-TR, uk-UA) | **(observed table; per-request use inferred)** |
| +0xac | uniproxy URL | `wss://uniproxy.alice.yandex.net/uni.ws` | |

**Opus encoder** (`NSpeechKit::OpusEncoder`, init at vaddr 0xbf5f54, decompiled, observed):

- It accepts only `PCM` input with 1 channel / 16-bit (otherwise it bails).
- `opus_encoder_create(rate, channels, application)`.
- `OPUS_SET_BITRATE(settings.encodingBitrate)` (ctl 4002) and `OPUS_SET_COMPLEXITY(settings.encodingComplexity)`
  (4010).
- `OPUS_SET_SIGNAL(OPUS_AUTO = −1000)` (4024) and `OPUS_SET_BANDWIDTH(1105 = OPUS_BANDWIDTH_WIDEBAND)` (4008).
- Frame bytes = `rate × frame_ms / 1000 × sampleSize × channels`. Output format is set to `SoundFormat 2`.
- `processPcm` (0xbf6274) encodes into a 4000-byte buffer and treats a packet of ≤ 2 bytes as DTX.
- `writeTags` (0xbf68f4) emits the Opus header packets, and **`NSpeechKit::OggOpusEncoder`/`OggEncoder.cpp`**
  (`addPacket` 0xc0a4d0, `ogg_stream_packetin`) puts them into an **Ogg** stream.

So the uplink is **Ogg-encapsulated Opus, wideband, 24 kbit/s, mono** **(observed)**. The Opus frame duration
(field +0x98) was not recovered. 20 ms is the usual SpeechKit choice **(inferred)**. A separate
`headerless_opus_stream` path (`processHeaderlessOpus` 0xbf6538) re-chunks already-encoded Opus into ≤ 40-byte
slices, probably for remote or BT-remote microphones **(inferred)**. The `pjsip` Opus encoder ("Initialize Opus
encoder, sample rate…", `codecs_priority opus/48000`) belongs to **phone calls**, which use `vqe_gain`. It is not
ASR.

### 3.3 Which channel is sent

- The config key `recognizerAudioChannelName` defaults to `""` (default arg at 0x260c6a is an empty string, and the
  field is set only if non-empty, 0xcd0150). It is not present in `quasar.cfg`. The recognizer therefore uses the
  audio device's **main channel**: `audiod.audioDevice.mainChannel: "vqe_0"`, `outRate: 16000`. **(observed
  config + code; "falls back to the main channel" is inferred)**
- `vqe_0` on bergamot is a single Kalman-AEC'd, HPF'd mic. There is no beamformer, and no NR/AGC in ASR mode
  (`docs/vqe-decompile.md` §6). `YandexVqeC_SetASRMode` is driven by `EventLoopAudioDeviceInputController::
  setASRMode`. **(observed symbols)**
- `speechkitLogChannels: ["vqe_0","vqe_bnr"]` and `audiosender.*` are **sound logging and debug dumps**
  (`StreamSoundLogger`, `sendSpotterSound`, `spotterLogging*`). They are not the ASR input. **(observed names;
  role inferred)**
- So the cloud ASR hears **mono 16 kHz AEC'd mic0**. That is exactly the signal a Wyoming satellite should stream
  to HA. This confirms the `hass-integration.md` design choice.

### 3.4 Request flow (inferred from config keys, state names and class names)

```
spotter hit (vqe_0 [+vqe_bnr]) ──► SpotterVadGate / TOpenStreamPolicy.TVadGate (client VAD decides to open)
   └► open uniproxy stream: TVoiceInput{TAsrRecognize{topic=quasar-general, lang=ru-RU, format=audio/opus}}
        audio = Ogg-Opus of vqe_0 starting spotterSubscribePointShiftMs(−80 ms) relative to the hit,
        plus activation audio (onlineSpotterValidationSoundBefore/AfterMs) for TSpotterValidation (cloud re-check)
   ◄─ partial TAsrResult / TSpeechControl … EOU (server) ◄─ final text ─► Megamind ─► directives + TTS stream
   client timeouts: startingSilence 5 s, recording 20 s, waitForResult 12 s
```

---

## 4. Client-side pieces adjacent to STT

### 4.1 Already covered elsewhere

The VQE (`vqe-decompile.md`), the activation/VAD/counteractivation spotters (`spotter-decompile.md`), and the
on-device biometry (ECAPA-TDNN voiceprint with age/gender TFLite heads, `voicetech/bio/ondevice/*`). Biometry only
labels *who* spoke. Its scores ride along in the request (`biometry_scoring/classification`).

### 4.2 `SpotterRecognizer`: an offline recogniser behind the ASR interface (observed)

`YandexIO::SpotterRecognizer` (`spotter_recognizer.cpp`, about 0xc42000) implements the same
`NSpeechKit::Recognizer` state machine as the cloud recogniser. It has states `RecognizerStartingSilence` and
`RecognizerRecording`, `startRecording` / `stopRecording` / `finishRecording`, an `onTimeout recordingTimer`, and
`onSpotterRecognized`. `SpotterRecognizerHandler` (`spotter_recognizer_handler.cpp`) creates the component
`spotterRecognizer` / `spotter-recognizer` from a model path ("Do not create spotterRecognizer spotter. Spotter
path is empty"). It handles `onPhraseSpotted phrase=… id=… context=…`, `maxRecognitionTime`, `resetAfterTrigger`
and `submitTimings`. `IRecognizerFactory` / `SpotterRecognizerFactory` let `LocalRequest` (`local_request.cpp`:
`createRecognizer`, "Data input is not supported in LocalRequest", `asrCompleted_`) use it in place of uniproxy.

### 4.3 Hybrid request routing (observed keys at 0xb79c00–0xb7da00)

`HybridRequestFactory` reads `local_voice_inputs`, `hybrid_voice_inputs`, `local_text_inputs`, `hybrid_text_inputs`,
`local_voice_inputs_when_disconnected`, **`hybrid_voice_inputs_when_backend_unavailable`** (= `true` in cfg),
`use_server_hint`, `use_uniproxy_eou_hint`, `wait_spotter_validation[_while_disconnected]`,
`spotter_validation_timeout_ms`, `local_wait_for_uniproxy_timeout`, `defer_uniproxy_until_local_declines`, and
`local{scenarios, classifiers}`. It reacts to `BackendStatusCapability` ("Backend unavailable: "). `HybridRequest`
runs a cloud `IRequest` and a local one side by side and takes the first acceptable result
(`onRecognitionDone RecognitionPriority=LOW` for local). **(observed names; arbitration logic inferred)**

### 4.4 `FastCommandClassifier`: text → fast command (observed)

`fast_command_classifier.cpp` (about 0xc3f75a) holds a Russian lexicon: «стоп/заткнись/хватит/замолчи/пауза/
продолжи…/громче/погромче/тише/потише/громкость N/следующий/предыдущий/сначала/включи/выключи/лайк/дизлайк/домой/
включи|выключи блютуз|свет|телевизор/яркость N» and the number words ноль…сто. It also has
`TClassifyRequest/Response`. It classifies **text**, either from server partials (`hybrid_asr_partial`,
`sendLocalUtteranceAsRecognitionPartialResults`) or from the local spotter phrase. That text drives
`FastCommandLocalScenario` / `StopCommandScenario` without waiting for Megamind. It is a keyword table, not a
model, so it is trivially re-implementable. **(observed strings; role inferred)**

---

## 5. The offline command recogniser, taken apart

There are two shipped variants of the same vocabulary. They share flags, vocabulary and decoder:

| | `spotter_models/command/acoustic_model.nnet` (also downloaded to `/data/.../a23e7ada…`) | `spotter_models/spotterRecognizer/model.tflite` |
|---|---|---|
| version | `ru-RU-quasar-commands-10Feb25-…-q16-rt-…-dqmm` | `…-fp16-v3-no-likes` |
| runtime | ynmt nnet3 (Q16, `dynamically-quantized-matmul true`) | TFLite, `spotter-npu-use-streaming true`, resolver `SPOTTER_ACTIVATION` |
| commands | 25 (incl. лайк/дизлайк) | 23 (no likes) |
| used by | `CommandSpotterCapability` (always-on command spotting, context-enabled) | `SpotterRecognizer` (§4.2) |

The TFLite one is the better reuse target: it has standard tensor I/O and TFLite tap-order semantics, so the nnet3
tap-order and `MultichannelAttention` ambiguities in `spotter-decompile.md` §3.2 do not apply.

### 5.1 I/O (observed, flatbuffer)

- Inputs: `frames FLOAT32[1,1,6,40]` (6 fbank frames per call) plus 5 streaming states
  `state_0..3 [1,1,{44,39,37,40},244]` and `state_4 [1,1,44,400]`.
- Outputs: `logits [1,1,6,28]` plus `state_*_new`, which are fed back next call.
- There is no batch or sequence flexibility. You call it every 60 ms of audio.

### 5.2 Graph (observed, 41 ops)

```
frames ─DW(1 tap: per-bin scale≈0.28–0.43, bias≈−2.5  = fixed CMVN)─ LIN 40→244
 ┌─ 4× block:  CONCAT(state_k[K−1], x) ─DW(K taps, VALID)─ HSWISH ─ LIN 244→96 ─ HSWISH ─ LIN 96→244   (K = 45, 40, 38, 41)
 │            (state_k_new = last K−1 rows)                                (4th block instead: LIN 244→252 ─ HSWISH ─ LIN 252→400)
 └─ CONCAT(state_4[44], x) ─DW(45)─ HSWISH ─ LIN 400→28
 logits[0:22] ─SOFTMAX─┐
 logits[22:28] ─SOFTMAX┴─ CONCAT ─ MUL 0.5 ─► output (two independent 22- and 6-way distributions, each scaled to 0.5)
```

The ops are `DEPTHWISE_CONV_2D`, `CONCATENATION`, `STRIDED_SLICE` (state roll), `HARD_SWISH`, `SOFTMAX` and `MUL`,
all builtin, plus 10× the custom **`LINEAR_FP16`**. There are about 0.38 M MAC per frame (about 38 MMAC/s), and
about 456 k fp16 weights. Total look-ahead is the `right_context = 16` metadata, about 160 ms
(`spotter-decompile.md` §3.2).

### 5.3 `LINEAR_FP16` (new; observed from the bytes)

- Custom options (flexbuffer): `{BatchSize:1, ChannelsIn, ChannelsOut, WindowSize:6}`.
- The single constant input is a `FLOAT16[(ChannelsIn+1)·COp]` blob, where `COp = ceil(ChannelsOut/8)·8`.
- **Layout:** COp/8 blocks of `[8 bias][ChannelsIn × 8 weights]`. So
  `W[8h+l, ci] = blob[h·(CI+1)·8 + 8 + ci·8 + l]` and `bias[8h+l] = blob[h·(CI+1)·8 + l]`. The op computes
  `y = x·Wᵀ + b` over the last axis.
- Evidence, all observed:
  - the total size matches for all 10 layers;
  - in the 4 layers with padding (244→248, 252→256, 28→32), padded lanes are zero only under this layout, out
    of all 4-D block permutations I tested;
  - the bias slot is exactly zero in the 5 layers whose TF names lack `BiasAdd` (`conv2d`, `conv2d_2/4/6/8`) and
    non-zero in the 5 that have it;
  - the network then behaves (§5.6).
- Replacing it with a builtin `FULLY_CONNECTED` (fp32 weights) or a matmul is mechanical.

### 5.4 Front-end, decoder and vocabulary (observed flags + `spotter-decompile.md`)

- **Front-end:** `feat-frontend log-mel-fbank`, 40 bins, 25/10 ms, 16 kHz, dither 0.1, empty `feat-pipeline`.
  This is the same Kaldi fbank as `spotter-decompile.md` §4. CMVN is baked into the first DW layer, so **input
  level matters**.
- **Decoder:** `spotter-type chen14`, `metric geometric`, `w-smooth 8`, `w-max 60`, `hit-at-peak subhit`,
  `interval-after-last-activation 25`, `zeroing-confidence-buffer-after-hit true`, `use-vad false`.
- **Labels (28)** are two softmax groups:
  - group A (0–21): `SIL дальше назад выше ниже вправо влево вверх вниз домой продолжи играй стоп хватит пауза
    следующий громче тише включи выключи свет телевизор`;
  - group B (22–27): `SIL включи выключи блютуз лайк дизлайк`.
- **Commands** (`commands 1:…:17:18,20:18,21:19,20:19,21:23,25:24,25`): 17 single words, plus «включи/выключи
  свет», «включи/выключи телевизор» and «включи/выключи блютуз».
- **Thresholds** are per command and range from 0.0059 (влево) to 0.484 (выключи блютуз). They are on the
  0.5-scaled posteriors.
- The extremely low navigation thresholds (вверх 0.0109, вниз 0.0193, влево 0.0059) only make sense when those
  commands are context-enabled (`enabledCommandSpotters`, `enableBackCommandSpotterForAnyContent`), e.g. on screen
  devices. On a speaker they should stay off. **(inferred; see §5.6 false alarms)**

### 5.5 Call sequence for a standalone host (inferred, clean-room)

```
pcm16 mono 16 kHz (AEC'd mic, roughly native mic level)
 → Kaldi fbank-40 (Povey, preemph .97, remove-DC, dither .1, 512-FFT, 20 Hz–8 kHz, log floor FLT_EPSILON)
 → every 6 frames: run(frames, states) → logits[6,28], states ← states_new
 → chen14 per command: smooth(w=8) → max over 60 frames → geometric mean over the phrase's labels
 → fire at peak if conf > threshold[cmd]; then hold-off 25 frames and zero the buffers
```

Alternatively, `dlopen` the vendor `libyandex_evil.so` and use `spotter_conf_read(".../spotterRecognizer")` →
`spotter_create` → `spotter_submit_raw_data` → `spotter_get_next_event` → `spotter_get_current_phrase`. This is
Path A of `spotter-decompile.md` §8; that doc lists the remaining unknown args.

### 5.6 Clean-room run (observed)

Script: `scratchpad/stt/scripts/sprec.py`, about 150 lines of numpy. It reads the vendor flatbuffer as data and
executes the 7 op types itself, without TFLite. Test audio: the vendor's own ru-RU prompts
(`system/vendor/quasar/sounds/ru-RU/*`), extracted from the tar and decoded with host `ffmpeg` to 16 kHz s16.

- **Noise / silence:** both SIL labels win (P(SIL) = 0.5 in each half) for white noise at σ = 1…30 LSB.
- **`backend_unavailable`** literally says «…Просто скажите: «Включи Bluetooth»…». Results:
  - `включи блютуз` peaks at **t = 7.58 s of 11.8 s**, which matches the phrase position;
  - labels `включи`(B) and `блютуз` reach 0.499/0.5, i.e. probability about 1;
  - chen14 confidence is 0.40–0.456 against a threshold of 0.468 (0.85–0.97×, depending on input gain
    0.03…0.003);
  - on every other prompt this command scores ≤ 0.01× threshold.
- **«Выключаю Bluetooth»** (out-of-vocabulary inflection) → `выключи блютуз` at 0.40× threshold: near-miss, no
  fire.
- **False alarms:** `вверх`/`вниз` (thresholds about 0.01–0.02) fire on several TTS prompts at 2–12× threshold.
  This confirms they must stay context-gated or be re-thresholded.
- **Caveats:**
  - TTS prompts are clean studio speech and full-scale, not far-field mic audio;
  - my chen14 is a re-implementation and may differ in edge handling (division by `min(t,w)`, peak rule), which
    probably explains landing just under the threshold;
  - this is a plausibility check, not an accuracy measurement.

Cost: about 42 MB RSS and 0.25 s for 2 s of audio in pure numpy on x86. A C/NEON or TFLite port fits easily on
the device's Cortex-A.

### 5.7 Effort to productise

| Step | Effort |
|---|---|
| Convert to a stock streaming TFLite: rewrite 10 `LINEAR_FP16` → `FULLY_CONNECTED` fp32 (or fp16 + dequant) with the layout above, then verify with the numpy runner | 0.5 day |
| On-device service (C or Python-less): fbank + model + chen14 → Wyoming/MQTT event, fed from the satellite's AEC3 stream | 1–2 days |
| Calibration: input gain against the CMVN, re-threshold on your own speech + 24 h negatives, pick an enabled subset (стоп, хватит, пауза, громче, тише, продолжи, следующий, включи/выключи свет) | 1 day + recording time |
| **Total** | **about 2–4 days**. Proprietary weights remain; no vendor code runs |

---

## 6. What is and isn't recoverable

| Recoverable (done or straightforward) | Not recoverable / not present |
|---|---|
| Cloud protocol shape: endpoint, protobuf message names, topic, codec, bitrate, channel, timeouts | The cloud ASR models themselves: they live on Yandex servers |
| Exact `.proto` field numbers: partially, from lite-runtime code. No descriptors are embedded | A working client without a Yandex account, OAuth and device attestation; also pointless, since the goal is offline |
| The offline command recogniser: full I/O, weights, front-end, decoder, vocabulary, thresholds, runnable clean-room | Any open-vocabulary on-device recogniser: none shipped. The custom CTC-head wake word is compiled out |
| `FastCommandClassifier` keyword table | Opus frame duration of the uplink (field +0x98); not needed for reuse |

---

## 7. Verdict

1. **Reusable client-side STT?** **No.** The device has no ASR model, decoder or ASR runtime. Everything beyond
   wake word, VAD gate, biometry and a 25-phrase command spotter is on Yandex's servers. There is nothing to
   decompile for dictation.
2. **What it costs to replicate the vendor split:** nothing new. The vendor uplink is *mono 16 kHz AEC'd mic →
   Opus 24 kbit/s → server ASR with server EOU*. The planned Wyoming satellite already does the equivalent: AEC3
   mic → PCM 16 kHz → HA → GigaAM v3 on `voice-server:24117`. Opus is unnecessary on a LAN. Raw PCM is about 256 kbit/s,
   which is trivial.
3. **Against the open alternatives for an offline HA speaker:**
   - **GigaAM v3 / faster-whisper on `voice-server` (Wyoming): the right choice.** It handles open vocabulary, is
     state-of-the-art for Russian, and the GPU/CPU box already exists.
   - **Vosk-small-ru on the device:** possible (about 50 MB model, Kaldi decoder) and works offline when `voice-server`
     is down. But the accuracy is clearly worse than GigaAM/Whisper, and it costs CPU and RAM on a box we want
     thin. It is only worth it as a "voice-server down" fallback, which `hass-integration.md` already scopes out.
   - **On-device Whisper:** not realistic on this SoC for interactive latency.
   - The Yandex command model is **not** an STT substitute. It recognises 25 fixed phrases.
4. **Worth using locally on the device?** **Yes, but only the command recogniser, and only as an offline-reflex
   layer.** Use «стоп/хватит/пауза/тише/громче/продолжи/следующий», plus optionally «включи/выключи свет» mapped
   to a fixed HA scene. It runs while `voice-server` or the network is down, and as a zero-latency "stop" during playback
   or TTS. This mirrors what Yandex itself uses it for (`hybrid_voice_inputs_when_backend_unavailable`,
   `StopCommandScenario`).
   - Route: the TFLite variant, with `LINEAR_FP16` → `FULLY_CONNECTED`, and a clean-room fbank + chen14.
     **About 2–4 days.** The cheaper but blob-bound alternative is Path A, `dlopen libyandex_evil.so`
     (`spotter-decompile.md` §8).
   - Keep the navigation words disabled.
   - Gate it by context: e.g. only while audio is playing, or within N seconds after the wake word. This cuts
     false accepts.
   - Re-threshold on your own household audio.
   - The blob-free alternative is a small microWakeWord/openWakeWord model set for 3–5 commands. It is more work
     to train but carries no licence ambiguity.

---

## 8. Tools, limits and resource use

- **Host:** WSL2, 23 GiB RAM, 20 vCPU. The sibling TTS agent ran concurrently.
- **Tools:**
  - `arm-linux-gnueabihf-{readelf,nm}`, `strings`, `ffmpeg` (host, used to decode vendor prompts);
  - Python 3.10 with `pyelftools` and `capstone` 5.0.7. My scripts: Thumb PC-relative xref scanner, BL-caller
    scanner, annotated disassembler with string resolution, function-start finder;
  - the existing venv (`numpy`, the `tflite` flatbuffer schema, `flatbuffers.flexbuffers`). **No TFLite
    runtime** was used; the model ran in my own numpy executor.
- **Ghidra 12.1.4 headless:** `maind` imported with **`-noanalysis`**. My `DecompAt.java` clears and sets Thumb
  `TMode`, creates functions at given entries and decompiles them. Each run was about 10 s, under
  `systemd-run --user --scope -p MemoryMax=5G -p MemorySwapMax=0`, `GHIDRA_HEADLESS_MAXMEM=3G`,
  `nice 19 ionice -c3`, `-max-cpu 4`. No full auto-analysis of the 22 MB binary was needed. Callee names are
  therefore anonymous (`func_0x…`), and strings were re-attached by a post-processor.
- **Peak resource use (observed):**
  - Ghidra JVM max RSS about **0.72 GB**; xref scan 0.33 GB; numpy runner about 42 MB;
  - system "used" stayed at about 3.2–3.3 GiB of 23 GiB throughout, with no swap.
- **Execution policy:** no vendor code was executed, natively or in qemu. Model files were parsed as data. Vendor
  audio was decoded by host `ffmpeg`.
- **Not done:**
  - full proto field numbering;
  - the exact WS chunk framing;
  - the Opus frame duration;
  - a vendor-reference posterior capture for the command model (Path A run), which would let §5.6 be checked
    numerically rather than behaviourally.
- **Scratch** (not in repo): `<session-scratchpad>`
  - `scripts/{xref.py, q.py, bl.py, dis.py, fstart.py, ann.py, sprec.py, kfbank.py, tfl_ops*.py, fp16layout*.py}`
  - `gscripts/DecompAt.java`, `dec/*.c` (decompiled functions), `maind.str`, `evil.str`, `tarlist.txt`.

## Appendix: address map (`maind` ELF vaddr; Ghidra = +0x10000)

| vaddr | What |
|---|---|
| 0xa6cef8 | `RecognizerSettings` ctor (defaults: OPUS, 24000 bps, timeouts, uniproxy URL) |
| 0xa6d5ec / 0xa6d748 | `SoundFormat::fromString` / static name+MIME tables (base 0x154349c) |
| 0xa47b38 | Megamind request payload builder (asr_result, end_of_utterance, asr_core_debug, …) |
| 0xb79c00–0xb7da00 | `HybridRequestFactory` config (local/hybrid voice inputs, backend-unavailable) |
| 0xbf5f54 / 0xbf6274 / 0xbf6538 / 0xbf68f4 | `OpusEncoder` init (ctls) / `processPcm` / `processHeaderlessOpus` / `writeTags` |
| 0xc0a4d0 | `OggEncoder::addPacket` |
| 0xc3f75a, 0xc4192e | `FastCommandClassifier` lexicon, `LocalClassifierWrapper` |
| 0xc42000–0xc44600 | `SpotterRecognizer`, `SpotterRecognizerHandler` |
| 0xbdb600–0xbdd100 | `LocalRequest` |
| 0xccea28 | `shortRecognizerModel` reader |
| 0xcd00f0 | recognizer-settings config parser (`recognizerAudioChannelName`, `recognizer{…}`, `encoding*`) |
| 0xcd04d8 | VoiceDialogSettings builder (uniProxyQuicUrl, PreferQuic, onlineSpotterValidationSound*Ms, TTS voice default) |
| 0xade7a4 | SpeechKit language / topic constants (`ru-RU`, `dialog-general`, …) |
