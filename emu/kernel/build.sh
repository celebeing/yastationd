#!/usr/bin/env bash
# Build the emulator guest kernel: Linux 5.4.x arm64 for QEMU "virt", everything built in.
#
#   emu/kernel/build.sh            fetch + verify + patch + configure + build -> emu/work/out/Image
#   emu/kernel/build.sh config     stop after configuring (emu/work/build/.config)
#   emu/kernel/build.sh clean      drop the source tree and build dir (keeps the download)
#
# Env: JOBS (default nproc), CROSS_COMPILE (default aarch64-linux-gnu-).
set -euo pipefail

KVER=5.4.283
KSHA256=250c6a3199b42a1cb1427c9bbeacf5d90fb91bcf9f227195f484a1c877e1f797
KURL=https://cdn.kernel.org/pub/linux/kernel/v5.x

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(cd "$HERE/.." && pwd)/work
DL=$WORK/dl
SRC=$WORK/src/linux-$KVER
BUILD=$WORK/build
OUT=$WORK/out
JOBS=${JOBS:-$(nproc)}
export ARCH=arm64
export CROSS_COMPILE=${CROSS_COMPILE:-aarch64-linux-gnu-}
# Reproducible build metadata.
export KBUILD_BUILD_USER=emu KBUILD_BUILD_HOST=emu
export KBUILD_BUILD_TIMESTAMP="1970-01-01 00:00:00 UTC"

say() { printf '>> %s\n' "$*"; }
die() { printf 'build.sh: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" = clean ]; then
	rm -rf "$WORK/src" "$BUILD"
	exit 0
fi

command -v "${CROSS_COMPILE}gcc" >/dev/null || die "${CROSS_COMPILE}gcc not found"

# --- fetch + verify --------------------------------------------------------------------------
mkdir -p "$DL"
tarball=$DL/linux-$KVER.tar.xz
if [ ! -f "$tarball" ]; then
	say "downloading linux-$KVER"
	curl -fL --proto '=https' -o "$tarball.part" "$KURL/linux-$KVER.tar.xz"
	mv "$tarball.part" "$tarball"
fi
[ -f "$DL/sha256sums.asc" ] || curl -fsSL --proto '=https' -o "$DL/sha256sums.asc" "$KURL/sha256sums.asc"
got=$(sha256sum "$tarball" | cut -d' ' -f1)
[ "$got" = "$KSHA256" ] || die "sha256 mismatch for $tarball: $got (pinned $KSHA256)"
grep -q "^$KSHA256  linux-$KVER.tar.xz\$" "$DL/sha256sums.asc" ||
	die "pinned sha256 is not in kernel.org's sha256sums.asc"
if command -v gpg >/dev/null && gpg --verify "$DL/sha256sums.asc" 2>/dev/null; then
	say "sha256sums.asc signature OK"
else
	say "note: sha256sums.asc signature not checked (import the kernel.org autosigner key to do so)"
fi
say "linux-$KVER.tar.xz sha256 OK"

# --- source tree (re-extracted whenever the patch set changes) --------------------------------
stamp=$( (echo "$KVER"; cat "$HERE"/patches/*.patch) | sha256sum | cut -d' ' -f1)
if [ ! -f "$SRC/.emu-stamp" ] || [ "$(cat "$SRC/.emu-stamp")" != "$stamp" ]; then
	say "extracting + patching"
	rm -rf "$SRC"
	mkdir -p "$WORK/src"
	tar -C "$WORK/src" -xf "$tarball"
	for p in "$HERE"/patches/*.patch; do
		say "  $(basename "$p")"
		patch -d "$SRC" -p1 --forward --quiet < "$p"
	done
	echo "$stamp" > "$SRC/.emu-stamp"
fi
# The driver is copied every time so edits under emu/kernel/emuboard/ are picked up.
mkdir -p "$SRC/drivers/misc/emuboard"
for f in "$HERE"/emuboard/*; do
	cmp -s "$f" "$SRC/drivers/misc/emuboard/$(basename "$f")" || cp "$f" "$SRC/drivers/misc/emuboard/"
done

# --- configure -------------------------------------------------------------------------------
mkdir -p "$BUILD" "$OUT"
frag=$HERE/emu.config
if [ ! -f "$BUILD/.config" ] || [ "$frag" -nt "$BUILD/.config" ] || [ "$0" -nt "$BUILD/.config" ]; then
	say "configuring (allnoconfig + emu.config)"
	make -s -C "$SRC" O="$BUILD" KCONFIG_ALLCONFIG="$frag" allnoconfig
fi
# Every explicit setting in the fragment must have survived dependency resolution.
bad=0
while IFS= read -r line; do
	case "$line" in
	CONFIG_*=*)
		sym=${line%%=*}
		grep -qxF "$line" "$BUILD/.config" || { echo "  wanted $line, got: $(grep -E "^$sym=|^# $sym is not set" "$BUILD/.config" || echo unset)"; bad=1; }
		;;
	"# CONFIG_"*" is not set")
		sym=${line#\# }; sym=${sym%% *}
		! grep -q "^$sym=" "$BUILD/.config" || { echo "  wanted $sym off, got: $(grep "^$sym=" "$BUILD/.config")"; bad=1; }
		;;
	esac
done < "$frag"
[ $bad = 0 ] || die "emu.config settings did not apply (missing dependency?)"
cp "$BUILD/.config" "$OUT/config"
[ "${1:-}" = config ] && { say "config at $BUILD/.config"; exit 0; }

# --- build -----------------------------------------------------------------------------------
say "building Image (-j$JOBS)"
make -s -C "$SRC" O="$BUILD" -j"$JOBS" Image
cp "$BUILD/arch/arm64/boot/Image" "$OUT/Image"
cp "$BUILD/System.map" "$OUT/System.map"
say "done: $OUT/Image ($(du -h "$OUT/Image" | cut -f1)), config $OUT/config"
