# Voice ID on the speaker: who is talking, guests ignored, the speaker's gender in HA

**Status:** research and design, 2026-10-28. Nothing built, no code yet. The firmware's models
were checked on the desk against real requests (below); the rest is a plan, with the two checks
it waits on (an aborted run in HA, the wake word as a sample) and a comparison with doing the same
on a server.

The speakers' firmware ships a voice biometry stack of its own (`/system/vendor/quasar/bio_lingware/`),
run by the vendor assistant (`maind`), which the CFW keeps off. Its models are ordinary TFLite files
that run outside `maind`. This document is about running them in the CFW: on the speaker, with no
server and no cloud. The server-side alternative (a Wyoming STT proxy with an open model) is
[speaker-id.md](speaker-id.md); the two can coexist.

## What the firmware has

| | Mini 3 0.6.1.4 | Mini 3 0.369, Mini 3 Pro, Station 3 |
|---|---|---|
| Voiceprint: ECAPA-TDNN, `body.tflite` (1.4 MB) + `head.tflite` (150 KB), 512-dim | an older model (`tflite-ecapa-tdnn-sqw-0.0.5`) | `next_tdnn_1-3M_v1_thr66`, **byte-identical on all three** |
| Gender: one dense layer + sigmoid on the voiceprint (`next-gender-0.2`, threshold 0.55) | — | yes |
| Age: the same shape (`next-age-0.2-thr60`); in our data it reads as "a child?" | — | yes |
| `libtflite.so` with the TFLite C API (`TfLiteInterpreterCreate`, …) | — (the author's unit runs the 0.369 one from `<cfw>/vendor/libs`) | yes |
| Thresholds (`config.json`) | guest 0.592 | guest 0.66, confidence 0.815, margin 0.02 |

- **Features** (`features_config.json`): Kaldi log-mel filterbank, 80 bins, 16 kHz, 40 ms frames
  every 24 ms, pre-emphasis 0.97, DC removal, Hamming window, power spectrum. Fully specified, so we
  compute it ourselves.
- **Streaming shape:** `body` takes blocks of 20 frames (0.48 s) → 128 values per frame; `head` takes
  up to 300 frames (7.2 s, windows overlapping 75 %) → the 512-dim voiceprint. The vendor computes
  partial voiceprints every 20 frames while the person speaks.
- **Ops:** only stock TFLite ops (conv, dense, mean, softmax, tanh…), no custom ones: the stock
  runtime loads all four files.
- One model on every current unit but the old Mini 3 firmware means voiceprints are comparable
  **across speakers**: one household profile serves all rooms. A unit with the old model takes the
  newer files from another firmware, the way the VQE models already move (`<cfw>/vendor/`).

### Checked on the desk (2026-10-28)

252 real requests from four speakers of one household (12 min; HA's pipeline debug recordings),
our fbank + the stock TFLite runtime + the Station 3 models, cosine to the robust centroid of the
dominant voice, transcripts from the household STT to tell who spoke:

| Cosine to the main voice | Count | What they were |
|---|---|---|
| 0.70–0.95 | ~200 | the household's main user, every room |
| 0.50–0.55 | a few | someone else's voice from a video played nearby (male) |
| 0.17–0.32 | ~15 | no speech at all: false wakes, noise, silence up to the 15 s timeout (gender ≈ 0.5) |
| 0.01–0.17 | 4 | the other household member (gender 0.85–0.98: female) |

The vendor's guest threshold 0.66 falls between the main user and everyone else; gender is
0.01–0.3 for the man, 0.85+ for the woman, ≈0.5 for non-speech. A desk check, not a benchmark: one
household, mostly one voice; it says the pipeline works, not how well it separates two similar
voices.

## What the owner gets

**In HA, per speaker** (MQTT discovery; updated as soon as a request has been heard, before HA
acts on it):

- `sensor.<speaker>_voice` — the enrolled person's name, `guest`, or `unknown` (too short, no
  speech). Attributes: `score`, the runner-up and its score, `gender`, the model tag.
- `sensor.<speaker>_voice_gender` — `male` / `female` / `unknown`, needs **no enrollment** (the
  firmware's classifier). In a household of two that alone says a lot.
- `binary_sensor.<speaker>_voice_known` — the household or not.

The LLM agent reads them through the prompt template (`speaker-id.md` §4 shows the shape);
automations use them directly.

**On the speaker, runtime options** (HA and the web app):

- `switch` **Ignore guests**: a request from a voice that is not enrolled is dropped before HA
  hears it; the `voice_ignored` moment shows that it was heard and ignored on purpose (a moment like
  the others: the owner picks its picture; default a glance on seg7, a "no" on Station 3).
- `number` **Guest threshold** (0.40–0.90, default the firmware's 0.66), and what happens when the
  verdict is unsure (too little speech): let it through (default) or drop it.

**In the web app, a Voices tab** (calibration and debugging, below).

## How it works

```
mic → spotter-host: VQE → wake word ─────────────────┐
                      └→ fbank → body (every 0.48 s) ─┴→ head → voiceprint (+ gender, age)
                                                              │ VOICE event (stdout)
yastationd: compare with the household's voices → verdict ────┘
   ├─ publish the sensors (MQTT)            ├─ arbitration: the winner's verdict counts
   └─ ignore guests: abort the request (or hold, then release or drop it)
```

1. **spotter-host** already holds the clean 16 kHz audio and the vendor libraries. It computes the
   fbank on the fly, runs `body` every 20 frames, and `head` when asked; it loads `libtflite.so`
   with `dlopen` like `libyandex_evil.so`. It keeps the last ~2 s of audio, so the wake word itself
   is part of the voiceprint: «Алиса» every time is the most comparable sample a household has.
2. **Stopping a guest before HA acts on it, without adding latency.** HA's VAD ends the request
   and its intent runs at once, so the verdict has to come while the person is still speaking. Two
   ways; the first is the one to build if its checks pass:
   - **The wake word decides (no added latency).** The satellite already has «Алиса» when the wake
     fires — the same phrase every time, the most comparable sample a household has. Its voiceprint
     takes ~0.1 s and runs in parallel with the start of the request; the audio streams to HA at
     once, as today. While the command goes on, the voiceprint is refined on it. A `guest` verdict
     aborts the request while HA is still listening; commands almost always last longer than the
     verdict takes. **To check first:** that HA drops a run the satellite aborts mid-speech instead
     of acting on the part it already transcribed (the Wyoming satellite code in HA, then one try on
     a unit), and how well «Алиса» alone separates voices (the HA recordings start after the wake
     word, so this needs voiceprints collected on a unit).
   - **Hold and release (fallback).** The satellite holds up to ~1.2 s of audio (the wake word and
     the start of the command), takes a voiceprint, then sends the held audio in one burst and
     streams on — HA hears the end of the phrase at the same moment as before, so the hold costs the
     voiceprint's ~0.1 s, not the 1.2 s — or drops the request. Safe whatever HA does with an
     aborted run; a little latency, and more audio to judge by than the wake word alone.

   Either way `voice_ignored` shows on a dropped request. With "ignore guests" off nothing is
   aborted or held: the sensors get the early verdict, and a better one (the whole utterance) when
   the speaker stops.
3. **yastationd** compares the voiceprint with the household's voices (cosine to each person's
   centroid; the best one above the threshold with a margin over the runner-up, else `guest`),
   publishes the sensors, keeps the recent requests for the Voices tab, and gates the request.
4. **Arbitration** ([wake-arbitration.md](wake-arbitration.md)): every speaker that heard the wake
   word starts its voiceprint at once, without waiting for the arbitration result; the winner's
   verdict counts (it streams to HA and publishes), the losers drop theirs. Later, maybe: the winner
   takes the best score any claimant reported.

## On the speaker or on the server

The same feature can live in a Wyoming STT proxy next to HA's STT on a server
([speaker-id.md](speaker-id.md)): it computes a voiceprint of each request in parallel with the
transcription and passes the transcript on, or an empty one for a guest. Compared (2026-10-28):

| | On the speaker (the firmware's models) | On the server (a proxy in front of the STT) |
|---|---|---|
| Added latency | none (the wake word, in parallel with the start); ≈0.1 s with hold and release | none: the voiceprint runs in parallel with the STT (CAM++ ≈30 ms on one x86 core) |
| Stopping a guest | abort the request before the speech ends; only as clean as HA's handling of an aborted run (to check) | clean: an empty transcript, HA does nothing |
| Judged on | «Алиса» + the start of the command | the whole phrase without the wake word (typically 2–3 s: more audio, but a different phrase each time) |
| Models | the firmware's ECAPA (512-dim); accuracy beyond our desk check unknown | open models measured on Russian (CAM++, ERes2Net; speaker-id.md); the firmware's could run there too |
| Gender | built in (the firmware's classifier) | needs a model of its own (or the firmware's classifier on the server) |
| Which satellites | only speakers running the CFW (Mini 3 0.369, Mini 3 Pro, Station 3; the old Mini 3 firmware with moved files) | any HA satellite, ESPHome and ReSpeaker included |
| What the owner needs | nothing but the option | a server with the proxy (a container or an HA add-on) between HA and its STT; few CFW users have one |
| Load | the speaker's CPU; on a Mini 3 to be measured | the server's, negligible |
| Arbitration | to handle (the winner's verdict counts) | not a concern: only the winner streams |
| Calibration | the Voices tab on the speakers, from normal use | a page or a script on the server; the same data, collected there |
| Where voiceprints live | on the speakers | on the server |

**For CFW users in general** the speaker is the better home: nothing to install, gender for free,
and no added latency — if HA handles an aborted run cleanly and the wake word separates voices well
enough. **For a household with a voice server** the proxy is more accurate (bigger models, the whole
phrase) and blocks a guest more cleanly. They do not exclude each other: the HA entities can be the
same with either behind them, and a speaker could even send its wake-word voiceprint along for the
server to combine.

## Calibration and debugging, for everyone

Nobody should need a week of HA debug recordings. The speaker learns from its normal use:

- **Recent requests.** Each speaker keeps its last ~100 requests: time, HA's transcript (the
  satellite receives it), the voiceprint, the verdict, the score and the gender — not the audio by
  default (a "keep audio for 10 minutes" switch lets you listen while labelling). The Voices tab
  lists them: who it thought it was, how sure.
- **Labelling.** Pick requests and say whose they are ("me", "<partner>"), or "not a person" (TV,
  videos, noise). A person's centroid is the mean of their labelled voiceprints; the tab shows how
  many there are and how tight they sit. Enough: ~10 requests per person, from more than one room.
- **Suggestions.** The tab groups unlabelled requests by similarity: "a voice heard 23 times, all
  alike — who is it?" — the first day of use is usually enough to name the household.
- **Guided enrollment**, for a person who rarely talks to the speaker: "Enroll a voice" asks for
  five short phrases on that speaker ("Алиса, сколько времени?") and labels them.
- **The threshold with a preview.** Next to the slider: the score histogram of the recent
  requests per person and for the rest, and "with this threshold, of the last 100 requests N would
  have been ignored: [list]". Calibration is moving the slider until the list is the guests.
- **One household, every speaker.** The voices (names and centroids, not recordings) are shared
  between the speakers with the same model: a label made in one room is used in all. Stored in
  `<cfw>/voices.json` on each; the web app keeps them in step (the peers' API), the model tag guards
  against mixing models.
- **Debug:** the sensors' attributes (score, runner-up, margin), a log line per verdict, and the
  Voices tab show every decision with its numbers; `GET /api/voices/recent` for scripts. A desk
  tool (`tools/voice-lab/`, the fbank + TFLite pipeline in Python) replays wav files through the same
  models to tune on recordings.

## Privacy

Voiceprints are biometric data. They stay on the household's own speakers: `<cfw>/voices.json`
holds names and centroids; recent requests live in RAM (and their voiceprints only, no audio,
unless the 10-minute switch is on); nothing leaves the LAN, nothing is committed. "Forget this
voice" and "forget everything" in the tab delete them on every speaker.

## Turning it on (for others)

- **Requirements:** the firmware's `bio_lingware/` and `libtflite.so` on the speaker — Mini 3
  0.369, Mini 3 Pro, Station 3 have both; the Mini 3 0.6.x firmware needs the newer model and the
  library from another firmware (`make voice-models`, the way `vqe-models` works: copied from the
  owner's own unit or vendor tree, never from the repo). The web app says what is missing.
- **Config:** `voice_id = true` (off by default). Gender works at once; names after labelling.
- **Nothing on the HA side** besides the new entities; the agent's prompt gets a line if wanted.

## Phases

1. **spotter-host voiceprints:** fbank in C, TFLite via `dlopen`, `VOICE` events (final; partials).
   Checked against the desk pipeline on the same wav (same voiceprint within float error).
2. **yastationd:** the voices store, the verdict, the HA sensors (gender first: no enrollment), the
   recent requests, the API.
3. **Voices tab:** recent requests, labelling, suggestions, the threshold preview, guided enrollment.
4. **Ignore guests:** abort on a guest verdict (or the hold-and-release fallback),
   `voice_ignored`, arbitration.
5. **Sync** between speakers; `make voice-models` for the old Mini 3 firmware.

## Open questions

- CPU on the Mini 3 (A53, armhf): `body` every 0.48 s while listening, `head` at the end. Measure
  in phase 1; if it is heavy, run `body` only on the held audio and at the end.
- What HA does with a run the satellite aborts mid-speech: drop it, or act on the part it already
  transcribed. Decides between the zero-latency abort and hold and release.
- How well the wake word alone («Алиса», ~0.6 s) separates voices, and two similar voices (two men
  of one household) in particular: needs voiceprints collected on a unit, since HA's recordings
  start after the wake word.
