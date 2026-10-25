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
| [deploying.md](deploying.md) | **Agent guide:** install, update (`make deploy`, `tools/deploy.sh`), HA side, disable/remove, keeping the vendor offline (IPv4 + IPv6 egress lock, the OTA key guard, DNS), verification checklist, diagnostics and gotchas (reboot reason, pstore, ALSA, BusyBox) |
| [porting-a-speaker.md](porting-a-speaker.md) | **Agent guide:** researching a new speaker and adding CFW support (inputs, profile mapping, audio path, voice front-end contract, definition of done) |
| [boards/README.md](boards/README.md) | Multi-model support: board profiles, bringing up a new station, per-model reversing checklist, the crossed codenames, possible ports nobody here is doing (Mini 2); per-board notes ([bergamot](boards/bergamot.md), [cucumber](boards/cucumber.md) — the retail Station Midi, wiki data only; [yandexmidi](boards/yandexmidi.md) — the Station 2 (internal "midi"), from firmware; [orion](boards/orion.md) — Station 3: install notes, 96 kHz mroute capture, NPU wake verifier, screen patterns at 20 ms; [pickle](boards/pickle.md) — incl. the Mini 3 Pro connectivity/Wi-Fi-hop logic, running without maind and the bottom pogo-pad module connector) |
| [music-sendspin.md](music-sendspin.md) | The speaker as a Music Assistant (Sendspin) player: sendspin-host, the shared dmix, wake word over music (hardware AEC reference), ducking, linked/separate volume, connecting and pairing, measurements, announce chime, the group-sync calibration plan (chirps + least squares) |
| [ha-music-setup.md](ha-music-setup.md) | **Guide:** music by voice — Music Assistant server + Spotify, the speakers as players, HA integration, the search/play scripts and automations (templates in `hass/music/`), LLM prompt rules, gotchas |
| [display-expressions.md](display-expressions.md) | Display engine design: independent surfaces, semantic expressions, scenarios; the per-display front catalogue (scenes, native animations, the seg7→pixel fallback); the stock clock clip formats (`.scled` / `.binscled`) and orion's screen patterns (`.led`, mid-file `loop`, gamma) with the expression map; HA exposure; phases |
| [led-preview.md](led-preview.md) | The LED preview: the daemon's frame tap (`frametap_addr`, a stream of what is drawn on the screen, ring and ambilight, plus its commands) and the host simulator `cmd/ledsim`; the viewer page that compares the simulator with a unit; the tap's protocol |
| [voice-slow-replies.md](voice-slow-replies.md) | Slow replies: how HA behaves (no timeout, the wedge on a new run, the clean cancel by dropping the connection), the speaker's reply deadline, «стоп», wake-word interrupt, wedge watchdog |
| [status-indication.md](status-indication.md) | Boot / first connection / degraded states and how the ring and the front display show them; the clock before the first time sync; voice attempts without HA |
| [../emu/README.md](../emu/README.md) | QEMU emulator for boot experiments: substitute kernel with board stubs (`emu/kernel/`), flash images from the dumps, maintenance initramfs, a CFW twin seeded read-only from a unit, smoke and power-cut tests |
| [permanent-install.md](permanent-install.md) | Making the CFW survive a hard reset: how the factory reset works per model, the reset as the safety net for rootfs work, replacing pickle's `aml_sysrecovery` restore package, why bergamot's recovery is not replaceable, the dev/golden/stock layers, plan and hazards |
| [wifi-setup.md](wifi-setup.md) | Wi-Fi setup mode: the recovery access point (key combo), our hostapd build (the firmware's wpa_supplicant has no AP mode), DHCP/DNS/captive page in the daemon, the page's home settings (broker, probes, NTP, MA), the handoff back to wpa_supplicant, crash recovery, boot without a network |
| [releasing.md](releasing.md) | Versions (SemVer tags, burned into the build, shown in HA) and how to cut a release |
| [intercom.md](intercom.md) | Two-way 1-to-1 calls between speakers: plan and v1 design — VQE uplink, RTP/L16 over UDP with AES-GCM, jitter buffer and drift, MQTT signalling, the voice scenario, HA entities, device checklist |
| [ha-intercom-setup.md](ha-intercom-setup.md) | **Guide:** the intercom with HA — speaker config, the entities, the "call a room" script for «Алиса, позвони в спальню», voice control during a call, automation ideas |
| [wishlist.md](wishlist.md) | Ideas under consideration, not planned: nearest-speaker arbitration (with refinements from another multi-speaker project), ESPHome API instead of MQTT+Wyoming, pipeline robustness, music, "someone is asleep here" detection, orion's ultrasound sonar rig; notes from reviewing pulsar |
| [speaker-id.md](speaker-id.md) | Speaker identification design (who is talking) for the HA voice pipeline, with a status log |

Also: [`../yastationd/README.md`](../yastationd/README.md) (daemon usage, entities, open items) and
[`../spotter-host/README.md`](../spotter-host/README.md) (vendor spotter/VQE host, on-device results).

## Conventions

- One doc per topic/area (architecture, an external API, a parsing quirk, a decision record).
- Record hard-won knowledge: API quirks, edge cases, known limitations, and the *why* behind decisions.
- When code changes invalidate a doc, update the doc in the same change.
