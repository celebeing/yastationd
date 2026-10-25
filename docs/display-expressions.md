# Display expressions: surfaces, expressions, scenarios, moments

Status (2026-10-25): working on the seg7 front display (bergamot, pickle) and the ring; checked by
eye on bergamot. Orion's 21×12 pixel screen plays the firmware's own screen animations for the
expressions (host-tested against the 3.21 dump, **not yet seen on glass**); its clock, text and
numbers are still the seg7 picture drawn as pixels (see "Front displays").

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

4. **Moments** — what happens in the CFW's life that the speaker reacts to: `startup`,
   `dialog_end`, `alarm`, `volume`, `call_connected`, `bluetooth_connected`, … The app announces a
   moment and never picks the picture: a per-platform table maps the moment to an expression, a
   scenario or a display-native animation (section "Moments").

Synchronisation without coupling: every surface renders from the same render-loop tick, and a
scenario starts its layers with one start time, so animations on the ring and the front stay
frame-aligned. All layers of one expression or scenario share a slot name, so a cancel or TTL
removes them everywhere. Conflicts are resolved per surface: a timer ring that pre-empts a scenario
on the front leaves its ring part running, and the front returns to the scenario afterwards. The
rear ambilight is never part of an expression (it is the user's lamp); only a scenario that names
it explicitly drives it.

## Front displays

The app never draws the front display itself. It asks a `display.Front` (`internal/display/front.go`),
the catalogue of one kind of display, for a **scene** or an **animation**, and pushes what it gets
onto the front stack:

- **Scenes**: the clock (and the clock before the first time sync), static or scrolling text, a
  number, a countdown, the Wi-Fi setup face. Each kind of display draws them its own way.
- **Animations**: `Anim(name)` resolves a semantic name (every expression, plus `booting`,
  `call_incoming`, `call_outgoing`, `setup`) or a plain animation name to an animation native to
  the display. An entry with no candidates means "this display has none": the expression then has
  no front part there (as `error` on seg7, or `sad` on orion).

| Kind | Scenes | Animations |
|---|---|---|
| `Seg7Front` (bergamot, pickle) | 4-digit seg7 pictures | the stock clock clips (`.scled`, below) and ours, via the candidate names in `seg7Anims` |
| `PixelFront` (orion) | the clock natively ("The pixel clock" below); **fallback** for the rest: inherited from `Seg7Front`, drawn by `Seg7ToPixels` (3×5 font doubled vertically) | the firmware's screen patterns (`.led`, below), via `pixelAnims` |

A layer's frame (`FrontFrame`) is either a seg7 picture or native pixels. A pixel screen draws a
seg7 picture through `Seg7ToPixels`; that is the fallback for every scene that has no pixel drawing
yet. To draw a scene properly on a pixel screen (a real pixel clock, a volume arc from the stock
`rcu_volume/` patterns), override that method on `PixelFront`; nothing in the app changes.
Animations are never converted from seg7 to pixels: a semantic animation the screen has no pattern
for is simply not shown. The only seg7 content a pixel screen draws as pixels besides the scenes is
HA's hand-made seg7 `frames`.

## The pixel clock (orion)

`internal/display/pixelclock.go`, chosen in the LED preview (2026-10-26). Our own bitmap fonts,
drawn in perceived intensity and turned into drive values with `matrix.gamma` and the display
brightness at the end, so half-tones look half as bright on the glass.

- `clock`: `H:MM` in thin 4×9 digits (1-px strokes); with two hour digits the line is exactly the
  21 columns (4+1+4 +1+1+1 +4+1+4); no leading zero, the line is centred. The colon is steady.
- `clock_date` (a display mode of its own): `H:MM` over `DD.MM` in 3×5 digits, the date at 45 %.
  A seg7 display has no room for both and shows the clock.
- A minute change cross-fades the old picture into the new one over 600 ms. It needs no state:
  in the first 600 ms of a minute the frame mixes the previous minute's picture in.
- Before the first time sync: only the colon, blinking.

## Screen patterns (pixel, orion)

The 3.21 firmware ships its screen animations in `/system/vendor/quasar/ledpatterns/screen/`
(26 top-level files and subdirectories, 90 patterns), the same text format as its ring patterns:
one frame per line, `RRGGBB` per pixel (252 = 21×12, row-major) and the frame's duration in ms
(20 ms almost everywhere, i.e. 50 fps). Directive lines can stand between frames:

- `loop` marks where the **repeating part starts**. On the first line the whole file loops; further
  down the frames above it are an intro played once: `call_incoming` (line 51), `configure`
  (line 368), `update_downloading` (60), `call_outgoing` (2).
- `smooth N` (interpolation) appears only in the factory `qc` pattern; we ignore it.
  `no_size_optimization` (in `volume`) means nothing for playback. `gammacorrection` ("apply the
  gamma", seen in bergamot's ring patterns and the Station Lite's) does not appear in orion's screen
  patterns: there the gamma always comes from quasar.cfg.

The files hold uncorrected levels: the firmware applies the screen's gamma (2.2, quasar.cfg
`leds.screen.colorCorrection`) when it plays them. We do the same (board key `matrix.gamma`). All
patterns are grey (R = G = B). Names are paths relative to the directory without `.led`; `a+b`
chains patterns (the frames of all but the last are the intro). `maind` names only the
subdirectories (`oknix`), not the files, so the stage order comes from the numbered file names.

| Expression / name | Pattern |
|---|---|
| listening | `oknix/01-activation` + `02-activation-to-listening` + `03-listening-loop` (a pulsing disc) |
| thinking | `oknix/04-listening-to-answer` + `05-thinking-loop` (a running dot) |
| speaking | `oknix/06-answer-loop` (a wave) |
| waiting | `oknix/05-thinking-loop` |
| error | `error` (two crosses, loops) |
| mute | `mute` (a crossed-out microphone) |
| boot / booting | `boot` / `boot/FirstBoot_LoadLoop01` (the boot-phase layer) |
| ringing (alarm, timer) | `alarm/alarm_loop` |
| call_incoming / call_outgoing | `call_incoming` / `call_outgoing` (a handset, intro + loop) |
| setup (Wi-Fi setup mode) | `configure` |
| pause / next / previous | `media/Pause` / `media/Next` / `media/Previous` |
| eyes, blink / wink / happy | `scenario_emotion/Smile_Neutral_Output` / `Smile_Wink_Output` / `emotion/joy` |
| busy, sad, sleep | none |

Unused so far, for later scenes: `media/Like`, `Dislike`, `Resume`; `rcu_volume/` (per-level volume
arcs: `volume_edge`, `volume_pulse/{up,down}/<0..10>`, `volume_rotation/…`), `volume`, `timer` and
`timer/timer_bell`, `calls/call_hold`, `call_connected`/`call_ended`, `bluetooth_*`, `block`,
`unmute`/`sound_mute`, `scenario_emotion/` (activation, listening, confirmation variants),
`emotion/surprise`. HA can play any of them by name (`display/cmd` `clip`, e.g. `media/Like`).

Not yet checked on glass: whether gamma 2.2 is right for our fb writes (the driver might correct
too), how the 50 fps patterns look on our 15 Hz render loop (frames are picked by time, so the
timing is right but in-between frames are skipped), and how they sit with auto-brightness.
`TestPixelAnimsOnFirmware` (`YASTATIOND_SCREEN_PATTERNS=<…>/ledpatterns/screen go test
./internal/display`) checks every mapped pattern against a firmware tree.

## Moments

Why a fourth layer: the platforms differ by orders of magnitude in what they can show. A Mini 3
has a handful of seg7 clips (eyes, wink, happy, the voice stages); orion's firmware ships about a
hundred screen patterns — calls, media, Bluetooth, timers, volume, several activation and
confirmation variants. An expression is a *picture* (`wink`), and the per-display maps already say
how each display draws it; but *which* picture fits a situation is a per-platform choice too:
the end of a dialog is a `wink` on a Mini 3 and a `blink` on orion, a Bluetooth connection is
nothing on a Mini 3 and the stock `bluetooth_connected` pattern on orion. Hard-coding
`express("wink")` at the call site makes every platform play the poorest common denominator.

**The rule:** code that reacts to an event calls `a.moment(name)`, never `express(…)` with a
picture. A moment resolves through, first match wins:

1. `<cfw>/board.conf` and the board profile: `moment.<name> = <candidate>, <candidate>, …` (the
   board.conf override already covers every profile key, so a user re-maps a moment there);
2. the defaults of the front display's kind in code (`seg7`, `pixels`), next to the per-display
   animation maps;
3. an expression of the same name, if there is one (so the voice stages need no entries);
4. nothing — the moment has no picture on this platform (not an error).

A candidate is an expression (`blink`), a scenario (`scenario:startup`), or a native animation of
the front display by its name or path (`anim:media/Like`); it counts only if it exists on this
speaker (the expression has a part on one of its surfaces, the pattern is in the firmware), so a
list is a fallback chain from the richest to the plainest. Later: `random(a, b, c)` picks one of the
available ones for variety (never the same twice in a row), and parameters for moments that carry
a value (`volume` → the level, `timer` → the time left) are passed through to scenes that take them.

Moments stay above the layers they use: priorities, slots and TTLs are those of the expression
or scenario a moment resolves to, and the ring and the ambilight follow the same rules as before.
HA keeps its direct access to expressions, scenarios and patterns; a moment can also be triggered
from HA (`moment/set`, later) to preview a mapping.

### The moment catalogue

Status: **in code** = called by the app through `moment()`; **planned** = the event exists, the
call site does not use a moment yet; the orion column is a first proposal from the pattern names,
to be reviewed on glass (the stock patterns are still being watched through).

| Moment | When | seg7 (Mini 3, Mini 3 Pro) | pixels (orion) | Status |
|---|---|---|---|---|
| `startup` | once per device boot | `scenario:startup` (eyes, wink) | `scenario:startup-pixel` (eyes, blink) | in code |
| `dialog_end` | a reply played and no follow-up listening | `wink` | `blink` | in code |
| `listening`, `thinking`, `speaking` | voice stages | the expressions | the expressions (`oknix/`) | via rule 3 |
| `error` | voice / network failure | the expression | `error` | via rule 3 |
| `mute`, `unmute` | the mic switch | — | `anim:mute`, `anim:unmute` | planned |
| `volume` | volume changed (value) | the number | a volume scene (`rcu_volume/` patterns or our own bar) | planned |
| `alarm`, `timer_done` | an alarm / a timer rings | `ringing` | `alarm/alarm_loop`, `timer/timer_bell` | planned |
| `call_incoming`, `call_outgoing`, `call_connected`, `call_ended`, `call_hold` | intercom | `ringing` for incoming | the stock `call_*` / `calls/*` patterns | planned |
| `media_pause`, `media_resume`, `media_next`, `media_previous`, `like`, `dislike` | music controls | `pause`, `next`, `previous` | `media/*` | planned |
| `bluetooth_connected`, `bluetooth_disconnected` | A2DP | — | the stock patterns | planned |
| `setup` | Wi-Fi setup mode | the setup face | `configure` | planned |
| `update_downloading`, `block` | not ours (updates are blocked) | — | — | not used |

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

Same text format, committed under `yastationd/internal/display/clips/` (our drawings), embedded:
`eyes`, `happy`, `sad`, `wink`, `sleep`, and `listening` / `thinking` / `speaking` for boards whose
stock voice clips are blank (pickle). A clip in the CFW or firmware directory with the same name
wins over ours.

## How the expressions are used

- Voice: `listening` → `thinking` → `speaking` on the ring and the front display; when a reply has
  played and no follow-up listening comes, the `dialog_end` moment for 1.5 s (`wink` on seg7,
  `blink` on orion), then the clock (not after an announcement). Pickle's stock voice clips are a single dark frame (its firmware blanks the clock
  while it talks), so a blank clip is skipped and our own bars play instead. Our `speaking` looks
  much like `thinking`; worth redrawing.
