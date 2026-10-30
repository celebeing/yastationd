# Wake word: «Алиса» and the local commands

What is built for the wake word, what was decided and why, and what is still not measured. The
wake word runs on each speaker's own vendor front-end (echo canceller, noise reduction, spotter
models) inside a child process; HA gets the request audio after it. Where «Алиса» ends in that
audio: [wake-word-boundary.md](wake-word-boundary.md). Which speaker answers when several hear it:
[wake-arbitration.md](wake-arbitration.md). The evidence behind this page is in `research/`
(linked per section); a fact below that the code does not show is marked **(not in code)**.

## What is built

**The child.** `spotter-host serve` (`spotter-host/spotter_host.c`, built per userland arch)
`dlopen`s the firmware's `libyandex_evil.so` and runs one VQE (the vendor's Kalman AEC against the
speaker reference, plus the background noise reducer) and two spotters, each in its own thread
behind a queue so the VQE loop never waits for one: the **activation** spotter («Алиса») and the
**command** spotter. The activation model takes three inputs, `0,3,4` = `vqe_0` (omni), the BNR
input, the raw omni mic; the command model takes `vqe_0` alone. In production the BNR input is fed
**`vqe_bnr`**: the daemon never passes `-b` (`vqe_0` into the BNR input), nor `-g` (`vqe_gain`
into the command model). Both flags remain for `listen` A/B tests.

**The contract** (also the boundary for any replacement engine, [porting-a-speaker.md](porting-a-speaker.md) §4):

| Channel | Content |
|---|---|
| args | `serve -P <wake.vqe_preset> -N <mic.channels> -M <wake.mics> (-R <wake.ref> \| -Q <wake.refs>) [-m <vqe models>] -a <wake model> [-k <command model>]` (`newSpotter`, `internal/app/wakeword.go`) |
| stdin | raw interleaved S16LE capture frames, as `arecord` gives them (48 kHz; orion 96 kHz) |
| stdout | `READY call-mode`; `AT <ms> <sample>` right before a `WAKE`/`CMD` (its position in fd 3's stream); `WAKE <ms> <phrase>`; `CMD <ms> <phrase>`; `SUBHIT WAKE\|CMD <ms> <phrase>`; `MODE <n> ok\|err`; `LEVEL …` only with `-v`. Unknown lines are ignored |
| fd 3 | `vqe_0`, 16 kHz mono S16LE, non-blocking (dropped, and accounted for in `AT`, when the daemon lags) |
| fd 4 | `vqe_gain`: the intercom's uplink in the VQE call mode (SIGUSR1 on, SIGUSR2 off; [intercom.md](intercom.md)) |

`WAKE` is the spotter's `CONFIRMATION_1ST_STAGE`, or `CONFIRMATION_2ND_STAGE` for a model with a
verifier (orion: `1ST_STAGE_HIT`, then the verifier's accept; a `REJECTION_2ND_STAGE` never
becomes a `WAKE`). `SUBHIT` is the spotter's sub-threshold peak (score above `subthresholds`, below
`thresholds`).

**Supervision** (`internal/wake`). The daemon starts the child when `wake_enabled` is on, the
binary exists and the board profile has `wake.vqe_preset/model/mics`; otherwise it logs why and
runs without it (push-to-talk on the raw mic channel). One `arecord` feeds it; the mic stays open
while the wake word is on. Input goes through a 256-block (~5 s) queue that drops rather than
stalls. A child that exits is restarted with backoff (1 s doubling up to about a minute, back to
1 s after a run longer than a minute). The daemon keeps the last 4 s of `vqe_0` in a ring, so a
session can start its audio at the WAKE's sample even though the line arrives late. The child's
stderr (model load errors, "spotter lagging") is **not collected**: `cmd.Stderr` is unset, so it
goes nowhere; run `spotter-host listen` by hand to see it.

**What the events do** (`onWakeEvent`, `onCommand`):

