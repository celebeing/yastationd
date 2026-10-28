# Video mode: a video on the pixel screen with its sound

A party trick for orion's 21×12 front screen: the `video` display mode plays a frames file on the
screen and its audio track through the speaker, in sync, then returns to the clock. The motivating
content is the "Bad Apple!!" shadow-art video (black and white silhouettes survive 21×12 well), but
the mode plays whatever you convert. Status: host-tested (unit tests, the LED preview simulator);
**not yet seen or heard on a unit**.

The video, its frames and its audio are third-party media: they are never committed, and nothing in
the CFW downloads them. You supply a local video file and convert it on the host.

## Quick start

The simplest way: the web app's **Assets → Videos** converts a video file in the browser and
uploads it, no host tools needed ([assets.md](assets.md)). On the host:

```sh
cd yastationd
make video VIDEO=~/Videos/bad_apple.mp4            # convert (ffmpeg) and upload to <cfw>/media/
# or only convert, into build/media/:
make video-convert VIDEO=~/Videos/bad_apple.mp4
```

Then pick **video** in the speaker's **Display** select in HA (or publish `video` to
`<prefix>/<id>/display_mode/set`), or press **video** on the LED preview page
([led-preview.md](led-preview.md)). It plays once; the select shows `video` meanwhile and goes back
to the previous mode at the end.

On the host, without a unit: `go run ./cmd/ledsim -media build/media` and the **video** button
(picture only; see "Limits").

## The files

| File | Default path (config key) | Made by |
|---|---|---|
| frames | `<cfw>/media/video.led` (`video_frames`); `video.led.gz` is tried when the plain file is missing | `videoconv -frames …` |
| audio | `<cfw>/media/video.wav` (`video_audio`) | `videoconv -audio …` |

- **Frames:** the stock screen-pattern text format (`.led`, see
  [display-expressions.md](display-expressions.md), "Screen patterns"): one line per frame, 252
  `RRGGBB` values row-major and the frame's duration in ms. Gzipped when the name ends in `.gz` —
  the daemon recognises gzip by its magic, whatever the name. A 3:39 video at 30 fps is 6570 frames:
  11.6 MB as text, about 0.5 MB gzipped (a test pattern; shadow art compresses even better), about
  5 MB in memory while it plays (loaded at the start, freed at the end). A binary format would save
  nothing that gzip doesn't, and keeping `.led` means `ParseLed`, the clip tools and the
  simulator's pattern player all read it.
- **Audio:** S16LE WAV, 48 kHz stereo (what `make sounds` produces), played by `aplay` through the
  shared dmix like every other sound. 3:39 is 42 MB; orion's CFW root lives on the 1.4 GB `/ota`
  partition.
- `video_av_offset_ms` (default 0, −2000..2000): shifts the picture against the sound; positive
  makes the picture later. Tune it on a unit if the picture leads (aplay's start-up and buffer
  latency) or lags.

## The converter (`cmd/videoconv`)

```sh
go run ./cmd/videoconv -frames out/video.led.gz -audio out/video.wav [-fps 30] [-fit pad|crop] [-size 21x12] <video>
go run ./cmd/videoconv -check out/video.led.gz       # frames, length
```

`-frames ""` or `-audio ""` skips that output. `make video-convert` passes `VIDEO_FLAGS` through.

- **Frame rate:** `-fps` (default 30, at most 50 — the screen ticks every 20 ms). ffmpeg resamples
  to a constant rate. Durations are whole ms, so the converter carries the rounding over (30 fps:
  33, 34, 33, …): plain 33 ms frames would run 1 % fast, 2 s ahead of the sound by the end.
- **Scaling:** ffmpeg area averaging, after squaring the pixels (SAR), so the edges of silhouettes
  become grey levels instead of jaggies. `-fit pad` (default) keeps the whole picture: a 4:3 video
  becomes 16×12 centred, with black columns (2 left, 3 right); 16:9 fills almost the whole width.
  `-fit crop` fills the screen and cuts the overhang — for 4:3 the top and bottom quarter, which in
  Bad Apple often holds the heads, so pad is the default.
- **Gamma:** the frames hold the video's own levels, which are gamma-encoded (sRGB-like) — the same
  convention as the firmware's patterns, which hold uncorrected levels. The daemon applies the
  screen's gamma (`matrix.gamma`, 2.2 on orion) on load, exactly as for a stock pattern, so a
  `matrix.gamma` tweak in `<cfw>/board.conf` changes both alike. Colour videos work (the screen is
  RGB); Bad Apple is grey.
- **Audio:** `ffmpeg -vn -ac 2 -ar 48000 -c:a pcm_s16le`.

The test `TestConvertWithFFmpeg` checks the whole path on a clip ffmpeg synthesises itself
(`testsrc` + `sine`), so the tests never need real media.

## Sync

The picture is clocked by the audio, not by a frame counter. When the frames are loaded, the daemon
starts aplay and takes that moment (plus `video_av_offset_ms`) as frame time zero; the base layer
then draws, on every 20 ms screen tick, the frame under the elapsed time (binary search over the
frames' start times). A late or skipped tick never accumulates: the next one shows the right frame
again. The remaining error is aplay's start-up latency (a constant: the offset knob) and the drift
between the audio clock and the system clock (tens of ppm, a few ms over a few minutes).

The frames follow the display brightness (auto brightness included), like every pattern.

## What ends it

The rule: anything that takes the speaker or the user's attention ends the video, and the display
goes back to its persisted mode (`video` is never persisted, so a restart does not replay it).

| Event | Why it ends |
|---|---|
| the end of the picture | (an audio track longer than the picture is cut there) |
| a voice session: the wake word, the voice key | stopped before the session opens, so the request is not drowned |
| «стоп» / «хватит» (the command spotter is armed while the video sounds, as for any playback) | the player stops; «громче»/«тише» work and do not end it |
| a reply (TTS), an HA announcement, a ringing timer | they take the player, which cuts our aplay |
| an intercom call (ringing, calling, in a call) | the call takes over |
| music starting on the Music Assistant player | the user started it |
| another display mode, or a `display_message` | |
| the frames file missing or unreadable | logged; nothing plays, the mode falls back at once |

Overlays (the volume bubble, HA's text, expressions) still draw on top while it plays.

**Music:** the mode refuses to start while Music Assistant is streaming to this speaker (logged;
the select goes back) — sendspin-host has no pause, and the two tracks would mix. Pause the music
first. While the video sounds, music started later stops the video (above); the video's own sound
counts as the assistant's playback, so it would duck music anyway.

**Missing audio:** no audio file, no `aplay`, or aplay failing at start (no such device — the host
simulator) → the video plays silently, clocked from its own start. That is how it runs in
`cmd/ledsim`.

## Limits and on-device checks

- Not yet run on a unit. To check there: the A/V offset (clap or cut points in the video against
  the sound), whether the first frames are smooth while aplay opens the dmix, the gamma and the
  dark grey levels at low brightness (see the screen work list in [boards/orion.md](boards/orion.md)),
  load time of the frames (parsed in the background before the sound starts; on the host 6570
  frames parse in under 0.1 s), and that the wake word still fires over the video's sound (it does
  over music, through the hardware AEC reference).
- Only pixel screens: the mode is offered (HA select option, command) only where the front display
  is a dot-matrix; seg7 speakers keep clock / off / message.
- One file pair at a time (the config keys). A playlist or several named videos would need a select
  of their own; not done.
