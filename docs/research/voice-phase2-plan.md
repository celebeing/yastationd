# Voice Phase 2: mic array, the vendor VQE + spotter on the device, and the yastationd plan

Status: 2026-10-22. Companion to [`spotter-host/README.md`](../../spotter-host/README.md), which has the exact commands and
raw numbers. Static background is in [wakeword-mic-array.md](wakeword-mic-array.md), [vqe-decompile.md](vqe-decompile.md)
and [spotter-decompile.md](spotter-decompile.md). Everything here was **measured on the device** unless marked
**(unverified)**. Nothing in this work starts `maind`; our own host links `libyandex_evil.so` directly.

## 0. TL;DR

- **Mic map** (`hw:0,1`, 8ch S16_LE 48 kHz):
  - ch0/1/2 are the three live mics;
  - ch3 is a copy of ch2;
  - ch4/5 are unpopulated;
  - ch6/7 read a constant −1 when idle; ch6 is the HW amp feedback (AEC reference). That ch6 carries playback is **unverified**, since nothing played during the captures.
- **The vendor VQE is drivable from our host.** `libyandex_evil.so` exports a complete `YandexVqeC_*` C API (preset → create →
  process → get output). The `bergamot` preset runs natively on live input and produces `vqe_0`, `vqe_bnr` and `vqe_gain` at 16 kHz. It
  costs 42 % of one A53 core. The reconstructed API is in `spotter-host/vqe_api.h`.
- **The vendor spotter runs natively.** «Алиса» (layout `0,3,4`) plus the **vendor command spotter** together take 87 % of one core,
  about 20 MB RSS, for the whole VQE + wake + command pipeline. A 12-minute live run in a quiet room produced 0 events.
- **The vendor command spotter exists and loads** with the same API. It is plain mono (`spotter_create`) with 27 outputs:
  дальше, назад, выше, ниже, вправо, влево, вверх, вниз, домой, продолжи, играй, **стоп, хватит, пауза**, следующий,
  **громче, тише**, включи/выключи (+свет/телевизор/блютуз), лайк, дизлайк.
- Real-voice recall has **not been measured**, because the agent could not speak to the device. The user runs `spotter-host/tools/listen.sh` (§6).

## 1. Mic channel map (measured)

The captures were `arecord -D hw:0,1 -c 8 -r 48000 -f S16_LE`, 6 s and 20 s, in a quiet room. Analysis was done on the dev box, and the captures were kept out of git.

| ch | What | Evidence |
|---|---|---|
| 0 | mic (PDM0) | live; floor about −61 dBFS rms (27–31 LSB); r≈0.95–0.98 to ch1/2 at low frequency, r≈0.2–0.4 above 150 Hz |
| 1 | mic (PDM1) | the same |
| 2 | mic (PDM2) | the same |
| 3 | **duplicate of ch2** (±1 LSB) | r=1.000 with ch2 |
| 4, 5 | unpopulated PDM lane (identical to each other) | an open transient of up to −1759 that decays to exactly 0 within ~1.5 s, then stays 0 |
| 6, 7 | TDM-B loopback = amp feedback (ch6 per `asound.conf`) | constant −1 with nothing playing |

Notes:
- **Open transient:** every channel shows a large DC/LF settling transient for the first ~1–1.5 s after `open` (up to about −20 dBFS
  on the mics). Either discard the first 1.5–2 s, or high-pass below about 150 Hz. The VQE's own HPF handles it after about 1 s.
- **Room noise is mostly below 150 Hz.** Raw mic energy is concentrated under 150 Hz: rms 27 LSB raw versus 13 LSB after a 150 Hz HPF. Above 1 kHz the mics sit
  near their self-noise.
- Mixer state (read-only): `Loopback Enable`=Enable, `Loopback datain source`=PDMIN, `Loopback tmdin lb source`=TDMIN_B,
  `PDM Gain`=0, `PDM Filter Mode`=1, `pdm dclk`=3.072 MHz (which supports 8/16/32/48 k). Only 48 k S16 8ch was tested.
