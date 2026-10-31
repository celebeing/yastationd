# Guides

How to set things up and use them: installing the CFW, connecting it to Home Assistant, sending
diagnostics. Steps for an owner (or an agent acting for one), not how the code works; that is in the
specs one level up ([../index.md](../index.md)).

| Guide | What it covers |
| --- | --- |
| [troubleshooting.md](troubleshooting.md) | **Something doesn't work:** by symptom (no reaction to «Алиса», what the ring shows, HA shows it unavailable, no sound or music, the intercom, after an update), each check marked read-only, makes sound or changes something, and where it is explained |
| [deploying.md](deploying.md) | **Agent guide:** install, update (`tools/deploy.sh`), HA side, disable/remove, keeping the vendor offline (IPv4 + IPv6 egress lock, the OTA key guard, DNS), verification checklist, diagnostics and gotchas (reboot reason, pstore, ALSA, BusyBox) |
| [backup.md](backup.md) | Backing up a speaker and putting it back (`tools/backup-unit.sh`, `tools/restore-unit.sh`): what is in a backup and why HA and Music Assistant depend on it, what the restore writes and what it never touches (Wi-Fi by default, the raw partitions) |
| [golden-image.md](golden-image.md) | A factory reset that lands in the CFW instead of the vendor firmware (`tools/golden.sh`, pickle tried, orion not): building and writing a restore image, the first boot, the way back. |
| [diagnostics.md](diagnostics.md) | Collecting diagnostics to send: `diag.sh` on the speaker (one redacted archive: versions, config, logs, network, vendor guards, ALSA from `/proc/asound`) and `tools/diag-build.sh` on the host (git, docker and the builder images' hash labels, sysroots, artifacts); flags, what is inside, the redaction, commands never to run (the 2026-10-28 `amixer contents` kernel freeze), the download from the web app (Status → Log, `GET /api/diag`) |
| [ha-assistant-setup.md](ha-assistant-setup.md) | Reference: setting up the voice assistant in HA — STT/TTS servers, conversation agent, Assist pipeline, adding the speaker, troubleshooting, a character voice (RVC over Silero) |
| [ha-music-setup.md](ha-music-setup.md) | **Guide:** music by voice — Music Assistant server + Spotify, the speakers as players, HA integration, the search/play scripts and automations (templates in `hass/music/`), LLM prompt rules, gotchas |
| [ha-intercom-setup.md](ha-intercom-setup.md) | **Guide:** the intercom with HA — speaker config, the entities, the "call a room" script for «Алиса, позвони в спальню», voice control during a call, automation ideas |
