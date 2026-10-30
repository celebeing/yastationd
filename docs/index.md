# Docs index — yastationd

This is the index of project documentation. Read the relevant doc here **before** planning work, and keep this index in sync when you add, rename, or remove docs.

How the docs are laid out and what goes where: [README.md](README.md). This index lists the
**specifications** (what the CFW does and how, as built) and the developer guides. The other kinds
have their own indexes, read when the task needs them:

- [guides/](guides/README.md): **user guides** — installing and updating the CFW, connecting it to
  Home Assistant (the voice assistant, music, the intercom), sending diagnostics, troubleshooting.
- [research/](research/README.md): **research** — the firmware's decompiles, the hardware teardown,
  measurements, the original design and the alternatives we chose against.
- [boards/](boards/README.md): the supported boards and per-board notes.

## Start here: the map and the developer guides

| Document | What it covers |
| --- | --- |
| [architecture.md](architecture.md) | **Start here:** what runs where, how it talks to HA and the other speakers, the daemon's areas, time without the internet |
| [adding-a-feature.md](adding-a-feature.md) | **Agent guide:** a feature the house way — settings, moments, HA entities, the web app's widgets, the API, child processes, tests |
| [fixing-a-bug.md](fixing-a-bug.md) | **Agent guide:** from a symptom to the code, reproducing without risking a speaker, a regression test, a dev unit, the changelog |
| [porting-a-speaker.md](porting-a-speaker.md) | **Agent guide:** a new speaker model — the ground rules (no trial-and-error on unknown components), the survey, the profile keys, verification, the voice front-end |
| [building.md](building.md) | **Agent guide:** `tools/build.sh`, the pinned builder containers, the vendor sysroot, one board's dist |
| [releasing.md](releasing.md) | Versions, the checklist before tagging, cutting a release |

## Features: how they are built

| Document | What it covers |
| --- | --- |
| [settings.md](settings.md) | The registry of config keys and its flags, live reload, the settings pages, sharing settings between speakers |
| [web-ui.md](web-ui.md) | The web app and its server: login, the JSON API, the vitals, discovery of the other speakers, `/metrics`, the config editor |
| [display-expressions.md](display-expressions.md) | The display engine: surfaces, expressions, scenarios, the front catalogue per display kind, the stock clip formats |
| [asset-packs.md](asset-packs.md) | Moments (every reaction), the default theme, the rings and stock sounds a default can use, the theme stack, packs and their format |
| [assets.md](assets.md) | The owner's media on the speaker (videos for the video mode) |
| [input.md](input.md) | Keys, the mic-mute latch, the touch ring: from evdev to moments, per board |
| [health.md](health.md) | What the speaker watches about itself (probes, lifecycle, power, network, supervisor, vitals, thermal) and the patterns for a new monitor |
| [status-indication.md](status-indication.md) | Booting, first connection, degraded, no HA: what the ring and the display show |
| [led-preview.md](led-preview.md) | The frame tap, the host simulator `ledsim`, the README's GIFs |
| [video-mode.md](video-mode.md) | Videos on pixel screens with sound in sync |
| [ambilight-stream.md](ambilight-stream.md) | Station 3's rear strip: the DDP receiver, our music effects, the framebuffer path |
| [ambilight-color.md](ambilight-color.md) | Station 3's ambilight colours: the measurement, the calibration, the RGBW solver |
| [wake-word.md](wake-word.md) | The wake word: `spotter-host serve`, events, per-board models, the sensitivity levers |
| [wake-word-boundary.md](wake-word-boundary.md) | Where «Алиса» ends in the audio sent to HA |
| [wake-arbitration.md](wake-arbitration.md) | Which speaker answers when several hear «Алиса» |
| [voice-slow-replies.md](voice-slow-replies.md) | Slow and stuck replies from HA: the deadline, «стоп», interrupting, the wedge watchdog |
| [voice-id.md](voice-id.md) | Who is talking, on the speaker: the voiceprint host, the verdict, ignoring guests, the People album |
| [music-sendspin.md](music-sendspin.md) | The speaker as a Music Assistant player: `sendspin-host`, ducking, volume, the wake word over music |
| [intercom.md](intercom.md) | Calls between speakers: audio, signalling, the voice scenario |
| [ssh.md](ssh.md) | SSH on the speaker: dropbear (default) or OpenSSH (certificates, SFTP), how it is chosen, switching through port 2222, boot |
| [wifi-setup.md](wifi-setup.md) | The Wi-Fi setup mode: the key combo, the access point, the setup page, the handoff back |
| [sensor-calibration.md](sensor-calibration.md) | The light sensor in lux and the mic in dB SPL: board base and per-speaker overrides |
| [storage.md](storage.md) | Flash and its wear, what the daemon writes, logs and their shipping, the network watchdog |
| [design.md](design.md) | The look: the logo, the palette, type, layout patterns, copy |

## The boards

| Document | What it covers |
| --- | --- |
| [boards/README.md](boards/README.md) | The supported boards (codename ↔ retail model), how profiles are picked, Yandex's codenames, ports we don't plan; one doc per board: [bergamot](boards/bergamot.md) (Mini 3), [pickle](boards/pickle.md) (Mini 3 Pro), [orion](boards/orion.md) (Station 3), [cucumber](boards/cucumber.md), [yandexmidi](boards/yandexmidi.md) (not planned) |

## Plans and debt

| Document | What it covers |
| --- | --- |
| [wishlist.md](wishlist.md) | Ideas under consideration, not planned (built ones leave it) |
| [backlog.md](backlog.md) | Technical debt from reviews, each with where, the fix, size and risk |

## Tools

| Document | What it covers |
| --- | --- |
| [../emu/README.md](../emu/README.md) | The QEMU emulator for boot experiments |

Also: [`../yastationd/README.md`](../yastationd/README.md) (daemon usage, entities, open items) and
[`../spotter-host/README.md`](../spotter-host/README.md) (vendor spotter/VQE host, on-device results).

## Conventions

- One doc per topic/area (architecture, an external API, a parsing quirk, a decision record).
- What goes where (specs here, user guides in `guides/`, research in `research/`):
  [README.md](README.md).
- Record hard-won knowledge: API quirks, edge cases, known limitations, and the *why* behind decisions.
- When code changes invalidate a doc, update the doc in the same change.
