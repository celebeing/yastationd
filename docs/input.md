# Input: keys, the mute switch, the touch ring

Status (2026-10-30): describes what is built. How a press or the mic-mute control reaches the
daemon, what it does there and what the speaker shows; each board's quirks. The codes per board
are in the board docs ("Inputs"); the profile keys in [porting-a-speaker.md](porting-a-speaker.md).

## The path

```
evdev device (by kernel name)          internal/hw/input.go: FindInputInfo, ReadEvents, SwitchState
  └ inputLoop, one per input.device    internal/app/input.go (reopens it if it fails)
      └ onInputEvent: the profile maps the code to an action
          ├ EV_SW  switch.<code> = mic_mute      → setMute(on)
          ├ EV_KEY key.<code>    = mute_key       → readSwitches: EVIOCGSW, then setMute(latch)
          │                       voice           → wifi setup combo check, then push-to-talk
          │                       volume_up/down  → nudgeVolume, volume_change, HA button event
          │                       panel_gesture   → HA event (debounced 2 s)
          └ EV_ABS abs.<code>    = volume_ring    → nudgePercent by pad crossings
```

- **Devices are found by name** (`input.device`, the `N: Name=` of `/proc/bus/input/devices`), not by
  `eventN`, which moves between boots. A device that fails to open is retried every 3 s; a reader that ends is reopened after 1 s.
- **Only key-down counts** (value 1); releases matter only to the Wi-Fi setup combo, autorepeat
  (value 2) is ignored.
- **Any mapped key but `mute_key`, and the touch ring, while a timer rings** silences it and does
  nothing else (`stopAlarm`).
- **HA events:** every key action but `mute_key` is also a `button` event (`volume_up`, `volume_down`,
  `voice_command`, `panel_gesture`) on the speaker's event entity, so HA automations can use the
  keys ([yastationd/README.md](../yastationd/README.md), "Entities").

## The mic mute

**The latch is the truth.** The mute state is the kernel's switch latch (`SW_MUTE_DEVICE`, code 14
on every board so far), read with `EVIOCGSW` when the device opens (so a speaker that boots muted
starts muted) and taken from every `EV_SW` event after. Boards also send `KEY_MUTE` (113) around
each change; it is mapped to `mute_key`, which only re-reads the latch, so a missed or doubled
switch event is corrected by the next press.

`setMute(on)` (`internal/app/input.go`) acts only on a change of the stored state, then:

| On a change to | What happens |
|---|---|
| muted | log `mic mute on=true`; moment `mic_muted` (held: the amber `mute` ring at `PrioLocal`, slot `mute`, until unmuted); an intercom call is declined or ended |
| unmuted | log `mic mute on=false`; `endHook("mic_muted")` ends the held moment (its layers, steps and sound), then moment `mic_unmuted` (default: nothing) |

and on every call, changed or not, publishes `binary_sensor …_mic_mute`.

While muted, the rest of the daemon reads `micMuted()`:

| Where | What it does |
|---|---|
| `wakeword.go` | a WAKE is ignored (log `wake: ignored, mic muted`) |
| `voice.go` | a voice attempt (wake or key) starts no session; moment `voice_muted` (the `mute` expression in slot `voice` for 2 s) |
| `intercom.go` | an incoming call is refused (`intercomPolicy`); the uplink sends silence |
| `app.go` | the room-level reading (`newRoomSound`) is paused, as while the speaker talks |
| `wifisetup.go` | the voice key pressed while muted arms the Wi-Fi setup combo ([wifi-setup.md](wifi-setup.md)) |

**The hardware cuts the mics on its own:** on pickle every capture channel reads exact zeros
([boards/pickle.md](boards/pickle.md)), on orion the microphones are cut
([boards/orion.md](boards/orion.md)). So the daemon's checks above are about behaviour (what to
show, what not to start), not privacy; nothing in software can un-cut the mics.

### What the mute control is, per board

| Board | Device (`input.device`) | Control | Observed |
|---|---|---|---|
| bergamot | `gpio-keys` | the mic-mute switch | SW 14 + KEY 113 per change |
| pickle | `mute` (a `gpio-keys` device) | the mic-mute switch | SW 14 + KEY 113 per change, as bergamot; mics read zeros when on |
| orion | `mute` | the mic-mute control | each change: KEY 113 down, SW 14 = 0/1, KEY 113 up (captured 2026-10-25). **Held, it does not stay:** the latch turns on and ~2 s into the hold back off by itself (2026-10-26, found building the setup combo) |

