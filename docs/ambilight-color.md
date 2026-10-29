# Ambilight colour: calibration

Station 3's rear strip (orion, RGBW, 12 positions) shows HA's colours as they are meant: a
measured calibration in the board profile, and a solver on the speaker that turns a wanted colour
into the strip's four drives. Before (up to v0.21.x) the HA light sent its RGBW to the strip as is,
and colours were far off: HA's teal came out blue, its white was a cold 11 000 K, and half
brightness looked like three quarters.

## What the strip does (measured 2026-10-29)

| channel | chromaticity x, y | luminance (W = 1) |
| --- | --- | --- |
| R | 0.6878, 0.2977 | 0.48 |
| G | 0.2664, 0.5659 | 1.63 |
| B | 0.1177, 0.0747 | 0.30 |
| W | 0.3388, 0.3811 | 1 |

- **The driver is linear**: the light is proportional to the level (0..255), nothing applies a
  gamma on the way (the vendor's stack applied `gamma 2` from quasar.cfg's `leds.ambilight`;
  without it every colour that mixes channels unequally is off). A ~7 % step sits between levels
  16 and 24 in every channel; the response table carries it.
- **Blue is strong for sRGB**: R+G+B at 255 is about 11 000 K, and G = B (HA's teal and cyan) is
  about 20° of hue toward blue.
- **W is 5300 K and greenish** (Duv +0.017, about three times a visible tint).
- **The green is paler than sRGB's** (0.30, 0.60): saturated yellows, oranges and limes are
  outside what the strip can make; they come out a little paler, keeping their hue.
- Channels add up within 2.5 %, the chromaticity holds over the levels (the LEDs are dimmed by
  PWM), and the light drifts −0.3 % over 50 minutes of use.

Two black units were measured; the second's primaries are within Δu'v' 0.005 of the first
(0.004–0.006 is about where a difference in a light's colour becomes visible) and its channel
balance within ±4–6 %, so one calibration in the board profile serves both. The vendor has no
per-unit LED calibration (the EEPROMs hold part numbers and serials); its `ledconfigs/<colour>.json`
are 3×3 corrections per **case colour** (red and blue cases only, read by maind, not by
`S61leds-current-coefficients`, which sets the clock's currents). A black case gets none. Other
case colours have not been measured: their fabric may tint the strip's light.

## How it was measured

A camera as the colorimeter (the owner's rig, a private sibling project): a mirrorless body on
USB shooting RAW, a ColorChecker's white patch lit by the strip at ~30 cm in a dark room, the
strip driven over DDP. A RAW file is linear in light, so the patch's averaged sensor values,
divided by the exposure time and with the room's ambient (dark frames) taken off, are a
tristimulus reading; the camera's own matrix (Adobe's, as LibRaw ships it) turns them into CIE
XYZ. A run showed each channel at 16 levels from 255 to 1 (auto-exposed per shot), mixes for
additivity, and W again at the end for drift. The fit gives the table above and the response.

Camera matrices are least accurate on narrowband LED primaries, so the check was the colours
themselves: what the old code drew and what the solver draws, measured the same way (Δu'v' from
the sRGB target, D65 white):

| colour | before | calibrated |
| --- | --- | --- |
| teal (0,128,128) | 0.041 | 0.0004 |
| cyan | 0.041 | 0.0007 |
| pink | 0.072 | 0.0012 |
| purple | 0.038 | 0.0012 |
| sky blue | 0.018 | 0.0020 |
| grey, white | 0.034 | 0.0022 |
| lime, orange, gold (out of gamut) | 0.009–0.050 | 0.007–0.009 |

The solver's prediction matched the measurement within 0.002. By eye, H 180° next to a video
light's HSI 180° read as the same cyan (not quite the same light: the video light has its own
primaries).

## The profile and the settings

```
ambilight.primaries = 0.6878 0.2977 0.4822, 0.2664 0.5659 1.6303, 0.1177 0.0747 0.3036, 0.3388 0.3811 1.0000
ambilight.response  = 1:0.0040702, 2:0.0078709, …, 192:0.75531, 255:1
```

`primaries`: R, G, B, W at full as `x y Y` (Y relative to W). `response`: drive level → light,
shared by the channels, linear between points. A board without `primaries` sends colours
uncorrected (plain RGB, W alone for a colour temperature).

| key | |
| --- | --- |
| `ambilight_white` | the white colours are made for: `srgb` (default: HA's colours as sRGB specifies them, 6500 K), `warm` (the white LED's own ~5300 K without its green tint: everything shifts warmer, whites are brighter), or a temperature in kelvin. Runtime, shared |
| `ambilight_calibration` | this unit's own primaries over the board's, same format; empty = the board's. Advanced, not shared. No tool in the CFW fills it: it is for someone who measured their unit |

## The solver (`internal/ledcolor`)

A colour is asked for as XYZ (an sRGB colour first adapted from D65 to the target white,
Bradford). With the RGB part written as `k·a − w·b` (a = M⁻¹·colour, b = M⁻¹·W for the R, G, B
matrix M), every channel in 0..1 bounds k at a given W share w; the brightest k is a small linear
program in (w, k) whose optimum is on a vertex, so each w where two bounds cross is tried. The
result has exactly the colour's chromaticity, as bright as the strip allows, with W used as much as
it helps (white is W at full plus some R and B to take its green off). A colour outside the gamut
is moved toward the target white along its hue line, as little as it takes (a binary search). The
linear drives go through the response table to levels.

## What it applies to

- **The HA light** (`light.<speaker>_ambilight`): colour modes `hs` and `color_temp`
  (2000–8000 K) instead of `rgbw` — the white LED is the solver's business now, and a colour
  temperature is made exactly (2700 K is R 255, G 119, W 85). HA's brightness scales the colour
  in sRGB terms (half is a fifth of the light, which looks half). A state file from before keeps
  its colour: its RGB part becomes a hue, white alone the default 5000 K.
- **The music effects**: their colours are corrected; their brightness stays linear (they were
  tuned by eye on the uncorrected strip). `ambilight_fx_white = false` keeps the white LED out of
  them.
- **Not DDP**: a sender (LedFx, WLED tools) gets the strip as is.
- The LED preview (frame tap, `ledsim`) shows the drives, not the colours.
