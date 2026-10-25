# Music: the speaker as a Music Assistant (Sendspin) player

Status (2026-10-23): **working on one bergamot unit** — music from Music Assistant (MA 2.10.7,
Sendspin protocol 1.0) plays, the wake word works over it, music ducks under the assistant, the
player volume is linked to the speaker's, a timer rings over music and «стоп» silences only the
ring (all checked by ear). Rolled out to all three speakers (two bergamot, one pickle): all
register in MA and survive a reboot; music has been played on one bergamot so far. Open items at
the end.

## Architecture

```
 Music Assistant ──Sendspin (WebSocket, Noise)──▶ sendspin-host ──▶ ALSA "default"
                                                       ▲              (plug → dmix → hw:0,0)
                       duck / volume (stdin)           │                   ▲
 yastationd ───────────────────────────────────────────┘     aplay (TTS, announcements, timer)
            ◀── READY / CONNECTED / STREAM / VOLUME (stdout)
```

- **`sendspin-host`** (`../sendspin-host/`) is a native child process of yastationd, the way
  `spotter-host` is: the official [sendspin-cpp](https://github.com/Sendspin/sendspin-cpp) client
  (player role, FLAC + PCM; no Opus, not worth the CPU) plus an ALSA sink. It has to be native:
  Sendspin's group sync needs sample-accurate output timing, and dmix is a libasound plugin, so it
  links the firmware's own libasound. yastationd stays a static Go binary.
- yastationd starts it (with backoff on exit), passes the name, state file, port and volume on the
  command line, and steers it over stdin: `duck <0..1>`, `volume <0..100>`. Events come back on
  stdout; its log lines go to yastationd's log.
- Build: zig 0.14.1 targeting the firmware's glibc (`arm-linux-gnueabihf.2.30` for bergamot,
  `aarch64-linux-gnu.2.36` for pickle), libc++ static; libasound is linked against a sysroot built
  from the alsa-lib tarball with the firmware's symbol versioning (bergamot 1.2.1.2 unversioned on
  both 0.369 and 0.6.x, pickle 1.2.10 versioned). The build glue follows
  [pulsar](https://github.com/Morvex885/pulsar) (MIT), which solved the same problem for other
  Stations.

## One speaker for two: the shared mixer

There was nothing to build here. The stock `default` PCM on both boards is `plug → dmix (ipc_key
1024) → hw:0,0`, and yastationd's aplay already plays through it. sendspin-host opens the same
`default`, so music and voice are mixed by dmix and neither holds the device exclusively. The sink
opens the PCM lazily at stream start and closes it at stream end.

## Wake word over our own music

The AEC reference is already post-mix: the VQE's speaker channel is the **amplifier's hardware
loopback** (bergamot capture ch 6, pickle ch 3, `wake.ref` in the profiles), which carries
whatever the amp plays, music included. So nothing has to be fed to the VQE; the only change was
policy: the "ignored while playing" rule covers our own output (TTS, alarm), not music. Measured
by ear on bergamot: «Алиса» triggers over music at moderate volume. The test matrix (music at
30/60/80 % × 1/3 m, hit rate and false triggers from the music itself) is still to do.

## Ducking

While the assistant is busy — a voice session, a reply being computed, HA audio streaming to us
(also while the jitter buffer waits), our own playback, a ringing timer — the music goes down by
`music_duck_db` (default −26 dB). The level ramps over 0.3 s in the sink (no clicks) and comes back
1.5 s after the assistant is idle, so it does not swell between a reply and its follow-up listening.
yastationd polls the state every 100 ms and sends `duck` on change; a restarted host gets the
current level again. Music is never stopped or paused by the assistant.

**Timer over music:** the alarm plays through dmix too, the music ducks under it. The local «стоп»
silences the ring first (or cuts a TTS reply); with only music playing it does nothing locally —
the command spotter is armed only while *we* make sound. Media commands go through HA → MA.

## Volume

Two modes, `sendspin_volume` in yastationd.conf:

- **`linked` (default):** the player volume in MA *is* the speaker's master volume. A volume
  change in MA sets the speaker's volume (same curve as HA's slider); the keys, HA and «громче» are
  reported back to MA. The host is started with `-G` and applies no gain of its own. This is how a
  physical speaker behaves, and it keeps MA from showing one level while the speaker plays another.
  Side effect: MA's volume also sets the assistant's loudness.
