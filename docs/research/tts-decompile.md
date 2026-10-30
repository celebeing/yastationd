# TTS on the Station Mini 3 (`bergamot`): what runs on the device, and what we can reuse

Status: static RE, 2026-10-22. This builds on [spotter-decompile.md](spotter-decompile.md),
[vqe-decompile.md](vqe-decompile.md), [wakeword-mic-array.md](wakeword-mic-array.md) and
[hass-integration.md](hass-integration.md), and does not repeat them. Claims are tagged **(observed)** (read directly
from a file, string, relocation or disassembly) or **(inferred)** (a reading of observed evidence). Addresses are
**ELF vaddrs**: `maind` is Thumb-2 PIE, `libyandex_evil.so` is ARM mode, and the Ghidra address is the vaddr plus
0x10000.

## TL;DR

- **There is no client-side speech synthesiser.** The device holds no TTS engine, no voice or acoustic model, no
  vocoder, no text normaliser and no G2P for synthesis. All «Алиса» speech that is not a canned file is rendered **in
  the Yandex cloud** and streamed to the device over the uniproxy WebSocket (`wss://uniproxy.alice.yandex.net/uni.ws`)
  as **Opus**. The device decodes and plays it. **(observed: absence + protocol code; codec/rate partly inferred)**
- **The "local TTS" in `maind` is a sound-file lookup, not synthesis.** `StaticTTSDataProviderFactory` maps an
  output-speech string, either `<speaker audio="X.opus">` or one of 9 fixed IoT ack phrases, to a canned sound ID and
  plays that file from sound storage. If the file is missing it fails: *"Sound was not found in sound storage"*.
  **(observed)**
- **`tts-timings-info/` is not TTS.** It is the spotter's config for the self-activation blocker. The cloud sends
  per-word and per-phoneme timings alongside the TTS audio. `maind` feeds them to the spotter
  (`spotter_submit_word/phoneme/phoneme_timing`), and the spotter blocks a wake hit while Alice herself is saying
  «Алиса». The files list the wake words and the server phone sequences to match. **(observed)**
- **Pre-rendered audio on the device:** 101 files. Of these, **37 are Russian «Алиса» voice clips (209 s)**: setup,
  errors, "no internet", Bluetooth on/off, and the «М?»/«Угу» activation acks. There are also **19 language-neutral
  earcons** and **45 Arabic (Yasmina) clips**. Everything is Ogg-Opus or MP3. Several `.wav` files are really MP3. The
  inventory is in §5, with transcripts made by our own GigaAM STT.
- **Verdict:** there is nothing to decompile for reuse, so the cost of reusing client-side TTS is zero because there
  is none. Live TTS has to come from an open engine. The existing Silero, or Piper via Wyoming, are both fine, and the
  HA design already assumes this. A few earcons and maybe a handful of Alice clips can go into the voice bank after
  host-side transcoding to WAV.

---

## 1. Search for an on-device engine (observed: negative)

All binaries were checked against `dumps/rootfs-full.tar` (`tar tvf`), and the intact copies were extracted to
scratch. The dump-dir `maind` (22,003,764 B) and `libquasar_gstreamer.so` (5,863,820 B) are intact. The dump-dir
`libyandex_evil.so` is truncated, so the intact 4,912,400 B copy was used. All sound files and `tts-timings-info`
files match the tar sizes byte for byte.

| Search | Result |
|---|---|
| Model and voice files across the whole rootfs, `data/` and `bkupgrade_data/` (`*.tflite *.onnx *.nnet *.bin *.pb *voice* *lingware* *.zip`; every file over 500 KB in the tar) | Only spotter `.nnet`/`.tflite`, VQE `model_call.nnet`, biometry `bio_lingware/*.tflite` (ECAPA, age, gender), and counteractivation `local-ab1-tiny-husky-53.tflite`. **No TTS model.** A Yandex neural voice would be tens to hundreds of MB. The largest unexplained file in the tar is `maind` itself. |
| Engine names in `maind`, `libyandex_evil.so`, `libquasar_gstreamer.so`, `libtflite.so` strings (`vocoder lpcnet tacotron fastspeech hifigan melgan wavenet espeak flite rhvoice piper`) | **0 hits.** The only "vocoder"/"synthesis" hits in gstreamer are FFmpeg/GStreamer codec tables (e.g. "Wave synthesis pseudo-codec"). |
| `phonem`, `g2p` in `libyandex_evil.so` | Only the TTS **blocker** (`tts/tts_timings.cpp`) and `lexicon_g2p_path`, which belongs to spotter enrollment/custom words (see spotter-decompile). |
| `Ondevice`/`offline` in `maind` | Only `YabioOndeviceProtobuf` (biometry) and "Ondevice recognizer enabled" (ASR side, out of scope here). No on-device TTS. |
| `quasar.cfg` | `aliced.withLocalVins false`. No TTS engine or voice paths. TTS keys concern only cloud/proto behaviour (`enableUpdateTtsDirectives`, `useProtoAliceApi`, `wsUseZstd`). |

