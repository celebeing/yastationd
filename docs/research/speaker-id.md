# Speaker identification for the voice pipeline (who is talking: Петя / Аня / unknown)

Status: research + a desk experiment, 2026-10-22. Nothing deployed; no changes to HA, voice-server or
the speakers. HA facts were read from the running container (HA 2026.9.0, `wyoming` 1.10.0).
The experiment ran on the dev box (WSL), with scripts and models kept in the session scratchpad.

**Superseded (2026-10-29):** voice ID was built on the speaker instead, with the firmware's own
biometry models: [voice-id.md](../voice-id.md) (also the comparison of the two). This server-side
design stays as research, for satellites without the CFW.

## 0. Recommendation (TL;DR)

1. **Model:** **3D-Speaker CAM++ `zh_en_16k-common_advanced`**. It is 7.2 M params, a 28 MB ONNX file and
   Apache-2.0. Run it through **`sherpa-onnx` 1.13.8** (2026-09-10, `SpeakerEmbeddingExtractor`) on CPU.
   - It takes about 30 ms per 3 s utterance on one x86 core.
   - It was the best accuracy/cost trade-off in our test on Russian speech: real recordings of Петя plus 17 Russian voice actors.
   - Back-up: **ERes2NetV2** (zh-cn common), which is more accurate on look-alike voices but 6× slower (≈200 ms) and 70 MB.
   - Avoid the WeSpeaker ONNX exports. Under sherpa-onnx their scores were mis-calibrated in our test.
2. **Where it runs:** **server-side on voice-server**, not on the satellites.
   - The utterance audio already reaches the STT on voice-server.
   - The ReSpeaker (ESPHome) can't run it at all.
   - The Station's A53 would need roughly 0.5–0.8 s and 60–90 MB of its ≈150 MB free RAM for one utterance (estimate, not measured).
   - Text-dependent verification on the «Алиса» wake-word segment stays an optional later add-on for the Station only (§3.3).
3. **Plumbing: a Wyoming STT proxy, `wyoming-voiceid`, on voice-server, placed in front of GigaAM.**
   - It tees the audio to GigaAM (`:24117`) and to the embedder.
   - It returns the **unchanged** transcript, puts `{"speaker":…, "score":…}` in `Transcript.context` (a field the Wyoming protocol already has, which HA currently drops), and posts the speaker to HA.
   - Keeping the text unchanged matters: the «Алиса» pipeline has `prefer_local_intents: true`, and any prefix in the text would break HA's local intent matching.
4. **How the agent learns the speaker. Two stages:**
   - **MVP, no HA code.** Before it returns the transcript, the proxy does a synchronous `POST /api/states/sensor.voice_speaker` with `state=petya|anya|unknown` and attributes `text`, `score` and `satellite`. One line goes into the Ollama prompt template. HA renders that template on **every** turn, and the template has `now()`/`states()`:
     ```jinja
     {% set s = states.sensor.voice_speaker %}{% if s and (now() - s.last_updated).total_seconds() < 20 %}
     Сейчас говорит: {{ {'petya':'Петя','anya':'Аня'}.get(s.state, 'неизвестный (гость или ТВ) — личное не трогай') }}.{% endif %}
     Комната Пети — «кабинет Пети», комната Ани — «спальня». «Моя комната» = комната того, кто говорит.
     ```
   - **Stage 2, a small custom integration `voice_id`.** It provides a conversation-agent wrapper entity that is set as the pipeline agent and delegates to `conversation.ollama_conversation`.
     - It looks up the speaker for *this* utterance by matching `user_input.text` to the proxy's recent results. That gives correct correlation with several satellites.
     - It passes `Context(user_id=<HA user of the person>)` and an `extra_system_prompt`. HA's stock `{{ user_name }}` prompt variable then works, and the logbook attributes actions to Petr or Anna.
     - It is also the only place to put **hard** permissions, by refusing or filtering sensitive tools for `unknown`. The LLM prompt is only a soft guard.
