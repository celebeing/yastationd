# Assets: the owner's own media on the speaker

Status (2026-10-28): videos built and tested on the desk (Go tests; the browser converter not yet
run on a real file against a unit). Packs, sounds and themes: [asset-packs.md](asset-packs.md).

The web app's **Assets** group holds media the owner adds to a speaker, one section per kind. The
group only lists the kinds this speaker can use, and is hidden when there is none (a Mini has no
pixel screen, so no videos).

## Videos

Clips for the video mode ([video-mode.md](video-mode.md)): a picture on the pixel screen with its
sound. Each video is a frames file and an optional WAV in the speaker's library,
`<cfw>/media/videos/<name>.led.gz` (or `.led`) and `<name>.wav`; a name is lower-case letters,
digits, `-` and `_`, up to 40.

**Adding one**, two ways:

- **Convert a video file** in the browser (no ffmpeg, nothing on the host): the app seeks the video
  frame by frame (at the chosen 15–30 fps, the frame time in the middle of each frame's slot),
  draws it on a canvas at 8× the screen's size (`pad`: the whole picture with black bars, `crop`:
  fill the screen), averages each 8×8 block into a pixel, and writes the frames as `.led` lines
  (`display.AppendLedFrame`'s format, with videoconv's rounding of the frame times), gzipped
  (`CompressionStream`). The sound is decoded by WebAudio, resampled to 48 kHz stereo
  (`OfflineAudioContext`) and written as 16-bit WAV. A file without a sound track (or one the
  browser cannot decode) gives a silent video. Seeking makes it slower than real time on long
  videos; the tab must stay open.
- **Upload converted files**: what `cmd/videoconv` makes (`make video-convert`), the frames and,
  if any, the WAV.

**On the speaker** (`internal/app/assets.go`): an upload streams to a temporary file in the library
(at most 64 MB of frames, 400 MB of sound; refused when it would leave less than 20 MB free), the
frames are parsed by the same loader the video mode uses (`display.LoadVideo`, so a file made for
another screen size or a broken one is refused with the parser's error), a WAV must start with
`RIFF…WAVE`; then it is renamed into place.

**Using one:** "Use for the video mode" points `video_frames` / `video_audio` at it (runtime keys:
no restart); "Play now" does that and starts the video mode. Deleting the video the mode uses sends
those keys back to their defaults (`<cfw>/media/video.led`, `video.wav`).

API (behind the login): `GET /api/assets` (per kind: whether this speaker supports it, the screen
size, the items with sizes and which one is in use; the free space), `PUT
/api/assets/videos/<name>/frames|audio` (the file as the body), `POST
/api/assets/videos/<name>/use`, `DELETE /api/assets/videos/<name>`.
