#!/bin/sh
# restore-unit.sh — put a backup from tools/backup-unit.sh back on a speaker reachable over SSH
# (docs/guides/backup.md).
#
#   tools/restore-unit.sh [-n] <ssh-target> <backup-dir> [rootfs] [rw]
#
#   -n       dry run: print what would be written, write nothing
#   rootfs   the rootfs files of the backup that differ on the speaker (hooks, maind gate, OTA stub,
#            authorized_keys...); the speaker's current versions of them are saved into the backup
#            dir first (replaced-<time>.tar.gz), then the rootfs is remounted rw, written, synced
#            and remounted ro
#   rw       the writable partitions (<cfw>, /data...), over what is there (nothing is deleted);
#            the daemon is stopped for it and started again. The Wi-Fi config is left alone unless
#            the speaker's has no network in it or WIFI=1: a wrong one can lock you out
#   default  both, rootfs first. REBOOT=1 reboots at the end (the rootfs hooks need it).
#
# The raw partitions of the backup (env, misc, reserved, system) are never written by this script:
# they hold the unit's identity and boot settings, and a wrong write there can stop it booting.
# Refuses a backup of another board (FORCE=1 overrides). Env: SSHKEY as for deploy.sh.
set -eu

dry=0
[ "${1:-}" = -n ] && { dry=1; shift; }
[ $# -ge 2 ] || { echo "usage: $0 [-n] <ssh-target> <backup-dir> [rootfs] [rw]" >&2; exit 2; }
target=$1 dir=$2
shift 2
parts=${*:-rootfs rw}
for p in $parts; do case $p in rootfs | rw) ;; *) echo "unknown part '$p' (rootfs, rw)" >&2; exit 2 ;; esac; done
for f in info.txt rw.tar.gz rootfs.tar.gz rootfs.sha256 SHA256SUMS; do
	[ -f "$dir/$f" ] || { echo "restore-unit: $dir has no $f — not a backup-unit.sh backup?" >&2; exit 2; }
done
(cd "$dir" && sha256sum --quiet -c SHA256SUMS) || { echo "restore-unit: $dir fails its SHA256SUMS" >&2; exit 1; }

keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$target" "$@"; }
say() { if [ $dry = 1 ]; then echo "   would: $*"; else echo "   $*"; fi; }

rc=0
board=$(ssh_ 'tr "\0" " " </proc/device-tree/compatible' </dev/null) || rc=$?
[ $rc = 0 ] || { echo "restore-unit: ssh to $target failed" >&2; exit 1; }
want=$(sed -n 's/^board: //p' "$dir/info.txt")
if [ "$(echo $board)" != "$(echo $want)" ] && [ "${FORCE:-0}" != 1 ]; then
	echo "restore-unit: $target is '$board', the backup is of '$want' (FORCE=1 to restore anyway)" >&2
	exit 1
fi
echo "== $dir -> $target$([ $dry = 1 ] && echo ' (dry run)')"

case " $parts " in *" rootfs "*)
	echo "-- rootfs"
	# Regular files whose checksum differs or that are missing; symlinks whose target differs.
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	sed 's|^\([0-9a-f]*\)  /|\1  |' "$dir/rootfs.sha256" >"$tmp/want"
	ssh_ 'cd / && while read -r sum path; do s=$(sha256sum "$path" 2>/dev/null | cut -d" " -f1); [ "$s" = "$sum" ] || echo "$path"; done' \
		<"$tmp/want" >"$tmp/list"
	tar tvzf "$dir/rootfs.tar.gz" | awk '$1 ~ /^l/ {print $6 "\t" $8}' >"$tmp/links"
	if [ -s "$tmp/links" ]; then
		ssh_ 'cd / && while IFS="	" read -r path to; do [ "$(readlink "$path")" = "$to" ] || echo "$path"; done' \
			<"$tmp/links" >>"$tmp/list"
	fi
	# What a golden image brings is the image's, not the unit's (docs/guides/golden-image.md); a
	# backup of a unit running one has it, older, and an early image's seed held the Wi-Fi password.
	grep -vE '^(usr/share/cfw-seed/|etc/init\.d/S00zcfwseed$|etc/cfw-image)' "$tmp/list" >"$tmp/list.f" || true
	mv "$tmp/list.f" "$tmp/list"
	if [ ! -s "$tmp/list" ]; then
		echo "   nothing differs"
	else
		sed 's|^|   /|' "$tmp/list"
		if [ $dry = 0 ]; then
			saved="$dir/replaced-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
			ssh_ 'cd / && l=; while read -r p; do [ -e "$p" ] || [ -L "$p" ] && l="$l $p"; done; [ -z "$l" ] || tar cf - $l | gzip' \
				<"$tmp/list" >"$saved"
			[ -s "$saved" ] && echo "   the speaker's versions saved in $saved" || rm -f "$saved"
			ssh_ "cat >/tmp/restore-list" <"$tmp/list"
			gzip -dc "$dir/rootfs.tar.gz" | ssh_ '
mount -o remount,rw / || exit 1
tar xf - -C / -T /tmp/restore-list; rc=$?
sync; mount -o remount,ro / || echo "restore-unit: remount ro failed: reboot soon" >&2
rm -f /tmp/restore-list; exit $rc' || { echo "restore-unit: writing the rootfs failed" >&2; exit 1; }
			echo "   written"
		fi
	fi
	;;
esac

case " $parts " in *" rw "*)
	echo "-- rw partitions"
	excl=
	if [ "${WIFI:-0}" != 1 ] && ssh_ 'grep -qs "network=" /bkupgrade/data/wpa_supplicant.conf /data/wpa_supplicant.conf' </dev/null; then
		excl="--exclude '*/wpa_supplicant.conf'"
		echo "   keeping the speaker's Wi-Fi config (WIFI=1 to restore the backup's)"
	fi
	say "stop yastationd, unpack rw.tar.gz ($(du -h "$dir/rw.tar.gz" | cut -f1)) over /, start yastationd"
	if [ $dry = 0 ]; then
		gzip -dc "$dir/rw.tar.gz" | ssh_ '
d=/etc/init.d/S52zyastationd
[ -x $d ] && $d stop >/dev/null 2>&1
tar xf - -C / '"$excl"'; rc=$?
sync
[ -x $d ] && $d start >/dev/null
exit $rc' || { echo "restore-unit: unpacking rw.tar.gz failed" >&2; exit 1; }
		echo "   written"
	fi
	;;
esac

if [ $dry = 0 ] && [ "${REBOOT:-0}" = 1 ]; then
	echo "-- reboot"
	ssh_ 'sync; reboot' </dev/null || true
fi
echo "ok $target$([ $dry = 1 ] && echo ' (dry run)')"