- **Best single mic:** any of 0/1/2, since they are equivalent in quiet. The VQE's HEALTHIEST picker chose **0** throughout.
- **Open item:** confirm that ch6 carries playback. Run `listen.sh` while yastationd plays an announcement; the `spk` column should
  leave −90 dBFS, and the `vqe` column should stay well below `raw` during playback if the AEC works.

## 2. Vendor VQE from our own host: verdict = tractable, done (prototype)

### 2.1 The exported API (from disassembly; confirmed by device runs)

Every function returns `YandexVqeC_Error*`, which is NULL on success. Read the message with `YandexVqeC_Error_GetMessage`, then call
`YandexVqeC_Error_Free(err)`.

| Call | Signature / behaviour |
|---|---|
| `YandexVqeC_Config_CreateFromPreset` (0x198d1c) | `(const char *name, Config **out)`. `"bergamot"` gives the compiled preset (vqe-decompile §2) |
| `…Config_GetMicsCount / GetSpeakersCount / GetInputSamplingRate / GetOutputSamplingRate / GetProcessingFrame / GetInputChunkSize` | plain getters. bergamot gives 3 / 1 / 48000 / 16000 / 256 / **768** |
| `…Config_GetOutputChannelCount` | `(cfg, type, int *out)`. Gives raw_mic 3, raw_sync_mic_main **1** (the decompile said 0), raw_spk 1, vqe 1, vqe_bf 0, vqe_bnr 1, vqe_gain 1 |
| `YandexVqeC_Create` (0x1994bc) | `(cfg, const char *model_dir, size_t n, const {name,path} *models, Vqe **out)`. Called as `("/system/vendor/quasar/vqe_models", 0, NULL)` |
| `YandexVqeC_Process` (0x199870 → 0x1d34ec) | `(vqe, const float *mic, size_t chunk*mics, const float *spk, size_t chunk*spk)`. Both buffers are **interleaved float**; the sizes are checked and the call throws on a mismatch |
| `YandexVqeC_GetOutputChannelData` (0x199900) | `(vqe, type, index, const float **data, size_t *n)`. Gives 256 floats per chunk at 16 kHz, owned by the VQE |
| `YandexVqeC_GetOmniMicIndex` / `GetMicHealthConfidences` | `(vqe, int*)` / `(vqe, const float**, size_t*)` |
| `YandexVqeC_SetASRMode / SetCommunicationMode` | `(vqe, int)`. **Not called**; semantics (?). maind passes an inverted bool to SetASRMode |
| `YandexVqeC_Free / Config_Free` | destructors |

The output-channel types are 1 raw_mic, 2 raw_sync_mic_main, 3 raw_sync_mic_aux, 4 raw_spk, 5 raw_spk_sync, 6 vqe, 7 vqe_bf, 8 vqe_bnr and
9 vqe_gain (vqe-decompile §6).

**Sample scale (unverified):** the host uses float = s16/32768, and converts outputs back with the same factor. With float = s16 (`-s 1`)
the quiet-room output levels relative to raw were the same, so the processing looks scale-invariant when there is no echo. Scale may matter
once the Kalman AEC is active. The scale maind uses was not traced, because its conversion sits deeper than the thin wrapper at
`maind` 0xddba16.

### 2.2 What it does to live audio (quiet room)

| Stream | Level | Notes |
|---|---|---|
| raw mic | −61 dBFS | LF-dominated |
| `vqe_0` | −79…−84 dBFS | about +0 dB versus raw above 1 kHz and −8 dB at 150–1000 Hz; below 150 Hz is removed (HPF). Speech band preserved: TTS at −30 dBFS raw came out at −31 dBFS |
| `vqe_bnr` | −85…−89 dBFS | adaptive 2-reference canceller (see the caveat below) |
| `vqe_gain` | `vqe_0` + 34 dB | the ×50 ASR-mode gain. It clips for anything louder than about −35 dBFS |

**Caveat:** `vqe_bnr` cancels any signal that is coherent across the mics. With synthetic speech that is bit-identical on all three
mics, it converged and wiped out the speech, which cost the «Алиса» model 5 of 6 hits. Real speech is not bit-identical across mics, so this is
probably a test artefact, but it is **unverified**. The fallback costs nothing: feed `vqe_0` into the model's BNR input as well
(`listen -b`), which scored 6/6. This is option (a) from spotter-decompile §7.3.

