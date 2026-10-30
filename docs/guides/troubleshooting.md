# Troubleshooting: from a symptom to its cause

Look up the symptom, go through its checks in order and stop at the first one that fails. Each
check says whether it is **read-only**, **makes sound** (do it only when the people in the room
expect it, and only on the speaker you mean) or **changes something**. How things work is in the
linked specs; this page only says where to look.

Before you start:

- **The log.** Web app → Status → Log, or `/tmp/yastationd.log` on the speaker (tmpfs: a reboot
  clears it, so read it before rebooting). Read-only. The lines quoted below are its `msg` texts.
- **An archive to send.** Web app → Status → Log → "Download diagnostics", or `<cfw>/bin/diag.sh`
  ([diagnostics.md](diagnostics.md)). Read-only, redacted by default. That guide also lists the
  commands **never to run on a speaker** (reading every ALSA mixer control froze a kernel); don't
  improvise around them.
- **Is the daemon alive?** HA shows the speaker's entities as unavailable when it is not (or when
  the broker is down), and `http://<speaker-ip>/` does not answer. The ring and the display show
  nothing about a dead daemon: there is no "daemon down" indication
  ([status-indication.md](../status-indication.md), "Not covered here").

## No reaction to «Алиса»

| # | Check | How | Kind | Explained in |
|---|---|---|---|---|
| 1 | The mic-mute switch | an amber ring while muted; HA's mic mute binary sensor; the log says `wake: ignored, mic muted` | read-only | [status-indication.md](../status-indication.md) (`mic_muted`) |
| 2 | The wake word is on | `wake_enabled` (web app, Voice settings). Off, only the voice key starts a request | read-only to look | [settings.md](../settings.md) |
| 3 | `spotter-host` is there and running | log at start: `wake: spotter ready` is good; `wake word disabled: spotter binary missing` (never deployed: deploy it), `wake word disabled: board profile has no wake.vqe_preset/model/mics` (a board without a wake profile), `wake: spotter exited, restarting` over and over (it crashes). `ps w` shows one `spotter-host` | read-only | [deploying.md](deploying.md) §6, §7 |
| 4 | Another speaker answered | the log says `arbitration: lost` with the winner's id; the display shows the `arbitration_lost` moment (a glance, then nothing). Normal when two speakers hear you; `arbitration = false` turns it off | read-only | [wake-arbitration.md](../wake-arbitration.md) |
| 5 | Voice ID dropped it as a guest | Ignore guests is on (`switch.<speaker>_ignore_guests`); HA's `sensor.<speaker>_voice` says `guest` (or `unknown` with `voice_unsure = drop`); the log says `voice id: request ignored`; the `voice_guest` moment | read-only | [voice-id.md](../voice-id.md) |
| 6 | HA holds the satellite | the log says `voice: no HA satellite connection` and the speaker answers «К сожалению, что-то пошло не так» with a bright red pulse (`voice_unavailable`). Add or reload the Wyoming integration for `<speaker-ip>:10700` | read-only | [ha-assistant-setup.md](ha-assistant-setup.md) §6, §9; [deploying.md](deploying.md) §3 |
| 7 | A reply is still pending | with `wake_while_waiting = ignore` the log says `wake: ignored, HA is still answering the previous request` and the `wake_busy` moment blinks; the default `interrupt` starts the new request instead | read-only | [voice-slow-replies.md](../voice-slow-replies.md) |
| 8 | The speaker was playing | `wake: ignored while playing`: its own playback must not wake it | read-only | — |
| 9 | The Assist pipeline | it wakes (listening ring) but nothing useful comes back: STT, the agent, the pipeline chosen on the `…_assistant` select | read-only | [ha-assistant-setup.md](ha-assistant-setup.md) §9 |

**Wake word or HA?** Press the voice key once (push-to-talk) and ask something. **Makes sound**:
ask the people in the room first. If push-to-talk works, HA and the pipeline are fine and the
problem is on the wake-word side (checks 1–5, 7, 8); if it fails the same way, look at HA (6, 9).
Do not hold the voice key with the mic muted: 5 s of that starts the Wi-Fi setup mode
([wifi-setup.md](../wifi-setup.md)).

## The ring and the front display

| What you see | Meaning | Explained in |
|---|---|---|
| Ring dark, clock on the display | idle and ready: the normal state | [status-indication.md](../status-indication.md) |
| Blue pulse, the `sleep` clip, no clock | booting: the daemon started and has not reached HA yet | same |
| Red pulse | no first connection within `boot_connect_timeout` (40 s): broker unreachable, or HA has not added the Wyoming satellite. It keeps trying | same; [deploying.md](deploying.md) §3, §7 |
| Dim red pulse after it was ready | degraded (`ha_lost`): HA, the broker or a `probe` target is gone for 10 s. The speaker still answers if HA is reachable | same |
| Bright red flash + «что-то пошло не так» | `voice_unavailable`: a voice attempt found no HA | same |
| Amber ring, held | the mic is muted | same |
| A blinking colon instead of the clock | the time is not confirmed yet (NTP blocked or unset: `ntp_server`) | [deploying.md](deploying.md) §7 |
| Ring frozen or dark, HA unavailable, no web app | the daemon is down: see "After an update" | — |

