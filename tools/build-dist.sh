#!/bin/sh
# Build every artifact for one board in the pinned container (Dockerfile, docs/building.md).
#
#   tools/build-dist.sh
#
# Env:
#   BOARD=<name>        board profile to build for (default: orion)
#   FW=<version_full>   firmware whose vendor tree is the spotter sysroot (default: newest in the
#                       vendor repo — pass the unit's own, the libs must match its firmware)
#   VENDOR=<path>       your vendor-tree checkout, laid out vendor/<board>/<version_full>/ (required)
#   ARCH=<arch>         userland arch (default: the profile's platform.userland_arch)
#   NO_SENDSPIN=1       skip the music player (its build fetches sources from the internet)
#
# Output (dist/, gitignored):
#   yastationd, hostapd          one static binary that runs on every board
#   <arch>/spotter-host          links the vendor front-end: needs the matching sysroot
#   <arch>/sendspin-host         the music player
#   SHA256SUMS
#
# The host only needs docker; every compiler lives in the image. The vendor tree is mounted
# read-only and never copied into the image.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
BOARD=${BOARD:-orion}
profile=$here/yastationd/internal/board/profiles/$BOARD.conf
[ -f "$profile" ] || { echo "no board profile: $profile" >&2; exit 2; }

ARCH=${ARCH:-$(sed -n 's/^[[:space:]]*platform\.userland_arch[[:space:]]*=[[:space:]]*//p' "$profile" | head -1)}
[ -n "$ARCH" ] || { echo "cannot read platform.userland_arch from $profile" >&2; exit 2; }

[ -n "${VENDOR:-}" ] || { echo "set VENDOR=<path to your vendor-tree checkout> (docs/building.md)" >&2; exit 2; }
[ -d "$VENDOR/vendor/$BOARD" ] || { echo "no vendor tree for '$BOARD' under $VENDOR" >&2; exit 2; }
fw=${FW:-$(ls -1 "$VENDOR/vendor/$BOARD" | sort -V | tail -1)}
[ -d "$VENDOR/vendor/$BOARD/$fw" ] || { echo "no firmware '$fw' for $BOARD in $VENDOR" >&2; exit 2; }

command -v docker >/dev/null 2>&1 || { echo "docker not found" >&2; exit 2; }

echo "board   = $BOARD"
echo "arch    = $ARCH"
echo "sysroot = $VENDOR/vendor/$BOARD/$fw"

docker build -q -t yastation-build -f "$here/Dockerfile" "$here" >/dev/null

docker run --rm \
	-v "$here:/src" \
	-v "$VENDOR:/vendor:ro" \
	-e ARCH="$ARCH" \
	-e SYSROOT="/vendor/vendor/$BOARD/$fw" \
	-e NO_SENDSPIN="${NO_SENDSPIN:-0}" \
	yastation-build sh /src/tools/dist-build-in-container.sh

echo
echo "artifacts in $here/dist:"
(cd "$here/dist" && find . -type f ! -name SHA256SUMS | sort | sed 's|^\./|  |')
