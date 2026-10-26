# Effects on the ambilight: DDP from LedFx and friends

Station 3 (orion) has a rear strip: 12 RGBW positions, top to bottom, two co-located dies each
([boards/orion.md](boards/orion.md)). Besides being an HA light, it takes LED frames streamed over
the network, so [LedFx](https://www.ledfx.app/) (or WLED tools, xLights, anything that speaks
DDP) can drive it as a 12-pixel strip; without a stream, our own music effects draw it from
sendspin-host's spectrum while music plays.

## Setting up a sender

DDP has no discovery: the sender has to be told everything by hand. The web app's LED preview
tab shows the same values for the speaker it is on.

| Setting | Value |
|---|---|
| Device type | **DDP** (in LedFx: "DDP", not "WLED" — we do not imitate WLED's JSON API) |
| Address | the speaker's IP |
| Port | **4048** (`ddp_port`) |
| Pixels | **12** |
| Channel order | **RGB**; RGBW data (type `0x1b`) is taken too and drives the white channel |
| Pixel 0 | the **top** of the strip; 11 is the bottom |
| Destination id | 1 (the default output) |
| Frame rate | anything up to ~60 fps; each frame is shown as it arrives |

## How it works

- **DDP** (Distributed Display Protocol) on **UDP 4048** (`ddp_port`, 0 = off; only on boards
  with an ambilight). `internal/ddp` parses v1 packets: RGB (data type `0x0b`) or RGBW (`0x1b`)
  8-bit, destination id 1 (the default output; control/config/status ids are ignored), an
  optional timecode, frames split over several packets by byte offset. A frame is shown when the
  sender sets push, or when a packet reaches the end of our 12 pixels (senders that never push).
  Queries get no reply.
- A stream **takes the strip over** while frames keep coming, **exactly as sent**: no switch and
  no brightness of ours apply (whoever streams wants that). 2.5 s after the last frame the strip
  is back. Frames are shown as they arrive (the render loop wakes on each). `ddp_port = 0` is the
  only way to refuse streams.
- LAN only, no authentication, like the anonymous MQTT broker and the Sendspin port.

## Music effects

Without a stream, while music plays on the speaker (spectrum frames from sendspin-host keep
coming), the selected effect draws the strip; otherwise it shows its HA light state. Runtime
settings (persisted, HA, the web app): `Ambilight music effects` (switch, on), `Ambilight music
effect` (select, default `rain`) and `Ambilight music effects brightness` (1..255, default 64: the
strip lights the wall like a floodlight). The effects are the usual audio-reactive strip set
(LedFx, WLED's sound-reactive effects), written from scratch (`internal/app/ambilighteffects.go`,
`ambilightrain.go`; LedFx is GPL-3.0, we take ideas, not code):

| Effect | What it does |
|---|---|
| `rain` | an attack in the lows, mids or highs drops a drop of that range's colour (deep blue, cyan, white — the highs in the white channel) at a random place; it spreads and fades |
| `vu` | a level meter from the bottom, green to red, with a peak dot that hangs and falls |
| `spectrum` | one band per pixel, lows at the bottom, colour by frequency |
| `energy` | a fill from the middle outwards by the overall energy; colour = lows red, mids green, highs blue |
| `scroll` | the sound's colour now enters at the top and runs down |
| `pulse` | the whole strip flashes warm on a beat in the lows and fades |

The highs of `rain` and the peak of `vu` use the strip's white channel; `ambilight_fx_white =
false` (config) draws that as R=G=B instead. A DDP stream is not affected.

The spectrum is sendspin-host's (`-b`, 21 bands, ~25 frames a second, timed to when the audio is
heard: [music-sendspin.md](music-sendspin.md#on-the-display)); the strip renders at 40 Hz.
Tuning (thresholds, colours, speeds) is by eye on the speaker and still open.

## The framebuffer path (why it is cheap)

Writing a full frame as sysfs LEDs is 12 positions × 2 dies × 4 channels = 96 writes, each going
to the LED driver: **25–34 ms per frame** on orion (measured 2026-10-26) — half a core at 15 fps,
all of it at effect rates. The strip also has a ledfb framebuffer, `fb0` "ambilight" (12×1,
48 bpp, stride 72): per position **B, G, R as 16-bit little-endian**, the LED level being half
the value (510 = 255), both dies following. One write per frame: **3.5 ms**. It has no white.

So with `ambilight.fb = ambilight` in the board profile, `hw.Ambilight` sets the R, G and B LEDs'
trigger to `ledfb-ambilight` and writes them through the framebuffer (when the frame changed, and
once more after, in case the glass latches on the next write like the screen's), while W stays
on sysfs. Streaming at 50 fps keeps yastationd at ~2% CPU.

## Trying it without LedFx

`cmd/ddpsend` streams test effects from a computer:

```sh
cd yastationd
go run ./cmd/ddpsend -to <speaker-ip> -fx rainbow   # chase | pulse | bars; -fps 50 -for 20s -n 12
```

## LedFx

Add a device of type DDP with the settings above. LedFx takes its audio from the computer it
runs on (a microphone or a loopback device), so play the same music there.
