#!/bin/sh
# Builds the maintenance initramfs: a static aarch64 BusyBox plus our /init.
#   emu/maint/build.sh            -> emu/work/maint/initramfs.cpio.gz
# Needs aarch64-linux-gnu-gcc (+ its static glibc) and cpio on the host. No root needed.
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
EMU=$(dirname "$HERE")
WORK=$EMU/work
OUT=$WORK/maint

BB_VER=1.36.1
BB_SHA256=b8cc24c9574d809e7279c3be349795c5d5ceb6fdf19ca709f80cde50e47de314
BB_URL=https://busybox.net/downloads/busybox-$BB_VER.tar.bz2
CROSS=${CROSS:-aarch64-linux-gnu-}

DL=$WORK/dl/busybox
SRC=$OUT/src/busybox-$BB_VER
mkdir -p "$DL" "$OUT/src"

TARBALL=$DL/busybox-$BB_VER.tar.bz2
if [ ! -f "$TARBALL" ]; then
	curl -fsSL -o "$TARBALL.part" "$BB_URL"
	mv "$TARBALL.part" "$TARBALL"
fi
echo "$BB_SHA256  $TARBALL" | sha256sum -c - >/dev/null || {
	echo "busybox tarball sha256 mismatch: $TARBALL" >&2; exit 1; }

if [ ! -x "$SRC/busybox" ] || [ "$HERE/busybox.fragment" -nt "$SRC/busybox" ]; then
	rm -rf "$SRC"
	tar xjf "$TARBALL" -C "$OUT/src"
	make -C "$SRC" -s ARCH=arm64 CROSS_COMPILE="$CROSS" defconfig
	# Apply our overrides on top of defconfig: drop each key, then append our value.
	while IFS= read -r line; do
		case $line in
			'# CONFIG_'*' is not set') key=${line#\# }; key=${key%% *} ;;
			''|'#'*) continue ;;
			*) key=${line%%=*} ;;
		esac
		sed -i "/^$key=/d; /^# $key is not set/d" "$SRC/.config"
		echo "$line" >> "$SRC/.config"
	done < "$HERE/busybox.fragment"
	yes '' | make -C "$SRC" -s ARCH=arm64 CROSS_COMPILE="$CROSS" oldconfig >/dev/null
	make -C "$SRC" -s ARCH=arm64 CROSS_COMPILE="$CROSS" -j"$(nproc)" busybox
fi

# Stage the initramfs tree.
ROOT=$OUT/root
rm -rf "$ROOT"
mkdir -p "$ROOT/bin" "$ROOT/sbin" "$ROOT/usr/bin" "$ROOT/usr/sbin" "$ROOT/proc" "$ROOT/sys" \
	"$ROOT/dev" "$ROOT/tmp" "$ROOT/mnt/root" "$ROOT/mnt/rw" "$ROOT/mnt/seed" "$ROOT/etc"
cp "$SRC/busybox" "$ROOT/bin/busybox"
"${CROSS}strip" "$ROOT/bin/busybox"
cp "$HERE/init" "$ROOT/init"
chmod 755 "$ROOT/init" "$ROOT/bin/busybox"
"${CROSS}readelf" -h "$ROOT/bin/busybox" | grep -q AArch64
# Applet links, relative so they work inside the initramfs. The host cannot run the aarch64
# binary, so the applet list comes from the build tree.
make -C "$SRC" -s ARCH=arm64 CROSS_COMPILE="$CROSS" busybox.links
sed 's|^/||' "$SRC/busybox.links" | while read -r link; do
	[ "$link" = bin/busybox ] && continue
	dir=$(dirname "$link")
	mkdir -p "$ROOT/$dir"
	case $dir in
		bin) rel=busybox ;;
		sbin) rel=../bin/busybox ;;
		usr/bin|usr/sbin) rel=../../bin/busybox ;;
		*) rel=/bin/busybox ;;
	esac
	ln -sf "$rel" "$ROOT/$link"
done
echo "root:x:0:0:root:/root:/bin/sh" > "$ROOT/etc/passwd"

# cpio with root ownership, no fakeroot needed. There is no /dev/console node in the archive
# (creating one needs root): /init mounts devtmpfs first and then reopens the console itself.
(cd "$ROOT" && find . | LC_ALL=C sort | cpio -o -H newc -R 0:0 --quiet) | gzip -9 > "$OUT/initramfs.cpio.gz"
echo "maint initramfs: $OUT/initramfs.cpio.gz ($(du -k "$OUT/initramfs.cpio.gz" | cut -f1) KiB)"
