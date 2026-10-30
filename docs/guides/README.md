# Guides

How to set things up and use them: installing the CFW, connecting it to Home Assistant, sending
diagnostics. Steps for an owner (or an agent acting for one), not how the code works; that is in the
specs one level up ([../index.md](../index.md)).

| Guide | What it covers |
| --- | --- |
| [deploying.md](deploying.md) | **Agent guide:** install, update (`tools/deploy.sh`), HA side, disable/remove, keeping the vendor offline (IPv4 + IPv6 egress lock, the OTA key guard, DNS), verification checklist, diagnostics and gotchas (reboot reason, pstore, ALSA, BusyBox) |
| [diagnostics.md](diagnostics.md) | Collecting diagnostics to send: `diag.sh` on the speaker (one redacted archive: versions, config, logs, network, vendor guards, ALSA from `/proc/asound`) and `tools/diag-build.sh` on the host (git, docker and the builder images' hash labels, sysroots, artifacts); flags, what is inside, the redaction, commands never to run (the 2026-10-28 `amixer contents` kernel freeze), the download from the web app (Status → Log, `GET /api/diag`) |
| [ha-assistant-setup.md](ha-assistant-setup.md) | Reference: setting up the voice assistant in HA — STT/TTS servers, conversation agent, Assist pipeline, adding the speaker, troubleshooting, a character voice (RVC over Silero) |
| [ha-music-setup.md](ha-music-setup.md) | **Guide:** music by voice — Music Assistant server + Spotify, the speakers as players, HA integration, the search/play scripts and automations (templates in `hass/music/`), LLM prompt rules, gotchas |
| [ha-intercom-setup.md](ha-intercom-setup.md) | **Guide:** the intercom with HA — speaker config, the entities, the "call a room" script for «Алиса, позвони в спальню», voice control during a call, automation ideas |
