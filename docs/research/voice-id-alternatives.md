# Voice ID: the alternatives we weighed

Research behind [../voice-id.md](../voice-id.md) (2026-10-28): doing voice ID on the speaker or on
a server, the ready-made server proxies, how the big assistants enroll voices, and where the voices'
store could have lived. The choice made — on the speaker, with an album of voices on one of them —
and how it is built are in voice-id.md; the server-side experiment itself is
[speaker-id.md](speaker-id.md).

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

## Where the voices' store could live

1. **One of the speakers, chosen** (`voice_store = true` in its config; a Station 3 if there is
   one: room on its eMMC, a faster CPU). The People tab lives there; the web app reaches every
   speaker, so it shows from any. While that speaker is off, the log waits on the others and edits
   wait. **The pick:** nothing to install besides the CFW.
2. **HA**, through an integration of our own that keeps the log and the groups and shows them in
   HA. A component outside the CFW to install and maintain.
3. **A server**: the proxy of [speaker-id.md](speaker-id.md); then computing there is natural too,
   and a speaker would send only its wake-word voiceprint.
