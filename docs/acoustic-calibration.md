# Acoustic measurement: the speaker measures its own room response

Status (2026-10-31): first on-device pass on Station 3 (orion) — measure and report only; no
correction, no filter applied anywhere.

The host-side analysis (cross-spectrum, one-third-octave bands, motion rejection) that this grew
from: the measurements in [boards/orion.md](boards/orion.md) ("Mic capture"); the analyzer stays a
host tool, `cmd/sweep-analyze` run by `tools/analyze-sweeps.sh` ([building.md](building.md)). The
captures it reads come from the research scripts `tools/orion-safe-sweep-capture.sh` (a sweep),
`tools/orion-safe-tone-capture.sh` (a 1 kHz tone), `tools/capture-raw.sh` (a plain capture) and
`tools/orion-audio-inspect.sh` (the mixer and PCM state); each states its bounds in its header.

An opt-in measurement the owner starts from **Settings → Calibration**: the speaker plays three
soft audible 4 s sweeps (~20 s in all) while recording itself through the direct LOOPBACK-A capture
(`hw:0,3`, 14 ch @ 96 kHz). The wake word and voice capture pause for the run and resume on
their own; music, calls and speech must be stopped first, and the API refuses to start otherwise.

Nothing automatic ever runs it: not a page open, not a restart, not an upgrade, not HA. It makes
sound only when the owner presses the button, on the unit they pressed it on.

## Safety envelope

- **The sweep level is capped at −40 dBFS** (the level verified on device, [boards/orion.md](boards/orion.md)).
  The owner may only pick the standard level or quieter (−45, −50); there is no way to request
  anything louder.
- **Playback rides a pinned mixer ceiling**: `Master Playback Volume` is set to raw **134** — the
  verified value for a −40 dBFS sweep — regardless of the ordinary volume, and restored exactly
  after the run (or on cancel, or on failure). The ordinary volume control cannot make the
  measurement louder, and the measurement cannot leave the volume changed.
- **Speech gate**: `POST` returns `409` while the speaker is playing sound, so a measurement
  never mixes with music or a call.
- **The mics' route is temporary**: `Loopback tdmin lb source` is switched to TDMOUTB (the
  speaker-reference slots) and restored after; the daemon's mic capture is suspended for the run
  (`hw.Mic.Suspend`) so the devices are free, and comes back by itself.

## The run

1. `Mic.Suspend` (arecord closed, subscribers kept), route and volume restored by
   `Mixer.SetRestoring` on the way out — all restore paths run on cancel, error and success.
2. Each capture: open `hw:0,3`, wait out the PDM open transient (1.5 s, measured: mic samples
   pinned at ±32766 until 1.01–1.05 s in), then play one faded 4 s, 100 Hz→10 kHz log sweep
   at 48 kHz mono through the stock playback chain and record 7 s.
3. On-device sanity check per capture, past the transient window: the reference channel's peak
   must be at least 90 % of the requested level, and no mic channel may touch full scale —
   without the skip, the transient made every capture fail on a live unit. A capture that
   fails either check ends the run with a reported reason; three passes are wanted.
4. **No raw audio is kept**: frames are inspected in memory and discarded. The compact per-run
   result (peaks, clipping flags, accept verdicts) replaces the JSON report below.

## API and storage

`GET/POST/DELETE /api/calibrate/acoustic` ([web-ui.md](web-ui.md), behind the login); the
Calibration page polls it once a second while a run is active and shows progress, cancel, and
each capture's verdict.

| File | When |
|---|---|
| `<cfw>/acoustic-calibration.json` (~1 KB, temp + rename) | once per completed or cancelled run — the last run's status only, never audio ([storage.md](storage.md)) |

## Report only

The result is a report on the page and in that file. There is deliberately **no correction
path**: no EQ is computed, no filter is loaded, nothing changes how the speaker sounds. Extending
this to a correction remains an explicit opt-in decision ([wishlist.md](wishlist.md)).
