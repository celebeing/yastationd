# Shared helpers for the emulator scripts (POSIX sh, sourced). Sets:
#   REPO EMU WORK BOARD BDIR (emu/work/<board>) IMGDIR SEED RESULTS KERNEL INITRD QEMU
# and everything from emu/boards/<board>.conf.

EMU=$(cd "$(dirname "$0")" && pwd)
while [ ! -f "$EMU/boards/bergamot.conf" ] && [ "$EMU" != / ]; do EMU=$(dirname "$EMU"); done
REPO=$(dirname "$EMU")
WORK=$EMU/work
QEMU=${QEMU:-qemu-system-aarch64}
KERNEL=${EMU_KERNEL:-$WORK/out/Image}
INITRD=$WORK/maint/initramfs.cpio.gz

die() { echo "emu: $*" >&2; exit 1; }
note() { echo "emu: $*" >&2; }

load_board() {
	BOARD=$1
	[ -n "$BOARD" ] || die "no board given"
	[ -f "$EMU/boards/$BOARD.conf" ] || die "unknown board '$BOARD' (see emu/boards/)"
	# shellcheck disable=SC1090
	. "$EMU/boards/$BOARD.conf"
	BDIR=$WORK/$BOARD
	IMGDIR=$BDIR/flash
	SEED=$BDIR/seed
	RESULTS=$BDIR/results
}

# Partition list without blank lines: "<index> <name> <size> <source>".
parts() {
	echo "$PARTS" | awk 'NF >= 2 { print i++, $1, $2, ($3 == "" ? "-" : $3) }'
}

# ---------------------------------------------------------------------------------------------
# Kernel cmdline contract with emu/kernel (patched block2mtd + emuboard). Everything that depends
# on the kernel side's exact syntax lives in this block. See emu/kernel/README.md.
#
# block2mtd.block2mtd=<dev>[,<erasesize>[,<name>[,<writesize>]]], repeatable, registered in order
# (emu/kernel/patches/0001-block2mtd-*.patch).
# Disk i (0-based, virtio-blk-pci in PCI slot DISK_SLOT0+i) is /dev/vd<a+i> in the guest, and
# backs mtd<i> named after the partition.
DISK_SLOT0=16
disk_letter() { printf "\\$(printf '%03o' $((97 + $1)))"; }
mtd_args() {
	parts | while read -r i name size src; do
		printf ' block2mtd.block2mtd=/dev/vd%s,%s,%s,%s' "$(disk_letter "$i")" "$ERASESIZE" "$name" "$WRITESIZE"
	done
	# Seconds block2mtd waits for each virtio disk to appear.
	printf ' block2mtd.timeout=10'
}
board_args() { printf 'emuboard=%s' "$BOARD"; }
# ---------------------------------------------------------------------------------------------

# 0xff-filled file of <size> bytes (blank NAND).
blank_file() {
	tr '\000' '\377' < /dev/zero | head -c "$(($2))" > "$1"
}

# Builds emu/work/<board>/flash/mtd<i>-<name>.img from the board file. Never writes to dumps/.
make_images() {
	mkdir -p "$IMGDIR"
	parts | while read -r i name size src; do
		img=$IMGDIR/mtd$i-$name.img
		if [ "$src" = - ]; then
			note "mtd$i $name: blank ($size)"
			blank_file "$img.tmp" "$size"
		else
			[ -f "$REPO/$src" ] || die "source image $src missing"
			have=$(wc -c < "$REPO/$src")
			[ "$have" -eq "$((size))" ] || die "$src is $have bytes, partition $name is $((size))"
			note "mtd$i $name: copy of $src"
			cp "$REPO/$src" "$img.tmp"
		fi
		mv "$img.tmp" "$img"
	done
	date -u +%FT%TZ > "$IMGDIR/created"
}

images_present() {
	[ -f "$IMGDIR/created" ] || return 1
	parts | while read -r i name size src; do
		[ -f "$IMGDIR/mtd$i-$name.img" ] || exit 1
	done
}

# QEMU arguments for the disks, in mtd order with fixed PCI slots.
disk_qemu_args() {
	parts | while read -r i name size src; do
		printf ' -drive if=none,id=mtd%s,format=raw,cache=writeback,file=%s' "$i" "$IMGDIR/mtd$i-$name.img"
		printf ' -device virtio-blk-pci,drive=mtd%s,addr=0x%x' "$i" $((DISK_SLOT0 + i))
	done
}

# PID of the QEMU instance of this board, if it is alive.
qemu_pid() {
	[ -f "$BDIR/qemu.pid" ] || return 1
	p=$(cat "$BDIR/qemu.pid" 2>/dev/null)
	[ -n "$p" ] && kill -0 "$p" 2>/dev/null && echo "$p"
}
