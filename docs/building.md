# Building the artifacts: pins and the container

What `make` needs, why the versions are not negotiable, and the one command that builds
everything the same way twice. [deploying.md](deploying.md) §1 has the per-artifact commands and
what each binary does on the device; this page is only about the toolchain.

**Status (2026-10-26):** the container and `tools/build-dist.sh` were written against the versions
the Makefiles enforce, but have not been run end to end on a docker host yet. The host commands
below are the ones in use.

## The pins

| Artifact | Toolchain | Where the requirement comes from |
|---|---|---|
| `yastationd` | Go 1.22.5, `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7` | `yastationd/go.mod` (`go 1.22.5`); the static armv7 binary runs on every board, including aarch64 userlands |
| `spotter-host` | `aarch64-linux-gnu-gcc` or `arm-linux-gnueabihf-gcc` **plus a vendor sysroot**, glibc of the device (2.30 armhf / 2.36 aarch64) | `spotter-host/build.sh`; it links the vendor `libyandex_evil.so` |
| `sendspin-host` | **zig 0.14.1**, cmake ≥ 3.16, ninja, git, curl | `sendspin-host/Makefile` refuses 0.11–0.13: those emit armhf C++ binaries that crash before `main` |
| `hostapd` | **zig 0.14.1**, git, curl | `hostapd/Makefile`; one static armv7/musl binary, links nothing from the device |
| `web` (the app) | Node 18+ and pnpm, **or just docker** | `web/` (`make web` in `yastationd/`; without pnpm it runs `tools/build-web.sh`, a pinned Node 18.20.8 + pnpm 8.14.1 container, `web/Dockerfile`). The output in `yastationd/internal/webui/dist` is committed, so a Go build needs no Node; the dist container rebuilds it |

A rolling distro will hand you a newer zig (or none), and the build either stops at the version
check or produces a binary that dies on the speaker. That is the whole reason for the container.

## The sysroot

`spotter-host` is the one artifact that cannot be built from this repo alone: it needs the vendor
runtime it links against. Take it from your **`<vendor-repo>`** (a private checkout of vendor trees, never published), `vendor/<board>/<version_full>/` —
`lib/` and `system/vendor/quasar/libs` are exactly the two directories, verbatim from that
firmware. It must be the firmware the unit runs (or a compatible one for the same board): the
soname set and glibc version are what bind the binary.

## One command

```sh
VENDOR=/path/to/<vendor-repo> tools/build-dist.sh   # BOARD=orion by default; FW=<version_full> to pick a tree
```

It builds the image from `Dockerfile`, mounts this repo at `/src` and the vendor checkout at
`/vendor` (read-only), and writes:

```
dist/yastationd                 dist/hostapd
dist/<arch>/spotter-host        dist/<arch>/sendspin-host
dist/SHA256SUMS
```

`NO_SENDSPIN=1` skips the music player (its first build fetches sendspin-cpp and its dependencies
from the internet — the only step that needs network). `VENDOR=<path>` (required) is your
vendor-tree checkout.

Deploying what it produced is the normal `make deploy*` / `tools/deploy.sh` flow: the Makefiles read
`spotter-host/build/<arch>/spotter-host` and `sendspin-host/build/<arch>/sendspin-host`, which is
where the in-container build leaves them.

## Without the container

Install the same versions by hand and run the commands in [deploying.md](deploying.md) §1. Check
what you have (`go version`, `zig version`, `<arch>-linux-gnu*-gcc --version`) before blaming the
sources; the current known-good pair is Go 1.22.5 + zig 0.14.1.
