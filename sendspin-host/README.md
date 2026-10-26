# sendspin-host — the Music Assistant (Sendspin) player

A native helper that yastationd runs as a child process: the official
[sendspin-cpp](https://github.com/Sendspin/sendspin-cpp) client (player role, FLAC + PCM) with an
ALSA sink on the device's `default` PCM (dmix, shared with yastationd's own output). Design,
behaviour and findings: [`../docs/music-sendspin.md`](../docs/music-sendspin.md).

## Build

Built by `tools/build.sh sendspin` in its pinned container (`docker/zig.Dockerfile`; the repo
docs/building.md). By hand: zig 0.14.1 (`ZIG=/path/to/zig`), git, curl, cmake ≥ 3.16, ninja, make.

```sh
make                 # both: build/armhf/sendspin-host (bergamot), build/aarch64/sendspin-host (pickle)
make ARCH=armhf      # one
```

The first build fetches sendspin-cpp and its dependencies, pinned (`third_party/fetch.sh`), into
`third_party/src/`, builds them with CMake + Ninja per arch, and builds an alsa-lib link sysroot
(`third_party/sysroot.sh`; at runtime the firmware's libasound is used). The binary needs only
glibc (≤ 2.30 armhf, ≤ 2.34 aarch64), libasound and libdl/libpthread from the device.

## Install

`make deploy-sendspin SENDSPIN=../sendspin-host/build/<arch>/sendspin-host` in `../yastationd`,
or `tools/deploy.sh` with the arch column set. Then `sendspin = true` in yastationd.conf.

## Interface

```
sendspin-host -n NAME -s STATE_FILE [-p PORT] [-D PCM] [-u URL] [-a] [-V VOL] [-G] [-b BANDS] [-l LEVEL]
```

Options are documented at the top of `src/main.cpp`. stdin: `duck <0..1>`, `volume <0..100>`;
EOF ends it. stdout: `READY <client_id> <version>`, `CONNECTED 0|1`, `STREAM start <rate> <ch> <bits>` /
`STREAM end`, `VOLUME <0..100> <muted>`, `PLAYBACK playing|stopped` (the group's state),
`META <json>` (the track: title, artist, album, track, duration_ms, progress_ms, speed — 0 when paused;
`META` alone = none)
and, with `-b BANDS`, `LEVELS <hex>` (the spectrum, one byte per band, ~25/s, each when its audio
is heard). Logs on stderr.

## Files

| File | What |
|---|---|
| `src/main.cpp` | Options, the client, the player and metadata roles, the stdin/stdout protocol |
| `src/levels.{h,cpp}` | The spectrum for the display: an FFT of the PCM written, stamped with when it is heard |
| `src/alsa_sink.{h,cpp}` | ALSA output with playback-time feedback for Sendspin's sync, volume and a ramped duck gain |
| `third_party/` | Pinned fetch, the CMake glue for sendspin-cpp, the alsa-lib sysroot, zig toolchain wrappers |

## Credits

The build layout (`Makefile`, `third_party/`) and the ALSA sink are adapted from
[pulsar](https://github.com/Morvex885/pulsar) (MIT, © 2026 pulsar contributors; license text in `LICENSE-pulsar`). The file-backed
persistence provider is sendspin-cpp's example (Apache-2.0), compiled from the fetched source.
