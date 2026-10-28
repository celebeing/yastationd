# Where «Алиса» ends: finding the wake-word boundary

**Status (2026-10-28):** the anchoring of recommendation 1 is built (see "What the CFW does"); the
offsets are still the desk estimates. All numbers below come from synthetic speech run through the
real vendor spotter under qemu, a first estimate to be redone on real recordings with `wake_capture`
(see "Validation plan").

## What the CFW does

- **The WAKE's position in vqe_0.** `spotter-host serve` prints `AT <stream_ms> <sample>` right before
  each `WAKE`/`CMD` line: the event's position in the vqe_0 stream on fd 3, less the blocks it dropped
  there when the daemon read too slowly (`vqe_pos()`). The daemon counts the bytes it has read from
  fd 3 (`Spotter.ringEnd`, reset with each child) and keeps 4 s of it, so it can find that sample in
  the ring even though the WAKE line arrives a little late (the spotter threads run behind a queue).
  An older spotter-host prints no `AT`: the daemon then falls back to the old pre-roll from now.
- **Where the audio for HA starts** (`wakeAudio`, `Spotter.SubscribeVQEAt`), counted back from that
  sample: `preroll_ms` (default 300) with the wake word cut off, or 1.3 s (`wakeWordLead`) with
  `wake_word_in_stream = true`, which keeps «Алиса» in the stream. Before this, `preroll_ms` counted
  back from the moment the session subscribed, a few tens of ms after the WAKE and more when fd 3
  had dropped blocks. The voice key and follow-ups still take `preroll_ms` back from now.
- **`wake_capture = true`** (a test option) saves, for every WAKE, 3.5 s of vqe_0 (2 s before the
  WAKE point, 1.5 s after) as `<cfw>/wakes/<time>.wav` and a JSON next to it: the board, the model,
  `stream_ms`, the WAKE's sample, the WAV's first sample, `wake_at_ms` (the WAKE point in the WAV),
  and a SUBHIT just before it. The newest 200 are kept (~22 MB). This is item 1 of the validation
  plan; the raw mic is not saved yet.

## The question

`spotter-host` prints `WAKE <stream_ms> <phrase>` when the vendor activation spotter fires. We want the
sample where «Алиса» ends, per utterance, so that `yastationd` can (a) stream to HA with or without the
wake word, and (b) cut a clean wake-word segment for voice ID ([voice-id.md](voice-id.md)).

## What the vendor gives us

- **No boundaries in the API.** The exported C API (`spotter-decompile.md` §2.2) has events and the phrase,
  nothing per frame. `spotter_get_current_activation_info_for_online_validation` and
  `spotter_get_current_activation_metainfo` are not decoded yet. `maind` uses the first one for cloud
  re-validation with fixed windows (`requestSoundBeforeTriggerMs` / `requestSoundAfterTriggerMs`). Decoding
  them takes about an hour of RE and may still turn up a start offset, so it is worth a look.
- **The vendor itself uses a fixed offset.** `quasar.cfg` has `recognizer.spotterSubscribePointShiftMs: -80`:
  `maind` starts the ASR stream 80 ms before the trigger point and lets the cloud ASR deal with what is left.
  orion's verifier also uses a fixed window: `left-context 194`, `right-context 16` frames around the hit.
- **The models are whole-word.** The active activation models are 2-class (`filler`, `алиса`). The
  `tts-timings-info/phones.txt` (`a ll ii s schwa`) feeds the TTS self-activation blocker and is not an
  aligner. The command spotter (`spotterRecognizer`, 28 outputs) has word classes (commands + `SIL`), not
  phones. `vad` (`seam-vad-decoder`) gives only `<speech_started>/<speech_finished>`; it is what the
  vendor uses for "seamless" (no-pause) activation. Neither ECAPA nor counteractivation helps here.
  **No firmware model aligns phones.**

## Desk experiment

Setup: the bergamot activation model (`…-x3-bnr-minispeakers-29Jan24`) in `spotter-host run -t 0,3,4
-c 10` under `qemu-arm-static`, so events have 10 ms resolution. The same mono signal goes to all three
inputs. Test audio: Silero TTS v4_ru (5 voices × 5 speaking rates × 2 pitches) plus espeak-ng (6
variants). Each «Алиса» is followed by a command after a pause of 0, 80, 200 or 500 ms. There are two
conditions: clean (3 LSB floor) and "room" (synthetic RT60 0.4 s reverb with pink noise at 15 dB SNR).
In total 448 utterances and 298 detections (the vendor model missed a third of the TTS tokens, more of
them in the room condition). Ground truth for the end is the last 10 ms frame above −30 dB of the
word's peak, taken from the isolated TTS token. That definition is itself uncertain: the −20/−30/−40 dB
crossings differ by a median of 40 ms (p90 105–165 ms on slow decays), so errors near 50 ms are at the
noise floor of this test.

