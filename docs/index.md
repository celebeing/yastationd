# Docs index — yastationd

This is the index of project documentation. Read the relevant doc here **before** planning work, and keep this index in sync when you add, rename, or remove docs.

| Document | What it covers |
| --- | --- |
| [device-teardown.md](device-teardown.md) | Neutral hardware/interface reference: SoC, partitions, init, LEDs, matrix, audio, inputs, sensors |
| [hass-integration.md](hass-integration.md) | Home Assistant integration design: MQTT agent, Wyoming satellite, display arbitration, phases |
| [yastationd-design.md](yastationd-design.md) | Implementation spec of the on-device daemon (Phase 1) incl. the verified audio/unmute recipe |
| [wakeword-mic-array.md](wakeword-mic-array.md) | Mic array + vendor wake-word/VQE reverse engineering reference |
| [spotter-decompile.md](spotter-decompile.md) | Decompiled SpeechKit spotter engine and the model-reuse feasibility |
| [vqe-decompile.md](vqe-decompile.md) | Decompiled `bergamot` VQE preset, mic geometry, neural NR, channel wiring |
| [stt-decompile.md](stt-decompile.md) | How the stock speech-to-text path works (client side) |
| [tts-decompile.md](tts-decompile.md) | What TTS runs on the device and what can be reused |
| [voice-phase2-plan.md](voice-phase2-plan.md) | On-device results of the vendor spotter + VQE and the yastationd wake-word integration plan |
| [deploying.md](deploying.md) | **Agent guide:** install, update (`make deploy`, `tools/deploy.sh`), HA side, disable/remove, keeping the vendor offline, verification checklist |
| [porting-a-speaker.md](porting-a-speaker.md) | **Agent guide:** researching a new speaker and adding CFW support (inputs, profile mapping, audio path, voice front-end contract, definition of done) |
| [boards/README.md](boards/README.md) | Multi-model support: board profiles, bringing up a new station, per-model reversing checklist; per-board notes ([bergamot](boards/bergamot.md), [midi](boards/midi.md), [pickle](boards/pickle.md) — incl. the Mini 3 Pro connectivity/Wi-Fi-hop logic and running without maind) |
| [speaker-id.md](speaker-id.md) | Speaker identification design (who is talking) for the HA voice pipeline, with a status log |

Also: [`../yastationd/README.md`](../yastationd/README.md) (daemon usage, entities, open items) and
[`../spotter-host/README.md`](../spotter-host/README.md) (vendor spotter/VQE host, on-device results).

## Conventions

- One doc per topic/area (architecture, an external API, a parsing quirk, a decision record).
- Record hard-won knowledge: API quirks, edge cases, known limitations, and the *why* behind decisions.
- When code changes invalidate a doc, update the doc in the same change.
