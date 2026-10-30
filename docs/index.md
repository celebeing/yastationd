# Docs index — yastationd

This is the index of project documentation. Read the relevant doc here **before** planning work, and keep this index in sync when you add, rename, or remove docs.

How the docs are laid out and what goes where: [README.md](README.md). This index lists the
**specifications** (what the CFW does and how, as built) and the developer guides. The other kinds
have their own indexes, read when the task needs them:

- [guides/](guides/README.md): **user guides** — installing and updating the CFW, connecting it to
  Home Assistant (the voice assistant, music, the intercom), sending diagnostics.
- [research/](research/README.md): **research** — the firmware's decompiles, the hardware teardown,
  measurements, the original design and the alternatives we chose against.
- [boards/](boards/README.md): the supported boards and per-board notes.

## Start here: the map and the developer guides

| Document | What it covers |
| --- | --- |
| [architecture.md](architecture.md) | **Start here:** how the CFW is put together — what runs where (the daemon, the child processes with the vendor's libraries), MQTT + Wyoming to HA and why not ESPHome, between the speakers, the daemon's areas, time without the internet |
| [adding-a-feature.md](adding-a-feature.md) | **Agent guide:** adding a feature the house way — where things are, settings (the registry, HA options), reactions as moments, HA entities and the web app as one definition, reusing the web app's widgets and painter, the API's conventions, vendor code in child processes, data on the speaker, test fixtures |
| [porting-a-speaker.md](porting-a-speaker.md) | **Agent guide:** researching a new speaker and adding CFW support — the ground rules (**no trial-and-error commands on unknown components**, the reads that froze a unit), inputs, profile mapping, verification order, audio path, voice front-end contract, definition of done |
| [building.md](building.md) | **Agent guide:** building the artifacts — `tools/build.sh` and one pinned builder container per toolchain (`docker/`: Go 1.22.5, Node 18 + pnpm, zig 0.14.1, the cross gcc), the vendor sysroot for `spotter-host` (`tools/build.env`), `tools/build-dist.sh` for one board's dist |
| [releasing.md](releasing.md) | Versions (SemVer tags, burned into the build, shown in HA) and how to cut a release |

## Features: how they are built

| Document | What it covers |
| --- | --- |
| [settings.md](settings.md) | Settings: the registry of config keys (descriptions, runtime / sync / advanced flags), live reload without a restart, the Settings and Config pages and the speaker card, sharing settings, options and moments between speakers over retained MQTT topics (no leader) |
| [web-ui.md](web-ui.md) | The web UI: (incl. the vitals API: load, temperatures, memory) one HTTP server per speaker (`http_addr`), login (default admin/yastation, PBKDF2 hash in the config), the JSON API built on the MQTT entities and their mirrored state, discovery of the other speakers, Prometheus `/metrics`, the config editor with trial and rollback, the app in `web/`, phases |
| [display-expressions.md](display-expressions.md) | Display engine design: independent surfaces, semantic expressions, scenarios; the per-display front catalogue (scenes, native animations, the seg7→pixel fallback); the stock clock clip formats (`.scled` / `.binscled`) and orion's screen patterns (`.led`, mid-file `loop`, gamma) with the expression map; HA exposure; phases |
| [asset-packs.md](asset-packs.md) | Asset packs and themes: every reaction of the speaker is a moment (the status indication and every sound included), steps that show and play at once, sounds by name with fallbacks, the default theme and the catalogue; the theme stack (`theme` key), the pack format and its checks, the owner's editable packs (`custom` first) over the built-in level 0, Behaviour → Moments / Packs / Sounds, the API, `cmd/packtool` |
| [assets.md](assets.md) | The web app's Assets group: the owner's media on the speaker, one section per kind; videos for the video mode (converted in the browser or uploaded from videoconv, the library under `<cfw>/media/videos/`, the API) |
| [health.md](health.md) | Self-monitoring: what the speaker watches about itself (health probes, the lifecycle, the power supply, the network watchdog, the supervisor, vitals and temperatures, metrics), the patterns a new monitor follows and the code to copy, thermal facts per board |
| [status-indication.md](status-indication.md) | Boot / first connection / degraded states and how the ring and the front display show them; the clock before the first time sync; voice attempts without HA |
| [led-preview.md](led-preview.md) | The LED preview: the daemon's frame tap (`frametap_addr`, a stream of what is drawn on the screen, ring and ambilight, plus its commands) and the host simulator `cmd/ledsim`; the viewer page that compares the simulator with a unit; the tap's protocol; `cmd/demoreel`, the README's GIFs |
| [video-mode.md](video-mode.md) | The `video` display mode (pixel screens): a converted video on the screen with its sound in sync — `make video` / `cmd/videoconv` (ffmpeg; scaling, gamma, frame timing), the files and config keys, the audio-clocked sync, what ends a playback (voice, «стоп», replies, music, calls), on-device checks |
| [ambilight-stream.md](ambilight-stream.md) | Effects on Station 3's rear strip: a DDP receiver (UDP 4048) for LedFx / WLED tools and its settings (no discovery), our music effects (rain, vu, spectrum, energy, scroll, pulse), the ledfb framebuffer path (3.5 ms a frame vs 25–34 ms over sysfs), the `cmd/ddpsend` test sender |
| [ambilight-color.md](ambilight-color.md) | Station 3's ambilight colours: what the strip does (linear driver, strong blue, greenish W, paler green), how it was measured (a camera as the colorimeter), the board calibration and `ambilight_white` / `ambilight_calibration`, the RGBW solver, results before/after, what it applies to (the HA light as hs + color_temp, music effects; not DDP) |
| [wake-word.md](wake-word.md) | The wake word as built: `spotter-host serve` (the vendor VQE, the «Алиса» spotter, the command spotter), its events and contract, per board (models, layouts, orion's NPU verifier), the settings and profile keys, the levers for sensitivity (no UI: a tuned model copy via `wake_model`, mic gain, near misses), why the vendor spotter and not microWakeWord, what is not measured |
| [wake-word-boundary.md](wake-word-boundary.md) | Where «Алиса» ends: what the vendor spotter gives (no boundaries; it fires near the word's end), desk measurements of WAKE against the true end, probing, DSP, Vosk as aligner, a tiny CTC model; the CFW anchors the HA stream on the WAKE's sample in vqe_0 (`AT` lines), `wake_word_in_stream`, the `wake_capture` recordings and the validation plan |
| [wake-arbitration.md](wake-arbitration.md) | Wake-word arbitration: speakers that hear one «Алиса» agree over MQTT which answers (the loudest, in calibrated dB SPL); claims, the adaptive wait, ties, what is not arbitrated, the first test |
| [voice-slow-replies.md](voice-slow-replies.md) | Slow replies: how HA behaves (no timeout, the wedge on a new run, the clean cancel by dropping the connection), the speaker's reply deadline, «стоп», wake-word interrupt, wedge watchdog |
| [voice-id.md](voice-id.md) | Voice ID on the speaker with the firmware's own biometry models (ECAPA voiceprint, gender, age): the models and the desk check; how it is built — `spotter-host voice` (the vendor's TFLite resolver), the classifiers in Go, the verdict, ignoring guests before `run-pipeline` (`voice_guest`), HA's sensors, the store and the album (grouping, pins, the API), settings; the server proxy alternative; how Alexa, Google, Siri and Alice enroll voices |
| [music-sendspin.md](music-sendspin.md) | The speaker as a Music Assistant (Sendspin) player: sendspin-host, the shared dmix, wake word over music (hardware AEC reference), ducking, linked/separate volume, connecting and pairing, measurements, announce chime, the group-sync calibration plan (chirps + least squares) |
| [intercom.md](intercom.md) | Two-way 1-to-1 calls between speakers: plan and v1 design — VQE uplink, RTP/L16 over UDP with AES-GCM, jitter buffer and drift, MQTT signalling, the voice scenario, HA entities, device checklist |
| [wifi-setup.md](wifi-setup.md) | Wi-Fi setup mode: the recovery access point (key combo), our hostapd build (the firmware's wpa_supplicant has no AP mode), DHCP/DNS/captive page in the daemon, the page's home settings (broker, probes, NTP, MA), the handoff back to wpa_supplicant, crash recovery, boot without a network |
| [sensor-calibration.md](sensor-calibration.md) | Calibrating the light sensor into lux and the mic level into dB SPL: per-board base (`lux.calibration`, `mic.spl_offset`) and per-speaker overrides (`lux_calibration`, `mic_spl_offset`), the procedure with snippets, our lux-meter measurements, the spread between units, the vendor's SPL calibration |
| [storage.md](storage.md) | Storage and logs on the speakers: the flash per board (NAND/UBI vs orion's eMMC), measured wear and write rates, what the daemon writes to flash, where the logs live and how they rotate (all tmpfs); logs shipped to a LAN syslog server with a flash spool during outages (`log_remote`); the network watchdog |
| [design.md](design.md) | The look: the deltoid logo and its files (generated by `docs/brand/make-logo.py`), what it must not become (Alice's marks, violet), the palette (the logo's deep blue `#0A4B9E` to sky `#2FB8F2`; warm colours only for statuses) as tokens for light and dark with their meaning and contrast, type, the layout patterns (speaker card, settings rows), motion, accessibility, copy |

## The boards

| Document | What it covers |
| --- | --- |
| [boards/README.md](boards/README.md) | The boards: the supported ones (codename, retail model, DT id), how board profiles are picked, Yandex's codenames and the crossed "midi", ports we don't plan (Mini 2, Station Midi, Station 2); porting itself is [porting-a-speaker.md](porting-a-speaker.md); per-board notes ([bergamot](boards/bergamot.md), [cucumber](boards/cucumber.md) — the retail Station Midi, wiki data only, not planned; [yandexmidi](boards/yandexmidi.md) — the Station 2 (internal "midi"), from firmware, not planned; [orion](boards/orion.md) — Station 3: install notes, 96 kHz mroute capture, NPU wake verifier, screen patterns at 20 ms; [pickle](boards/pickle.md) — incl. the Mini 3 Pro connectivity/Wi-Fi-hop logic, running without maind and the bottom pogo-pad module connector) |

## Plans and debt

| Document | What it covers |
| --- | --- |
| [wishlist.md](wishlist.md) | Ideas under consideration, not planned (built ones leave it for their area's doc): pipeline robustness, "someone is asleep here" detection, a dead-daemon face, an activity blinker, orion's ultrasound rig, the mics as a measurement mic, persistent logs, swipe gestures, the ambilight's power budget, arbitration vs the context spotters, CI and test gaps |
| [backlog.md](backlog.md) | Technical debt from reviews: correctness issues, duplication, oversized functions and files, hardcoded values, dead code, doc debt — each with where, the fix, size and risk |

## Tools

| Document | What it covers |
| --- | --- |
| [../emu/README.md](../emu/README.md) | QEMU emulator for boot experiments: substitute kernel with board stubs (`emu/kernel/`), flash images from the dumps, maintenance initramfs, a CFW twin seeded read-only from a unit, smoke and power-cut tests |

Also: [`../yastationd/README.md`](../yastationd/README.md) (daemon usage, entities, open items) and
[`../spotter-host/README.md`](../spotter-host/README.md) (vendor spotter/VQE host, on-device results).

## Conventions

- One doc per topic/area (architecture, an external API, a parsing quirk, a decision record).
- What goes where (specs here, user guides in `guides/`, research in `research/`):
  [README.md](README.md).
- Record hard-won knowledge: API quirks, edge cases, known limitations, and the *why* behind decisions.
- When code changes invalidate a doc, update the doc in the same change.
