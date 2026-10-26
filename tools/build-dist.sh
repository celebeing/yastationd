#!/bin/sh
# Build every artifact for one board in the pinned builder containers (tools/build.sh,
# docs/building.md) and collect them in dist/ with checksums.
#
#   VENDOR=<vendor-repo> tools/build-dist.sh
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
sysroot=$VENDOR/vendor/$BOARD/$fw
[ -d "$sysroot/system/vendor/quasar/libs" ] && [ -d "$sysroot/lib" ] ||
	{ echo "not a vendor sysroot (lib/ and system/vendor/quasar/libs/): $sysroot" >&2; exit 2; }

echo "board   = $BOARD"
echo "arch    = $ARCH"
echo "sysroot = $sysroot"

up=$(echo "$ARCH" | tr a-z A-Z)
targets="web yastationd hostapd spotter:$ARCH"
[ "${NO_SENDSPIN:-0}" = 1 ] || targets="$targets sendspin:$ARCH"
# shellcheck disable=SC2086 # targets is a list
env "SPOTTER_SYSROOT_$up=$sysroot" "$here/tools/build.sh" $targets

dist=$here/dist
rm -rf "$dist"
mkdir -p "$dist/$ARCH"
cp "$here/yastationd/build/yastationd" "$here/hostapd/build/hostapd" "$dist/"
cp "$here/spotter-host/build/$ARCH/spotter-host" "$dist/$ARCH/"
[ "${NO_SENDSPIN:-0}" = 1 ] || cp "$here/sendspin-host/build/$ARCH/sendspin-host" "$dist/$ARCH/"
(cd "$dist" && find . -type f ! -name SHA256SUMS -exec sha256sum {} + | sort -k2 > SHA256SUMS)

echo
echo "artifacts in $dist:"
(cd "$dist" && find . -type f ! -name SHA256SUMS | sort | sed 's|^\./|  |')
