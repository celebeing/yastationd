# yastation-companion — the computer-side app

A helper that runs on the owner's computer, next to the [yastationd](../README.md) speakers, for
the jobs a speaker can't do alone: first, hearing what the computer plays so the speakers' lights
can pulse with it ([docs/pulse.md](docs/pulse.md)). It is meant to grow (more modules, Windows,
macOS and Linux, a tray icon with a local web page); what it does today is small.

**Status (2026-11-08): an experiment.** One command, `record`: it captures what the computer plays
(Windows only: WASAPI loopback) into a file, the reference side of the pulse measurements. Nothing
talks to a speaker yet, and nothing is decided about the protocol.

```sh
tools/build.sh companion                      # → companion/build/yastation-companion.exe (Windows, amd64)
yastation-companion.exe record -d 60s -o name # → name.raw (S16LE mono, the device's rate) + name.json
```

Docs: [docs/index.md](docs/index.md).