Every one of these is a moment: a pack or the board profile may have changed how it looks
([asset-packs.md](../asset-packs.md)). The web app's Behaviour tab lists them.

## HA shows the speaker unavailable

1. Does `http://<speaker-ip>/` answer? (read-only) No: the speaker is off the network or the
   daemon is down. Yes: the daemon runs; go on.
2. Is the MQTT broker up and does the speaker reach it? The entities' availability is the daemon's
   MQTT will: a broker the speaker cannot reach looks the same as a dead daemon. The web app's
   card shows the health chip; HA's Health sensor reads `degraded` or `offline` once back. Read-only.
3. The log for `mqtt connected` / `mqtt connection lost` lines and `probe` failures (read-only), or download an archive.
4. Only the voice part unavailable: the Wyoming integration (check 6 above).

## No sound, too quiet, music doesn't play

| # | Check | Kind | Explained in |
|---|---|---|---|
| 1 | The volume: the web app's toolbar mute sets the volume to 0; HA's volume number | read-only to look | [settings.md](../settings.md) ("The speaker card") |
| 2 | `ps w` shows `hardvold` (without it the master volume does not reach the amps) | read-only | [deploying.md](deploying.md) §7 ("Two speakers, different loudness") |
| 3 | Music: `sendspin = true` (**off by default**), `<cfw>/bin/sendspin-host` exists, one `sendspin-host` in `ps w`, the log says `music: player ready` | read-only | [music-sendspin.md](../music-sendspin.md) |
| 4 | Music Assistant has the player: it is not found by mDNS; give MA the address, or set `sendspin_server` | read-only to look | same, "Connecting to Music Assistant" |
| 5 | With `sendspin_volume = linked` MA's player level *is* the speaker's: 5 % there is inaudible | read-only | same, "Volume" |
| 6 | Voice-driven music (search, play): the HA scripts | read-only | [ha-music-setup.md](ha-music-setup.md) §6 |
| 7 | A real sound test: push-to-talk, a TTS from HA | **makes sound** | — |

## An intercom call doesn't connect

| # | Check | Kind |
|---|---|---|
| 1 | `intercom = true` and the **same** `intercom_secret` on both speakers; else the log says `intercom: off, intercom_secret is not set` | read-only |
| 2 | Both reach the MQTT broker (`intercom: off, it needs the MQTT broker for signalling`); the callee shows up as `intercom: peer` | read-only |
| 3 | The callee's name: `intercom: unknown callee` means no speaker has that `intercom_name` | read-only |
| 4 | UDP 10710 (`intercom_port`) open between the speakers | read-only |
| 5 | The callee's mic is not muted and it is not in the Wi-Fi setup mode: both refuse calls (`call_busy`) | read-only |
| 6 | A test call | **makes sound on both speakers** |

Explained in: [ha-intercom-setup.md](ha-intercom-setup.md) (setup, the HA script, limits),
[intercom.md](../intercom.md) (states, signalling).

## Something broke after an update

1. **Which version runs?** HA's Version sensor, the web app's speaker card, or
   `<cfw>/bin/yastationd -version`. Read-only. Two speakers on different versions behave
   differently; deploy all with `tools/deploy.sh`.
2. **Did the supervisor swap back?** Look for `=== supervise: …` lines in the log (`… swapped in
   .prev (bad binary kept as .bad)`) and for `<cfw>/bin/yastationd.bad`. After 5 short crashes in a
   row the supervisor puts `yastationd.prev` back, so the speaker may be running the *previous*
   build without telling you. Read-only. [deploying.md](deploying.md), "Supervisor".
3. **The log from the start**: WARN/ERROR lines right after `=== start`, a helper that is missing
   (`… disabled: … missing`), skipped config lines (the web app's settings pages show them).
   Read-only.
4. **The CHANGELOG** for what changed between the two versions (a renamed key, a new default).

**Rolling back: what exists and what does not.**

- The automatic swap to `yastationd.prev` on a crash loop exists and has been exercised. Each
  deploy keeps only one `.prev`: the build that was there before. [deploying.md](deploying.md)
  ("Supervisor") also gives the manual form of that same swap (run as a script on stdin, §4).
- **Only the daemon** has a `.prev`. `tools/deploy.sh` overwrites `spotter-host`,
  `sendspin-host` and `hostapd` in place; the old helpers are gone.
- **A manual rollback to an older release has not been worked through by the maintainers.** There
  is no procedure; the safer way back is to fix forward or redeploy a known build with
  `tools/deploy.sh`.
- **Config compatibility:** since v0.18.0 the daemon loads its config leniently (an unknown key is
  logged and skipped, [settings.md](../settings.md)). A build **older than v0.18.0** refuses a
  config with a key it does not know (`load config err="…: unknown key"`, exit 1) and then
  crash-loops.

If none of this explains it: download the diagnostics archive and send it with the symptom, the
version before and after, and the time it happened ([diagnostics.md](diagnostics.md)).