**Cost:** VQE alone uses 42 % of one A53 core at 1.2 GHz and 13 MB RSS. It runs on the ARM CPU; this unit has no HiFi4 DSP path
(vqe-decompile §2).

### 2.3 Comparison with the alternatives

| Option | AEC | Effort | Status |
|---|---|---|---|
| **Vendor VQE via `YandexVqeC_*`** | Yandex Kalman AEC on the HW feedback, as tuned for this device | done | prototype works; AEC unverified |
| webrtc-audio-processing AEC3 + HPF (vqe-decompile §7) | yes | cross-build a C++ lib for armhf/glibc-2.30 | not started |
| No AEC: HPF + mic0 into omni and BNR (`listen -x 0,0,1`) | **no**. Wake detection during our own playback will be poor | none | works; 6/6 synthetic |

Recommendation: use the vendor VQE as the default front-end, since it costs nothing more and matches the training data of the vendor models. Keep `-x` as the no-VQE fallback.

## 3. Spotter results (summary; details in the spotter-host README)

| Test | Result |
|---|---|
| `probe` of all models, natively | all load. «Алиса» `0,3,4`; built-ins `0,4`; command/VAD/intonation/spotterRecognizer `0` |
| live 12 min, VQE + «Алиса» + commands, quiet room | **0 events**; 90 % of one core; RSS 20 MB flat |
| synthetic «Алиса» (6×, −30 and −45 dBFS, real device room noise) | 6/6 with `-b` and with `-x 0,0,1`; 1/6 with the real `vqe_bnr` (artefact, §2.2) |
| synthetic commands (20 utterances) | 8–10/20 at −30 dBFS; 1–7/20 at −45 dBFS; never тише/дальше/включи свет in TTS. The command model is **level-sensitive** |
| false hits («сегодня хорошая погода», «Яндекс», quiet room) | 0 in every run |

**Overruns:** arecord's default `hw:0,1` buffer (2048 frames, about 43 ms) overran 853 times in 12 minutes behind the spotter pipe.
`--buffer-time=1000000 --period-time=20000` gave 0. **This applies equally to `yastationd/internal/hw/mic.go`**, which runs arecord with
the default buffer. Add those two flags there.

## 4. Phrase / command spotting

**Vendor models present on the device:**
- `command`, at `/data/quasar/data/spotter_model/a23e7ada-…` and identical to `/system/vendor/quasar/spotter_models/command`.
  - Version `ru-RU-quasar-commands-10Feb25-08-25-shrd-bt-likes-q16-rt-lower-fah-lower-stops-dislikes-fah-dqmm`.
  - `chen14`, mono, 27 outputs (word list in §0).
  - Composite commands are configured as id pairs (`18,20` = «включи свет»).
  - It has its own per-command `thresholds`, with lowered thresholds for stops (the name says "lower-stops").
  - Loads with plain `spotter_create`. Events carry the phrase text, e.g. `CMD CONFIRMATION_1ST_STAGE стоп`.
- `spotterRecognizer`: the same command set as a TFLite model (`spotter-npu-model-path model.tflite`). It loads on the CPU too.
  It could be A/B-tested against the nnet one.
- `intonationInterruption` (`<intonation>`, meant for barge-in while the speaker talks), `vad` (`<speech_started>/<speech_finished>`), and
  `counteractivation` (a TFLite false-trigger suppressor; it has only `multiflags.txt` and was not probed).
- In maind the command spotter is armed **contextually**: during playback and after activation. The `interval-after-last-activation 25` flag
  supports that reading. Its stock thresholds were tuned for that gated use, so always-on use will false-accept more **(inferred)**.

**How to use it in yastationd:** don't run commands always-on. Arm the command detector:
- while something is playing (стоп/хватит/пауза/громче/тише/следующий are the useful ones);
- for a few seconds after a wake.

That also halves the CPU while idle (§3 cost table: about 14 % of a core for commands).

**Level:** feed the command model a level-normalised stream. It did better on `vqe_gain` than on `vqe_0` for quiet speech, and clipped on loud
speech. A simple AGC (for example a slow RMS normaliser to about −30 dBFS) in front of it is the next thing to try.

