#!/bin/sh
# backup-unit.sh — a backup of one speaker, enough to put it back after a factory reset or a bad
# experiment with Home Assistant and Music Assistant noticing nothing (docs/guides/backup.md).
#
#   tools/backup-unit.sh <ssh-target> [out-dir]      e.g. root@<speaker-ip>
#
# Into out-dir (default dumps/backups/<device_id>/<UTC time>/, gitignored: it holds the unit's
# keys, Wi-Fi password and tokens):
#   rw.tar.gz        every writable flash partition (ext4/ubifs mounted rw: orion /ota + /data,
#                    bergamot /bkupgrade, pickle /data): <cfw> with the config, state, Sendspin
#                    identity, voices, packs, SSH host keys; the Wi-Fi config; Bluetooth pairings
#   rootfs.tar.gz    the rootfs files changed since the firmware was built (newer than its
#                    os-release: the install hooks, the maind gate, the OTA stub, authorized_keys...)
#   rootfs.sha256    their checksums (restore-unit.sh writes only the ones that differ)
#   rootfs.list      every rootfs path with its size and mode, to see later what was there
#   part-<name>.img.gz  the small raw partitions that hold the unit's identity or boot settings:
#                    env, misc, reserved (orion: MAC and serial live there), nand_env and the
#                    "nv" mtd (bergamot)
#   part-system.img.gz  only with SYSTEM=1 (orion: the whole 1 GiB rootfs, ~5 min)
#   info.txt         board, firmware, mounts, free space, when; SHA256SUMS of everything
#
# Read-only on the speaker: it runs cat, find, tar c, gzip and dd from the partitions, writing
# nothing there. Every raw image is compared with a checksum taken on the speaker. Env: SSHKEY as
# for deploy.sh.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
[ $# -ge 1 ] && [ $# -le 2 ] || { echo "usage: $0 <ssh-target> [out-dir]" >&2; exit 2; }
target=$1

keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$target" "$@"; }

# The same lookups the speaker side of every step uses.
remote_env='
CFW=/bkupgrade/cfw; [ -d "$CFW" ] || CFW=/data/cfw
ROOTREF=$(readlink -f /etc/os-release)
rwmounts() { awk '"'"'$4 ~ /^rw/ && $3 ~ /^(ext[234]|ubifs|vfat)$/ && $2 != "/" {print $2}'"'"' /proc/mounts; }
'

rc=0
id=$(ssh_ "$remote_env"'sed -n "s/^[ \t]*device_id[ \t]*=[ \t]*//p" "$CFW/yastationd.conf" 2>/dev/null | head -1 | tr -d "\r\" "; [ -n "$ROOTREF" ]' </dev/null) || rc=$?
case $rc in
0) ;;
255) echo "backup-unit: ssh to $target failed (login refused, unreachable or no key)" >&2; exit 1 ;;
*) echo "backup-unit: $target has no /etc/os-release — not a supported speaker?" >&2; exit 1 ;;
esac
case "$id" in "" | *[!A-Za-z0-9._-]*) id=$(echo "${target#*@}" | tr -c 'A-Za-z0-9._\n-' _) ;; esac

out=${2:-$here/dumps/backups/$id/$(date -u +%Y%m%dT%H%M%SZ)}
[ ! -e "$out" ] || [ -z "$(ls -A "$out")" ] || { echo "backup-unit: $out is not empty" >&2; exit 1; }
mkdir -p "$out"
echo "== $target ($id) -> $out"

echo "-- info"
ssh_ "$remote_env"'
echo "date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "board: $(tr "\0" " " </proc/device-tree/compatible 2>/dev/null)"
echo "firmware: $(cat /system/vendor/quasar/version_full 2>/dev/null)"
echo "kernel: $(uname -r)"
echo "cfw: $CFW"
echo "rootfs reference: $ROOTREF ($(ls -l "$ROOTREF" | awk "{print \$6, \$7, \$8}"))"
echo "rw partitions: $(rwmounts | tr "\n" " ")"
echo; echo "## mounts"; cat /proc/mounts
echo; echo "## df"; df -k
echo; echo "## partitions"; cat /proc/partitions; cat /proc/mtd 2>/dev/null
' >"$out/info.txt" </dev/null

echo "-- rw partitions"
ssh_ "$remote_env"'cd / && tar cf - $(for m in $(rwmounts); do ls -A "$m" | grep -vx lost+found | sed "s|^|${m#/}/|"; done) | gzip' \
	>"$out/rw.tar.gz" </dev/null
gzip -t "$out/rw.tar.gz"

echo "-- rootfs changes"
# A golden image's own files are the image's, not this unit's edits (docs/guides/golden-image.md).
notimage='grep -vE "^/(usr/share/cfw-seed/|etc/init\.d/S00zcfwseed\$|etc/cfw-image)"'
ssh_ "$remote_env"'find / -xdev -newer "$ROOTREF" ! -type d 2>/dev/null | '"$notimage"' | sed "s|^/||" >/tmp/backup-rootfs.$$
cd / && tar cf - -T /tmp/backup-rootfs.$$ | gzip; rm -f /tmp/backup-rootfs.$$' >"$out/rootfs.tar.gz" </dev/null
gzip -t "$out/rootfs.tar.gz"
ssh_ "$remote_env"'find / -xdev -newer "$ROOTREF" -type f 2>/dev/null | '"$notimage"' | xargs sha256sum' >"$out/rootfs.sha256" </dev/null
ssh_ 'find / -xdev -exec ls -lnd {} + 2>/dev/null' >"$out/rootfs.list" </dev/null
echo "   $(wc -l <"$out/rootfs.sha256") changed files"

parts="env misc reserved nand_env"
[ "${SYSTEM:-0}" = 1 ] && parts="$parts system"
# "name dev" pairs: the eMMC boards name their partitions in /dev; on NAND (bergamot, pickle) the
# small ones are mtd devices named in /proc/mtd ("nv" holds bergamot's keys).
devs=$(ssh_ "for p in $parts; do [ -b /dev/\$p ] || [ -c /dev/\$p ] && echo \"\$p /dev/\$p\"; done
sed -n 's/^\(mtd[0-9]*\): .* \"\(env\|nv\|misc\|reserved\)\"\$/\2 \/dev\/\1/p' /proc/mtd 2>/dev/null" </dev/null)
echo "$devs" | while read -r p dev; do
	[ -n "$p" ] || continue
	[ ! -e "$out/part-$p.img.gz" ] || continue
	echo "-- partition $p ($dev)"
	sum=$(ssh_ "sha256sum <$dev | cut -d' ' -f1" </dev/null)
	ssh_ "dd if=$dev bs=64k 2>/dev/null | gzip" >"$out/part-$p.img.gz" </dev/null
	got=$(gzip -dc "$out/part-$p.img.gz" | sha256sum | cut -d' ' -f1)
	[ "$sum" = "$got" ] || { echo "backup-unit: part-$p.img.gz does not match the speaker's checksum" >&2; exit 1; }
done

(cd "$out" && sha256sum -- * >SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS)
echo "ok $target: $(du -sh "$out" | cut -f1) in $out"
