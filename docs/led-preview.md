# LED preview: the frame tap and the host simulator

Developing scenes and animations for orion's 21×12 screen, 36 px ring and 12 px ambilight on the
glass alone is slow, and the screen's framebuffer reads back as zeros, so a unit cannot even be
asked what it shows. Two pieces fix that:

- **The frame tap** (`internal/frametap`): an optional HTTP endpoint in `yastationd` that streams
  every surface exactly as the daemon hands it to the hardware, takes the daemon's commands, and
  serves a viewer page.
- **The host simulator** (`cmd/ledsim`): `yastationd` itself running on the host over a throwaway
  sysroot — sysfs LED files, a virtual front screen, the firmware's screen patterns from a local
  copy — with its frame tap on. It is the same code as on a unit (scenes, animations, overlays,
  the 20 ms screen tick, the 15 Hz ring), so where the simulator and a unit differ, the cause is
  the unit's state (brightness, what HA set) or the glass, not the rendering.

**In the web app:** every speaker has the preview behind its login, in the app's LED tab
(`led_preview`, on by default; [web-ui.md](web-ui.md)). The standalone tap below (`frametap_addr`,
its own port, no login, the comparison viewer) is for development and the host simulator.

## Running

```sh
cd yastationd
go run ./cmd/ledsim -patterns ../dumps/orion/quasar/ledpatterns/screen   # any copy of the
                                                                          # firmware's ledpatterns/screen
# open http://localhost:8930/
```

Flags: `-board` (default `orion`), `-screen WxH` (the virtual screen, default `21x12`), `-addr`
(default `localhost:8930`), `-dir` (keep the sysroot, including `yastationd.state`, between runs),
`-media <dir>` (the video mode's `video.led[.gz]` and `video.wav`, e.g. `build/media` from
`make video-convert`; [video-mode.md](video-mode.md)), `-v`. Without `-patterns` the screen has no
stock animations (the seg7 fallback still draws the clock and text). No MQTT, audio, wake word or
time sync (the video mode plays silently); the input and mixer warnings at start are expected.

On a unit, add to `<cfw>/yastationd.conf` and restart the daemon:

```
frametap_addr = :8930
```

The key exists from **v0.8.1** (commit `6e99fba`). An older binary treats it as an unknown key,
which is fatal: `load config err="…: unknown key"`, exit 1, five times in a row, then the
supervisor restores `yastationd.prev`. Check `yastationd -version` first, or leave the key out.

To compare, open the simulator's page with both sources:
`http://localhost:8930/?src=http://localhost:8930&src=http://<speaker-ip>:8930` (or add a source
with the field at the top; the list stays in the URL).

## The viewer

- One panel per source: the screen as square cells with no gaps (Station 3's screen LEDs are
  square and touch), the ring as a circle (index 0 at the top,
  clockwise, as on the unit), the ambilight as a vertical strip (top to bottom), with the source's
  version, frame rate, sequence number and dropped frames.
- **perceived** (on by default): the values are LED drive levels, linear light; the page undoes the
  gamma (1/2.2) so the monitor shows roughly what an eye sees on the glass. Either way a lit LED starts at a visible
  grey (48/255) and an unlit one is near black: on the glass a drive of 1 is still a visible dot. Off shows the raw drive
  values — a pixel driven at 1 is then black on the monitor, while on the glass it is a visible dot.
- Hover a screen dot: its coordinates and drive value.
- **Pause** (Space) freezes all sources; **◀ / ▶** (arrow keys) step through the last 30 s of
  frames, the other sources following by time (each on its own clock).
- Controls go to every source at once: expressions, any screen pattern (with loop), display mode
  (clock, off, video), text, number, countdown, volume, brightness (and back to auto), ring colour
  and effect, ambilight.
  They are the daemon's own command handlers, the same as over MQTT (payloads as in
  [hass-integration.md](hass-integration.md)); commands a source does not have are skipped.

## The tap's protocol

| Path | What |
|---|---|
| `GET /` | the viewer |
| `GET /info` | `device`, `name`, `board`, `model`, `version`, `screen` `[w, h]`, `ring`, `ambilight` (pixel counts), `expressions`, `ring_effects`, `patterns` (what a `clip` can name), `commands` |
| `GET /frames` | server-sent events, one JSON frame per message: `t` (ms, the daemon's clock), `seq`, `screen` (hex RGB per pixel, row-major), `ring` (hex RGB), `ambilight` (hex RGBW) |
| `POST /cmd/<name>` | runs a command; the body is its MQTT payload; 404 for an unknown name |

A frame carries the latest state of every surface and is sent only when something changed, at most
every 20 ms; a slow client loses frames (the `seq` gap), the render loop never waits. With nobody
connected the tap does not even encode frames. CORS is open so one page can read several taps.

**Security:** no authentication, like the anonymous MQTT broker it mirrors — anyone on the LAN who
can reach the port can drive the displays and volume. Off by default; turn it on for development
units only. It listens on all interfaces (`:8930`); the egress lock filters only outgoing traffic.

## Limits

- On a screen with a backlight (orion) the tap shows each frame scaled by the backlight value
  written with it, so the preview dims like the glass; the framebuffer itself holds the frame at
  2/3..full scale.
- What the tap shows is what the daemon writes, not what the glass emits: the gowin screen's
  one-write lag, the LED drivers' current coefficients and the per-unit case colour are not
  modelled. Glass comparisons are by eye.
- The simulator does not know a unit's persisted state; set brightness and colours from the page
  on both sides before comparing.
