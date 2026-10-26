# Calibrating the sensors: light and sound

The light sensor and the mics report in their own units, and the units differ a lot from
speaker to speaker. A calibration turns them into lux and dB SPL. Each board profile carries a
base calibration from our measurements; a speaker can carry its own in `yastationd.conf`, which
wins over the profile.

| What | Profile key (base, per board) | Config key (this speaker) | Reported as |
|---|---|---|---|
| Light | `lux.calibration` | `lux_calibration` | HA `Illuminance`, lux |
| Mic level | `mic.spl_offset` | `mic_spl_offset` | HA `Sound floor` / `Sound level`, dB SPL (dBFS without one) |

## Light

### Why a speaker needs its own

All our boards use the same sensor (ltr308) behind a grille, and the grille, the window and the
sensor's place in the case decide what it sees. Measured on 2026-10-26, four speakers side by side
under one ceiling light, white light from 11 to 180 lx, raw sensor counts per lux:

| Speaker | Counts per lux | vs the black Mini 3 |
|---|---|---|
| Mini 3 Pro (pickle) | ~68 | ×9 |
| Mini 3 (bergamot), lilac | ~19 | ×2.6 |
| Station 3 (orion) | ~14 | ×1.9 |
| Mini 3 (bergamot), black | ~7.5 | ×1 |

- **Between models up to ×9, between two units of one model ×2.6.** A board's base calibration
  is a start; a speaker that matters gets its own.
- **The shape is nearly linear** (lux ∝ reading^0.96–0.98): two points calibrate a speaker to
  within ~5 % over 11–180 lx (~3 % rms).
- **Coloured light reads low** — lilac and pink light by 1.5–1.8× on every speaker: the sensor's
  spectral response, which no calibration in white light removes.
- The firmware has no factory light calibration: none in `quasar.cfg`, the board EEPROMs or
  `/data`; maind only has a `darkeningLuxThreshold`.

### What "reading" is

The sensor value the daemon uses: `in_intensity_input` of the light sensor's IIO device (the
driver's own scaling of the raw count: ×2.25 on orion, ÷120 on the minis; with the profile's gain
and integration time). The daemon smooths it (a 5-reading median, a 10 s average) and shows it
as the `reading` attribute of the HA `Illuminance` sensor, next to the calibration in use
(`calibration: unit: …` / `board: …` / `none`).

### Procedure

You need a lux meter (a photographer's meter in lux mode, a phone app is a poor substitute) and a
light you can dim, or a few lamps.

1. Turn off the speaker's own light where it can reach the sensor: HA `Display` → `off`, the
   `Ring` and `Ambilight` lights off. (They are dim, but at a few lux they count.)
2. Put the meter's sensor next to the speaker, facing up, at the height of the speaker's top, in
   the same light (not in the speaker's shadow, not closer to the lamp). White light; avoid
   coloured bulbs and daylight mixed with lamps.
3. For each light level, wait ~20 s (the reading is smoothed over ~10 s), then note the meter's
   lux and the speaker's `reading`. Two levels do: a dim one (~10 lx) and a bright one
   (~150–300 lx). More levels let you check the fit.
4. Write the pairs into `<cfw>/yastationd.conf` as `reading:lux`, and restart the daemon
   (`/etc/init.d/S52zyastationd restart`):

   ```
   # this speaker against a lux meter, white light, <date>
   lux_calibration = 0.6126:10, 13.11:200
   ```

   Any number of points, any order; between them and beyond the ends the daemon interpolates on
   log-log axes (two points are a power law, one point is a plain scale).
5. Check: `Illuminance` should now match the meter at a third light level.

Reading the sensor without HA, over SSH on the speaker (20 samples, the median):

```sh
L=$(for d in /sys/bus/iio/devices/iio:device*; do grep -qE '^(ltr|light-sensor)' $d/name && echo $d; done | head -1)
for i in $(seq 20); do cat $L/in_intensity_input; sleep 1; done | sort -n | sed -n 10p
```

With many points, a power-law fit on a computer (Python, pairs as `reading lux` lines in a file):

```python
import math, sys
pts = [tuple(map(float, l.split())) for l in open(sys.argv[1]) if l.strip()]
xs, ys = [math.log(r) for r, _ in pts], [math.log(l) for _, l in pts]
n = len(pts); mx, my = sum(xs) / n, sum(ys) / n
b = sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sum((x - mx) ** 2 for x in xs)
a = math.exp(my - b * mx)                      # lux = a * reading^b
print("lux_calibration = " + ", ".join(f"{(L / a) ** (1 / b):.4g}:{L}" for L in (10, 200)))
```

### Our measurements (2026-10-26)

Sekonic meter on the floor next to four speakers under a dimmable ceiling light, 16 levels
(11–180 lx white, plus coloured and one 450 lx point that the speakers disagreed on — uneven light
— left out). Power-law fits on the white points:

| Speaker | Fit | Written as | Where |
|---|---|---|---|
| Station 3 (orion) | lux = 0.0388 r^0.970 | `306:10, 6709:200` | orion profile (the only unit) |
| Mini 3 Pro (pickle) | lux = 1.875 r^0.969 | `5.624:10, 123.7:200` | pickle profile (the only unit) |
| Mini 3, lilac | lux = 6.63 r^0.964 | `1.532:10, 34.24:200` | that unit's config |
| Mini 3, black | lux = 16.1 r^0.978 | `0.6126:10, 13.11:200` | that unit's config |
| Mini 3 base | the geometric middle of the two | `0.9686:10, 21.19:200` | bergamot profile |

After calibrating, the four read 159–165 lx under the same light.

### Auto-brightness is in lux now

`auto_brightness_curve` maps lux (calibrated) to the display level. The default,
`0.7:<min>, 9:128, 150:255`, is the curve the black Mini 3 was tuned with in its own reading
units, converted by its calibration; `<min>` is the board's `matrix.min_brightness`. A curve
written before 2026-10-26 is in the old reading units of one speaker: convert its points with that
speaker's calibration, or drop it.

## Sound

The room sound sensors measure one raw mic channel in dBFS (yastationd/README.md, item 12). With
an offset, `Sound floor` / `Sound level` are dB SPL: dB SPL = dBFS + offset.

The firmware has a factory calibration for this: `quasar.cfg` → `spl.splCalibShift`, per model
(Mini 3 124.3, Mini 3 Pro 122.9, Station 3 121.8), used by the vendor's ambient-noise volume
(`volumeLevelSpls`). The profiles take these values as their base. **Not verified on our side:**
the vendor's dBFS convention and mic channel may differ from ours (we take the RMS of the voice
channel's samples, full scale = 0 dBFS, after a 150 Hz high-pass, unweighted).

To check or set a speaker's own offset you need a sound level meter (Z- or C-weighted matches our
unweighted level best; A-weighting reads lower in a room with low hum):

1. Put the meter next to the speaker. Make a steady sound around 60–70 dB SPL — pink noise from
   another speaker, or a fan — for a few minutes; our level is per minute.
2. Note the meter's level and the speaker's `Sound level`. That is already offset by the board's
   `mic.spl_offset`, so the speaker's own dBFS is `Sound level − mic.spl_offset`.
3. `mic_spl_offset` = meter dB SPL − the speaker's dBFS. Write it into `yastationd.conf` and
   restart.