| Event | Effect |
|---|---|
| `WAKE` | saved by `wake_capture` if on; then: mic muted → ignored; intercom ringing or in a call → a short command window, nothing goes to HA; the speaker's own playback (TTS, local sounds) or a ringing alarm → **ignored** (Music Assistant music does not count, the wake word works over it); a reply pending → `wake_while_waiting`; else arbitration if on, else the session: the `wake_word` button event, a Wyoming `detection`, voice ID's gate ([voice-id.md](voice-id.md)), and `vqe_0` from the WAKE's sample less `preroll_ms` |
| `CMD` | the intercom's commands first (answer/decline/hang up). «стоп», «хватит», «отмена», «выключись» end a listening session, or cancel a pending reply. Otherwise **only while the speaker makes sound** (`speaking()`: our own playback or the alarm): «стоп»/«хватит»/«отмена»/«выключись»/«пауза» stop the playback and the alarm and drop the follow-up, «громче»/«тише» move the volume two steps; anything else is logged. Music Assistant music is not `speaking()`, so commands do nothing over it |
| `SUBHIT WAKE` | logged at `debug` ("wake: near miss"); kept as the last near miss, which `wake_capture` records with a WAKE that follows within 3 s. Not published to HA |
| `SUBHIT CMD` | logged at `debug` only |

Commands are armed this narrowly on purpose: the command model's stock thresholds were tuned for
the vendor's context-gated use (some are near zero), so always-on they would false-accept
household talk ([research/voice-phase2-plan.md](research/voice-phase2-plan.md) §4,
[research/stt-decompile.md](research/stt-decompile.md)).

**Per board** (`internal/board/profiles/*.conf`; details in the board docs):

| Board | VQE preset, capture | Activation model | Command model | `wake.preroll_ms` |
|---|---|---|---|---|
| [bergamot](boards/bergamot.md) | `bergamot`, `hw:0,1` 8 ch 48 kHz, mics 0,1,2, ref 6 | the downloaded `bergamot-alisa-13Nov-x3-bnr-…` under `/data/quasar/data/spotter_model/<uuid>` (the uuid differs per unit, see below) | the downloaded `quasar-commands-10Feb25…` | 150 |
| [pickle](boards/pickle.md) | `pickle`, `hw:0,2` 4 ch 48 kHz, mics 0,1,2, ref 3 | built-in `spotter_models/activation/alisa` | built-in `spotter_models/command` | none (CFW default 300) |
| [orion](boards/orion.md) | `orion`, `mroute` 13 ch 96 kHz, mics 0–5, refs 6–12 | built-in `activation/alisa` (`ru-RU-cucumber-alisa-02Aug24-bnr-verifier-no-ca`), with a **second-stage verifier on the NPU** (`libnnsdk.so`, `/dev/galcore`) | built-in `command` | 220 |

All three activation models use layout `0,3,4`. Only orion's has the verifier; bergamot and pickle
confirm at the first stage. The vendor libraries come from `/system/vendor/quasar/libs` on all
three. The bergamot profile pins a model directory by its uuid, and the profile's own comment says
the ids differ per unit **(not checked across units here)**: a bergamot whose directory differs
needs `wake_model`.

## Settings and profile keys