- Startup: the `startup` moment once per device boot (the `startup` scenario: eyes, then wink;
  `startup-pixel` on orion: eyes, then blink). The stock `boot` clip is
  not used: it is timed to a boot jingle and runs ~10 s with a 3 s dark start.
- Timer and alarm ring: the stock `ringing` clip on the front display (the blinking "00:00" where a
  board has no clips).
- One-shot clips shown for a TTL hold their last frame until it ends.
- Clips are normalised so their brightest element is 255: the stock voice clips peak at about a
  third, which vanished under the display's own (auto) brightness.
- A one-shot expression's ring part ends with its clip (ring effects never end on their own).

## Transitions (seg7)

The front display goes through a per-element smoother: every segment moves towards its new level
at a rate chosen per frame. A layer coming on top takes 80 ms (the volume number must be readable
at once), a lower one coming back 400 ms (the clock after a face), a plain picture changing within
a layer 120 ms (digits morphing, scrolling text); frames inside one clip are passed through, they
carry their own timing. Elements two pictures share stay lit, so back-to-back animations join
without a blank. Layers are replaced in place (a push over the same slot, never remove-then-push),
and scenario steps replace the previous step's layers. Pixel screens keep a plain fade-in on a
change of the top layer (80 ms up, 400 ms back down); their patterns carry their own transitions
(the `oknix/` intros, the `loop` intros).

