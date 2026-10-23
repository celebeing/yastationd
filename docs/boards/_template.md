# <codename> — <Station model>

Profile: `yastationd/internal/board/profiles/<codename>.conf` · Survey: `dumps/survey/<codename>-<date>.txt`
(local only) · Status: _surveyed / partially working / supported_.

## Identity
- device-tree compatible: `yandex,<codename>` …
- SoC / userland ABI / RAM:
- quasar `deviceType`:

## LED ring
- backend (per-pixel sysfs / framebuffer / other), pixel count, LED name pattern, colour order, default trigger:
- verified with `ringdemo`: _yes/no_

## Front display
- kind (7-segment N digits / dot matrix WxH / none), LED names:

## Inputs
| Device (`N: Name=`) | Code | Physical control | Action |
|---|---|---|---|

## Audio out
- playback chain, volume control (+ range, curve), unmute recipe (**verified by ear**):

## Mic array & voice front-end
- capture device, channels/rate, live mics, speaker feedback channel:
- mic gain control + chosen value, measured speech/floor dBFS:
- VQE preset, outputs sanity:
- spotter models (activation / commands), layouts, thresholds:
- wake-word recall / false accepts measured:

## Sensors & extras
- IIO devices:
- radios / coprocessors (Zigbee, Thread, BLE …):

## Open questions