**Open alternatives, if the vendor models disappoint:**
- **microWakeWord** (TFLite-micro streaming). The user already has a Russian training pipeline in `<ha-config-repo>/mww-training/`. Train
  per-command models such as «стоп» and «громче». It is sovereign, but each word is its own model and its own CPU cost.
- **openWakeWord**: a shared embedding plus small per-phrase heads, which suits many commands. Its Russian TTS-synthetic training is weaker.
- **Vosk small-ru with a constrained grammar** (`["стоп", "хватит", "громче", "тише", "[unk]"]`): no training needed and a good fit for
  context-armed commands. It is heavier (about 50 MB model, about 1 core while active) but would only run when armed.

## 5. yastationd integration plan (Phase 2)

Constraints: keep the blob out of the Go process. `hw:0,1` is exclusive (no dsnoop), and **yastationd is the single mic owner**
(`internal/hw/mic.go`: one arecord, fan-out to subscribers, push-to-talk subscribes to ch0 → 16 k mono).

```
arecord hw:0,1 8ch 48k (--buffer-time=1000000)  [yastationd hw.Mic, always on while the wake word is enabled]
   ├── raw 8ch frames ──stdin──> spotter-host serve  (child process, LD_LIBRARY_PATH=/system/vendor/quasar/libs)
   │                               VQE(bergamot) -> «Алиса» (0,3,4) [+ command spotter when armed]
   │                    <─stdout── event lines
   │                    <──fd 3─── vqe_0 16k mono S16 (AEC'd)  → Wyoming audio-chunk to HA during a session
   └── ch0 -> HPF -> 16k mono  (existing push-to-talk path / fallback when the spotter is off or dead)
```

### 5.1 Host side (C) – the `serve` mode to add (small delta from `listen`)

- **stdin:** raw `hw:0,1` frames (8 × S16LE @ 48 kHz), exactly as arecord delivers them, so the daemon doesn't need to reformat.
- **stdout:** one line per event; the daemon ignores lines it doesn't know.
  ```
  READY vqe=bergamot wake=<model-version> cmd=<model-version>
  WAKE <stream_ms> <phrase>                 # CONFIRMATION_1ST_STAGE on the activation model
  SUBHIT <stream_ms> <phrase>               # optional, for "almost" diagnostics
  CMD <stream_ms> <phrase>                  # command confirmed (only while armed)
  LEVEL <stream_ms> vqe=<dB> raw=<dB> spk=<dB>   # 1/s, only with -v
  ERR <message>
  ```
- **stdin control (optional):** single-letter lines on a second pipe (fd 4), so that stdin stays pure PCM. `C1`/`C0` arms or disarms the command
  detector; `R` resets the spotters after a session.
- **fd 3 (optional):** a continuous `vqe_0` 16 kHz mono S16 stream. The daemon reads and discards it, except during a voice session, when it
  forwards it as Wyoming `audio-chunk`. HA then gets AEC'd, high-passed audio for STT. Keep fd 3 drained, or use a non-blocking write
  with drop-on-full in the host.
- The host exits on stdin EOF. If it crashes, the daemon falls back to push-to-talk only and respawns it with backoff.

### 5.2 Go side (draft, not wired)

```go
// internal/wake/wake.go (draft)
package wake

// Event is one line from spotter-host serve.
type Event struct {
	Kind   string // "WAKE" | "CMD" | "SUBHIT" | "LEVEL" | "READY" | "ERR"
	At     time.Duration
	Phrase string // "алиса", "стоп", "громче", ...
	Fields map[string]string
}

// Spotter runs the vendor host as a subprocess. The blob never enters the Go process.
type Spotter struct {
	Bin, WakeModel, CmdModel string // /bkupgrade/cfw/bin/spotter-host, /data/quasar/data/spotter_model/...
	Log                      *slog.Logger

	cmd   *exec.Cmd
	stdin io.WriteCloser // raw 8ch 48k frames from hw.Mic
	ctl   io.WriteCloser // fd 4: "C1"/"C0"/"R"
	vqe   io.ReadCloser  // fd 3: 16k mono vqe_0
}

func (s *Spotter) Start(ctx context.Context) (<-chan Event, error) // exec + line scanner; restarts with backoff
func (s *Spotter) Write(raw8ch []byte)                             // non-blocking: drop + count if the pipe is full
func (s *Spotter) ArmCommands(on bool)
func (s *Spotter) SubscribeVQE(fn func(pcm16k []byte)) (unsub func())
```

