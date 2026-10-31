# Research

Findings, not specifications: what we learned about the firmware, the hardware and the options
before building something (static reverse engineering of the vendor's binaries, measurements,
designs we chose against). They record evidence and reasoning; they may describe the device rather
than our code, and may be outdated where the code went another way. What the CFW does and how is in
the specs one level up ([../index.md](../index.md)); a spec cites the research it rests on. The
status column says whether a doc's conclusions still stand, so you know before opening a long one;
a doc whose recommendation was not taken says so where it makes it.

| Document | What it is | Status |
|---|---|---|
| [device-teardown.md](device-teardown.md) | The Station Mini 3 (`bergamot`) taken apart: SoC, partitions, init, LEDs, the clock, audio, inputs, sensors | reference; hardware facts hold |
| [wakeword-mic-array.md](wakeword-mic-array.md) | First pass at the vendor's wake word and mic array (bergamot), with the corrections the later passes made | partly superseded: its microWakeWord plan was not taken; corrections inline |
| [vqe-decompile.md](vqe-decompile.md) | The vendor's voice front-end (VQE): the bergamot preset, mic geometry, the neural noise reducer, channel wiring | current for the VQE; its wake word line is corrected |
| [spotter-decompile.md](spotter-decompile.md) | The vendor's keyword spotter: its C API, models and runtime, and whether «Алиса» can run without the vendor stack | current for the engine; its recommendation superseded (the vendor spotter shipped) |
| [stt-decompile.md](stt-decompile.md) | How speech-to-text works on the firmware: it is the cloud's; nothing to reuse on the device | current: nothing to reuse on the device |
| [tts-decompile.md](tts-decompile.md) | What the firmware's TTS is and what of it we can reuse (the recorded phrases) | current; the recorded phrases are what we use |
| [voice-phase2-plan.md](voice-phase2-plan.md) | The vendor VQE and spotter measured on the device, and the plan that became the wake word integration | done: the plan was built, differently in places (noted in §5) |
| [hass-integration.md](hass-integration.md) | The original Home Assistant integration design (historical; the current map is [../architecture.md](../architecture.md)) | historical: the original design |
| [permanent-install.md](permanent-install.md) | Taking over the factory reset: how it works on each board, what a reset does, measured, the restore package, what was done and the hazards. Done on pickle and orion, impossible on bergamot | done: [guides/golden-image.md](../guides/golden-image.md) |
| [vendor-removal.md](vendor-removal.md) | Deleting the vendor's software instead of gating it, on pickle: what the CFW needs, what can reach the network, what the egress counters say. |
| [speaker-id.md](speaker-id.md) | Speaker identification on a server (a Wyoming STT proxy with open models measured on Russian); superseded by voice ID on the speaker | superseded by voice ID on the speaker |
| [voice-id-alternatives.md](voice-id-alternatives.md) | Voice ID: on the speaker or on a server, the ready-made proxies, how Alexa, Google, Siri and Alice enroll voices | current: the reasoning behind voice-id.md |