5. **Enrollment:** passive and on the same satellites.
   - Turn on HA's `assist_pipeline: debug_recording_dir` for a week, or let the proxy store embeddings with their transcripts.
   - Then cluster and label. Aim for **≥ 30 s of net speech per person** (≈15–20 commands) from **each** satellite/room.
   - Decide with argmax cosine to the person centroids, plus an accept threshold (start at **0.45**) and a margin over the runner-up (**0.10**). Anything else is `unknown`.
6. **Expected accuracy for this household:**
   - Петя vs Аня (male vs female, closed set): **≤ 1–2 % confusion**.
   - "Unknown" rejection is the weak spot: a same-gender guest or TV voice could be accepted at **≈5–10 %** (EER ≈5 % on short real lines in our test).
   - So: personalise with speaker id, and **never** authorise anything security-relevant (locks, alarm, cameras) on the speaker score alone.

## 1. Models (survey)

EERs are the publishers' figures on VoxCeleb1-O (cleaned), unless noted. All the ONNX files listed are in the sherpa-onnx
release [`speaker-recongition-models`](https://github.com/k2-fsa/sherpa-onnx/releases/tag/speaker-recongition-models)
(sic; the files were uploaded 2024-10-14 with a `checksum.txt`; mature). The SHA-256s of the six files we downloaded matched it.

| Model | Params / ONNX | Licence | Vox1-O EER | Training data | Notes |
|---|---|---|---|---|---|
| **3D-Speaker CAM++ zh_en common advanced** | 7.2 M / 28 MB | Apache-2.0 | 1.16 % (CN-Celeb 5.98 %) | "large-scale zh+en" speakers (card) | **pick**: fast, best separation in our RU test, 192-dim |
| 3D-Speaker CAM++ (VoxCeleb) | 7.2 M / 29 MB | Apache-2.0 | 0.65 % | VoxCeleb2 | the 3dspeaker en_voxceleb ONNX did poorly in our test (§2) |
| 3D-Speaker ERes2NetV2 (zh-cn common) | 17.8 M / 70 MB | Apache-2.0 | 0.61 % (repo table) | 200k-spk zh | **backup**: best ID accuracy in our test, 6× slower |
| 3D-Speaker ERes2Net-base / large | 6.6 M / 22.5 M | Apache-2.0 | 0.84 / 0.52 % | — | not tested |
| WeSpeaker ResNet34(-LM) / CAM++ / ResNet293-LM | 6.6 M–28 M | weights follow the dataset licence (VoxCeleb: CC-BY-4.0) | ≈0.7 % / 0.65 % / ≈0.45 % | VoxCeleb2 | under sherpa-onnx the scores were compressed (impostors 0.7–0.9 cosine). Likely a front-end/normalisation mismatch (unverified). Usable natively in `wespeaker`, but not plug-and-play here |
| WeSpeaker VoxBlink2 SimAM-ResNet34/100 | 25–50 M | dataset licence | — | **multilingual** VoxBlink2 (+Vox2 ft) | interesting for RU, but checkpoints only (no sherpa ONNX). Not tested |
| SpeechBrain ECAPA-TDNN `spkrec-ecapa-voxceleb` | 20.8 M / ~80 MB | Apache-2.0 | 0.80 % | VoxCeleb1+2 | PyTorch; the classic baseline. Heavier than CAM++ at similar accuracy. SpeechBrain 1.1.2 is 8 days old; pin 1.1.1 if used |
| NVIDIA TitaNet-small | ≈6 M / 39 MB | CC-BY-4.0 / NeMo | ≈1.1 % (paper, from memory, unverified) | Vox1+2, Fisher, SWBD, Libri | 2nd in our RU test, fastest. Male look-alikes confused more |
| TitaNet-large | 23 M / 99 MB | CC-BY-4.0 | 0.66 % | same | not tested |
| pyannote `wespeaker-voxceleb-resnet34-LM` / old `pyannote/embedding` | 6.6 M / 4 M | CC-BY-4.0 / MIT (gated) | ≈0.7 % / ≈2.8 % | VoxCeleb | the same WeSpeaker net in pyannote clothing; old one is outdated |
| Resemblyzer (GE2E LSTM) | 1.4 M | Apache-2.0 | ≈4–5 % (unverified) | LibriSpeech/VoxCeleb | last release 0.1.4 (2023-10), dated. Not recommended |

**Russian:** none of these is trained on Russian specifically. Speaker embeddings mostly carry voice timbre, not
language, and VoxCeleb/VoxBlink are multilingual. Our test (§2) confirms the zh/en CAM++ and ERes2NetV2 separate Russian
speakers well.

**Short utterances:** published Vox1 EERs are for 4–8 s trials. On 1–2 s, EER typically degrades several times over.
Our test used 1.1 s and 1.5–2.8 s clips on purpose.

**Cortex-A53 (the Station):** CAM++ is the only realistic candidate. It ran in 33 ms per 3 s clip on one i5-13600K core. An
in-order A53 at 1.2 GHz is very roughly 15–25× slower, which gives ≈0.5–0.8 s per utterance, plus ≈60–90 MB RSS for ORT and the model
(estimate, not measured). Our 32-bit armhf userland needs an armv7 onnxruntime/sherpa build. sherpa-onnx ships arm builds,
but the exact armhf/glibc-2.30 fit is **unverified**. With one core already taken by VQE + spotter and ≈150 MB free, this isn't
worth it while the audio goes to voice-server anyway.

## 2. Desk experiment (done, dev box)

Setup: `sherpa-onnx==1.13.8`, `numpy==2.2.6` and `soundfile==0.13.1` in a scratchpad venv, run with `python -I`. Scripts:
`spkid_eval.py` and `multi.py` (scratchpad, not committed).

Data. All of it was already on disk in `<mww-training-dir>`, so nothing new was recorded:
- **Петя:** 60 real recordings of one fixed word (the old «Джайна» wake-word set), 1.1 s average, one session, one mic.
- **Real impostors:** 400 Warcraft III **Russian dub** unit lines, 2.8 s average. These are real actors, but the audio is studio and game-processed.
- **XTTS:** 150 Russian XTTS voice clones saying the same word.
- **Closed-set opponents:** the Jaina (female) and Arthas (male) actors.

Test A (target = Петя, centroid of 8 clips, 52 test clips):

| Model | Петя↔Петя cos (min) | max impostor cos | EER vs real / XTTS | FRR @ FAR 1 % | 2-spk error vs female / male | ms per 2 s (1 thread) |
|---|---|---|---|---|---|---|
| **CAM++ zh_en advanced** | 0.71 (0.54) | 0.32 / 0.35 | **0 % / 0 %** | 0 % | 0 / 0 % | 22 |
| TitaNet-small | 0.74 (0.58) | 0.38 / 0.41 | 0 / 0 % | 0 % | 0 / 0 % | 18 |
| ERes2NetV2 zh-cn | 0.76 (0.63) | 0.53 / 0.58 | 0 / 0 % | 0 % | 0 / 0 % | 146 |
| CAM++ en VoxCeleb (3dspeaker) | 0.88 (0.43) | 0.95 / 0.79 | 17 / 4 % | 65 % | 16 / 13 % | 22 |
| WeSpeaker ResNet34-LM | 0.88 (0.76) | 0.89 / 0.84 | 8 / 6 % | 23 % | 0 / 6 % | 60 |
| WeSpeaker ResNet293-LM | 0.91 (0.73) | 0.90 / 0.93 | 6 / 21 % | 35 % | 0 / 0 % | 372 |

Test B (harder): 17 speakers, 16 Russian voice actors plus Петя. Each has ≥9 lines of ≥0.8 s; enrolment uses ⅓ of the lines, and identification runs on the rest across all 17:

| Model | closed-set ID error | verification EER (threshold) |
|---|---|---|
| **ERes2NetV2** | **3.8 %** | 5.1 % (0.55) |
| **CAM++ zh_en advanced** | 8.0 % | 5.5 % (0.48) |
| TitaNet-small | 10.9 % | 7.9 % (0.42) |
| WeSpeaker ResNet34-LM | 43 % | 22 % |

Most of the remaining errors are between units the RU dub plausibly gave to the **same actress** (sorceress↔Jaina) or a
processed creature voice. That makes Test B pessimistic for real people. Test A is **optimistic**: all of Петя's clips come from one
session and one mic, while the impostors come from a different channel.

Real-life numbers will fall between the two; they need the real satellite data from §5.

Memory (Python + model, 1 thread): CAM++ 115 MB, TitaNet-small 167 MB, ERes2NetV2 295 MB maxRSS. All of these are trivial on voice-server.

## 3. Where to run it

### 3.1 Server-side on voice-server (recommended)

```
satellite ─(Wyoming)→ HA pipeline ─(Wyoming STT)→ voice-server:24119 wyoming-voiceid ──tee──→ :24117 GigaAM (unchanged)
                                                         └→ CAM++ embedding → cosine vs centroids
                          ←── Transcript(text=<GigaAM text, untouched>, context={speaker,score,margin})
                          (+ POST /api/states/sensor.voice_speaker before replying)
```

- In HA, re-point the existing Wyoming STT entry (`voice-server:24117`) to `:24119`. Rollback means putting the port back.
- The proxy buffers the whole utterance (HA streams it until its VAD ends), then runs the embedding **in parallel** with GigaAM's
  final decode. Added latency is ≈30 ms plus the HA REST call (a few ms on the LAN).
- Embed only the speech part: trim leading/trailing silence with a simple energy VAD, or sherpa's silero-vad. Skip speaker id when
  there is less than 0.7 s of net speech, and return `unknown`.
- Every satellite (ReSpeaker, Station, future rooms) gets speaker id for free, and there is one place for enrollment data.

### 3.2 On the satellite

- ReSpeaker/ESPHome: no.
- Station: possible but not worth it (§1 A53 note). It would also need its own side channel to HA (MQTT
  `yastation/<id>/speaker`), because Wyoming `run-pipeline`/`audio-start` carry no speaker field. HA's Wyoming
  satellite parses known fields only (`components/wyoming/assist_satellite.py`), and nothing from the satellite reaches
  `ConversationInput`.

### 3.3 The «Алиса» wake-word segment (text-dependent): later, Station only

- A fixed phrase on the same mic is the best case for verification. Our Test A, a fixed word, gave a large margin.
- However, neither satellite sends the wake word to HA:
  - yastationd detects it on-device and pre-rolls only ≈300 ms;
  - the ReSpeaker runs microWakeWord on-device.
- Pre-rolling the whole «Алиса» into the STT stream would put «Алиса, …» into the transcript. HA does **not** strip the wake
  phrase (`wake_word_phrase` is used only for duplicate-wake suppression), which would hurt local intent matching.
- The option: yastationd sends the ≈1 s wake segment from its ring buffer straight to `wyoming-voiceid` over a side request, keyed
  by satellite + timestamp. The proxy then fuses the two scores, e.g. the mean of the cosines. Do this only if the text-independent score
  proves insufficient.

## 4. Getting the speaker into HA / the agent

Facts from the HA 2026.9 code:
- `ConversationInput` fields are `text, context, conversation_id, device_id, satellite_id, language, agent_id,
  extra_system_prompt`. There is no speaker field.
- `assist_satellite` starts the pipeline with `context = Context()`, so there is **no user_id** for voice.
- The Wyoming STT client (`components/wyoming/stt.py`) reads only `Transcript.text` and ignores `Transcript.context`.
- The LLM system prompt is a Jinja template rendered on **every** turn (`ChatLog.async_provide_llm_data` →
  `_async_expand_prompt_template`). Its variables are `ha_name`, `user_name` and `llm_context` (with `device_id`), plus all template
  functions (`states()`, `area_name()`, `now()`).
  - `user_name` comes from `context.user_id`.
  - `extra_system_prompt` is appended, and it is **sticky** per conversation.
- `conversation.async_converse` called from inside an agent reuses the pipeline's chat log (the `current_chat_log` ContextVar,
  same `conversation_id`), so a wrapper agent is cheap.
