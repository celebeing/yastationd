# Acoustic correction on Station 3: design

Status: designed (2026-10-31), not built. The measurement half is shipped
([acoustic-calibration.md](../acoustic-calibration.md)); this is the correction half.
Everything below is **software-only, per-speaker opt-in, reversible**; the amplifier's
I²C/DSP registers are never touched ([porting-a-speaker.md](../porting-a-speaker.md)).

## Goal

Turn the acoustic measurement's accepted captures into a bounded, owner-enabled room
correction: a set of biquad filters applied to everything the speaker plays, computed
from its own measurement, with a hard safety ceiling, a one-tap bypass and a delete.

## Why a custom ALSA PCM plugin (and not LADSPA/CAPS)

Surveyed from this unit's vendor dump (`usr_lib/`, orion 3.21):

- `ladspa/caps.so` **ships on the speaker** — `Eq4p`, `EqFA4p` (4-band parametric) are there.
- `alsa-lib/` has **no `pcm_ladspa.so`** — the stock libasound cannot host CAPS in the chain,
  so an `asound.conf` route through LADSPA is impossible without shipping a new module.

Two viable insertions for a filter that covers *all* playback (our `aplay`, HA's TTS
stream, and music via `sendspin-host`, which all play through the same `default → dmix`
chain):

1. **Our own `libasound_module_pcm_eq.so`** — a small ALSA PCM plugin (a chain of biquads,
   S16_LE 48 kHz, our code, built in the pinned toolchain against the vendor's arm64
   libasound headers in the dump). An `eq` PCM type in `asound.conf` wraps the stock
   `default`; removing one config line reverts the whole thing.
2. A user-space loopback daemon (capture → filter → `aplay`): rejected — doubles the audio
   path, adds latency to voice replies, race-prone with dmix.

Chosen: **(1)** — the plugin is the only insertion point that keeps every consumer
unchanged (they keep opening `default`) and keeps revert to a one-line config delete.

## The correction

**Measure.** The shipped run keeps only accept-verdicts. Correction needs numbers, so the
measurement's inspection step grows a real analysis: per mic channel, the reference-to-mic
cross-spectrum in one-third-octave bands (the math already lives host-side in
`cmd/sweep-analyze`; a trimmed version moves on-device, reusing its FFT). The band medians
across the accepted captures, plus their spread, go into `<cfw>/acoustic-calibration.json`
(still a few KB, still never raw audio).

**Fit.** Only **cuts** in the strictest v1:

- The target is the *speaker's own flat band*, not an absolute curve: correction cancels
  peaks (room modes, boundary boost) and never chases nulls (a dip costs >
  +N dB to fill — wasted headroom and a hot driver).
- Bands where the accepted captures disagree by more than the measurement's own noise
  (median spread > ~2 dB) are left alone.
- Filters: up to 4 notch/shelf biquads (the constraint keeps us inside
  the flat-gain-headroom bar and honest about a 2.1-channel single-box speaker).
- **Hard ceiling:** total gain of the correction at any frequency ≤ 0 dB. Never a boost,
  however compelling the dip — this is the rule that makes miscalibration harmless.

**Apply.** `Eq` config key (registry, `Advanced` off, **not** `Sync` — a room is this
speaker's): `true` = the daemon writes the computed coefficients to the plugin's state
file and installs the `asound` config; explicit, never on measurement completion. Empty =
bypass. `DELETE` on the API + a page control removes the calibration (file + key + config
line) — the bypass/delete contract from [wishlist.md](../wishlist.md).

**Safety.** Coefficients are computed on-device from the band medians with clamped Q and
gain, sanity-checked (bounded b coefficient magnitudes, stable poles), and preceded by a
peak-limiter argument: since v1 only cuts, the filter cannot increase output level; a wrong
correction can only make the speaker quieter or colored, never louder. The mixers' own
`ChL/ChR Volume` ceilings are untouched.

## UI

Same Calibration card, below the report ([web-ui.md](../web-ui.md) widget rules: a
`SettingRows`-style switch, not a new checkbox design):

- the measured band curve + the chosen correction (canvas, one image, no live audio);
- Correction: `[Off | On]` (disabled with a hint until a full accepted run exists);
- Delete calibration, in-place confirm, exactly like the light sensor's.

## Implementation phases

1. On-device band analysis (no behavior change; JSON grows curve fields) + API/UI show it.
2. `pcm_eq` plugin: build, bench on the dev unit by ear at low volume, revert-verified.
3. Fit + apply + registry key + limits + tests (fit math unit-tested against captured
   k1–k3 reference data).
4. Wire into `webserver`/deploy (`tools/deploy.sh`, [deploying.md](../guides/deploying.md)).

Phases 2–3 need the dev orion and the owner's ears; phase 1 is pure code.
