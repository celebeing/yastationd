# Display expressions: surfaces, expressions, scenarios

Status (2026-10-24): design agreed; the seg7 front display (bergamot, pickle) and the ring are
being moved onto it. Pixel screens (orion's 21×12, Midi's panel) come later: until they are
explored on a device, their boards show no expression layers on the front screen.

## Three layers

1. **Surfaces** — ring, front display, rear ambilight, later anything else. Each keeps its own
   layer stack, priorities and catalogue of scenes for its capabilities. Surfaces are independent:
   models differ in which they have (6/16/36-px rings, seg7 or pixel fronts, an ambilight on one
   model only), several owners use them at once (the voice on the ring while HA shows text on the
   front, the user's ambilight as a plain lamp), and a failure or a test stays local.
2. **Expressions** — semantic states: `listening`, `thinking`, `speaking`, `error`, `boot`,
   `ringing`, `pause`, `next`, `previous`, `happy`, … The app calls `express(name)` and never
   addresses surfaces. A per-board map says what each expression shows on each surface and at what
   priority; a surface the board lacks, or a scene not available on it, is skipped. The stock
   firmware is organised the same way: one event name per pattern directory (`clock/`, `ring/`,
   `oknix/`, `panel/`, `screen/`).
3. **Scenarios** — named compositions in time: steps in sequence or in parallel, each a surface +
   scene + duration or a whole expression. Callable from HA by name or as inline JSON. Built-in ones
   live in the repo; the user's in `<cfw>/scenarios/`.

Synchronisation without coupling: every surface renders from the same render-loop tick, and a
scenario starts its layers with one start time, so animations on the ring and the front stay
frame-aligned. All layers of one expression or scenario share a slot name, so a cancel or TTL
removes them everywhere. Conflicts are resolved per surface: a timer ring that pre-empts a scenario
on the front leaves its ring part running, and the front returns to the scenario afterwards. The
rear ambilight is never part of an expression (it is the user's lamp); only a scenario that names
it explicitly drives it.

## Priorities (high → low), per surface

error → timer ring → volume bubble → voice expressions → HA overlay / scenario → music → base
(clock, message, idle, off). The mechanism is the existing `display.Stack` (priority + TTL).

## Stock clock clips (seg7)

Every model with a 7-segment clock ships its animations as data in
`/system/vendor/quasar/ledpatterns/clock/`:

| Firmware | Files | Format |
|---|---|---|
| bergamot 0.369 | `*.binscled.zst` | binary, zstd-compressed |
| bergamot 0.6.x | `*.scled` (+ `eyes`) | text |
| pickle | `*.scled` (+ `battery_*`, `low_battery`; `next_track`/`pause`/`previous_track` without the `player_` prefix) | text |

**Text `.scled`:** an optional first line `loop`, then one frame per line:
`A B C D E F G | A..G | A..G | A..G | x0 x1 x2 x3 | ms` — brightness 0..255 per segment of digits
0..3 (0 = leftmost), four extra LEDs, the frame duration in ms.

**Binary `.binscled`** (after zstd): 35-byte frames: `[flag][32 brightness bytes][duration u16 BE ms]`,
the 32 bytes in the same order as the text form (digit-major, A..G, then the four extras). The flag
is 1 on the first frame of a looping clip — the text form's `loop` line.

The order was found by rendering the clips under each candidate order: with digit-major A..G the
pictures are clearly the stock ones (two eyes on the outer digits in `boot`/`blink`, middle bars
pulsing in `listening`/`speaking`/`thinking`, `||` for pause, `|||` sliding for next/previous,
running verticals in `ringing`). The device-tree channel order of the LED driver (36 channels,
including four unused `H` segments) is a different one; the clip format does not follow it. The
four extras are taken as DH, DL, DM, DEGREE (the frame builder's `middleSymbols` order H, L, M, then
the degree); the stock clips barely use them.

The daemon reads only the text form, from `<cfw>/clips/` first, then the firmware directory.
`make clips` converts 0.369's binary files on the host (the device has no zstd), like
`make sounds` does for audio. Vendor clips are never committed.

## Our own clips

Same text format, committed under `yastationd/internal/display/clips/` (our drawings), embedded.
They fill the gaps (e.g. `happy`, `sleep`, `wink`) and are what a board without stock clips uses.

## HA exposure

- `display_mode` select keeps clock / message / off (later: idle, music on pixel screens).
- `display/cmd` gains `{"surface":"matrix","op":"clip","args":{"name":…,"loop":…}}` and
  `"op":"frames"` with raw seg7 frames, next to the existing text / number / countdown.
- `express/set` `{"name":…, "ttl_ms":…}` and `scenario/set` `{"name":…}` or an inline scenario.
- A select of the clips available on this speaker, a switch for the voice expressions on the front
  display, and the surfaces' capabilities as attributes, so HA scripts and an LLM agent know what a
  given speaker can show.

## Phases

1. Recon — done (clip formats and order above).
2. Core: per-segment brightness in the seg7 frame, the clip loader and player, the expression layer;
   the ring's voice stages move onto it unchanged.
3. Clips + voice on the front: stock clips for listening / thinking / speaking / boot / ringing;
   `make clips`.
4. HA: the topics and entities above, scenarios.
5. Music: pause / next / previous clips; levels and metadata from sendspin-host for a visualiser.
6. Pixel screens (orion first): their own scenes, once a unit is on the desk.
