# Wishlist — ideas under consideration

Things we may want, with what is known about them. Nothing here is planned or committed. An item
moves into a design doc (and out of this list) once we decide to build it. Several came from
reviewing [pulsar](https://github.com/Morvex885/pulsar) (fork with the Mini 2 port:
[ste4ik/pulsar](https://github.com/ste4ik/pulsar)). pulsar is a C++ daemon for the Station Midi /
Lite 2 / Mini 2 that speaks the ESPHome Native API to HA. It's a useful reference, but it lets the
vendor stack start at every boot and expects internet access (GitHub updates), so its solutions
need checking against our doctrine before reuse. Both projects are MIT.

## Nearest speaker answers (wake-word arbitration)

**Status:** idea; no need felt yet (several speakers, but no double answers reported so far).

When several speakers hear the wake word, only the closest should answer. Without arbitration HA's
own `duplicate_wake_up_detected` (first phrase per ~2 s wins across satellites) picks whichever
request arrives first, not the nearest speaker.

pulsar's scheme, for reference:
- On a wake word each speaker broadcasts a UDP claim (`<name> <wake word> <busy> <level dB>`, three
  copies 40 ms apart, since Wi-Fi broadcast has no retries) and decides 250 ms later. A busy speaker
  (talking, announcing, ringing a timer) wins; otherwise the louder claim wins; ties go by name. Mic
  audio is buffered during the window, so nothing said right after the wake word is lost. Losers
  blink and stay silent.
- **The level must come from a VQE output without AGC** (they use `VQE_OMNI`), not from the
  gain-normalized channel the spotter hears, which erases the distance. They average the 8 loudest
  of the last 1.5 s of frames.
- HA's `duplicate_wake_up_detected` error is then handled silently (no error ring or sound).

For us: it is protocol-independent and would fit next to the Wyoming satellite. Open questions: how
much the near/far level gap really is on our units (not measured anywhere), and whether the
on-device firewall and the access point pass LAN broadcast.

## ESPHome Native API instead of MQTT + Wyoming

**Status:** a direction worth keeping open; not a rewrite we're starting.

We have already hit Wyoming's limits in the HA integration. Linux Voice Assistant (LVA, a Linux
voice satellite that speaks the ESPHome protocol to HA) is reported to work much better than
Wyoming. pulsar shows that the protocol itself is practical on these speakers.

What it would give:
- one connection for entities, voice and media, with no MQTT broker;
- mDNS discovery in HA;
- first-class `assist_satellite` and `media_player` entities (the latter also visible to Music
  Assistant);
- device logs in HA;
- Noise encryption (today's broker is anonymous and Wyoming is plaintext).

Note that `hass-integration.md` §2.4 ("Why NOT ESPHome") argues against ESPHome the firmware
builder. That argument does not cover the API protocol. Cost: the whole HA integration layer
(`internal/mqtt`, `internal/wyoming`) would be replaced.

## Voice pipeline robustness

**Status:** note; unclear we need it.

Ideas from pulsar:
- A watchdog per phase: a dialog with no pipeline event for 30 s while listening or 120 s while
  processing is closed with an error indication.
- Pipeline events carry no run id, so events that arrive before the new run's start belong to the
  run it replaced and are dropped.
- With several API clients, the voice pipeline goes back to the previous subscriber when the latest
  one leaves, so a test client never strands HA.

### Slow replies (seen 2026-10-23)

**Status:** idea, under consideration.

A request whose tool call took ~46 s in HA («включи Металлику»: MA searched, then started the
music, then the reply was spoken) exposed two timeouts on our side that are shorter than a real
wait:

- The "thinking" comet has the 15 s safety TTL of every voice stage, so the ring went dark while
  HA was still working.
- `awaitReplyMax` (20 s) expired before the reply came. The reply then looked like an
  announcement: it got the announce chime. The music, which HA started ~1.5 s before the spoken
  reply, also came in at full level, because nothing was ducking it any more. The result was music,
  chime and reply almost at once.

Proposal:
1. Separate "a reply is expected" from the wake-word guard. Set it at the transcript, and clear it
   at HA's first audio, a pipeline error or a new session (with a safety expiry of a few minutes).
   Audio from HA while it is set is the reply: no chime.
2. Keep the comet on as long as a reply is expected.
3. Keep the music ducked as long as a reply is expected. Music that HA starts before the reply then
   starts quietly, lets the reply through, and comes up after it (falls out of 1).

Open question: the wake word during a long wait. Today it is ignored inside the `awaitReplyMax`
window, because a new run would make HA cancel the one still computing (and wedge the satellite).
(a) Keep that 20 s window and run 1–3 on the separate flag: the voice stays usable, and a wake
word after 20 s cancels the slow request. (b) Ignore the wake word for the whole wait and
interrupt with the key only.

## Music

**Status:** the Sendspin player is built — see [music-sendspin.md](music-sendspin.md). What
remains here is the wider picture.

- The speaker has no internet access, so only LAN sources qualify: Music Assistant or HA serving the
  stream, never a cloud URL fetched by the speaker.
- pulsar plays HTTP streams through the vendor's GStreamer (`libquasar_gstreamer.so`, dlopen'ed
  into its daemon). It is also a [Sendspin](https://www.sendspin-audio.com/) player for Music
  Assistant: FLAC over the LAN, with group sync in the protocol. For us, vendor code would have to
  run in a helper process like `spotter-host`, never inside the agent.
- Local commands for music: pulsar keeps the command spotter on for a minute after music stops, so
  «продолжи» works after a pause.
- This also covers open item 3 in `yastationd/README.md` (remote-URL announcements).

## Other models

pulsar's `AGENTS.md` has hardware notes for the Midi (`cucumber`: aarch64, glibc 2.36), the Lite 2
(`plum`) and the Mini 2. Their Midi does not match our firmware-only Midi profile (`yandexmidi`:
armhf, glibc 2.30), so there may be two Midi revisions. We don't port models without a unit in
hand: ports are for contributors who have the speaker.