## HA exposure

- `display_mode` select keeps clock / message / off (later: idle, music on pixel screens).
- `display/cmd` gains `{"surface":"matrix","op":"clip","args":{"name":…,"loop":…}}` and
  `"op":"frames"` with raw seg7 frames, next to the existing text / number / countdown. `clip`
  takes a semantic name too (`listening`) and, on a pixel screen, a pattern path (`media/Like`);
  `frames` on a pixel screen go through the seg7 fallback.
- `express/set` `{"name":…, "ttl_ms":…}` and `scenario/set` `{"name":…}` or an inline scenario.
- A select of the clips available on this speaker, a switch for the voice expressions on the front
  display, and the surfaces' capabilities as attributes, so HA scripts and an LLM agent know what a
  given speaker can show.

## Phases

1. Recon — done (clip formats and order above).
2. Core — done: per-segment levels in the seg7 frame, the clip loader and player, the expression
   layer; the ring's voice stages run on it.
3. Clips + voice on the front — done (see "How the expressions are used"); `make clips`.
4. HA — done: the topics and entities above, scenarios.
5. Music: pause / next / previous clips; levels and metadata from sendspin-host for a visualiser.
6. Pixel screens (orion first): the `Front` catalogue and the firmware's screen patterns for every
   expression and the call / alarm / boot / setup states — done blind (host-tested on the 3.21
   dump). Left: check on glass (gamma, frame rate), then proper pixel scenes in place of the seg7
   fallback (clock, text, volume from `rcu_volume/`).