- **`separate`:** MA's volume is a music-only gain under the master volume (quadratic curve,
  `(v/100)²`), persisted as `music_volume` in the state file; the keys and HA change everything.

At start the host announces a volume (`-V`): the library's default is 0, which MA adopts as the
player volume (seen on the first test: MA showed 0 while the speaker played).

The volume key tick is skipped while music plays: the music's own level is the feedback, and a
tick mixed over it sounded off.

## Connecting to Music Assistant

- The host always listens on `ws://<speaker>:8928/sendspin` (`sendspin_port`); MA can be told the
  speakers' addresses in its Sendspin provider (manual discovery), and it then connects and
  reconnects by itself. There is no mDNS responder on the speaker yet, so MA does not find it on
  its own.
- With `sendspin_server = ws://<ma-host>:8927/sendspin` the host also dials MA itself. The
  library's `connect_to()` is a single attempt and never redials, so the host redials while it is
  disconnected (5 s, doubling to 60 s). Found by a reboot test: the host starts before the network
  is up, and its one attempt was lost. That is what
  runs now: no MA-side configuration needed. (Don't set both a manual IP in MA and
  `sendspin_server` for the same speaker without checking how MA treats two connections from one
  client.)
- The friendly name is `sendspin_name` (default `device_name`); use the speaker's name in HA, so
  the MA player can be put in the same room. The client_id is derived from a static keypair stored
  in `sendspin_state` (`<cfw>/sendspin/state.json`, 0600), so it survives restarts and updates.
  MA wraps each Sendspin player in a "universal player" of the same name.
- HA integration and voice control of the players: [ha-music-setup.md](ha-music-setup.md).

### Encryption and pairing

sendspin-cpp ≥ the Sendspin 1.0 implementation always encrypts (Noise KKpsk2) and has pairing.
We run with **unpaired access** on (`sendspin_unpaired = true`): a server that is not paired may
play. That is acceptable here because the speakers are LAN-only and have no internet, but anyone
on the LAN could stream to them. Pairing (a Pairing PSK token, or a pairing code) would need a way
to show the code or hand over the token; not done yet. The state file holds the private key and
the PSKs; the library logs the Pairing PSK at info level when it first provisions it, which is why
the host runs at `-l warn`.

## Measurements (bergamot, 2026-10-23)

| | |
|---|---|
| Binary | 1.4 MB (armhf), 1.5 MB (aarch64), stripped |
| RAM | 0.6 MB RSS idle, ~4 MB while streaming; 7 threads |
| CPU | ~20 % of one core while playing FLAC 44.1 kHz (whole system 72 % idle) |
| Output | 44.1 kHz/2 ch/16 bit from MA (Spotify), dmix delay up to 160 ms, 0 xruns in the first test |

## Latency seen from the speaker

- "Play" in MA to the stream starting took ~4.7 s, almost all of it MA resolving the album's tracks
  from Spotify (`Fetching tracks to play for album`). Not the speaker.
- Pause/resume in MA is a stream stop and a new stream start (MA's flow stream), ~3 s apart in the
  log. Startup on our side is the library's lead time plus `min_buffer_ms` (500 ms default);
  shrinking it is possible but untested.

## Config (yastationd.conf)

```
sendspin          = true                 # off by default
sendspin_name     = <speaker name>       # default: device_name
sendspin_server   = ws://<ma-host>:8927/sendspin   # optional: dial MA ourselves
sendspin_volume   = linked               # or separate
music_duck_db     = -26                  # -60..0
# sendspin_port = 8928, sendspin_unpaired = true, sendspin_state = <cfw>/sendspin/state.json,
# sendspin_bin = <cfw>/bin/sendspin-host
announce_chime    = true                 # see below
```

## Announce chime

Wyoming does not carry HA's preannounce sound. When HA starts audio with no dialog going on (no
voice session, no reply awaited), it is an announcement: yastationd plays the stock notification
sound (decoded 8 dB quieter by `make sounds`, ~1.8 s) and the jitter buffer holds the announcement
until it has played. **Not tested on the device yet.**

