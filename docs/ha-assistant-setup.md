# Setting up the voice assistant in Home Assistant

A general recipe for turning a speaker running the CFW into a working voice assistant in Home
Assistant (HA). The speaker itself only listens, wakes up and plays audio. Speech recognition, the
"brain" and speech synthesis all run on the HA side. Anything works that HA's **Assist** supports;
below is what you need and how the pieces connect.

```
speaker ──(Wyoming: 16 kHz voice in, audio out)──► HA Assist pipeline
                                                     ├─ STT  (speech → text)
                                                     ├─ conversation agent (text → action/answer)
                                                     └─ TTS  (answer → speech)
speaker ──(MQTT)──► HA devices/entities (ring, display, volume, sensors…)
```

## 1. Prerequisites

- **HA** with the **MQTT** integration and a broker (e.g. the Mosquitto add-on/container) on the LAN.
  The speaker's controls and sensors appear by themselves through MQTT Discovery once
  `mqtt_host` in its config points at the broker.
- The speaker's address is reachable from HA (port **10700**, the Wyoming satellite).
- Some local compute for STT/TTS. A small CPU box is enough for basic models; larger STT models or
  an LLM agent benefit from a GPU.

## 2. Speech-to-text (STT)

Run a **Wyoming STT server** and add it to HA (*Settings → Devices & services → Add integration →
Wyoming Protocol*, host + port). Options, roughly:
- **faster-whisper** (`wyoming-faster-whisper`): multilingual, good default; pick a model size that
  fits your hardware (bigger = better and slower).
- Language-specialised models served over Wyoming (e.g. Vosk, or a dedicated Russian ASR model
  wrapped in a Wyoming server) are often faster and more accurate for one language.
- HA Cloud STT also works, but defeats the offline point.

The speaker already sends **echo-cancelled, noise-reduced** 16 kHz mono (its own front-end), so STT
gets clean input. Keep HA's own noise suppression low (see §5).

## 3. Text-to-speech (TTS)

Run a **Wyoming TTS server** and add it the same way:
- **Piper** (`wyoming-piper`): fast, many voices and languages, runs on a CPU.
- Other neural TTS engines with a Wyoming wrapper, for nicer or more natural voices in a given
  language.

Any sample rate is fine: the speaker resamples what HA streams to it.

## 4. Conversation agent (the "brain")

Pick one in the pipeline:
- **Home Assistant** (built-in Assist): fast and fully local, understands HA's built-in sentence
  patterns ("turn on the light in …", timers, …) in supported languages. No AI chit-chat.
- **An LLM agent** (Ollama, or another provider integration): free-form questions and flexible
  phrasing. With *Prefer handling commands locally* enabled, simple device commands still go
  through the fast built-in path, and the LLM handles the rest.

Whichever you use, **expose** the entities the assistant may control (*Settings → Voice
assistants → Expose*). It can only act on what is exposed.

LLM tips:
- See §8 for the prompt rules and model choice that made a local agent reliable.

## 5. The Assist pipeline

*Settings → Voice assistants → Add assistant*:
- **Language** and the **STT**, **conversation agent** and **TTS** from above, plus a voice.
- **Wake word: leave it unset.** The speaker detects the wake word on the device and starts the
  pipeline at the STT stage, so no HA-side wake word engine is needed.

## 6. Adding the speaker

- Add a **Wyoming Protocol** integration with host = the speaker's address, port **10700**. HA
  creates an `assist_satellite` device for it.
- On that device, choose the **assistant (pipeline)** from §5. Satellite options:
  - *noise suppression*: low or off (the speaker already cleans the audio);
  - *auto gain*: as needed;
  - *finished speaking detection*: default, or relaxed for slow speakers.
- Put the device (both the MQTT device and the satellite) in its **area**. Then "turn on the light"
  without a room name means this room, and timers ring on the speaker that set them.
- Optional: import [`hass/scripts.json`](../hass/scripts.json). It holds scripts that play a ring
  animation or show text on the speaker, and exposing them to Assist lets an LLM agent use them.

## 7. What then works

- Wake word or the voice key → speak → the answer is spoken; follow-up questions keep listening.
- Timers ("set a timer for 5 minutes"): countdown on the speaker's display, alarm when done.
- `assist_satellite.announce`: speak a message, or play a media URL, from automations.
- Everything on the MQTT device (ring light, display text, volume, brightness…) from automations
  and dashboards.
- Music by voice through Music Assistant: [ha-music-setup.md](ha-music-setup.md).

## 8. Gotchas and recommendations

Lessons from running several of these speakers daily with a local stack. Each one fixed an observed
failure.

### Exposure (what the assistant can see)

- **Expose little, and one canonical entity per concept.** Ghost or duplicate entities, and several
  sensors that all look like "temperature in the room", make answers wrong or inconsistent. If a room
  has a climate/thermostat entity, make that *the* "temperature in X" and leave raw sensors
  unexposed.
- **Name things with aliases, not renames.** Aliases ("Temperature in the kitchen", situational
  names for scenes) help the matcher and the LLM without breaking dashboard labels.
- **Every satellite and every scene needs an area.** Room-relative commands, timers and "which room
  am I in" depend on it. A scene without an area can't be picked "for this room".
- **Don't expose the speaker's own plumbing** (ring light, lux, text entities): it's noise to the
  agent. Expose the scripts from `hass/scripts.json` instead, if you want it to animate the ring or
  show text.
- Weather/outside temperature are only answerable if they are exposed.

### The built-in matcher first

- Keep **Prefer handling commands locally** on, with any agent. The rule-based matcher answers
  simple commands (lights, timers, time) instantly and never hallucinates. The LLM only sees what it
  misses.
