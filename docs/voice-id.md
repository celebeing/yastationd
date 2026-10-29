# Voice ID on the speaker: who is talking, guests ignored, the speaker's gender in HA

**Status (2026-10-29):** built: the voiceprints on the speaker, the verdict, HA's sensors, ignoring
guests with the `voice_guest` moment, the store with the album and the People tab, `make
voice-models`. Checked: the voiceprints against the desk pipeline (the same values) and their cost
on a Mini 3 and a Station 3; the album and the protocol in Go tests. Not yet checked on a live
household: how well «Алиса» plus the start of a request tells two voices apart, and how the
grouping copes with real data. The research behind it, the comparison with a server proxy and the
vendors' approach follow below.

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

**In HA, per speaker** (MQTT discovery, with `voice_id = true`; updated as soon as a request has
been heard, before HA acts on it, and again on the whole request):

- `sensor.<speaker>_voice` — the person of the household (their name, or their group's id when
  unnamed), `guest`, or `unknown` (too little speech, or nobody marked as the household yet).
  Attributes: `score` (cosine to the closest person), `closest`, `second` /
  `second_name` (the runner-up), `gender`, `female` (the classifier's output), `age`, `secs`,
  `stage` (`early`: «Алиса» and the start of the request; `final`: the whole request), `model`.
- `sensor.<speaker>_voice_gender` — `male` / `female` / `unknown`, needs **no enrollment** (the
  firmware's classifier; `female` ≥ 0.65, `male` ≤ 0.40, between them unknown: non-speech sits
  near 0.5). In a household of two that alone says a lot.
- `binary_sensor.<speaker>_voice_known` — the household or not.
- `switch.<speaker>_ignore_guests` — the option below (shareable like the other options).

The LLM agent reads them through the prompt template (`speaker-id.md` §4 shows the shape);
automations use them directly.

**Ignore guests** (the switch; in the web app with the Voice settings): a request from a voice
that is not one of the household never reaches HA; the `voice_guest` moment shows that it was
heard and ignored on purpose (a moment like the others: the owner picks its picture; by default the
`angry` expression). It does nothing until someone is
marked as the household. The settings (Configuration → Voice → Voice ID):

| Key | Default | |
|---|---|---|
| `voice_id` | off | the feature; restart |
| `voice_store` | off | this speaker keeps the voices (one per household); restart; not shared |
| `voice_guest_threshold` | 0.66 | cosine a voice needs to be taken for a person (the firmware's own) |
| `voice_guest_margin` | 0.02 | how much the best match must beat the runner-up |
| `voice_hold_ms` | 800 | with guests ignored: how much of the request after «Алиса» the check waits for |
| `voice_unsure` | allow | with guests ignored: an unsure check (too little speech, a failed voiceprint) lets the request through, or `drop` |
| `voice_bio_dir` | (found) | the models: `<cfw>/vendor/bio_lingware`, else the firmware's |

**In the web app, the People tab** (below): the people, the log, the edits, the threshold preview.

## How it works

```
mic → spotter-host serve: VQE → wake word → yastationd ─┐ WAKE
                                     vqe_0 (fd 3) ──────┤ audio from 1.3 s before the wake point
                                                        ▼
            spotter-host voice (a second process): fbank → body every 0.48 s → head on request
                                                        │ voiceprint (512 values)
yastationd: gender/age (Go) → verdict against the household's centroids
   ├─ guests ignored: the request waits voice_hold_ms (held), then goes to HA or is dropped
   ├─ HA's sensors (early, then final)        └─ the event (voiceprint, transcript) → the store
```

1. **`spotter-host voice <bio_lingware>`** (`spotter-host/voiceprint.c`) is a process of its own,
   started by the daemon on first use: a crash in TFLite never touches the wake word. It reads
   commands on stdin (`a <n>` + 16 kHz mono PCM, `v <tag>`, `r`), computes the Kaldi fbank as
   `features_config.json` says, runs `body` as 20-frame blocks fill up (so the work is done while
   the person speaks) and `head` over the last 300 rows when asked, and prints `VOICE <tag> <secs>
   <512 values>`. TFLite is the firmware's `libtflite.so`, loaded with `dlopen`.
   - **The vendor's TFLite builds interpreters only through its own op resolver**:
     `TfLiteInterpreterCreate` registers no ops (it crashes on our models), so we call
     `TfLiteCustomOpResolverCreate("next")` and `TfLiteInterpreterCreateWithCustomResolver(model,
     resolver, options)` as `libyandex_evil.so` does (signatures read from the library's code; the
     tag is the frontend's `custom-opresolver-tag`). An empty tag is not registered.
   - **The gender and age classifiers can't run there**: the `next` resolver lacks their
     `LOGISTIC` op. They are two dense layers (int8 weights with one scale, ReLU, one output,
     sigmoid), so the daemon reads them from the `.tflite` files (a small FlatBuffers reader,
     `internal/voiceid/classifier.go`) and computes them; within 0.02 of TFLite's outputs.
   - Checked (2026-10-29): the same voiceprints as the desk pipeline (cosine 0.9998–1.0) on both
     arches; under qemu-user the armhf build crashes inside libtflite's CPU detection (an x86
     `/proc/cpuinfo`), on a real Mini 3 it runs. Cost: a Mini 3 takes 0.18 s of CPU for 2.9 s of
     speech (6 % of real time), a Station 3 0.08 s; loading the models 0.03–0.06 s.
2. **The voiceprint of a request** (`internal/app/voiceid.go`) starts at the wake word, before
   arbitration: the audio from 1.3 s before the wake point (all of «Алиса», whatever
   `wake_word_in_stream` sends to HA), then the request until the session ends. A session started
   by the voice key or a follow-up takes its voiceprint from its start. The speaker that loses the
   arbitration drops its voiceprint; the winner's counts.
3. **The early verdict** comes `voice_hold_ms` after the wake word: the voiceprint of «Алиса» and
   the start of the request against the household (`voiceid.Match`: the closest person's centroid
   at or above the threshold and beating the runner-up by the margin; else a guest; with nobody
   marked as the household, unknown).
   - **Guests ignored:** the request is held until then (`heldWake`, as for arbitration) and
     `run-pipeline` is not sent; a guest's request is dropped: HA sees nothing, nothing
     reconnects, `voice_guest` shows. Otherwise the held speech goes to HA at once and the rest
     streams live, so HA hears the end of the request no later than without the check (the check
     costs the voiceprint's ~0.1 s, not the hold).
   - **Not ignored:** the request goes on at once; the early verdict only updates the sensors.
4. **The final verdict** on the whole request (up to its last 7.2 s) when the session ends updates
   the sensors, and the event — the voiceprint, the verdict, the speaker, HA's transcript, whether
   it was ignored — goes to the store over MQTT (`<prefix>/voice/event`, not retained; spooled
   in memory, up to 200, while the broker is away).
5. **The household** comes from the store, retained (`<prefix>/voice/people`: the model tag, the
   store's id, the household's centroids). A message from another voiceprint model is not used.

### The store and the album

On the speaker with `voice_store` (`internal/app/voicestore.go`, `internal/voiceid/album.go`):

- **Every event is an entry** in `<cfw>/voices/log.jsonl` (appended; the newest 3000 kept, the
  oldest unpinned ones dropped first). Events from another voiceprint model are not filed.
- **The grouping**, in time order: an entry with almost no speech (under 0.9 s) or that HA heard
  no words in (no transcript, and not an ignored guest's) goes to "not a person"; any other joins
  the group whose centroid (the mean of its normalised voiceprints) is closest, if at least 0.6
  close, else starts its own. Then groups whose centroids are at least 0.6 close are joined, the
  closest pair first: going entry by entry, a voice whose first phrase was atypical starts two
  groups (seen 2026-10-30: a woman's first request scored 0.49 on gender and 0.33 to her second;
  her two halves were 0.64 apart, both ~0 from the other voice). Two groups the owner named or marked are never
  joined, and a group the owner pinned without naming (a request moved to "a new person") takes
  no part. A group's gender is its members' mean.
- **Each entry keeps the early verdict too** (`early`, `early_score`, `early_secs`), to see how
  often «Алиса» and the start of the request agree with the whole request.
- **The owner's edits are pins** (`<cfw>/voices/edits.json`): naming a group or marking it as
  the household pins its entries to it, merging pins the one group's entries to the other, moving an entry pins it (to a group, to
  "not a person", or to a new group). Regrouping places pinned entries first, so an edit is never
  undone, and later requests join such a group by similarity.
- **The household is a mark of its own, not the name** (since 2026-10-30): a named visitor stays a
  guest, and a household member may stay unnamed (the sensor then shows the group's id). The
  household's centroids are published (2 s after the last change, and on every connect); the
  other groups stay in the tab. Edits from before the mark: every named group became the
  household on load.
- **Forgetting** a person deletes the group's entries and its name; "forget everything" empties
  the log and the edits; both republish at once.

API (on every speaker; the album calls only on the store): `GET /api/voices` (`on`, `store`,
`store_id`, `people`, `model`, `threshold`, `ignore_guests`; on the store also `groups` and
`entries`), `GET /api/voices/log?group=&limit=`, `GET /api/voices/preview?threshold=` (the last
100 requests of real speech judged again at that threshold), `POST /api/voices/groups/<id>`
`{name}`, `DELETE /api/voices/groups/<id>`, `POST /api/voices/merge` `{from, into}`,
`POST /api/voices/entries/<id>` `{group}` (`noise`, `new` or a group), `DELETE /api/voices`.

### Arbitration

Every speaker that heard the wake word starts its voiceprint at once, without waiting for the
arbitration ([wake-arbitration.md](wake-arbitration.md)); the losers drop theirs, the winner's
verdict counts (it gates, publishes and files the event). The hold is counted from the wake
word, so the arbitration's wait overlaps it.

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
| Calibration | the People tab on the speakers, from normal use | a page or a script on the server; the same data, collected there |
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
   one: room on its eMMC, a faster CPU). The People tab lives there; the web app reaches every
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

- **Requirements:** the firmware's `bio_lingware/` with the 512-dim voiceprint model
  (`next_tdnn_1-3M_v1_thr66`) and `libtflite.so` — Mini 3 0.369, Mini 3 Pro and Station 3 have
  both. The Mini 3 0.6.x firmware has an older 256-dim model the daemon doesn't take: `make
  voice-models VOICE_MODELS=<a newer firmware's bio_lingware dir>` puts the newer one in
  `<cfw>/vendor/bio_lingware` (copied from a firmware you have, the way `vqe-models` works; never
  from the repo). The daemon logs what is missing.
- **Config:** `voice_id = true` on the speakers (shareable), `voice_store = true` on one of them.
  Gender works at once; people appear in the People tab after some use, to be named and marked as the household there. Then
  "Ignore guests", if wanted.
- **Nothing on the HA side** besides the new entities; the agent's prompt gets a line if wanted.

## Phases

1. ~~spotter-host voiceprints~~ (2026-10-29): a separate `voice` mode, not inside `serve`.
2. ~~yastationd: the verdict, the HA sensors.~~
3. ~~The store and the People tab~~: guided enrollment ("five phrases") is not built yet; naming a
   group that formed by itself does the job.
4. ~~Ignore guests~~: judged before `run-pipeline`, so HA's handling of an aborted run does not
   matter.
5. ~~`make voice-models`~~.
6. Next: a week of real use on the author's speakers — how the groups form, the scores of the
   household against guests on «Алиса» + 0.8 s, the threshold — and guided enrollment if the
   groups need it.

## Open questions

- ~~CPU on the Mini 3~~: 6 % of real time while someone speaks (measured, "How it works").
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
    hotword); the open question below decides whether «Алиса» is enough. **Built this way**, with
    `voice_hold_ms` of the request added to «Алиса» (0 = the wake word alone).
- How well the wake word alone («Алиса», ~0.6 s), or with 0.8 s of the request, separates voices,
  and two similar voices (two men of one household) in particular: the store now collects the
  early and the final verdicts from real use; compare them after a week.