## Open items

1. Wake-word test matrix over music (30/60/80 % × 1/3 m: hit rate, false triggers).
2. A synchronized group of two speakers (echo-free?) — needs the second unit rolled out. Tune it
   by measurement, not by ear: [Group sync by measurement](#group-sync-by-measurement-plan).
3. pickle and the second bergamot: play music; on pickle check the ducking and the AEC.
4. The announce chime by ear (length, loudness).
5. mDNS advertisement (`_sendspin._tcp`) so MA finds the speakers without manual IPs or
   `sendspin_server`.
6. Pairing instead of unpaired access.
7. Local media keys (e.g. a long press = pause) would need the controller role.

## Group sync by measurement (plan)

**Status:** plan, not started. The idea comes from another DIY project that put rooted Stations and
PCs into one Sendspin group: by ear their group looked fine, but a measurement showed 328 ms of
spread between players. After calibration it was 2–3 ms.

### What we already account for

- The sink reports each write's finish time as `now + snd_pcm_delay()` (`alsa_sink.cpp`) to
  `notify_audio_played`. That delay covers our ring, the plug/rate converter and the dmix ring, so
  the library schedules against the real queue depth, not "as received". (The other project's
  player missed this and played ~200 ms early. Check that we don't.)
- `set_output_delay_adjustable(true)`: MA's per-player output delay (0–5000 ms) is applied to
  sync timing, so a measured offset can be set from MA without a firmware change.

### What it does not cover, so it has to be measured

- Whatever `snd_pcm_delay()` doesn't see: dmix reports the delay approximately, and the codec, DSP
  and amplifier after the DMA pointer aren't counted at all. bergamot and pickle have different
  chains, so expect a constant offset per board.
- Any mismatch between the players' clocks and MA's that the library's time filter leaves.
- Other players in the same group (a PC, the SHIELD through a Sendspin bridge), whose chains we
  don't control.

### Method: chirps heard by every microphone

1. Each player in turn plays a short chirp (sweep 1→6 kHz, 100 ms, 10 ms raised-cosine edges) at a
   known time T_i on the Sendspin server clock, through the same path as music (sendspin-host →
   dmix), not through aplay. Gaps of ~0.8 s plus random jitter up to ~0.45 s keep chirps from
   different players from overlapping; the first one starts ~3 s after the command so the output
   is open and the delay estimate has settled.
2. Every microphone in the house records the whole sequence at once: each speaker's own mic array
   (one raw channel or the VQE omni output, **not** an AGC'd channel) and, optionally, a PC mic.
3. Per microphone: matched filter against the chirp, then the analytic-signal envelope, so the peak
   doesn't depend on the phase the speaker introduces. The arrival is the first peak ≥ 0.5 × the
   strongest one in the search window (reflections arrive later and can be louder). SNR floor ~8.
4. For mic m and player p: `arrival − T_p = c_m + d_p (+ sound path)`. The mic's own latency `c_m`
   is the same for every chirp it hears and cancels out. Least squares over all heard
   (mic, player) pairs gives `d_p`, the lateness of each player relative to the others. Speakers
   hear each other in both directions, so the ~3 ms/m path mostly cancels on average. Rooms that
   don't hear each other are linked through players that both can hear. A player nobody hears stays
   unmeasured.
5. Apply `−d_p` as each player's output delay in MA (or as a firmware-side calibration that the
   sink approaches gradually, inserting or dropping a frame per block, and persists). Iterate until
   the spread is below ~4 ms; measurement noise is about ±2 ms.

### What the firmware would need

- A way to play a chirp at server time T through sendspin-host (a stdin command next to `duck`
  and `volume`, using the library's server-clock conversion). The host already has the PCM path
  and the clock.
- A timestamped raw mic capture on request (the intercom's `intercom_record_dir` already records
  the raw array capture). It needs a server-clock timestamp for its first sample, so the solver can
  align recordings.
- The solver itself (numpy, ~500 lines) runs off-device on the HA or PC side; it is not firmware.
- Emitting sound on demand follows the doctrine: only when the user expects it.