- **The wake word can end up in the transcript.** The speaker starts the stream a little before the
  point where the spotter heard the wake word (default 300 ms, `preroll_ms`; the spotter fires near
  the word's end, see [wake-word-boundary.md](wake-word-boundary.md)) so the first syllable isn't
  cut. `wake_word_in_stream = true` sends the whole wake word on purpose. When it is still in it, STT
  returns "<wake word>, turn on the light", the built-in matcher misses, and simple commands go to
  the slower LLM. Workarounds:
  - **custom sentences** with an optional wake-word prefix for the commands that matter (timers,
    "turn on/off {name}");
  - an STT-side strip;
  - a smaller `preroll_ms`, at the risk of clipping the first word.
- **Timers belong to the built-in intents.** LLMs tend to misuse `HassStartTimer`'s
  `conversation_command` field (a hidden delayed command instead of a timer: no countdown on the
  speaker, the alarm fires after twice the time). They also map "stop" to *pause*, which leaves a
  paused timer alive forever. Cover start/cancel/pause with custom sentences (cancel on
  "stop/enough/cancel", pause only on "pause") so they never reach the LLM.

### Prompting an LLM agent

- **Give the assistant the wake word as its name.** Otherwise it may "correct" the user ("I'm not
  …"), and HA's shared history makes it repeat that.
- **State of the home and the time come only from tools, never from memory.**
- **HA keeps one conversation per satellite for ~5 minutes**, so small models copy their earlier
  answers (a stale time, a previous temperature). Set the agent's history low (e.g. 2 rounds); in
  current HA it can't be turned off. Add a prompt line: "history is context for follow-ups only;
  look up time, temperatures and device states with a tool every time".
- "I didn't catch that" only for real gibberish. Answer every part of a multi-part question.
- Colours as English names, never hex: HA's light intents reject hex.
- For a mood or situation ("make it cosy"), pick a matching *scene of this room* by name. Otherwise a
  model may turn on all scenes of the area.
- Keep answers short: they are spoken.
- **Colloquial time** ("half past five") is not an LLM job: models parrot the prompt's examples or
  get it wrong. Use a deterministic template response for the time intent.

### Choosing models

- **STT:** latency matters more than benchmark WER, because every command waits for it. A
  language-specialised model through a Wyoming server (e.g. a GigaAM-class model on the
  `wyoming-faster-whisper` image's onnx-asr backend for Russian) transcribes a command in ~0.1–0.2 s
  on a CPU, with punctuation and digits. Multilingual Whisper models are the general fallback.
- **TTS:** use a server that streams (synthesis by sentence), so long LLM answers start speaking
  right away. Piper streams by default; for Russian, Silero over Wyoming is a good balance of quality
  and speed.
- **Conversation LLM: judge truthfulness, not speed.** The failure that matters is a model that skips
  the tool and *invents* a state or a "done" (it "turned on" a light without calling anything).
  Observed:
  - mid-size MoE instruct models (~30–35B total, ~3B active, 3-bit quant) were accurate with tools at
    ~1–2 s;
  - small dense models (≤ 10B) misread tool results or cop out;
  - **always-reasoning** models are accurate but add 5–10 s pauses, which invite a second wake word
    and cancelled runs;
  - some models break HA's tool-result format outright.
  - A small cloud model was the most accurate (22/22 vs 19/22 locally) at ~3× the latency. A good
    split: built-in matcher → local model for the home, cloud for chit-chat and general knowledge.
- **Local LLM housekeeping (Ollama):** keep the model resident (`keep_alive -1`; a cold start costs
  8–17 s), and make the context size fit next to anything else on the GPU. Preloading without HA's
  `num_ctx` loads the server default and can evict other models. Hybrid-attention models re-prefill
  the whole prompt after any prompt/exposure change (~5 s once).
- **Benchmark properly:** run the real pipeline (`assist_pipeline/run`, `start_stage: intent`, the
  satellite's `device_id`, a fresh conversation per query) and check answers against live states.
  Snapshot the agent config first and restore it after. With the same model, the prompt moved
  accuracy more than switching models did.

## 9. Troubleshooting

| Symptom | Look at |
|---|---|
| Speaker log says "no HA satellite connection" on the voice key | the Wyoming integration for `<speaker-ip>:10700` is missing or unavailable |
| Wakes up but "nothing heard" | mic-mute switch, mic gain (board doc), STT server reachable from HA |
| Recognised text is garbled | STT model/language; try a bigger or language-specific model |
| Long pause before the answer | STT/LLM speed (§8); a reasoning model; the first request after a prompt change |
| A simple command goes to the LLM (slow) | the wake word in the transcript (§8): custom sentences with an optional wake-word prefix |
| The answer pauses mid-sentence, the ring shows the white comet while it waits | audio arrives slower than it plays. The `wyoming: audio stop` log line has `secs` (audio length) vs `wall_secs` (delivery time), `max_gap_ms` and `rebuffers`. Big gaps on a short answer mean Wi-Fi, or a TTS that stalls between sentences. The speaker holds 0.5 s of audio before playing and again after a stall, so a stall gives one pause rather than a stutter |
| Satellite stuck: new runs end instantly or hang in STT | a new run was started over a running one (HA's stale run-end wedges the satellite, [voice-slow-replies.md](voice-slow-replies.md)). The speaker avoids this and heals it itself by dropping the connection; if it persists, reload the Wyoming integration entry |
| Two speakers answer at once | both heard the wake word; areas help commands target the right room, and moving the speakers apart helps detection |
| HA API calls fail with an empty reply | HA may serve HTTPS on port 8123; use `https://` |