| Key | Where | What it does |
|---|---|---|
| `wake_enabled` | config (voice, synced) | runs the child at all; off = voice key only. Restart key |
| `wake_while_waiting` | config (runtime, synced) | a WAKE while HA's reply is pending: `interrupt` (default) or `ignore` (blinks busy) |
| `preroll_ms` | config (runtime, advanced) | where HA's audio starts, back from the WAKE's sample; 0/empty = the board's `wake.preroll_ms`, else 300 |
| `wake_word_in_stream` | config (runtime, synced) | start 1.3 s before the WAKE instead, keeping «Алиса» in HA's audio |
| `wake_capture` | config (runtime, synced, advanced) | saves 3.5 s of `vqe_0` around each WAKE (+ timing JSON, the near miss before it) under `<cfw>/wakes`, newest 200 |
| `arbitration`, `arbitration_wait_ms` | config (synced) | the loudest speaker answers ([wake-arbitration.md](wake-arbitration.md)) |
| `wake_model`, `command_model`, `spotter_libs` | config (advanced, per speaker) | override the profile's model dirs and library dir. Restart keys |
| `mic_pdm_gain` | config (advanced, per speaker) | overrides the profile's `audio.mic_gain_value` (the PDM gain control, set once at start; -1 leaves it) |
| `spotter_bin`, `vqe_models` | config (advanced) | the host binary; the VQE's model dir (the call mode's noise reducer) |
| `wake.vqe_preset`, `wake.mics`, `wake.ref` / `wake.refs` | profile | the VQE preset and which capture channels are its mics and speaker references, in preset order |
| `wake.model`, `wake.command_model`, `wake.libs` | profile | the model dirs and vendor library dir on the device |
| `wake.preroll_ms` | profile | the per-model `preroll_ms` default, measured with `wake_capture` |
| `mic.device/channels/rate`, `audio.mic_gain(_value)` | profile | the capture the child gets, and the mic gain |

## Sensitivity and false accepts: the levers as they stand

**There is no sensitivity setting** in the web app, HA or the config. The thresholds are in each
model directory's `flags.txt` (`thresholds`, `subthresholds`; e.g. bergamot's active model 0.928,
pickle's built-in 0.81 / 0.405, [research/spotter-decompile.md](research/spotter-decompile.md) §5,
§9; [boards/pickle.md](boards/pickle.md)). What can be done today:

- **Re-threshold a copy.** Copy the model directory under `<cfw>` (check the free space first:
  orion's verifier `model.nb` alone is 16.9 MB; [storage.md](storage.md) has each board's room), edit `thresholds` in its
  `flags.txt`, and point `wake_model` (or `command_model`) at the copy's absolute path, then restart.
  The code supports this path: the key replaces `wake.model`, which goes to `-a` as is, and the
  library reads the directory's `flags.txt` (`spotter_conf_read`). **Not yet done on any unit**, so
  untested; for orion, whether a copied directory still finds its verifier (`verifier-model`,
  `model.nb`) is unknown. Never edit the firmware's own directory.
- **Mic gain** (`mic_pdm_gain`). All three profiles set 24; porting targets about −30..−45 dBFS RMS
  speech at 1 m. The activation model barely cares about level; the command model does
  ([../spotter-host/README.md](../spotter-host/README.md), on-device results).
- **Watch near misses:** `log_level = debug` shows every `SUBHIT`; many of them in normal room audio
  are the first sign of false accepts, and a `SUBHIT` just before a missed «Алиса» means the
  threshold is too strict for that voice or distance.
- **Collect data:** `wake_capture` saves the processed audio around each WAKE (what was accepted,
  true or false). It records what is said in the room: off in normal use.
- **Arbitration** and **voice ID** cut the cost of a false accept (one speaker answers; guests can
  be ignored), not its rate.

## Decision record: the vendor spotter, not microWakeWord

**Decided:** the wake word is the vendor's own «Алиса» spotter and VQE, run from our host
("Path A"), on every board.

**What research recommended first:** microWakeWord (via Wyoming, on an open AEC3 front-end) as the
"sovereign default", with Path A only as a time-boxed A/B
([research/spotter-decompile.md](research/spotter-decompile.md) §0, §9;
[research/wakeword-mic-array.md](research/wakeword-mic-array.md) §5.2;
[research/hass-integration.md](research/hass-integration.md), historical).

**Why it went the other way:**
- Path A turned out cheap and contained: the library exports a full C API, and our host drives it
  without `maind`, the vendor stack staying gated ([../spotter-host/README.md](../spotter-host/README.md)).
- The models and the VQE were trained and tuned for this hardware, on far more real far-field data
  than a TTS-trained microWakeWord model would see (spotter-decompile §0); the same front-end gives
  echo cancellation against the speaker's own reference for free, where the open path needed AEC3
  cross-built for each userland ([research/voice-phase2-plan.md](research/voice-phase2-plan.md) §2.3).