Whether orion's flip-back sends an `EV_SW` event (so the daemon sees it) or only changes the latch
read by `EVIOCGSW` is not recorded. The daemon did learn of it: the first setup combo checked the
mute at the end of the 5 s hold and found it off (commit 15df6fd), by an `EV_SW` event or a
`KEY_MUTE` re-read. Check it with an evdev capture before relying on either.

Orion's control was first written up as "a physical switch, authoritative"; the second
observation shows its latch is kept below us (driver or controller) and can change while the
control is held. The daemon follows the latch either way. What it means for code:

- the Wi-Fi setup combo needs the mute only when the voice key goes down, and holding the mute
  through it is fine (`setupKey`, `wifisetup.go`);
- a fast on → off → on of the latch shows `mic_muted`, ends it, shows it again: each change is
  acted on, there is no debounce. `TestRegressMute` (`regress_test.go`) pins one on/off cycle.

### Ring shows muted, the speaker is not

What to look at, read-only, before touching anything:

1. The log's `mic mute on=…` lines: the last one says what the daemon believes.
2. HA's `…_mic_mute` sensor history: the same, over time.
3. The LED preview ([led-preview.md](led-preview.md)): what the daemon draws, apart from what the
   glass shows. The ring is composed again on every tick of the render loop (~15 Hz,
   `frameInterval` in `app.go`), so a stale layer can't outlive the next frame unless it is
   still on the stack. Drawn amber with `on=false` last → a moment or a pack (an owner's pack can
   override `mic_muted` / `mic_unmuted`: [asset-packs.md](asset-packs.md)); drawn dark but the
   glass amber → the LED write path: the ring is per-LED sysfs on every board so far (orion's
   too, not its `fb2` mirror), and `hw/sysfs.go` skips a write when it caches the same value
   (`ledNode.set`), so a write the chip lost is not repeated until the value changes.
   `TestRingFromPattern` (`internal/hw/hw_test.go`) is the fake-sysfs test to extend.
   A *visible* `mic_unmuted` reaction means a pack or the owner's moments: by default it shows
   nothing.
4. The control itself can only be moved by someone at the speaker: a repro needs the owner.

## Keys

| Action | What it does | Code |
|---|---|---|
| `voice` | push-to-talk (`startListening(srcKey)`); pressed again during a request, ends it; in a call, the intercom's key ([intercom.md](intercom.md)); with the mic muted, `voice_muted` and the Wi-Fi setup combo | `input.go`, `voice.go`, `wifisetup.go` |
| `volume_up` / `volume_down` | `volume_step` levels (1–4) of the board's volume curve (stock 5 % levels); moment `volume_change` (the stock tick, not over music; `volume_beep`), `volume_edge` at the ends | `nudgeVolume`, `volumeBeep` |
| `mute_key` | re-read the mute latch | `readSwitches` |
| `panel_gesture` | only an HA event, at most one per 2 s (orion's inner arc) | `onInputEvent` |

## The touch ring (orion)

`abs.0 = volume_ring`: the outer groove reports an angle (`ABS_X`, wrapping near 1000). The kernel
reports a wrong range for it, so the profile pins `abs_range.0 = 0,1000`. The ring has about ten
pads per turn and reports mostly their centres, so a step is one pad crossing
(`hw.CircularAxis`, `ringStepsPerTurn`), and its size follows the speed: 1 % for a slow turn up to
5 % for a quick one (`ringStepPercent`). One HA `volume_up` / `volume_down` event per gesture (a
new one after a 750 ms pause or a change of direction). Measurements and the inner area's
gestures (not recognised yet): [boards/orion.md](boards/orion.md), "Inputs".

## Board quirks in one place

- **bergamot:** the capacitive keys and the mute switch are two devices (`aw933xx`, `gpio-keys`);
  `input_event` comes in both layouts, detected from the first read.
- **pickle:** the same keys; the mute device is `mute`.
- **orion:** no volume keys (the ring instead); the inner area gives KEY 582 (voice) and 581
  (`panel_gesture`); vertical swipes are a pair of those in order, not recognised yet; the mute
  latch can flip back while held (above).

## Tests

`internal/hw/input_axis_test.go` (the ring's axis), `regress_test.go` `TestRegressMute` (the
held moment), `wifisetup_test.go` (the combo, including the flip-back). `ReadEvents` is tested on fake
readers (`internal/hw/hw_test.go`, both `input_event` layouts); the mute path by calling `setMute`
in a test. Nothing tests `onInputEvent` yet: a key sequence test would build an `App` and call it
with `hw.InputEvent`s.

## Related

[status-indication.md](status-indication.md) (what the ring shows), [asset-packs.md](asset-packs.md)
(the moments), [wifi-setup.md](wifi-setup.md) (the combo), [porting-a-speaker.md](porting-a-speaker.md)
("Inputs": finding the codes on a new board).