## 2. How cloud TTS reaches the speaker

### 2.1 Transport and messages (observed: `maind` strings and xrefs)

- **Endpoint:** `quasar.cfg aliced.uniProxyUrl = wss://uniproxy.alice.yandex.net/uni.ws`, plus
  `uniProxyQuicUrl = uniproxy-quic.alice.yandex.net:8443/uni.ws`. With `wsUseZstd true`, the frames are
  zstd-compressed using `${QUASAR}/zstd.dict`. `useProtoAliceApi true` means protobuf messages
  (`NAlice.NAliceApi.*`), with JSON as the legacy alternative.
- **Request:** `TTS.Generate` is built in `VocalizerState::trySendFromQueue` (`0xd423b0`, refs "TTS", "Generate",
  "Synthesis is not requested", "Protocol is not connected"). The protobuf fields are
  `TTtsGenerate.{Text, Voice, Lang, Emotion, Quality, Format, Tag}`, plus `TClientTtsOptions.VoiceId` and
  `TDeferredTtsGenerate.{Text, RequestMessageId}` (lazy or deferred TTS).
  - Voice enum (`0xadea78`): `ermil zahar jane alyss omazh oksana shitova shitova.us`. The **default voice for
    Alice is `shitova.us`**: `alice_config.cpp getVoiceServiceSettings` (`0xcd04d8`) loads it as the default
    string.
  - Quality enum (`0xade9e0`): `Low High UltraHigh`.
  - The device also uses this path for arbitrary text: the `Vocalize` request type and
    `TAliceCapability.TVocalizeDirective.{Text,Voice}` (`AliceCapability::handleVocalizeDirective` `0xce40ac`,
    `startVocalize` `0xce60a8` with *"Unsupported SynthesisMode"*). Even "say this text locally" goes to the
    server.
- **Response:** the server sends a `TTS.Speak` directive with `format` and `lazy_tts_streaming`
  (`UniProxyProtocolState::onDirectiveMessage` `0xbc4d30`: *"Json TTS.Speak received: messageId…, streamId…"*;
  `TTtsSpeak.Format`). The audio follows as a **binary data stream keyed by streamId**
  (`VocalizerProcess::onUniProxyProtocolStream{Begin,Data,End}`, `UniProxyRequest.cpp` `0xbe5108`). Chunk
  metadata travels in `TTtsChunkMeta` (string `header.head.preskip`), and timeouts are
  `synthesisStartTimeout` / `synthesisChunkTimeout`.
- **Timings:** `TTtsTimings { TTiming{Word, Phoneme, …}; TUtterance{ Word{Word, Phonemes} } }` and
  `NAlice.TTtsInterruptionTimings`. These feed §4 and the intonation-interruption spotter.

### 2.2 Codec and framing

- `SoundFormat::fromString` (`0xa6d5ec`, table `0xa6d748`) knows `pcm`, `opus`, `headerless_opus_stream`,
  `headerless_opus_stream_byte_size`, `octet`, `aac_lc` and `flac`, with MIME types `audio/x-pcm`, `audio/opus`,
  `application/octet-stream` and `audio/flac`. **(observed)**
