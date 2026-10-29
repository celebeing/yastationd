#!/bin/sh
# fetch-vendor.sh — copy the vendor files the build needs off your own speaker (read-only).
#
#   tools/fetch-vendor.sh <ssh-target> <board>      e.g. root@<speaker-ip> bergamot
#
# Into vendor-local/<board>/<version_full>/ (gitignored; the layout of a vendor-repo tree, so either
# works as a sysroot):
#   lib/, etc/os-release                         the firmware's libraries (the glibc spotter-host binds to)
#   system/vendor/quasar/libs/                   the voice front-end library spotter-host links
#   system/vendor/quasar/ledpatterns/            the stock animations, for the simulator (cmd/ledsim -patterns)
#   system/vendor/quasar/VERSION, version_full   which firmware this is
# Then it names the tree in tools/build.env as SPOTTER_SYSROOT_<ARCH> (the board's userland arch),
# unless that is set already; tools/build.sh also finds a lone tree here by itself.
#
# On the speaker it runs only `cat` and `tar c` of firmware paths: nothing is written there, and
# nothing under /data or <cfw> (the unit's keys and state) is read. Vendor files are never
# committed (docs/building.md, "The sysroot"). Env: SSHKEY as for deploy.sh.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
[ $# -eq 2 ] || { echo "usage: $0 <ssh-target> <board>" >&2; exit 2; }
target=$1 board=$2
profile=$here/yastationd/internal/board/profiles/$board.conf
case "$board" in "" | *[!A-Za-z0-9_-]*) echo "bad board '$board'" >&2; exit 2 ;; esac
[ -f "$profile" ] || { echo "unknown board '$board' (yastationd/internal/board/profiles/)" >&2; exit 2; }
arch=$(awk -F= '{k=$1; gsub(/[ \t]/,"",k)} k=="platform.userland_arch" {v=$2; gsub(/[ \t]/,"",v); print v; exit}' "$profile")

keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$target" "$@"; }

rc=0
ver=$(ssh_ 'cat /system/vendor/quasar/version_full' </dev/null) || rc=$?
case $rc in
0) ;;
255) echo "fetch-vendor: ssh to $target failed (login refused, unreachable or no key)" >&2; exit 1 ;;
*) echo "fetch-vendor: $target has no /system/vendor/quasar/version_full — not a supported speaker?" >&2; exit 1 ;;
esac
case "$ver" in "" | *[!A-Za-z0-9._-]*) echo "fetch-vendor: odd version_full '$ver'" >&2; exit 1 ;; esac

dest=$here/vendor-local/$board/$ver
if [ -d "$dest/system/vendor/quasar/libs" ]; then
	echo "vendor-local/$board/$ver: already here (delete it to fetch again)"
else
	stage=$dest.part
	rm -rf "$stage"
	mkdir -p "$stage"
	echo "== $target: $board firmware $ver -> vendor-local/$board/$ver"
	# Only the paths that exist; relative names from /, links kept as links.
	paths="lib etc/os-release system/vendor/quasar/libs system/vendor/quasar/ledpatterns system/vendor/quasar/VERSION system/vendor/quasar/version_full"
	ssh_ "cd / && l=''; for p in $paths; do [ -e \"\$p\" ] && l=\"\$l \$p\"; done; tar cf - \$l" </dev/null |
		tar xf - -C "$stage" || { rm -rf "$stage"; echo "fetch-vendor: copy from $target failed" >&2; exit 1; }
	[ -d "$stage/system/vendor/quasar/libs" ] && [ -d "$stage/lib" ] ||
		{ rm -rf "$stage"; echo "fetch-vendor: no lib/ or quasar/libs/ came over" >&2; exit 1; }
	# Absolute links (/lib/libm.so.6 -> /lib/libm-2.36.so) would point into the host when the tree
	# is a sysroot: make them relative to the tree.
	find "$stage" -type l | while IFS= read -r l; do
		t=$(readlink "$l")
		case $t in
		/*)
			rel=$(realpath -m --relative-to="$(dirname "$l")" "$stage$t")
			ln -sfn "$rel" "$l"
			;;
		esac
	done
	mv "$stage" "$dest"
	echo "   $(du -sh "$dest" | cut -f1): lib/, quasar/libs/, ledpatterns/"
fi

env=$here/tools/build.env
var=SPOTTER_SYSROOT_$(echo "$arch" | tr a-z A-Z)
if [ -f "$env" ] && grep -q "^$var=" "$env"; then
	echo "tools/build.env already names $var: $(grep "^$var=" "$env" | cut -d= -f2-) (left as it is)"
else
	printf '# fetched from %s by tools/fetch-vendor.sh, %s\n%s=%s\n' "$board" "$(date +%F)" "$var" "$dest" >>"$env"
	echo "tools/build.env: $var=$dest"
fi
case $board in
orion) echo "simulator: tools/build.sh hosttool:ledsim; yastationd/build/ledsim -patterns $dest/system/vendor/quasar/ledpatterns/screen" ;;
*) echo "simulator: tools/build.sh hosttool:ledsim; yastationd/build/ledsim -board $board -patterns $dest/system/vendor/quasar/ledpatterns/clock" ;;
esac
