# Building the companion

`tools/build.sh companion` (Windows, the default) or `tools/build.sh companion:linux`, in the
same pinned Go container as the daemon ([../../docs/building.md](../../docs/building.md)): vet for
the host and the target OS, the tests, then `companion/build/yastation-companion[.exe]` for amd64.

- **No cgo.** Windows' audio is COM, called from Go (`go-ole`, `go-wca`), so one Linux container
  cross-builds every OS. A backend that needs C (macOS' CoreAudio) will change this; decide then.
- **Its own Go module** (`companion/go.mod`), so its dependencies never reach the speakers' binary.
  Code shared with the daemon (MQTT, the speakers' discovery) will come through a `replace` of
  `../yastationd`, not by copying.
- **Dependencies:** `github.com/go-ole/go-ole` v1.3.0, `github.com/moutend/go-wca` v0.3.0 (both
  MIT), exact versions in `go.mod`/`go.sum`.