- The «Алиса» pipeline uses `conversation.ollama_conversation` with `prefer_local_intents: true`, and the Ollama prompt already
  defines «спальня = комната Ани» and «кабинет Пети». Persons: `Petr` and `Anna`, both linked to HA users.

| Option | How | Pros | Cons |
|---|---|---|---|
| (a1) STT proxy **prefixes** text («[Петя] выключи свет») | proxy | zero HA work | **breaks local intents** (`prefer_local_intents`), gets into history and the UI; rejected |
| **(a2) STT proxy + `sensor.voice_speaker` + prompt template** | proxy POSTs the state before replying; template reads it | no HA code, works today, ordering is guaranteed (state set before the transcript returns) | global sensor: two satellites talking at the same instant could swap speakers (rare); soft (LLM-only) guard; `user_name` stays empty. **MVP** |
| (b) satellite sends speaker | Wyoming has no slot; MQTT side channel | — | ReSpeaker can't; correlating with the pipeline is still needed; rejected as primary |
| **(c) custom conversation-agent wrapper** (`custom_components/voice_id`) | wrapper entity = pipeline agent. Finds the speaker by `user_input.text` (+ time, `satellite_id`) in the proxy's recent results (MQTT retained map or HTTP), then calls `async_converse(agent_id=ollama, context=Context(user_id=…), extra_system_prompt=…)` | per-utterance correct; `{{ user_name }}` works; logbook attribution; **hard permission gate** (deny tool calls on locks/alarms for `unknown`); can add per-person memory | ≈150 lines of custom integration to maintain across HA versions. **Stage 2** |
| (c') custom **STT entity** in HA that talks Wyoming itself and keeps `Transcript.context` | replaces `stt.faster_whisper` | correlation inside HA without text matching | more code than (c); STT providers don't get `device_id` either |
| (d) per-speaker HA users / persons | used *through* (c): map speaker→`person`→`user_id` | native user semantics | HA has no per-entity user permissions in practice, so it is useful for attribution and `user_name`, not for security |

**What the LLM needs** is one line: «Сейчас говорит: Петя» (or «неизвестный»). Add a mapping «моя комната» → that person's
area, and a policy line: for an unknown speaker, don't touch personal reminders/calendars, and refuse sensitive devices.

**Room of a person / "kitchen and my room":**
- "my room" → the speaker's area via the prompt (MVP). In stage 2, use a `person`→`area` map in the wrapper config.
- Timers: `HassStartTimer` rings only on the requesting device (README open item 8). So "на кухне и в моей комнате"
  needs our own Assist script tool with args `areas[]`, `duration` and `person`, which fans out to the satellites in those areas
  (yastationd MQTT countdown + local alarm; ReSpeaker via `assist_satellite.announce`).
- Per-person reminders use the same kind of tool with a `person` arg. The LLM fills it from «Сейчас говорит».

## 5. Enrollment and accuracy plan

- **Data collection without touching the satellites** (user step, an HA config change): add
  `assist_pipeline:\n  debug_recording_dir: /config/assist_debug` to `configuration.yaml` and restart.
  - HA then saves each run's STT audio as WAV (`01_stt-…`) under a per-run folder.
  - Use a week of normal use, or the proxy's own store of embedding + text + timestamp + satellite, which needs no HA restart.
- **Labelling:** cluster the embeddings (2 big clusters = 2 adults; the rest is TV, guests or noise). Listen to 3–5 clips per
  cluster and name it. Alternatively, run an **active** enrollment: an HA script `voice_id.enroll(person, n=15)` tells the proxy that the next n
  utterances from satellite X belong to that person, and the user then speaks normal commands.
- **Amount:** ≥30 s of net speech per person, i.e. 15–20 commands of 1.5–3 s each, spread over **every satellite/room** and a few
  days and times of day (morning voice, distance). Store one centroid per person per satellite, plus a global one.
- **Decision rule:** `best = argmax cos(e, centroid_p)`. Accept if `best ≥ T` **and** `best − second ≥ M`; otherwise `unknown`.
  - Start with T = 0.45 and M = 0.10 for CAM++ advanced. Our scores: targets 0.54–0.9, impostors ≤0.35 (Test A); EER threshold 0.48 (Test B).
  - Re-fit T per person once there are 2 weeks of labelled logs: the 1st percentile of that person's genuine scores, floored at
    the 99.5th percentile of non-household scores.
  - Skip if net speech is under 0.7 s ("да", "стоп"). In a follow-up within the same conversation, inherit the previous speaker.
- **Adaptation:** fold utterances with `best ≥ T + 0.15` into a running centroid (EMA, cap the weight), so the centroid tracks the
  mic/room. Never adapt on borderline scores.
- **Expected:**
  - Петя↔Аня confusion ≤1–2 %;
  - known speaker → `unknown` (false reject) ≈3–8 % on short commands;
  - guest/TV → accepted as a household member ≈5–10 % for a same-gender voice, lower for the other gender.
  - The **2-person closed set is easy; open-set rejection is not.**
- **Failure modes:**
  - TV/radio voices: the wake word gates most of them, but a TV wake → "unknown" or, rarely, a household member.
  - Sick or whispering voice, shouting from another room, very short commands: more `unknown`.
  - Kids (none now) cluster near the adult female.
  - A recording or imitation of a household member is accepted, because there is no anti-spoofing.
  - Far-field reverb in a new room until it is enrolled.
  - Mitigations: fail soft (`unknown` = generic household behaviour for harmless actions), and add a confirmation question for anything personal.
- **Privacy:** everything stays local on voice-server. Store embeddings (192 floats) and short transcripts; enrollment audio is
  optional and deletable. No cloud. `debug_recording_dir` should be turned off again after collection, because it keeps raw audio.

## 6. Prototype path (smallest end-to-end validation)

Step 1 is done on the dev box (§2). Next:

1. **Collect real satellite audio.** The user enables `debug_recording_dir` (§5) and uses both satellites normally for a few days,
   Петя and Аня, about 20+ commands each. Copy the `01_stt-*.wav` files to a scratch dir on voice-server.
2. **Offline eval on voice-server** (CPU, about 10 minutes):
   ```sh
   python3 -m venv ~/voiceid/venv && ~/voiceid/venv/bin/pip install "sherpa-onnx==1.13.8" "numpy==2.2.6" "soundfile==0.13.1"
   curl -LO https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_zh_en_16k-common_advanced.onnx
   # label clips into petya/ anya/ other/ (by listening), then: enroll on 1/3 per person, score the rest,
   # report confusion, FRR, FAR on other/, score histograms; repeat with ERes2NetV2.
   ```
   The `spkid_eval.py` logic (centroid + cosine + EER) carries over unchanged.
   **Go/no-go:** Петя↔Аня confusion ≤2 % and FRR ≤10 % at T where `other/` FAR ≤5 %.
3. **`wyoming-voiceid` proxy** (Python, `wyoming==1.10.2`, `sherpa-onnx==1.13.8`; about 200 lines):
   - Wyoming `AsyncServer`. On `transcribe`/`audio-start`/`audio-chunk`/`audio-stop`, forward each event to `voice-server:24117` and buffer the PCM.
   - On `audio-stop`, embed (VAD-trimmed) while waiting for GigaAM's `transcript`.
   - `POST http://<ha-host>:8123/api/states/sensor.voice_speaker` (long-lived token for a dedicated HA user).
   - Reply with `Transcript(text, context={speaker,score,margin})`.
   - Pass `describe`/`info` through, so HA still sees the same STT models/languages.
   - Run it as a second container in `voice-server:~/voice/docker-compose.yml`. Enrollment centroids go to a JSON file and are hot-reloaded.
4. **Wire the MVP:** re-point the HA Wyoming STT entry to `:24119` and add the prompt lines from §0.4 to the Ollama subentry prompt.
   Test: «Алиса, включи свет в моей комнате» said by Петя and by Аня should land in different rooms; a TV clip should get «неизвестный».
5. **Stage 2** (only if the MVP is useful): `custom_components/voice_id` wrapper agent. Add the permission gate and `user_name`, then
   the multi-room timer/reminder script tools.

## 7. Open risks / unverified

- Real far-field accuracy on our satellites (Station `vqe_0` AEC'd stream vs the ReSpeaker XMOS stream) has **not been measured**. Both
  tests used non-satellite audio. Channel mismatch between the two satellites is the most likely error source, which is why we keep
  per-satellite centroids.
- The WeSpeaker ONNX mis-calibration under sherpa-onnx was not root-caused. CAM++ advanced vs ERes2NetV2: decide on real data.
- TitaNet-small params/EER are from memory of the paper (≈6 M / ≈1.1 %), and the zh_en CAM++'s training speaker count is unknown.
- A53 latency/RAM for on-device use is an estimate. The armhf sherpa/ORT build fit is unverified.
- Global `sensor.voice_speaker` race with simultaneous satellites (MVP only; fixed by stage 2).
- In stage 2, wrapping `async_converse` inside a conversation entity relies on the current chat-log ContextVar behaviour
  (HA 2026.9). Re-check it on HA upgrades.
- No anti-spoofing. Speaker id is for personalisation, not authentication.

## Sources

- sherpa-onnx speaker models release: <https://github.com/k2-fsa/sherpa-onnx/releases/tag/speaker-recongition-models>;
  PyPI `sherpa-onnx` 1.13.8 (2026-09-10): <https://pypi.org/project/sherpa-onnx/>
- 3D-Speaker (CAM++/ERes2Net, Apache-2.0, EER table): <https://github.com/modelscope/3D-Speaker>;
  CAM++ zh_en advanced card (Vox1-O 1.16 %, CN-Celeb 5.98 %): <https://www.modelscope.cn/models/iic/speech_campplus_sv_zh_en_16k-common_advanced>;
  CAM++ paper: <https://arxiv.org/abs/2303.00332>
- WeSpeaker pretrained models + licence note: <https://github.com/wenet-e2e/wespeaker/blob/master/docs/pretrained.md>
- SpeechBrain ECAPA (Apache-2.0, 0.80 %): <https://huggingface.co/speechbrain/spkrec-ecapa-voxceleb>
- TitaNet-large (CC-BY-4.0, 0.66 %, 23 M): <https://huggingface.co/nvidia/speakerverification_en_titanet_large>;
  TitaNet-small: <https://catalog.ngc.nvidia.com/orgs/nvidia/teams/nemo/models/titanet_small>; paper <https://arxiv.org/abs/2110.04410>
- Resemblyzer: <https://github.com/resemble-ai/Resemblyzer> (PyPI 0.1.4, 2023-10)
- pyannote embedding: <https://huggingface.co/pyannote/wespeaker-voxceleb-resnet34-LM>
- HA code read in the container (2026.9.0): `components/conversation/{models,chat_log}.py`,
  `components/assist_pipeline/pipeline.py`, `components/assist_satellite/entity.py`, `components/wyoming/{stt,assist_satellite}.py`,
  `wyoming/asr.py` (`Transcript.context`).
- Local context: [voice-phase2-plan.md](voice-phase2-plan.md), [../yastationd/README.md](../../yastationd/README.md),
  `<ha-config-repo>/docs/voice-stack-refresh-2026-10.md`.

## Status log

- **2026-10-22 — data collection started.** `assist_pipeline: debug_recording_dir: /config/assist_debug`
  appended to `<ha-config-dir>/configuration.yaml` on `hass` (backup:
  `configuration.yaml.bak.20261022-voiceid`), config checked valid, HA restarted (~30 s; the Station's
  Wyoming satellite reconnected on its own). Target: ~1 week of normal use, ≥20 commands each from Петя
  and Аня per satellite. **Revert after collection** (raw audio of every request): remove the
  `assist_pipeline:` block (or restore the backup), restart HA, and delete/archive `/config/assist_debug`.
- **2026-10-28 — 6 days of recordings: 252 requests (12 min, 20 MB) from four satellites.** Used
  for a desk check of the firmware's own biometry models ([voice-id.md](../voice-id.md), "Checked on
  the desk"): the main user's requests sit at cosine 0.7–0.95 to his centroid, the other household
  member's at 0.01–0.17 (gender classifier 0.85+), a video's voice at ≈0.5, non-speech at 0.2–0.3.
  The recording is still on (revert as above when done).
- **2026-10-28 — ready-made proxies surveyed.** Several community Wyoming speaker-ID proxies now
  exist (Murdock, assist-speaker-id and others), so `wyoming-voiceid` need not be written from
  scratch; how big assistants enroll voices is compared in the same place:
  [voice-id.md](../voice-id.md), "Ready-made proxies" and "How the big assistants do it".
