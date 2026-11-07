# A light pulse from the computer's music (research)

**Status (2026-11-08): measured; the scheme holds.** Nothing is built beyond the reference capture
([architecture.md](architecture.md)).

## The idea

Lights that follow music played on a computer usually get a stream from it: pixels (DDP, E1.31)
or a spectrum (WLED's audio sync), tens of frames a second. The bytes are no problem; the timing
is: a frame crosses Wi-Fi in 5 to 200 ms with outliers, and nothing accounts for the path from the
computer's speakers to the listener, so a flash lands on the beat or off it at random.

Instead, the output of all this is a **pulse model**, not a data stream:
`phase(t) = (t − t₀) · bpm / 60 mod 1` plus an energy envelope. A speaker evaluates it locally at
any moment, and anything can be modulated by it (a brightness, a speed, a colour). Its parameters
come from two places:

- **The computer says what:** the tempo and the beat grid from the clean digital signal (the
  bass, see below), as a prediction (beats in the future), so a late packet costs nothing; plus a
  coarse envelope of the highs for matching, about a byte per 10 ms.
- **The speaker's mics say when:** the speaker matches the envelope of the highs it hears against
  the computer's; the lag found covers the network, the clocks and the room at once. No NTP.

## What the speaker hears (Station 3, quiet music from the computer's speakers)

Raw mic captures (the six mics averaged) and the same run through the vendor VQE (`vqe_0`, what
the daemon gets continuously from `spotter-host serve` on fd 3):

| Signal | Music | Between tracks | Use |
| --- | --- | --- | --- |
| broadband, raw | −57…−64 dBFS | −63…−65 | none: at this volume it is the room's noise |
| lows < 150 Hz, raw | about the same | about the same | none |
| highs > 2 kHz, raw | −80 | −101 | +20 dB: the hi-hats and snares come through |
| `vqe_0`, lows | −98 | −98 | none: the VQE removes the lows entirely |
| `vqe_0`, highs | −68…−80 | −90 | as good as raw for matching (below) |

So the mics give the highs only. A fixed high-pass and an RMS per 10 ms; no spectrum.

## Tempo from the mics alone: no

On a steady track the highs' tempo (autocorrelation of the onsets, 8 s windows) held 136.2 bpm in
11 of 14 windows; the misses were ¾ and ½. On a track with a 3-against-4 hi-hat pattern (true
pulse 130 bpm, steady in its clean lows) the mic said 171, 87, 65, 130, 172…: always a ratio of
130, rarely 130. The clean signal's own highs say 172.7 and 86.8 there. **The tempo has to come
from the computer, from the bass or the whole band.**

## Phase against the clean track: about 10 ms

The same track's clean audio (the published recording) against the mic capture, high-band onsets
cross-correlated: the track's start was found unambiguously (0.30 against 0.22 for the next
candidate). The lag on each 4 s chunk then stayed within ±10 ms (one envelope frame) in 12 of 13
chunks on `vqe_0` (the outlier, +70 ms, at the quiet start) and in 10 of 13 on the raw mics (two
off by a sixteenth). Light against sound looks out of sync from about 20–40 ms.

## Live: the computer's capture against the speaker's mics

4 minutes at once (2026-11-08): `record` on the computer (Windows, 44.1 kHz) and Station 3's mics
2–3 m away, quiet music at 87 bpm, two tracks with a quiet stretch. High-band onsets, the lag
found per 5 s chunk within ±200 ms of the overall one:

| | raw mics | `vqe_0` |
| --- | --- | --- |
| overall match (correlation) | 0.29 | 0.49 |
| chunks with a confident match | 33 of 47 | 35 of 47 |
| of those, a sixteenth off (±172 ms at 87 bpm) | 2 | 1 |
| the rest: lag spread (sd), worst | 3.2 ms, 13.5 ms | **0.8 ms, 2.4 ms** |
| drift over the 4 minutes | +4 ms per 1000 s | −5 ms per 1000 s |

- **The lag is stable to a millisecond on `vqe_0`.** It is the whole path at once: the computer's
  output buffer, its speakers, the room, the speaker's capture and the VQE (whose own delay shows
  as ~90 ms between the raw and the `vqe_0` lag).
- **The two audio clocks drift by about 5 ppm**: 0.3 ms a minute, nothing a tracker would notice.
- **The misses are whole sixteenths**: once locked, a tracker searching ±50 ms around its lag
  never sees them. Quiet stretches (12 of 47 chunks) give no match; the model coasts through them.
- The computer's timestamps against the speaker's (the start of each capture) were off by ~0.2–0.3 s
  (ssh and `arecord` starting up), so the mics' match does the real work; a prior within a
  second is enough.

## Open

- Where the matching runs (the speaker, from `vqe_0` in the daemon), what goes over the network
  and how often, how a pulse reaches effects (the ambilight's music effects, the rings).
