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
- Tell it in the prompt how to treat ambiguous verbs, e.g. that "stop the timer" means *cancel*,
  not *pause*. A paused timer stays alive in HA.
- Keep answers short in the prompt: they are spoken aloud.

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

## 8. Troubleshooting

| Symptom | Look at |
|---|---|
| Speaker log says "no HA satellite connection" on the voice key | the Wyoming integration for `<speaker-ip>:10700` is missing or unavailable |
| Wakes up but "nothing heard" | mic-mute switch, mic gain (board doc), STT server reachable from HA |
| Recognised text is garbled | STT model/language; try a bigger or language-specific model |
| Long pause before the answer | STT/LLM speed: smaller models, a GPU, or the built-in agent for commands |
| Two speakers answer at once | both heard the wake word; areas help commands target the right room, and moving the speakers apart helps detection |
| HA API calls fail with an empty reply | HA may serve HTTPS on port 8123; use `https://` |
