# Building the artifacts: one script, a container per toolchain

Everything the CFW puts on a speaker is built in pinned builder containers by one script,
`tools/build.sh`. The host needs **docker** (and sh, git); no Go, Node, zig or cross gcc.
[deploying.md](deploying.md) §1 has what each binary does on the device; this page is the
toolchain.

## The builders

One image per toolchain, in `docker/` (built on first use, then cached by docker):

| Image | Dockerfile | Builds | Pins |
|---|---|---|---|
| `yastation-go` | `docker/go.Dockerfile` | `yastationd` (tests too) | Go 1.22.5 (`golang:1.22.5-bookworm`), `GOTOOLCHAIN=local`; one static armv7 binary for every board |
| `yastation-node` | `docker/node.Dockerfile` | the web app (`web/`), embedded in yastationd | Node 18.20.8, pnpm 8.14.1 |
| `yastation-zig` | `docker/zig.Dockerfile` | `sendspin-host` (per arch), `hostapd`, `dropbear` | **zig 0.14.1** (0.11–0.13 emit armhf C++ binaries that crash before `main`), cmake, ninja, git, curl |
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
and `system/vendor/quasar/libs/` — from your **`<vendor-repo>`** (`vendor/<board>/<version_full>/`,
verbatim from that firmware; a private checkout, never published) or pulled off a unit
([deploying.md](deploying.md) §1). It must match what the units run: the soname set and glibc
version bind the binary. Name them in `tools/build.env` (gitignored; `tools/build.env.example`):

```sh
SPOTTER_SYSROOT_ARMHF=/path/to/<vendor-repo>/vendor/bergamot/<version_full>
SPOTTER_SYSROOT_AARCH64=/path/to/<vendor-repo>/vendor/pickle/<version_full>
```

Without one, `all` and `deploy.sh` skip that arch's spotter-host (the last build is shipped);
`spotter:<arch>` asked for by name fails.

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