- `maind` links its own **libopus** (`contrib/libs/opus/{celt,silk}`), an `OggOpusDecoder` (`0xbc9d0c`), an
  `OggDecoder`, and an `OpusStreamAppSrc` that strips repeated Opus header and comment packets
  (`opus_stream_app_src.cpp pullData` `0x13d93c8`: "First opus header", "Drop opus comment
  header", "Opus header is different from previous one"). **(observed)**
- **Reading (inferred):** TTS arrives as **Opus**, either Ogg-encapsulated or as a "headerless" packet stream whose
  OpusHead is carried in `TTtsChunkMeta` (hence `preskip`). The `audio/x-pcm;bit=16;rate=16000` strings are used
  only on the biometry and ASR request side (`0xe5ec5c`, `0xe64ba8`), not for TTS. I could not observe the exact
  Opus mode (rate, frame size, bitrate) on the wire, because the device is firewalled and capturing traffic would
  mean re-opening the vendor link. Opus always decodes at 48 kHz internally, and the sink below is 48 kHz.

### 2.3 Local playback (observed config + one inferred default)

- Two players exist: `SpeechkitTtsPlayer` (SpeechKit `AudioPlayer` + `OggOpusDecoder`) and `AudioClientTtsPlayer`
  (`audio_client_tts_player.cpp`). The selector `useAudioClientTtsPlayer` (`0xcceef0`) is read through a bool
  getter with **default `true`** (`movs r2,#1`). **(inferred: the default is the 3rd argument)**
- The audio-client path uses `quasar.cfg audioclient.controller.playbackParams.OPUS_STREAM`:
  `appsrc name=stream-src ! opusparse ! opusdec ! {VOLUME_MASTER} ! {OPT_SPECTRUM} ! {SINK}` with `streamMode true`
  and `COMPENSATE_STREAM_UNDERRUN_PTS`. `SINK = alsasink device=default`, which goes to `/etc/asound.conf` dmix at
  48 kHz stereo on `hw:0,0` (see wakeword-mic-array §2.2). At the end of each reply the player appends
  *"Generating N ms of silence at the end of TTS"* (`onStreamEnd` `0xd6c83c`). **(observed)**
- Volume ducking (`volumeOnTtsStart/End`), `silenceTimeAfterTtsInMs`, `tts_play_placeholder` and similar are all
  dialog-flow logic. None of it synthesises anything.

## 3. "Local TTS" = canned sounds (observed)

`hybrid_request_factory.hybrid_voice_inputs_when_backend_unavailable = true` lets a **local stack** answer when the
backend is down (`local_stack.cpp`, `0xb7f238`: "Start scenario loop", "Scenario run name=", "outputSpeech=",
"local_tts", *"Failed to create local TTS directive"*). Its `outputSpeech` is turned into audio by
`StaticTTSDataProviderFactory::create` (`0xb87148`), which can fail in three ways:
*"No sound storage or output speech could not be converted"*, *"Sound was not found in sound storage"* and
*"Failed to read sound file"*. **There is no synthesis fallback.**

The conversion table (`0xb85818`) maps strings to sound IDs. Each `<speaker audio="…">` file or phrase on the left
maps, in the same order, to the sound ID on the right:

| output-speech key | sound ID |
|---|---|
| `alice-iot-sounds-turn-off.opus` / `-turn-on.opus` / `-on-off-unknown-state.opus` | `alice-iot-sounds-turn-off` / `-turn-on` / `-on-off-unknown-state` |
| «Есть.» «Легко.» «Хорошо.» «Без проблем.» «Конечно.» «Окей.» «С удовольствием.» «Пожалуйста.» | `alice-iot-sounds-done / easy / good / no-problem / ofcourse / ok / with-pleasure / please` **(order-matched, inferred)** |
| «К сожалению, в вашем регионе эта станция недоступна.» | a region-block clip |

The sound-ID enum (`0xb875a0`) has 106 names, shared across Yandex devices (battery, water, CO2, screen…). Sound
storage is `file_player_capability.sound_storage`: defaults come from `${QUASAR}/sounds`, and per-language zips from
the Yandex CDN are cached in `${DATA}/data/sounds`. **58 enum IDs have no file on this device.** Among them are the 8
IoT phrase acks above, `power_on/off`, `mics_muted/unmuted`, `volume_change`, `error`, `cannot_without_internet` and
`searching_for_wifi`. They would only arrive with a CDN download, which now never happens. The «М?»/«Угу» set is
played by `activation_feedback_player` with cumulative probabilities 11/22/…/100, so each clip has an equal 1/9
chance.

## 4. `tts-timings-info` and the TTS self-activation blocker

### 4.1 Files (observed, per activation model; identical copy in `data/quasar/data/spotter_model/8cf52356…/`)

```
alisa/tts-timings-info/words.txt   : Алиса / алиса
alisa/tts-timings-info/phones.txt  : a ll ii s schwa                       (1-based ids 1..5)
alisa/tts-timings-info/rules.txt   : 15  then "<flag> <id:id:…>" lines, e.g.
                                     1 1:2:3:4:5      1 1:2:3:4:5:0:0 …   1 2:3:4:5   0 1   0 2
yandex/…  words яндекс/Яндекс ; phones j aa n d y k s g z ; 17 rules (…:6:7 = «…кс», …:8:9 = «…гз»)
yasmina/… words Yasmina/yasmina/ياسمينة ; phones ARPAbet (Y AE1 S M IY1 N AH0) + BW_* (Buckwalter Arabic)
flags.txt (all three): use-tts-blocker true
```

`phones.txt` reveals the **server TTS phone set**. For Russian, doubled symbols mark palatalised or stressed
variants (`ll` = л', `ii` = stressed и), plus `schwa`. For English it is ARPAbet with stress digits, and for Arabic
it is Buckwalter. **(inferred)** `rules.txt` is a count followed by phone-id sequences. Flag `1` means a blocking
pattern; `0` appears only on single-phone lines. A trailing `0` pads to the same pattern at various lengths, and is
read as a wildcard or unknown phone, because an unknown phone maps to id 0 (see below). **(inferred from the
decompile)**

### 4.2 Code (observed: Ghidra decompile of `libyandex_evil.so`, `voicetech/spotter/embedded/libdecode/neo/tts/tts_timings.cpp`)

| Export / function (vaddr) | Behaviour |
|---|---|
| `spotter_submit_word(h, const char *word, err)` `0x285154`, vtable +0x4c, then `0x432934` | Looks the word up in `words.txt` and sets "current word index". An unknown word gets 0. |
| `spotter_submit_phoneme(h, const char *ph, err)` `0x285390`, vtable +0x50, then `0x432ad0` | Requires a current word ("Phoneme %s was submitted before providing current word"). Maps the phone to its `phones.txt` id (0 if unknown) and pushes `{phoneme_idx, word_idx}` onto a queue. `ph == NULL` closes the word. It then runs the `rules.txt` matcher. The queue is capped at 100,000 entries. |
| `spotter_submit_phoneme_timing(h, …, int64 time, int unit, …, err)` `0x2855cc`, vtable +0x54, then `0x432d8c` | Pops the queued phone, checks it matches by name ("Cant't match %d (%s) and %s phoneme"), and converts the timestamp to a frame number: `frameno = (stream_offset + time) / unit` (64-bit `__aeabi_ldivmod`). |
| `spotter_reset_timings(h, err)` `0x285814`, vtable +0x58 | Clears the state. |
| per-frame check `0x4337b0`; stats `0x43b1f8` (`TTSDeadHits`, `TTSDeadFrames`); log `{phoneme_idx, word_idx, frameno, block}` `0x435a8c` | Blocks activations in frames covered by a matched pattern. After **1000** consecutive blocked frames it gives up: *"turning off tts blocker!"*. |
| config `0x4268a4` / path builder `0x431c30` | Keys `use-tts-blocker`, `tts-blocker-read-from-root`, `tts-timings-info[-]`. |

**Caller (observed):** `maind` imports these functions through GOT (`R_ARM_GLOB_DAT` at `0x14f26a0`, `0x14f2690`,
`0x14f2720` and `0x14f26b8`). All four are called from `MultiSpotterModel::submitTimings` (`0xc57c10`) and the reset
function at `0xc56fe0`. The sequence is: TTS.Timings arrives, `SpottersService::submitTtsTimings`, then
`TTSAnnotationsMapper` ("Start tagging sound buffers with tts timings", `0xcfb738`) aligns them to the played audio,
then submit_word, submit_phoneme… and submit_phoneme_timing.

**Relevance for us:** this only matters if we run the Yandex spotter on a host (spotter-decompile "Path B"). Our TTS
would then need to supply word and phone timings in *Yandex's* phone set, and Piper/Silero do not emit those out of
the box. The cheaper option is to set `use-tts-blocker false` and suppress wake while our own playback is active
(half-duplex), or rely on AEC. With microWakeWord (the recommended path) none of this applies.

## 5. Inventory of pre-rendered audio (observed; not copied into the repo)

The dumps hold 101 files, all of which decode cleanly with `ffmpeg -f null`. The transcripts below come from the
home GigaAM v3 STT (`voice-server:24117`, Wyoming). The audio never left the LAN.

| Set | Path | Count | Total | Format |
|---|---|---|---|---|
| ru-RU «Алиса» (factory) | `system/vendor/quasar/sounds/ru-RU/` | 23 | ~152 s | MP3 48 kHz mono 64–96 kb/s (`*.wav` and `*.mp3`, all MPEG Layer 3) + Ogg-Opus 48 kHz mono 45–75 kb/s |
| ru-RU «Алиса» (CDN delta) | `data/quasar/data/sounds/ru-RU/` (= `bkupgrade_data/…`) | 14 | ~57 s | MP3 48 kHz mono |
| common earcons | `system/…/sounds/common/` (17) + `data/…/sounds/common/` (2) | 19 | ~53 s | MP3 8/32/44.1/48 kHz, Ogg-Opus |
| ar-EG / ar-SA (Yasmina) | `system/…/sounds/ar-{EG,SA}/` | 25 + 20 | ~729 s | Ogg-Opus 48 kHz mono |

**Russian clips (transcribed):**

| file | says |
|---|---|
| `no_internet.mp3` (2.3 s) | «Отсутствует подключение к Интернету.» |
| `no_internet_reboot.mp3` (4.1 s) | «Не могу подключиться к интернету. Попробую перезагрузиться.» |
| `no_internet_reconfigure.mp3` (9.7 s) | «Нет подключения к интернету. Сначала перезагрузите меня. Если это не поможет, нажмите кнопку отключения микрофона и настройте Wi-Fi заново.» |
| `backend_unavailable.opus` (11.8 s) | «Я не очень хорошо себя чувствую, не могу сейчас ответить. Но могу играть музыку через Bluetooth…» |
| `vins_error.wav` (4.2 s) | «Произошла какая-то ошибка. Спросите попозже, пожалуйста.» |
| `playback_error.mp3` (3.9 s) | «Ой, простите. Зажевало плёнку, скоро мы всё починим.» |
| `radio_error.wav` (5.0 s) | «Ой, кажется, я потеряла эту волну. Попробуйте включить другое радио.» |
| `wrong_sound.wav`, `wrong_sound_version.opus` | «Извините, я не расслышала. Можете проиграть звук ещё раз?» |
| `bluetooth_turning_on.mp3` / `_off.mp3` (1.3 s) | «Включаю Bluetooth.» / «Выключаю Bluetooth.» |
| `outgoing_call_failed.mp3` | «Не смогла начать звонок. Попробуйте ещё раз, пожалуйста.» |
| `calls_nanny_turn_off.mp3` | «Радионяня отключена.» |
| `seam_activation_feedback_1…9.mp3` (0.5–0.9 s) | «М?» (1), «Угу» (2–9) |
| setup and Wi-Fi: `first_enter_configure`, `enter_configure`, `configure_success`, `configure_success_update`, `cannot_connect_to_the_wifi`, `wrong_wifi_password`, `no_access`, `something_went_wrong`, `hotel_setup_prompt` | Yandex-app onboarding. They mention «Дом с Алисой» and «Станция Мини 3». |
| account and commerce: `auth_failed`, `brick`, `stolen`, `no_power`, `guest_enrollment_failed`, `bluetooth_turning_blocked_by_subscription` | Subscription, card, "stolen device", non-original PSU, account re-link. |

**Non-speech earcons** (GigaAM returns empty): `spotter_start.mp3` (0.58 s, wake/listen start; `playStartVoiceInputEarcon true`),
`spotter_cancel.mp3` (0.61 s; `playCancelEarcon true`), `boot.wav` (MP3, 9.5 s), `timer.wav` (MP3, 9.2 s),
`notification_sound.mp3` (1.8 s), `volume_sound.mp3`/`qc_buttons_test.mp3` (0.1 s tick),
`bluetooth_turning_{on,off}_jingle.opus` (0.56 s), `alice-iot-sounds-turn-{on,off}.opus` (1.0 s tones), and the
`calls_*` telephony tones (8 kHz, ringback, busy and so on).

## 6. Verdict

1. **Is there reusable client-side TTS?** **No.** Synthesis runs entirely server-side (uniproxy `TTS.Generate`, then
   `TTS.Speak` with an Opus stream). The device has no engine, no voice model, no front-end and no vocoder. Its
   only TTS-related code is an Opus decode/play path (libopus plus a GStreamer `opusdec` pipeline), a canned-file
   lookup for offline acks, and the spotter's self-activation blocker. None of it can produce new speech.
2. **At what cost?** Reusing client-side TTS costs nothing, because there is nothing to reuse. Recreating the Alice
   voice would mean cloning it from about 3.5 minutes of clips. That is technically plausible with modern zero-shot
   TTS, but the voice is a commercial voice built from a real person's recordings, so it is a licensing and consent
   problem. I do not recommend it. Restoring the cloud path is out of scope, because no vendor egress is allowed.
3. **What pre-rendered audio can be reused?** Play it with `aplay` after **host-side transcoding to WAV or FLAC at
   48 kHz**, since the device CLI has no MP3/Opus decoder (hass-integration §2.3), and keep it out of the repo.
   - **Earcons, high value and voice-neutral:** `spotter_start` (wake ack), `spotter_cancel`, `notification_sound`,
     `timer`, `boot`, the volume tick, the BT jingles and the IoT on/off tones. They are the device's "native" UX
     sounds.
   - **Alice speech, optional:** `no_internet*`, `vins_error`, `playback_error`, `bluetooth_turning_on/off` and the
     «М?/Угу» acks. They work as degraded-mode clips, but they mix Alice's voice with the home Silero voice and some
     reference the Yandex app. The setup, subscription and account clips are useless to us.
   - **Not available:** the IoT phrase acks («Хорошо.», «Готово» and so on), `power_on/off` and `mics_muted`. They
     are referenced but were never downloaded, so render them with the home voice.
4. **Compared with open alternatives:** the decision is already made in hass-integration: live replies come from
   **Silero** on `voice-server` (MaryTTS), and the voice bank is rendered from the same voice. **Piper via `wyoming-piper`**
   (`ru_RU-irina/-denis/-dmitri/-ruslan`) is a drop-in Wyoming alternative. It is lower quality than Silero for
   Russian, but fast, MIT-licensed, and could even run off-host. Both give arbitrary text, not a fixed phrase list,
   and need no internet. Nothing on the Station beats or replaces them. The Station's only role in TTS is to be a
   48 kHz WAV sink. Yandex's cloud voice (`shitova.us`, quality `UltraHigh`) is better than all of them, but it is
   cloud-only and not recoverable.

## 7. Tools and limits

- **Tools:** `tar tvf`/`tar x` (intact binaries), `readelf`/`nm`/`objdump` (arm-linux-gnueabihf), `strings`, and
  `ffprobe`/`ffmpeg` (decode, volumedetect) on the audio as data. Two custom Python scripts using capstone 5.0.7 and
  pyelftools: a Thumb/ARM **string-xref scanner** (literal-pool `ldr` + `add rX, pc` and `movw/movt`, with
  function starts from `.ARM.exidx`, giving about 157k refs in `maind`) and a pair dumper. Ghidra 12.1.4 headless,
  `-readOnly -noanalysis`, on a **copy** of the existing `libyandex_evil.so` project, decompiling 10 functions. The
  Wyoming STT client streamed decoded PCM to `voice-server:24117` for the transcripts. All scripts are in the session
  scratchpad (`…/scratchpad/tts/scripts/`). Nothing was executed from the vendor stack, and no qemu was needed.
- **Limits:**
  - `maind` was not run through Ghidra (14 MB of Thumb `.text`). Behaviour is reconstructed from string xrefs,
    tables and short disassembly, so function names come from `__FUNCTION__` and log strings.
  - The exact Opus parameters of the TTS stream, and whether the proto path uses Ogg or headerless Opus, are
    inferred, not captured. A pcap would need vendor egress, which is forbidden.
  - The `rules.txt` semantics (meaning of flag 0, padding with 0) are inferred from the decompile.
  - The phrase-to-sound-ID mapping is inferred from table order.
  - The STT transcripts are machine output and may have small errors.
  - Files under `data/` are not in `rootfs-full.tar`, so I could not check their sizes, but all of them decode
    without error.
- **Resources:** the xref scan peaked at about 216 MB RSS (37 s). The Ghidra run was capped (scope
  `MemoryMax=5G`, JVM 3 GB, `-max-cpu 4`, nice/ionice). Available RAM stayed at or above about 19 GiB of 23 GiB
  throughout, with no swap.
