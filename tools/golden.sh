#!/bin/sh
# golden.sh — make the CFW what a factory reset restores, on boards with an unsigned restore package
# (pickle, orion; docs/guides/golden-image.md).
#
#   tools/golden.sh build <ssh-target> <platform> [work-dir]
#       From a donor running the CFW: its rootfs, its restore package and the part of <cfw> that is
#       not the owner's (a list below) go into a new package in work-dir (default
#       dumps/golden/<platform>-<time>, gitignored: it holds vendor files, never publish it). The
#       unit's package stays there as stock.img.
#       Nothing on the speaker changes (a bind mount, undone at once). No keys, Wi-Fi, settings.
#   tools/golden.sh flash <ssh-target> <image>
#       Write a package (golden.img, or a stock dump to go back) into the unit's aml_sysrecovery,
#       check it by reading it back. A normal boot never reads that partition; a reset does.
#
#       pickle and orion have been through a reset. On any other board it refuses without
#       UNTRIED=<board>: a package the recovery refuses there has no way back.
#
# Env: SSHKEY as for deploy.sh. The host needs docker, sh, ssh.
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$target" "$@"; }
die() { echo "golden: $*" >&2; exit 1; }

# On the speaker: DEV = the aml_sysrecovery partition, for reading (a mtd or block device).
FINDDEV='
DEV=
n=$(sed -n "s/^\(mtd[0-9]*\):.*\"aml_sysrecovery\"$/\1/p" /proc/mtd 2>/dev/null)
[ -n "$n" ] && DEV=/dev/$n
if [ -z "$DEV" ]; then
	for u in /sys/class/block/*/uevent; do
		grep -q "^PARTNAME=aml_sysrecovery$" "$u" 2>/dev/null || continue
		DEV=/dev/$(sed -n "s/^DEVNAME=//p" "$u")
	done
fi
[ -n "$DEV" ] || { echo "no aml_sysrecovery partition here" >&2; exit 1; }
CFW=/bkupgrade/cfw; [ -d /bkupgrade ] || CFW=/data/cfw
'

# The part of <cfw> that goes into the image: programs and vendor-derived assets, nothing of the
# owner's. mkgolden.py refuses a seed with a name that looks like an owner's file.
CFWLIST='bin init.d openssh egress.sh sounds clips vendor iptables'

build() {
	[ $# -ge 2 ] || { echo "usage: $0 build <ssh-target> <platform> [work-dir]" >&2; exit 2; }
	target=$1 platform=$2
	[ -f "$here/yastationd/internal/board/profiles/$platform.conf" ] || die "unknown platform $platform"
	work=${3:-$here/dumps/golden/$platform-$(date -u +%Y%m%dT%H%M%SZ)}
	case $work in "$here"/*) ;; *) die "the work dir must be inside the repo (it is what the container sees)" ;; esac
	mkdir -p "$work"
	compat=$(ssh_ 'tr "\0" " " </proc/device-tree/compatible' </dev/null) || die "ssh to $target"
	case $compat in *"yandex,$platform"*) ;; *) die "$target is '$compat', not $platform" ;; esac

	echo "== the restore package of $target"
	ssh_ "$FINDDEV"'
s=$(od -An -tu8 -j12 -N8 "$DEV" | tr -d " ")
head -c "$s" "$DEV"' >"$work/stock.img" || die "reading the restore package"
	echo "== the rootfs"
	ssh_ 'mkdir -p /tmp/rb && mount --bind / /tmp/rb && (cd /tmp/rb && tar cf - .); rc=$?; umount /tmp/rb; rmdir /tmp/rb; exit $rc' \
		>"$work/rootfs.tar" </dev/null || die "reading the rootfs"
	echo "== the CFW"
	ssh_ "$FINDDEV"'
cd "$CFW" || exit 1
l=; for f in '"$CFWLIST"'; do [ -e "$f" ] && l="$l $f"; done
tar cf - --exclude="*.prev" --exclude="*.bad" --exclude="*.new" --exclude="*.upload" \
	--exclude="ssh_host_*" --exclude="trusted_user_ca_keys" --exclude="principals" \
	--exclude="sshd_config.local" $l' >"$work/cfw.tar" || die "reading <cfw>"
	ssh_ "$FINDDEV"'"$CFW/bin/yastationd" --version' >"$work/version" </dev/null || die "the donor has no yastationd"
	"$here/tools/build.sh" "golden:$work:$platform"
	echo "ok: $work/golden.img"
}

flash() {
	[ $# -eq 2 ] || { echo "usage: $0 flash <ssh-target> <image>" >&2; exit 2; }
	target=$1 img=$2
	[ -f "$img" ] || die "no $img"
	magic=$(od -An -tx4 -j8 -N4 "$img" | tr -d ' ')
	[ "$magic" = 27b51956 ] || die "$img is not a restore package"
	compat=$(ssh_ 'tr "\0" " " </proc/device-tree/compatible' </dev/null) || die "ssh to $target"
	case $compat in
	*yandex,pickle* | *yandex,orion*) ;;
	*)
		board=$(echo "$compat" | sed -n 's/.*yandex,\([a-z0-9]*\) .*/\1/p')
		[ "${UNTRIED:-}" = "$board" ] || die "nobody has reset a $board with a rebuilt package yet (docs/guides/golden-image.md); a refusal has no way back. UNTRIED=$board to write it anyway"
		;;
	esac
	sum=$(sha256sum "$img" | cut -d' ' -f1)
	size=$(wc -c <"$img")
	ssh_ "$FINDDEV"'
