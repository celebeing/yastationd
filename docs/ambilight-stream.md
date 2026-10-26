# Effects on the ambilight: DDP from LedFx and friends

Station 3 (orion) has a rear strip: 12 RGBW positions, top to bottom, two co-located dies each
([boards/orion.md](boards/orion.md)). Besides being an HA light, it takes LED frames streamed over
the network, so [LedFx](https://www.ledfx.app/) (or WLED tools, xLights, anything that speaks
DDP) can drive it as a 12-pixel strip. Native music effects drawn from sendspin-host's spectrum
are the next step ([wishlist](wishlist.md)).

## How it works

- **DDP** (Distributed Display Protocol) on **UDP 4048** (`ddp_port`, 0 = off; only on boards
  with an ambilight). `internal/ddp` parses v1 packets: RGB (data type `0x0b`) or RGBW (`0x1b`)
  8-bit, destination id 1 (the default output; control/config/status ids are ignored), an
  optional timecode, frames split over several packets by byte offset. A frame is shown when the
  sender sets push, or when a packet reaches the end of our 12 pixels (senders that never push).
  Queries get no reply.
- A received frame **takes the strip while frames keep coming**, scaled by the effects'
  brightness; 2.5 s after the last one the strip is back to its HA light state. Frames are shown
  as they arrive (the render loop wakes on each), not on the loop's 15 Hz tick.
- **Runtime settings** (persisted, HA, the web app): the `Ambilight effects` switch (on by
  default; off = frames are ignored) and `Ambilight effects brightness` (1..255, default 64: the
  strip lights the wall like a floodlight).
- LAN only, no authentication, like the anonymous MQTT broker and the Sendspin port.

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

Add a device of type **DDP** with the speaker's IP, port 4048 and **12** pixels (not "WLED": we
do not imitate WLED's JSON API, so its discovery will not find the speaker). LedFx takes its
audio from the computer it runs on (a microphone or a loopback device), so play the same music
there to judge the effects on the strip.
