# Companion architecture

## The stack (decided 2026-11-08)

- **Go**, the daemon's language: the speakers' protocol, MQTT and discovery can be shared rather
  than written twice.
- **Audio per OS behind one interface** (`internal/capture`): `Loopback(ctx, started, handler)`
  gives mono float blocks with their stream position. Windows: WASAPI loopback in shared mode, pure
  Go over COM. Linux (PipeWire/Pulse monitor source) and macOS (no system loopback without a
  virtual device or ScreenCaptureKit) are not written.
- **The interface, when there is one:** a tray icon and a local web page (the speakers' web app's
  stack, Preact), not a native UI toolkit; chosen over Qt for the shared code and a docker build.

## Modules

| Module | State | What |
| --- | --- | --- |
| `internal/capture` | Windows | what the computer plays, mono, with stream positions (gaps = nothing played) |
| `cmd/yastation-companion record` | works | the capture into a file + a JSON sidecar, for measurements |
| pulse | research | the beat model sent to the speakers ([pulse.md](pulse.md)) |

## Platforms

| OS | Capture | Built | Tried |
| --- | --- | --- | --- |
| Windows 11 (amd64) | WASAPI loopback, the default output device | yes | yes (44.1 kHz stereo mix) |
| Linux | — | yes (capture says unsupported) | — |
| macOS | — | no | — |

Notes from Windows: the loopback's device position counts from when the device started, not from
the stream (the first packet is position 0 for us); the device sends no packets while nothing
plays, so a jump in the position is silence.