### 1. The WAKE time itself (fixed offset)

| WAKE − true end | median | p5 | p95 | sd |
|---|---|---|---|---|
| all (n=298) | +40 ms | −180 | +250 | 127 |
| clean | +25 | −180 | +120 | 110 |
| room | +140 | −83 | +290 | 130 |

- It **does not depend on the pause** after the word: the medians are +35…+40 ms for 0/80/200/500 ms.
- It depends on **how long the final «-а» is**. In slow speech the spotter fires before the word ends
  (x-slow median −20 ms, down to −420 ms); per-voice medians range from −80 to +100 ms.
- Reverb pushes it later by about 100 ms.
- End = WAKE − 40 ms: **MAE 89 ms** (clean 75, room 128), 66 % within ±100 ms.
- `SUBHIT` came before only 69 of the 298 hits and is no better.

Where to start the HA stream, with start = WAKE − X (the cost is clipping the command against leaving
part of the wake word in):

| X | clean: command clipped (pause 0) | clean: wake word left in, median / p90 | room: clipped (pause 0) | room: left in |
|---|---|---|---|---|
| 0 | 19 % (60 %) | 0 / 129 ms | 33 % (80 %) | 0 / 50 |
| 150 | 1 % (5 %) | 125 / 279 | 16 % (60 %) | 10 / 200 |
| 300 | 0 % (0 %) | 275 / 429 | 1 % (7 %) | 160 / 350 |

WAKE − start of word: median 550–700 ms, max 1.3 s, so a segment starting at WAKE − 1.3 s holds the whole word.

### 2. Black-box probing of the vendor spotter

Re-run the spotter on audio cut at a 20 ms grid before the WAKE, padded with the noise floor, and find
the earliest cut that still fires (42 utterances, 1344 runs). The model has enough evidence **185 ms
(median) before the end**, in the middle of the final vowel, and WAKE − that point = 242 ± 55 ms (look-ahead
plus smoothing plus the 6-frame batch). Estimating the end from that point has an MAE of 102 ms against 93 ms for WAKE on
the same utterances. **No gain, and 15–30 extra spotter runs per wake.** For the same reason, a posterior
trace from a clean-room re-implementation (Path B) would mark where the evidence is complete, not
where the word ends.

### 3. DSP heuristics around the WAKE

Two hand-built detectors: the largest drop of low-band energy + voicing, and departure from a spectral
template of the final vowel. Both were searched in a window set by the latency prior. **MAE 141–174 ms,
worse than the fixed offset.** With no pause, the vowel runs straight into «включи», «какая», «открой…»,
and the energy cues fail. Not recommended.

### 4. A small STT as aligner (Vosk `vosk-model-small-ru-0.22`, Apache-2.0)

Window [WAKE − 1.6 s, WAKE + 0.3 s], grammar `["алиса", "[unk]"]`, word timestamps:

- found 295/298; **end MAE 65 ms** (bias −10 ms; clean 48–79, room 53–75), 81 % within ±100 ms;
  **start MAE 11–23 ms**;
- start = end − 50 ms: 0 % commands clipped, with a median of 60 ms (clean) / 80 ms (room) of the wake
  word left in, against WAKE − 300 ms: 1 % clipped, 160 ms left in (room);
- x86, one core: 140 ms CPU per 1.9 s window; RSS 262 MB (with Python). Under the 150 MB free on a
  Mini 3, and next to the VQE and the spotters, this **does not fit on bergamot**. On orion/pickle it might fit, but would take
  several hundred ms per wake on an A53/A55 (not measured). It runs fine on a server.

Vosk is about 25 ms better on average than the fixed offset and mainly cuts the tail errors. That is
close to the noise floor of this test.

### 5. Not tried, with estimates

- **Our own tiny CTC/phone model** (40 log-mel → filler/«а л и с а»/blank, ~100–300 k params, streaming
  SVDF or 1-D conv, ~1–3 MMAC per frame), run through the firmware's `libtflite` with `dlopen` from
  `spotter-host`, only on the ~1.5 s around a WAKE: a few ms of CPU per wake, < 2 MB RAM. Expect
  Vosk-like accuracy (±30–60 ms) after training on our own recordings labelled offline (Vosk/MFA)
  plus TTS augmentation. Effort: days, plus data. This is the realistic "micro STT" for the Mini 3.
- **sherpa-onnx / wav2vec2-phoneme / whisper timestamps:** server-side options of the same kind; all
  heavier than Vosk on a Mini 3.
