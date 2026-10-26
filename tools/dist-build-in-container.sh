#!/bin/sh
# Runs INSIDE the pinned build container (repo root Dockerfile). Called by tools/build-dist.sh;
# not meant to be run on a host. Inputs: ARCH, SYSROOT (the vendor tree mount), NO_SENDSPIN.
#
# Everything lands in /src/dist (the repo's dist/, mounted back to the host).
set -eu

: "${ARCH:?}" "${SYSROOT:?}"
[ -d "$SYSROOT/system/vendor/quasar/libs" ] || {
	echo "not a vendor sysroot (no system/vendor/quasar/libs): $SYSROOT" >&2
	exit 2
}
[ -d "$SYSROOT/lib" ] || { echo "not a vendor sysroot (no lib/): $SYSROOT" >&2; exit 2; }

dist=/src/dist
rm -rf "$dist"
mkdir -p "$dist/$ARCH"

echo "== web app (embedded in yastationd: rebuilt here so dist never ships a stale one)"
(cd /src/web && npm_config_store_dir=/src/web/node_modules/.pnpm-store pnpm install --frozen-lockfile && pnpm build)

echo "== yastationd (Go, static armv7 — one binary for every board)"
make -s -C /src/yastationd test build
cp /src/yastationd/build/yastationd "$dist/yastationd"

echo "== hostapd (zig, static armv7 — one binary for every board)"
make -s -C /src/hostapd
cp /src/hostapd/build/hostapd "$dist/hostapd"

echo "== spotter-host ($ARCH, links $SYSROOT)"
mkdir -p "/src/spotter-host/build/$ARCH"
ARCH="$ARCH" SYSROOT="$SYSROOT" OUT="/src/spotter-host/build/$ARCH/spotter-host" \
	sh /src/spotter-host/build.sh
cp "/src/spotter-host/build/$ARCH/spotter-host" "$dist/$ARCH/spotter-host"

if [ "${NO_SENDSPIN:-0}" = 1 ]; then
	echo "== sendspin-host skipped (NO_SENDSPIN=1)"
else
	echo "== sendspin-host ($ARCH; fetches its sources on the first run)"
	make -s -C /src/sendspin-host ARCH="$ARCH" binary
	cp "/src/sendspin-host/build/$ARCH/sendspin-host" "$dist/$ARCH/sendspin-host"
fi

echo "== what comes off the device's own symbols"
file "$dist/yastationd" "$dist/$ARCH/spotter-host" 2>/dev/null || true

(cd "$dist" && find . -type f ! -name SHA256SUMS -exec sha256sum {} + | sort -k2 > SHA256SUMS)
echo "== dist/SHA256SUMS written"