p=$(cat /sys/class/mtd/${DEV##*/}/size 2>/dev/null)
if [ -z "$p" ]; then
	for u in /sys/class/block/*/uevent; do
		grep -q "^PARTNAME=aml_sysrecovery$" "$u" 2>/dev/null && p=$(($(cat "${u%/uevent}/size") * 512))
	done
fi
[ -n "$p" ] || p=0
[ "$p" -ge '"$size"' ] || { echo "the partition holds $p bytes, the image '"$size"'" >&2; exit 1; }
b=$(cat /sys/class/mtd/${DEV##*/}/bad_blocks 2>/dev/null || echo 0)
[ "$b" = 0 ] || { echo "$b bad blocks in $DEV: not handled" >&2; exit 1; }
need='"$((size / 1024 + 4096))"'
d=
for c in "$(dirname "$CFW")" /tmp; do
	[ "$(df -k "$c" | awk "END{print \$(NF-2)}")" -gt "$need" ] && { d=$c; break; }
done
[ -n "$d" ] || { echo "no room to stage the image (a partition or /tmp with $need KB free)" >&2; exit 1; }
echo "$d" >/tmp/golden.stage
echo "$DEV, staging in $d"' </dev/null || die "the unit is not ready"
	echo "== upload"
	ssh_ 'cat >"$(cat /tmp/golden.stage)/golden.img"' <"$img" || die "upload"
	echo "== write"
	ssh_ "$FINDDEV"'
set -e
g=$(cat /tmp/golden.stage)/golden.img
trap '\''rm -f "$g"; sync'\'' EXIT
[ "$(sha256sum "$g" | cut -d" " -f1)" = '"$sum"' ] || { echo "the upload does not match" >&2; exit 1; }
case $DEV in
/dev/mtd*)
	flash_erase "$DEV" 0 0 >/dev/null
	nandwrite -p "$DEV" "$g" >/dev/null ;;
*)
	dd if="$g" of="$DEV" bs=1048576 conv=fsync 2>/dev/null ;;
esac
sync
[ "$(head -c '"$size"' "$DEV" | sha256sum | cut -d" " -f1)" = '"$sum"' ] || { echo "READ-BACK DIFFERS: write it again before any reset" >&2; exit 1; }
echo "read back equal: $DEV"' </dev/null || die "write"
	echo "ok: $target restores $img on a reset"
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
build) build "$@" ;;
flash) flash "$@" ;;
*) sed -n '2,18p' "$0" >&2; exit 2 ;;
esac
