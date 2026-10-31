# Voice ID on the speaker: who is talking, guests ignored, the speaker's gender in HA

**Status (2026-10-29):** built: the voiceprints on the speaker, the verdict, HA's sensors, ignoring
guests with the `voice_guest` moment, the store with the album and the People tab, `make
voice-models`. Checked: the voiceprints against the desk pipeline (the same values) and their cost
on a Mini 3 and a Station 3; the album and the protocol in Go tests. Not yet checked on a live
household: how well «Алиса» plus the start of a request tells two voices apart, and how the
grouping copes with real data. The firmware's models and the desk check are below; why on the
speaker and not on a server, the ready-made proxies and how the big assistants enroll voices are in
[research/voice-id-alternatives.md](research/voice-id-alternatives.md).

The speakers' firmware ships a voice biometry stack of its own (`/system/vendor/quasar/bio_lingware/`),
run by the vendor assistant (`maind`), which the CFW keeps off. Its models are ordinary TFLite files
that run outside `maind`. This document is about running them in the CFW: on the speaker, with no
server and no cloud. The server-side alternative (a Wyoming STT proxy with an open model) is
[speaker-id.md](research/speaker-id.md); the two can coexist.

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

The LLM agent learns who is talking from one templated line in its prompt, which finds the
sensor of the speaker the request came from (`llm_context.device_id`;
[ha-assistant-setup.md](guides/ha-assistant-setup.md), "Telling the agent who is talking"); automations use
the sensors directly.

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

**In the web app, the People tab** (below): the people, the log, the edits, and a Guests card with
Ignore guests and the guest threshold themselves (the same settings, each with its share switch):
dragging the threshold previews what it does to the last requests, letting go saves it.

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

Calibrating and debugging voice ID should need neither a week of HA debug recordings nor labelling
requests one by one: the speakers collect voiceprints from normal use and group them the way a
photo album groups faces, and the owner names the groups and marks the household in the People
tab (the log with HA's transcripts, merge, move a misheard request, forget, the threshold with a
preview of what it does to the last requests).

On the speaker with `voice_store` (`internal/app/voicestore.go`, `internal/voiceid/album.go`):

- **Every event is an entry** in `<cfw>/voices/log.jsonl` (appended, ~3 KB each; how much stays:
  "How much is kept" below). Events from another voiceprint model are not filed.
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

- **How much is kept:** per group, so a voice heard all the time can't push out one heard rarely.
  A trim cuts each group to its level, oldest entries first: 1000 for a named or household group,
  100 for an unnamed one, 100 for "not a person"; of the unnamed groups only the 20 last heard
  stay. It starts when 1500 entries have piled up over those levels, counted across all the
  groups, because a rewrite costs the whole file: one small group of its own would rewrite it for
  a few entries. Limits are constants (`internal/voiceid/album.go`). The numbers:
  - the flash sees **1/(1−k)** of the bytes appended, k being the share of the log a trim leaves,
    **whatever the limits are**: 10× for k = 0.9, 5× for 0.8, 2× for 0.5, 1.5× for 0.33. With a
    guaranteed history M the peak is M/k and the wear 1/(1−k): k = 0.5 minimises their sum, and
    going lower saves little. The levels and the trigger here give k ≈ 0.5.
  - a household with one main voice: 1000-2500 of that voice's entries, 4-9 MB, a rewrite every
    1500 requests; at 40 requests a day ~0.2 MB a day on flash, a trim every 5-6 weeks.
  - the worst case (several busy voices, 20 full unnamed groups) stays around 7500 entries, ~22 MB.
  A trim pins the surviving entries of named and marked groups: such a group lives by its pins
  and new requests aren't pinned, so the window would otherwise slide past the last pin and take
  the name with it. An entry moved by hand has no extra protection: it goes with its group's age.
  Before this a full log (3000 entries) was rewritten whole on every request, ~9 MB each, and with
  nearly all entries pinned a new request dropped out of the log at once.
- **Why one store and two one-way streams:** syncing a log, groups and edits between speakers
  would be a mess, and it is not needed. Every speaker sends its events to the store and reads the
  household's centroids back (retained); only the store edits, so nothing conflicts. The verdict
  stays local (no network round trip in the middle of a phrase), the traffic is a couple of KB per
  request. While the broker or the store is away a speaker keeps its events in memory (up to 200,
  the store's presence read from its availability topic and retained `<prefix>/voice/store`), and
  classifies with the last centroids it got. A store with an empty album publishes no household
  until it has one, or until "forget" empties it: a new store, or a config restored without
  `<cfw>/voices/`, doesn't clear what the other speakers hold. Where else the store could have lived (HA, a server):
  [research/voice-id-alternatives.md](research/voice-id-alternatives.md).
- **Not built yet from the album's design:** guided enrollment ("Enroll a voice", five short
  phrases into one group) for someone who rarely talks to the speakers; a switch that keeps the
  requests' audio for ten minutes to listen while correcting; a desk tool that replays wav files
  through the same models (the desk check's scripts were not committed).

API (on every speaker; the album calls only on the store): `GET /api/voices` (`on`, `store`,
`store_id`, `people`, `model`, `threshold`, `ignore_guests`; on the store also `groups` and
`entries`), `GET /api/voices/log?group=&limit=&before=` (`before`: an entry's id, the page after it), `GET /api/voices/preview?threshold=` (the last
100 requests of real speech judged again at that threshold), `POST /api/voices/groups/<id>`
`{name}`, `DELETE /api/voices/groups/<id>`, `POST /api/voices/merge` `{from, into}`,
`POST /api/voices/entries/<id>` `{group}` (`noise`, `new` or a group), `DELETE /api/voices`.

### Arbitration

Every speaker that heard the wake word starts its voiceprint at once, without waiting for the
arbitration ([wake-arbitration.md](wake-arbitration.md)); the losers drop theirs, the winner's
verdict counts (it gates, publishes and files the event). The hold is counted from the wake
word, so the arbitration's wait overlaps it.

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
