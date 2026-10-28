# Wake-word arbitration: the nearest speaker answers

When several speakers hear one «Алиса», only the loudest — the nearest — answers. Built
2026-10-26 (`internal/app/arbitration.go`); on by default, `arbitration = false` turns it off.
Background and other projects' lessons: "Background" below.

## How it works

1. **The level.** On a wake word the speaker takes the level of the word itself from its raw voice
   mic channel (before the VQE and its AGC, which would erase the distance): the energy average
   of the 8 loudest 20 ms blocks of the last 1.2 s, the ring the room sound sensors keep. It adds
   the board's mic calibration (`mic.spl_offset`, [sensor-calibration.md](sensor-calibration.md)),
   so the levels of different models compare in dB SPL — in dBFS a Station 3 reads 9 dB below a
   Mini 3 in the same sound and would always lose.
2. **The claim.** It posts `{"id", "level", "at"}` on `<topic_prefix>/arbitration` (MQTT, QoS 0,
   not retained) and starts buffering the speech (the pre-roll and what follows), so nothing said
   after the wake word is lost while it decides.
3. **The decision,** by each speaker on the claims it has, the same rule everywhere:
   - a louder claim (from a speaker that heard it earlier or later) → it lost, at once;
   - else it waits: 200 ms when its own claim is loud (≥ 25 dB over the room's floor), up to
     `arbitration_wait_ms` (default 300; 700 until 2026-10-28) when it is quiet — a far speaker
     that claims first must not take a request meant for the near one;
   - claims arriving within 1.5 s of each other are one round; within 0.5 dB the lower device id
     wins.
4. **The winner** starts the session with the buffered speech (the `wake_word` button event goes
   to HA from it only); **the losers** show the `arbitration_lost` moment (seg7: our `glance` clip, ` > >` then `< < `;
   Station 3: the stock `Deactivation_Output`) and stay silent: no session, nothing sent to HA.

Not arbitrated: the voice key, follow-ups, a wake during an intercom call, a wake that interrupts a
pending reply. With no broker connection, no mic levels yet, or the feature off, a speaker answers
as before. If a claim is lost on the way, two speakers may answer — the behaviour without
arbitration.

Every claim and decision is logged (`arbitration: claim / claimed / won / lost`, with levels), so
a wrong answer can be explained from the logs ([storage.md](storage.md): they reach the log
server).

## First test (2026-10-26)

Four speakers side by side on the floor, two of them 10 cm apart; «Алиса, который час?» at normal
voice from a couple of metres, first from the Station 3's side, then from the Mini 3's. Only those
two heard the wake word; in each of six rounds the level gap was 1–2 dB, and every time the
speaker on the caller's side answered, the other blinked. Loud claims decided in 100–200 ms,
quiet ones waited out the 700 ms.

## How far apart the claims come (2026-10-28)

From the logs of every round two or more speakers claimed between 2026-10-26 and 10-28 (25
rounds, Mini 3s and Station 3s, through the one broker), measured on one speaker's clock: the
last claim came 3–141 ms after the first, median 68 ms. A quiet claim waited 700 ms for that —
and so did every request only one speaker heard, the common case. The default wait is 300 ms:
twice the worst spread seen, 400 ms off every quiet request. Reverb makes a far speaker's
spotter fire later (by about 100 ms on synthetic speech): if a far speaker ever answers
instead of a near one, look at the round's spread in the logs before raising it.

## «Стоп»

«Стоп», «хватит», «отмена», «выключись» (the local command spotter) end a voice session in
every phase: while it listens (the connection is dropped before HA turns the speech into a
request — a lone "Stop." was taken for "cancel the timers"), while HA thinks (the pending reply
is cancelled), while it speaks (playback stops and no follow-up opens).

## Background: what it was designed from

The idea as it was weighed in the wishlist (2026-10-25). When several speakers hear the wake word, only the closest should answer. Without arbitration HA's
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

Refinements from another DIY multi-speaker project (where several speakers on custom firmware
share one apartment), each one found on a real failure:
- **A quiet first claim waits for louder ones.** A fixed window favoured the far speaker: it
  claimed first, and the room it was in got the command (the wrong room's light was switched).
  A quiet claim (low level) now waits up to ~1.5 s for a louder one; a loud claim decides at once.
  For us that is an adaptive window instead of pulsar's fixed 250 ms: short when the claim is
  loud, long when it is quiet.
- **Claims that arrive together are one round,** even if the speakers' timestamps for the phrase
  start differ by seconds. They do differ, because of a different number of segments or an
  utterance opened by loud music. Decided phrases are remembered in a ring for ~1 min, not as a
  single "last" that the next claim overwrites, and a round's timer never closes another round.
  Log every claim (`claim: <speaker> <level> <decision>`); without that, double answers can't be
  explained afterwards.
- **A neighbour's reply is not a new command.** A speaker that hears the next room's spoken
  answer («включил свет на кухне») can take it as a command. Their filter: a phrase is an echo of
  a reply only if it has no long word the reply didn't have. For us this matters less, since we
  need a wake word before every command, but it applies to HA's `continue_conversation` reopening
  the mic.

We went with MQTT claims instead of UDP broadcast (the broker already connects the speakers), the
raw voice mic for the level, and the adaptive wait above.

## Not done

- A neighbour's spoken reply taken for a request — matters less here, since every request needs a
  wake word.
- A busy speaker (playing, ringing) does not claim at all: it ignores the wake word while making
  sound (no AEC verified yet).
