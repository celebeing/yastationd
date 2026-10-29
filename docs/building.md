# Building the artifacts: one script, a container per toolchain

Everything the CFW puts on a speaker is built in pinned builder containers by one script,
`tools/build.sh`. The host needs **docker** (and sh, git); no Go, Node, zig or cross gcc.
[deploying.md](deploying.md) §1 has what each binary does on the device; this page is the
toolchain.

## The builders

One image per toolchain, in `docker/`. Each carries its Dockerfile's hash as a label; `build.sh`
builds an image only when it is missing or its Dockerfile changed, with the progress shown, and
otherwise never calls `docker build` (a broken docker build cache once made a builder rebuild the
zig image on every target; if a build of an image keeps failing, `docker builder prune`):

| Image | Dockerfile | Builds | Pins |
|---|---|---|---|
| `yastation-go` | `docker/go.Dockerfile` | `yastationd` (tests too) | Go 1.22.5 (`golang:1.22.5-bookworm`), `GOTOOLCHAIN=local`; one static armv7 binary for every board |
| `yastation-node` | `docker/node.Dockerfile` | the web app (`web/`), embedded in yastationd | Node 18.20.8, pnpm 8.14.1 |
| `yastation-zig` | `docker/zig.Dockerfile` | `sendspin-host` (per arch), `hostapd`, `dropbear` | **zig 0.14.1** (0.11–0.13 emit armhf C++ binaries that crash before `main`; the tarball for the builder's own arch, x86_64 or aarch64, checked by sha256), cmake, ninja, git, curl |
| `yastation-cross` | `docker/cross.Dockerfile` | `spotter-host` (per arch) | Ubuntu 22.04's gcc 11 cross toolchains (glibc 2.35): the toolchain the deployed builds were made with — its output is byte-identical to them (checked 2026-10-27) |

## tools/build.sh

```sh
tools/build.sh                 # all: web yastationd hostapd dropbear sendspin spotter
tools/build.sh yastationd      # tests + the daemon (the web app first, when its source changed)
tools/build.sh sendspin:armhf  # one arch; "sendspin" = both
tools/build.sh spotter:aarch64 # needs a sysroot (below); "spotter" = both, skipping one without
tools/build.sh web hostapd
```

| Target | Output (where deploy.sh and the Makefiles look) |
|---|---|
| `web` | `yastationd/internal/webui/dist/app/` (not committed) |
| `yastationd` | `yastationd/build/yastationd` |
| `hostapd` | `hostapd/build/hostapd` |
| `dropbear` | `dropbear/build/dropbearmulti` (static aarch64, every board; [dropbear/README.md](../dropbear/README.md)) |
| `sendspin:<arch>` | `sendspin-host/build/<arch>/sendspin-host` |
| `spotter:<arch>` | `spotter-host/build/<arch>/spotter-host` |
| `packtool` | `yastationd/build/packtool` (for the building host, not the speakers: the asset-pack checker) |

- Each target is a `docker run` of its image with the repo mounted **at its own path** (so the
  CMake caches of `sendspin-host` stay valid in and out of the container), as the calling user.
- Caches survive between runs in `${XDG_CACHE_HOME:-~/.cache}/yastation-build` (Go build and
  module caches, zig's cache; the pnpm store sits in `web/node_modules/`). A warm `yastationd`
  build takes seconds.
- `sendspin-host`, `hostapd` and `dropbear` fetch their pinned sources on the first build (network needed
  once); everything else builds offline.
- A `sendspin-host/build/<arch>` configured in another directory (a moved checkout, or the
  builder before v0.16.2 that mounted the repo at `/src`) is wiped and built anew by its Makefile:
  CMake refuses a cache made elsewhere.
- `tools/deploy.sh` runs it for what it ships (`NATIVE=1` there builds `yastationd` with the host's
  own Go instead). `make build` in `yastationd/` still builds natively with a host Go, and the web
  app through `tools/build-web.sh` (= `build.sh web`).

## The sysroot (spotter-host)

`spotter-host` is the one artifact that cannot be built from this repo alone: it links the vendor
front-end library. It needs a sysroot per userland arch — a directory with the firmware's `lib/`
and `system/vendor/quasar/libs/` — and it must match what the units run: the soname set and glibc
version bind the binary. Three ways to get one:

- **Off your own speaker** (the simple one):

  ```sh
  tools/fetch-vendor.sh root@<speaker-ip> <board>     # bergamot | pickle | orion
  ```

  It copies `lib/`, `etc/os-release`, `system/vendor/quasar/libs/`, `ledpatterns/` (the stock
  animations, for `cmd/ledsim -patterns`) and the version files into
  `vendor-local/<board>/<version_full>/` (gitignored, the layout of a vendor-repo tree), makes the
  tree's absolute symlinks relative so they never point into the host, and names it in
  `tools/build.env` as `SPOTTER_SYSROOT_<ARCH>` unless that is set already. On the speaker it runs
  only `cat` and `tar c` of firmware paths. **`tools/deploy.sh` does this by itself** for an arch
  that has no sysroot yet, from the first speaker of that arch in its list.
- **From your `<vendor-repo>`** (`vendor/<board>/<version_full>/`, verbatim from that firmware; a
  private checkout, never published), named in `tools/build.env`:

  ```sh
  SPOTTER_SYSROOT_ARMHF=/path/to/<vendor-repo>/vendor/bergamot/<version_full>
  SPOTTER_SYSROOT_AARCH64=/path/to/<vendor-repo>/vendor/pickle/<version_full>
  ```
- **Set in the environment** for one run; it wins over `tools/build.env`.

Without a setting, `build.sh` takes the one `vendor-local/` tree of a board with that arch (with
several it asks you to name one). Without any, `all` and `deploy.sh` skip that arch's spotter-host
(the last build is shipped) and say how to get a sysroot; `spotter:<arch>` asked for by name fails.

A speaker that runs vendor libraries moved from another firmware (`<cfw>/vendor/libs`, the black
Mini 3 of the author) still gives its own firmware's: the binary links against the front-end
library's interface, which has been the same across the firmwares we have.

## A dist for one board

```sh
VENDOR=/path/to/<vendor-repo> tools/build-dist.sh   # BOARD=orion by default; FW=<version_full>; NO_SENDSPIN=1
```

`build.sh` for that board's arch with its vendor tree as the sysroot, then the artifacts and
their checksums in `dist/` (gitignored): `yastationd`, `hostapd`, `<arch>/spotter-host`,
`<arch>/sendspin-host`, `SHA256SUMS`.

## Without docker

The Makefiles and `spotter-host/build.sh` are the recipes the containers run; with the same
versions installed by hand they work on a host (`make -C yastationd test build`,
`make -C sendspin-host`, `make -C hostapd`, `spotter-host/build.sh`). Check `go version`,
`zig version`, `<arch>-linux-gnu*-gcc --version` before blaming the sources.

## Host tools

Not shipped to a speaker; they run on the host: `cmd/packtool` builds and checks an asset pack
(`tools/build.sh packtool` builds it in the Go container, no Go on the host needed; then
`yastationd/build/packtool [-o x.zip] <dir or .zip>`; with Go installed, `make -C yastationd pack
PACK=<dir or .zip>` does both; [asset-packs.md](asset-packs.md)). The others need the Go
toolchain: `cmd/videoconv` converts a video (`make video-convert`,
[video-mode.md](video-mode.md)), `cmd/ledsim` and `cmd/demoreel` simulate the LEDs
([led-preview.md](led-preview.md)).
