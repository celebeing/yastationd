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
   publishes the sensors, sends the request event to the store, and gates the request.
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

### A server proxy does more than one job

The proxy sits on the path HA → STT. Three things can live there at once:

- **Voice ID** for every satellite, ESPHome included, with no CFW changes.
- **One store.** The voiceprints, the log and the groups are on the server, so the sync problem of
  the album UX below is gone: there is nothing to sync between speakers.
- **STT routing and fallback.** The proxy picks the STT backend and falls back to a second one
  when the first is down, e.g. a GPU box first, then a CPU server. Otherwise that job needs a TCP
  failover proxy (HAProxy) of its own.

The cost is that the firmware's own models stay unused, except perhaps the gender classifier.

### Ready-made proxies (surveyed 2026-10-28)

Writing our own is not needed: several community projects put a speaker-ID proxy in front of a
Wyoming STT. All are from 2026 and young, so pin a commit and read the code before running it.

| Project | Stars / last push | What it has | Catches |
|---|---|---|---|
| [Murdock](https://github.com/BobMcGlobus/Murdock), a fork of [wyoming-voice-match](https://github.com/jxlarrea/wyoming-voice-match) (91 stars) | 2 / 2026-10-11 | The closest to the album UX: an "Unknown voices" tab with cluster review and assignment, a recognition log, a 2-D voice map, profile health and drift. Platt-scaled scores, per-satellite thresholds and same-mic profiles, auto-enrollment from confident matches. Wyoming and OpenAI-style STT upstreams with a fallback STT. MQTT-discovered sensors, ZIP backup | Blocks unknown voices by default (guests need it off). WeSpeaker CAM++, whose ONNX exports scored badly in our desk test ([speaker-id.md](speaker-id.md) §1); its Platt scaling may fix that. One global "current speaker" sensor. Browser recording does not work through HA ingress |
| [home-assistant-assist-speaker-id](https://github.com/witold-gren/home-assistant-assist-speaker-id) | 0 / 2026-10-25, 5 commits | Our pick of model (3D-Speaker CAM++ via sherpa-onnx). The speaker is tied to the pipeline run, not to a global sensor. An agent wrapper, requests run as the speaker's HA user. `speaker_id.calibrate` suggests thresholds. Voice-guided enrollment through a satellite | Days old. No view of unlabelled voices, no merge |
| [speaker-recognition](https://github.com/conorod1992/speaker-recognition), a fork of [EuleMitKeule's](https://github.com/EuleMitKeule/speaker-recognition) (56 stars) | 3 / 2026-10-25 | A sidebar panel in HA. A log of decisions you mark correct / wrong speaker / missed, and thresholds suggested from those marks. Per-turn correlation | Resemblyzer, an older and weaker model than CAM++ |
| [ha-addon-wyoming-speaker-id](https://github.com/olegchumakov/ha-addon-wyoming-speaker-id) | 0 / 2026-10-27 | An add-on, ReDimNet2 by default, a web UI for people | Prefixes the transcript (`[speaker: Anna] …`), which breaks local intents. Unsuitable |

The cheap next check needs no new voices: replay the HA recordings already collected through
Murdock and assist-speaker-id on a desk machine and compare with our desk check.

## How the big assistants do it

Alexa, Google, Siri and Alice all recognise voices, and all of them need an explicit enrollment.
Owners rarely notice because it is folded into setup:

| Assistant | Enrollment | Who a voice belongs to |
|---|---|---|
| Alexa (Voice ID) | "Alexa, learn my voice", or the Alexa app; a few phrases. Alexa may later ask to repeat commands on other Echo devices | the person's Amazon profile in the household |
| Google (Voice Match) | the Home app: a few phrases with "Hey Google"; up to six people per home | each person's Google account |
| Siri (HomePod, "Recognize My Voice") | a switch in the Home app; as far as we know it reuses the "Hey Siri" training from that person's iPhone; up to six people | each person's Apple ID |
| Alice (Yandex) | since 2019 "Алиса, запомни мой голос" and a few phrases repeated after her; now in the app ("Мой голос"), each family member with their own Yandex account, per station | the account. Early on only the owner's voice; others counted as guests |

What they share:

- **The identity is the account, so there is no naming step.** A person enrolls from their own
  phone and account; the assistant never asks "who is this?" afterwards.
- **No grouping of strangers.** Unenrolled voices are not collected or clustered; they are guests.
  Biometric privacy laws (Illinois' BIPA has been used against voice assistants) make collecting
  voiceprints without consent a legal risk, which is a reason vendors don't build an album.
- **Calibration is the vendor's.** Thresholds are tuned on fleet data and shipped fixed; owners
  see no slider. When unsure, the assistant falls back to the household default or asks; HomePod
  sends sensitive requests to the iPhone for confirmation, and Alexa purchases use a voice code.
  Voice alone never authorises anything security-relevant.
- **Some features need no enrollment:** a child's voice switching Alice into the kids' mode
  (reported by users, not documented in Yandex' help) is a classifier like the firmware's age model.

The vendors' way next to the album design above (a comparison; no choice made yet):

| | Enrollment first (the vendors) | The album (this design) |
|---|---|---|
| Who acts | each person, once, five phrases | the owner, naming groups that appeared on their own |
| Voices that are not enrolled | not collected; guests | collected and grouped, shown for naming or "not a person" |
| What it needs from us | a guided flow ("Алиса, запомни мой голос" or the web app), HA persons standing in for accounts | the grouping, the log, rename / merge / correct, the store |
| Privacy | only voiceprints people agreed to give | voiceprints of everyone who speaks, guests included, kept in the household |
| Rare speakers | have to enroll on purpose | appear once they have spoken a few times |
| Thresholds | fixed by the vendor from fleet data | set by the owner with the preview, from their own data |

The two can be combined, e.g. guided enrollment feeding a group of the album.

## Calibration and debugging: voices like faces in a photo album

Nobody should need a week of HA debug recordings, and nobody should label requests one by one.
The speakers collect voiceprints from normal use and group them the way photo albums group faces:

- **People appear by themselves.** Every request adds a voiceprint; similar ones are grouped
  automatically and shown as "Person 1 · male · 87 phrases", "Person 2 · female · 4 phrases" (the
  gender is the group's average from the firmware's classifier). A separate "not a person" group
  takes false wakes, noise and empty transcripts (in the desk check they stand out: gender ≈ 0.5,
  low similarity to everyone).
- **A log of requests:** the time, the speaker it came from, HA's transcript (the satellite
  receives it), who it was taken for and how sure. No audio by default (a "keep audio for 10
  minutes" switch lets you listen while correcting).
- **The owner's edits:** rename a group ("Person 1" → "me"), merge two groups, and mark a request
  in the log as misrecognised (move it to another person, or to "not a person"). Edits are
  constraints for the grouping, as tagged faces are in an album: later requests are grouped around
  them, and regrouping never undoes them.
- **Guided enrollment**, for someone who rarely talks to the speakers: "Enroll a voice" asks for
  five short phrases and puts them in one group.
- **The threshold with a preview**, on top of the groups: the score histogram per person and for
  the rest, and "with this threshold, of the last 100 requests N would have been ignored: [list]".
  Calibration is moving the slider until the list is the guests.
- **Debug:** the sensors' attributes (score, runner-up, margin), a log line per verdict, the log
  in the web app, `GET /api/voices/log` for scripts, and a desk tool (`tools/voice-lab/`, the fbank +
  TFLite pipeline in Python) that replays wav files through the same models.

### One store, two one-way streams

Syncing all of that between speakers would be a mess; it is not needed. The speakers already share
an MQTT broker, and two one-way streams are enough:

```
every speaker ── a request event ──────────► the store (one place)
  (voiceprint ~2 KB, transcript, speaker, time, verdict)
                                                │ grouping, names, the owner's edits
the store ── the people's centroids (retained) ─► every speaker
  (name, gender, 512 values per person: a few KB)
```

- **The verdict stays local and fast:** each speaker classifies against the centroids it holds
  from the retained topic; no network round trip in the middle of a phrase.
- **The log, the groups, the names and the edits live in the store only.** Speakers append events
  and read centroids; only the store edits, so nothing can conflict.
- **Small:** a couple of KB per request, tens of requests a day.
- **While the store is unreachable** speakers keep their events and send them later (as
  `log-spool` does for logs), and keep classifying with the last centroids they got.

Where the store lives:

1. **One of the speakers, chosen** (`voice_store = true` in its config; a Station 3 if there is
   one: room on its eMMC, a faster CPU). The Voices tab lives there; the web app reaches every
   speaker, so it shows from any. While that speaker is off, the log waits on the others and edits
   wait. **The pick:** nothing to install besides the CFW.
2. **HA**, through an integration of our own that keeps the log and the groups and shows them in
   HA. A component outside the CFW to install and maintain.
3. **A server**: the proxy of [speaker-id.md](speaker-id.md); then computing there is natural too,
   and a speaker would send only its wake-word voiceprint.

## Privacy

Voiceprints are biometric data. They stay in the household: the store keeps the log (voiceprints,
transcripts, no audio unless the 10-minute switch is on) and the groups on its flash, the other
speakers only the people's centroids; the broker carries them inside the LAN; nothing is
committed. "Forget this person" and "forget everything" in the tab delete them in the store, and
the retained centroids with them.

## Turning it on (for others)

- **Requirements:** the firmware's `bio_lingware/` and `libtflite.so` on the speaker — Mini 3
  0.369, Mini 3 Pro, Station 3 have both; the Mini 3 0.6.x firmware needs the newer model and the
  library from another firmware (`make voice-models`, the way `vqe-models` works: copied from the
  owner's own unit or vendor tree, never from the repo). The web app says what is missing.
- **Config:** `voice_id = true` on the speakers (off by default), `voice_store = true` on one of
  them. Gender works at once; people appear in the Voices tab after a day or so of use, to be named.
- **Nothing on the HA side** besides the new entities; the agent's prompt gets a line if wanted.

## Phases

1. **spotter-host voiceprints:** fbank in C, TFLite via `dlopen`, `VOICE` events (final; partials).
   Checked against the desk pipeline on the same wav (same voiceprint within float error).
2. **yastationd:** the verdict against centroids, the HA sensors (gender first: no enrollment).
3. **The store and the Voices tab:** request events and retained centroids over MQTT, automatic
   grouping, the log, rename / merge / correct, the threshold preview, guided enrollment.
4. **Ignore guests:** abort on a guest verdict (or the hold-and-release fallback),
   `voice_ignored`, arbitration.
5. `make voice-models` for the old Mini 3 firmware.

## Open questions

- CPU on the Mini 3 (A53, armhf): `body` every 0.48 s while listening, `head` at the end. Measure
  in phase 1; if it is heavy, run `body` only on the held audio and at the end.
- ~~What HA does with a run the satellite aborts mid-speech.~~ Read in HA 2026.9's code
  (`wyoming/assist_satellite.py`, `assist_satellite/entity.py`, `assist_pipeline/pipeline.py`),
  not yet confirmed on a live run:
  - **Dropping the connection while audio streams is a clean cancel.** The read loop sees the
    socket close, ends the audio queue and at once calls `cancel()` on the pipeline task, before
    the STT stage gets to run again. The STT call gets `CancelledError`, so there is no
    transcript and no intent. HA reconnects ~3 s later.
  - **`audio-stop` is not a cancel:** HA ends the stream, the STT transcribes the part it got,
    and the intent runs on it.
  - **Too late once the speech has ended:** if HA's own VAD has closed the STT stage before our
    verdict, the intent is running, and cancelling does not undo actions
    ([voice-slow-replies.md](voice-slow-replies.md)).
  - **Simplest of all: judge on the wake word alone and never send `run-pipeline` for a guest.**
    HA sees nothing and nothing reconnects. This is how the phone assistants verify (on the
    hotword); the open question below decides whether «Алиса» is enough.
- How well the wake word alone («Алиса», ~0.6 s) separates voices, and two similar voices (two men
  of one household) in particular: needs voiceprints collected on a unit, since HA's recordings
  start after the wake word.
