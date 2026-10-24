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
| [ha-assistant-setup.md](ha-assistant-setup.md) | Reference: setting up the voice assistant in HA — STT/TTS servers, conversation agent, Assist pipeline, adding the speaker, troubleshooting |
| [deploying.md](deploying.md) | **Agent guide:** install, update (`make deploy`, `tools/deploy.sh`), HA side, disable/remove, keeping the vendor offline, verification checklist |
| [porting-a-speaker.md](porting-a-speaker.md) | **Agent guide:** researching a new speaker and adding CFW support (inputs, profile mapping, audio path, voice front-end contract, definition of done) |
| [boards/README.md](boards/README.md) | Multi-model support: board profiles, bringing up a new station, per-model reversing checklist; per-board notes ([bergamot](boards/bergamot.md), [midi](boards/midi.md), [orion](boards/orion.md) — Station 3 sound bring-up, 96 kHz mroute capture; [pickle](boards/pickle.md) — incl. the Mini 3 Pro connectivity/Wi-Fi-hop logic, running without maind and the bottom pogo-pad module connector) |
| [music-sendspin.md](music-sendspin.md) | The speaker as a Music Assistant (Sendspin) player: sendspin-host, the shared dmix, wake word over music (hardware AEC reference), ducking, linked/separate volume, connecting and pairing, measurements, announce chime |
| [ha-music-setup.md](ha-music-setup.md) | **Guide:** music by voice — Music Assistant server + Spotify, the speakers as players, HA integration, the search/play scripts and automations (templates in `hass/music/`), LLM prompt rules, gotchas |
| [display-expressions.md](display-expressions.md) | Display engine design: independent surfaces, semantic expressions, scenarios; the stock clock clip formats (`.scled` / `.binscled`) and their LED order; HA exposure; phases |
| [voice-slow-replies.md](voice-slow-replies.md) | Slow replies: how HA behaves (no timeout, the wedge on a new run, the clean cancel by dropping the connection), the speaker's reply deadline, «стоп», wake-word interrupt, wedge watchdog |
| [status-indication.md](status-indication.md) | Boot / first connection / degraded states and how the ring and the front display show them; the clock before the first time sync; voice attempts without HA |
| [releasing.md](releasing.md) | Versions (SemVer tags, burned into the build, shown in HA) and how to cut a release |
| [wishlist.md](wishlist.md) | Ideas under consideration, not planned: nearest-speaker arbitration, ESPHome API instead of MQTT+Wyoming, pipeline robustness, music; notes from reviewing pulsar |
| [speaker-id.md](speaker-id.md) | Speaker identification design (who is talking) for the HA voice pipeline, with a status log |

Also: [`../yastationd/README.md`](../yastationd/README.md) (daemon usage, entities, open items) and
[`../spotter-host/README.md`](../spotter-host/README.md) (vendor spotter/VQE host, on-device results).

## Conventions

- One doc per topic/area (architecture, an external API, a parsing quirk, a decision record).
- Record hard-won knowledge: API quirks, edge cases, known limitations, and the *why* behind decisions.
- When code changes invalidate a doc, update the doc in the same change.