- **The vendor `vad` model** (`<speech_started>/<speech_finished>`, 5×SVDF) through the same C API in a
  second instance: it would tell whether a pause follows the word, but not where the word ends.

## Recommendation (ranked)

1. **Now: anchor on `WAKE stream_ms`, sample-exact, with configurable offsets** (built, see "What the
   CFW does"; the per-board defaults wait for the recordings). Before it, `SubscribeVQE` took
   `preroll_ms` back from the moment of subscription, not from the WAKE, and fd 3 dropped audio when
   the reader lagged, so the daemon could not map `stream_ms` to a ring position. Then:
   - stream "after the wake word": start = WAKE − 150 ms on clean speech, more for far-field. The
     vendor uses −80. Make it a config key (per board, since the models differ and orion's verifier
     adds delay).
   - stream "with the wake word": start = WAKE − 1.3 s.
   - voice ID segment: [WAKE − 1.3 s, WAKE + 0.1 s], optionally trimmed at the front by an energy onset
     (the start is easy: there is usually silence before «Алиса»). ECAPA tolerates ±100 ms of edge
     error; check it on the voice-id data.
2. **If the leftover wake word or clipped commands matter in practice:** strip a leading «алиса»/«са»
   from the transcript on the HA side, or align on a server (Vosk/whisper timestamps) when one exists.
3. **For on-device precision:** train the tiny CTC aligner (§5) on the recordings from the validation
   plan.
4. **Not recommended:** black-box probing, DSP heuristics, Vosk on bergamot, and Path B for this purpose.

## First real recordings (Mini 3, 2026-10-28)

One voice, one room, the speaker a metre or two away, «Алиса, сколько времени?» said eleven times
at different speeds, `preroll_ms = 150`, `wake_capture` on. Labels: Vosk small-ru on each capture,
with a grammar of the phrase's words (9 of 11 got a «алиса»; the other two were heard only as the
command). Offsets are against the WAKE's sample (`wake_at_ms`, 2000 in every capture: the ring
always held the audio):

| | median | range |
|---|---|---|
| «алиса» starts | −410 ms | −770 … −380 |
| «алиса» ends | **−110 ms** | −230 … −80 |
| the command starts | −110 ms (said without a pause) | −230 … −80 |

- **The spotter fires about 110 ms after the word ends** on this unit and voice, close to the desk
  estimate for the room condition (+140) and later than for clean speech (+25).
- With `preroll_ms = 150` the stream starts ~40 ms before the word's end: **9 of 9 transcripts
  were the request alone** («Сколько времени?», answered by HA's built-in matcher at once). With 300
  ms (the default before) 3 of 4 transcripts began with «Алиса», and those went to the LLM.
- The slowest «Алиса» (ending 230 ms before the WAKE) lost ~80 ms of the next word: HA heard
  «Сколько время?», still understood. So 150 trades a rare clipped first syllable for no wake word.
- Taken as bergamot's default: the profile's `wake.preroll_ms = 150` (the config's `preroll_ms`
  overrides it; it is no longer shared between speakers, since the right value is the board's).
- Still to measure: a Station 3 (orion's model has a second-stage verifier: likely later WAKEs, so a
  larger offset), more voices and distances, a pause after «Алиса».

## Validation plan (real recordings)

1. A CFW test option saves, per WAKE: `vqe_0` from [WAKE − 2 s, WAKE + 1.5 s] (anchored on the sample
   counter, not wall time), the `WAKE`/`SUBHIT` `stream_ms`, the model and board (`wake_capture`,
   built; the raw mic is still to add).
2. Collect ≥ 200 wakes per board from ≥ 3 household voices: 1/3/5 m, with and without a pause, with
   music playing, and some long drawn-out «Алиса».
3. Label offline on the desk with Vosk (start/end), spot-check ~10 % in a spectrogram, and keep the
   labels with the audio.
4. Measure WAKE − end per board and per condition. Choose the per-board defaults for the "after" offset
   from the clip/left-in trade-off, as in the table above.
5. Voice ID: equal error rate of segments cut at the fixed offsets against Vosk-cut segments. If they
   are equal, stop at item 1.
6. HA: run the same recordings through the HA STT with each offset; count leaked «алиса» and clipped
   first words.

## Reproducing

Scratch scripts (not in the repo): TTS generation (Silero v4_ru from the torch hub cache,
espeak-ng), track builder with ground truth, `spotter-host run` under qemu (sysroot as in
`spotter-host/README.md`), the probing harness, the DSP heuristics, and the Vosk aligner (pip
`vosk==0.3.45`, model `vosk-model-small-ru-0.22` from 2021).
