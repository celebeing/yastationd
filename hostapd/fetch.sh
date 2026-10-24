#!/bin/sh
# One-time fetch of the sources into src/ (gitignored), each pinned: the hostapd release tarball by
# its sha256, libnl-tiny (OpenWrt's small libnl replacement, which hostapd's nl80211 driver links)
# by commit. Re-running is a no-op once fetched; delete src/ to refetch after a pin change.
set -eu
cd "$(dirname "$0")"
mkdir -p src

HOSTAPD=hostapd-2.11
HOSTAPD_SHA256=2b3facb632fd4f65e32f4bf82a76b4b72c501f995a4f62e330219fe7aed1747a
LIBNL_TINY=40493a655d8caa2ccf5206dde1e733abe2920432

if [ ! -e src/$HOSTAPD/.fetched ]; then
	curl -fsSL -o src/$HOSTAPD.tar.gz https://w1.fi/releases/$HOSTAPD.tar.gz
	echo "$HOSTAPD_SHA256  src/$HOSTAPD.tar.gz" | sha256sum -c -
	rm -rf src/$HOSTAPD
	tar -xzf src/$HOSTAPD.tar.gz -C src
	rm src/$HOSTAPD.tar.gz
	touch src/$HOSTAPD/.fetched
	echo "fetched $HOSTAPD"
fi

if [ ! -e src/libnl-tiny/.fetched ] || [ "$(cat src/libnl-tiny/.fetched)" != "$LIBNL_TINY" ]; then
	rm -rf src/libnl-tiny
	git init -q src/libnl-tiny
	git -C src/libnl-tiny remote add origin https://github.com/openwrt/libnl-tiny.git
	git -C src/libnl-tiny fetch -q --depth 1 origin $LIBNL_TINY
	git -C src/libnl-tiny -c advice.detachedHead=false checkout -q FETCH_HEAD
	git -C src/libnl-tiny rev-parse HEAD >src/libnl-tiny/.fetched
	echo "fetched libnl-tiny @ $LIBNL_TINY"
fi