`hw.Mic` gains `SubscribeRaw(func([]byte))`, which delivers the 8-channel frames before channel selection. While the wake word is enabled the
mic runs continuously; the refcount keeps it open.

### 5.3 Behaviour in `internal/app`

- `WAKE`: run the same path as the voice key (`satellite.startListening`): ring "listening" effect, Wyoming `run-pipeline`
  starting at `asr`, and stream audio. Stream `vqe_0` from fd 3 if available, otherwise the ch0 path. Pre-roll about 300 ms of buffered audio, so that HA's
  VAD doesn't lose the first word after «Алиса».
- **Mute:** if the mic-mute switch (event0) is on, the daemon stops feeding the host. The HW mute may already silence PDM; that is **(unverified)**.
- **Commands:** arm while `aplay` is playing, and for about 4 s after a wake.
  - `стоп`/`хватит`/`пауза`: stop playback, plus a Wyoming `pause`/announce-cancel if HA initiated it.
  - `громче`/`тише`: ±10 % volume via the existing volume path, with the matrix bubble shown.
  - These are local reflexes: they work without HA, like the buttons.
- **AEC reference:** our own playback goes to `hw:0,0` via dmix. The amp feedback on ch6 is what the VQE wants, so no software reference
  is needed. **Verify ch6 during playback before relying on it**; if it stays at −1, use the `Loopback` controls (read-only so far), or a
  software reference from the daemon's own playback buffer (`-x` mode as the fallback).
- Expose `binary_sensor wake_word_detected` and a `select wake_mode` (off / wake / wake+commands) over MQTT.
  Wyoming `detection` events let HA show «Алиса» as the wake word used.

### 5.4 Deployment

- **Install path:** `/bkupgrade/cfw/bin/spotter-host` (about 23 KB). It needs `LD_LIBRARY_PATH=/system/vendor/quasar/libs` and reads the models in place.
  Deploying it doesn't touch rootfs or the maind gate. Dlopening `libyandex_evil.so` does not start the Yandex stack. Keep `maind_off`.
- **Thresholds:** to tune, copy the model dir to `/bkupgrade/cfw/spotter_models/alisa-local/` and edit `thresholds` in `flags.txt`.
  The stock activation threshold assumes cloud re-validation follows (spotter-decompile §7).

## 6. What the user should run next (real voice)

```sh
sh /tmp/spotter/listen.sh 600 | tee /tmp/spotter/live.log         # default (real vqe_bnr)
sh /tmp/spotter/listen.sh 600 -b | tee /tmp/spotter/live-b.log    # BNR <- vqe_0
sh /tmp/spotter/listen.sh 600 -x 0,0,1 | tee /tmp/spotter/live-x.log   # no VQE
```

Staging instructions are in the spotter-host README. In each run, say «Алиса» about 5 times at 1 m, 3 m and 5 m, plus «стоп», «громче» and «тише». Note the
wall-clock times, then count `WAKE CONFIRMATION_1ST_STAGE` lines against them. Once yastationd owns the mic, `listen.sh` refuses to
start (via `hwfree.sh`). Stop the daemon's capture first, or wait for the `serve` integration.

## 7. Open questions

1. Does ch6 carry the amp feedback during playback, and does the vendor AEC cancel our own playback (wake-during-music)?
2. Real-voice recall: default vs `-b` vs `-x`, at 1/3/5 m. Does `vqe_bnr` behave with real speech?
3. Which input scale does maind use for `YandexVqeC_Process`? (It matters only if the AEC misbehaves.)
4. FA/h on a day of household audio (TV, talk), with commands armed and disarmed.
5. What do `SetASRMode`/`SetCommunicationMode` change? (Possibly the ×50 gain and NN-NR; not needed so far.)