- Measured on real voice: bergamot «Алиса» **10/10 at 1 m and 3 m**, 0 false confirmations in the
  test (host default mode, BNR ← `vqe_bnr`, PDM gain 24; [boards/bergamot.md](boards/bergamot.md));
  orion **14/14** in a 60 s live test with one verifier rejection and no false wakes in the silent
  half ([boards/orion.md](boards/orion.md)); pickle «Алиса, сколько времени?» recognised and
  answered ([boards/pickle.md](boards/pickle.md), no count). A 12-minute quiet-room run on bergamot
  gave 0 events. The full pipeline costs about 90 % of one core and 20 MB on bergamot.
- The offline command spotter («стоп», «громче», …) comes with it from the same engine.
- No A/B against microWakeWord was run: the vendor path worked first and well enough.

**What it costs:** a proprietary blob stays on the device and in the loop (loaded only in the
child; never copied into the repo). Only the words the vendor shipped («Алиса»; the firmware also
has «Яндекс» and «yasmina» models, not wired). The stock thresholds assume stages we don't run
(counteractivation, the cloud's online re-validation; orion keeps its on-device verifier), so
false accepts per hour will be higher than stock (spotter-decompile §6.5). The engine and the
thresholds can only be tuned through `flags.txt`, not retrained.

**When to revisit:** if false accepts prove too high and re-thresholding a copy does not fix them;
for a custom wake word; for a board whose firmware lacks a usable front-end; or to drop the blob.
Another engine needs only a host speaking the serve contract above (`spotter_bin` points at it),
the whole of it: the VQE's streams too (fd 3 `vqe_0` for the requests sent to HA, fd 4 the
intercom's uplink), not only the `WAKE`/`CMD` lines, or requests and calls lose their echo
cancellation; the daemon does not change ([porting-a-speaker.md](porting-a-speaker.md) §4, "Contract").

## Not measured / open

- **Far field (5 m)**, and recall over music and over our own TTS: the protocol is in
  [research/voice-phase2-plan.md](research/voice-phase2-plan.md) §6–7 and
  [research/spotter-decompile.md](research/spotter-decompile.md) §7.3; only 1 m / 3 m were counted.
- **False accepts per hour**: no soak on household audio (TV, talk) on any board (voice-phase2-plan
  §7 item 4); the quiet-room run is weak evidence.
- **Threshold sweep** (0.6–0.95 against ≥ 200 utterances, spotter-decompile §7.3): not done.
- **`vqe_bnr` with real speech** across distances: the synthetic test failed on it for an artefact
  ([../spotter-host/README.md](../spotter-host/README.md)); real voice at 1/3 m worked (above), the
  `-b` and no-VQE variants were never compared on real voice (voice-phase2-plan §7 item 2).
- **Wake during our own playback**: ignored today (the code's reason: AEC unverified); the AEC has
  since been measured ([intercom.md](intercom.md)), so the rule is up for review.
- **Commands during Music Assistant music**: not armed (see the events table).
- Per-board `wake.preroll_ms` for pickle, and more voices for bergamot and orion
  ([wake-word-boundary.md](wake-word-boundary.md), "Validation plan").

## See also

[wake-word-boundary.md](wake-word-boundary.md) (where «Алиса» ends, `AT`, `wake_capture`) ·
[wake-arbitration.md](wake-arbitration.md) · [voice-id.md](voice-id.md) ·
[intercom.md](intercom.md) (the call mode, «Алиса» in a call) ·
[porting-a-speaker.md](porting-a-speaker.md) §4 · [../spotter-host/README.md](../spotter-host/README.md) ·
research: [spotter-decompile.md](research/spotter-decompile.md),
[voice-phase2-plan.md](research/voice-phase2-plan.md),
[wakeword-mic-array.md](research/wakeword-mic-array.md),
[vqe-decompile.md](research/vqe-decompile.md), [stt-decompile.md](research/stt-decompile.md)
(the command model) · boards: [bergamot](boards/bergamot.md), [pickle](boards/pickle.md),
[orion](boards/orion.md)
