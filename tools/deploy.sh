#!/bin/sh
# deploy.sh — build yastationd once and roll it out to several speakers (example; adapt freely).
#
#   tools/deploy.sh [hosts-file]          # default: tools/deploy.hosts (gitignored)
#
# hosts-file: one speaker per line, "<ssh-target> <platform> [cfw-root] [helper-arch]",
# '#' comments. Root and native helper architecture default from the board profile:
#   root@<speaker-a>  bergamot
#   root@<speaker-b>  orion
# Per-unit overrides remain available; use "-" to keep the profile root while overriding arch:
#   root@<speaker-c>  pickle  -  aarch64
# Native helpers are loaded from spotter-host/build/<arch>/spotter-host and, when built,
# sendspin-host/build/<arch>/sendspin-host. hostapd (one static binary for every board) from
# hostapd/build/hostapd, else from a recorded build in your vendor tree
# ($VENDOR/builds/hostapd/<version>/armv7/hostapd, the newest); with neither, a speaker that has
# no hostapd yet gets a warning (its Wi-Fi setup mode cannot start).
#
# Env: SSHKEY (default ~/.ssh/id_yastation), VENDOR (optional, the vendor-tree checkout). Speakers are updated one at a time; the script stops
# at the first failure so a broken build never reaches the rest.
#
# What it does per speaker (the same as `make deploy`): upload the binary next to the live one
# (and the supervisor script), then run stop -> swap -> start as a script on stdin, keeping the
# old binary as yastationd.prev for the supervisor's crash-loop rollback. Keep it that way: the init hook's stop
# matches processes by the binary's path, and an ssh command line that mentions the path would be
# killed along with the daemon.
set -eu

here=$(cd "$(dirname "$0")/.." && pwd)
hosts=${1:-$here/tools/deploy.hosts}
key=${SSHKEY:-$HOME/.ssh/id_yastation}
[ -f "$hosts" ] || {
	echo "no hosts file: $hosts (see the header)" >&2
	exit 1
}

ssh_() { ssh -i "$key" -o IdentitiesOnly=yes -o BatchMode=yes -o ConnectTimeout=5 "$@"; }

hap=$here/hostapd/build/hostapd
if [ ! -x "$hap" ] && [ -n "${VENDOR:-}" ]; then
	hap=$(ls -1d "$VENDOR"/builds/hostapd/*/armv7/hostapd 2>/dev/null | sort -V | tail -1)
fi
[ -n "$hap" ] && [ -x "$hap" ] && echo "hostapd: $hap" || hap=

profile_value() {
	awk -F= -v wanted="$2" '
		{
			key = $1
			gsub(/^[ \t]+|[ \t]+$/, "", key)
			if (key == wanted) {
				value = substr($0, index($0, "=") + 1)
				gsub(/^[ \t]+|[ \t]+$/, "", value)
				print value
				exit
			}
		}
	' "$1"
}

echo "== build"
(cd "$here/yastationd" && make -s test build)
bin=$here/yastationd/build/yastationd

grep -v '^[[:space:]]*\(#\|$\)' "$hosts" | while read -r target platform cfw arch; do
	case "$platform" in
	"" | *[!A-Za-z0-9_-]*)
		echo "invalid platform '$platform' for $target" >&2
		exit 1
		;;
	esac
	profile=$here/yastationd/internal/board/profiles/$platform.conf
	[ -f "$profile" ] || {
		echo "unknown platform '$platform': $profile missing" >&2
		exit 1
	}
	default_cfw=$(profile_value "$profile" platform.cfw_root)
	default_arch=$(profile_value "$profile" platform.userland_arch)
	[ -n "${cfw:-}" ] && [ "$cfw" != "-" ] || cfw=$default_cfw
	[ -n "${arch:-}" ] && [ "$arch" != "-" ] || arch=$default_arch
	case "$cfw" in
	/*) ;;
	*)
		echo "invalid CFW root '$cfw' for $platform" >&2
		exit 1
		;;
	esac
	case "$cfw" in
	*[!A-Za-z0-9_./-]*)
		echo "unsafe CFW root '$cfw' for $platform" >&2
		exit 1
		;;
	esac
	case "$arch" in
	armhf | aarch64) ;;
	*)
		echo "invalid helper architecture '$arch' for $platform" >&2
		exit 1
		;;
	esac

	echo "== $target ($platform: $cfw, $arch)"
	ssh_ "$target" "test -d $cfw/bin" </dev/null || {
		echo "  $cfw/bin missing — not installed here?" >&2
		exit 1
	}

	ssh_ "$target" "cat > $cfw/bin/yastationd.upload" <"$bin"
	ssh_ "$target" "mkdir -p $cfw/init.d && cat > $cfw/init.d/supervise.sh.new && mv $cfw/init.d/supervise.sh.new $cfw/init.d/supervise.sh" <"$here/yastationd/init.d/supervise.sh"
	if [ -n "${arch:-}" ]; then
		sp=$here/spotter-host/build/$arch/spotter-host
		[ -x "$sp" ] || {
			echo "  no $sp — build it with spotter-host/build.sh ARCH=$arch" >&2
			exit 1
		}
		ssh_ "$target" "cat > $cfw/bin/spotter-host.upload" <"$sp"
		ss=$here/sendspin-host/build/$arch/sendspin-host
		[ -x "$ss" ] && ssh_ "$target" "cat > $cfw/bin/sendspin-host.upload" <"$ss"
	fi
	if [ -n "$hap" ]; then
		ssh_ "$target" "cat > $cfw/bin/hostapd.upload" <"$hap"
	elif ! ssh_ "$target" "test -x $cfw/bin/hostapd" </dev/null; then
		echo "  warning: no hostapd here and none to install (make -C hostapd, or set VENDOR) — the Wi-Fi setup mode cannot start" >&2
	fi

	# The swap runs from stdin; it restarts the daemon, which restarts spotter-host with it.
	ssh_ "$target" 'sh -s' <<EOF
set -e
D=$cfw
chmod +x \$D/bin/yastationd.upload
for h in spotter-host sendspin-host hostapd; do [ -f \$D/bin/\$h.upload ] && chmod +x \$D/bin/\$h.upload; done
/etc/init.d/S52zyastationd stop >/dev/null 2>&1 || true
sleep 1
l0=\$(cat /tmp/yastationd.log 2>/dev/null | wc -l)
[ -f \$D/bin/yastationd ] && cp -p \$D/bin/yastationd \$D/bin/yastationd.prev
mv \$D/bin/yastationd.upload \$D/bin/yastationd
for h in spotter-host sendspin-host hostapd; do [ -f \$D/bin/\$h.upload ] && mv \$D/bin/\$h.upload \$D/bin/\$h; done
/etc/init.d/S52zyastationd start >/dev/null
sleep 8
n=\$(ps w | awk -v b="\$D/bin/yastationd" '\$3 == b' | wc -l)
[ "\$n" -ge 1 ] || { echo "  yastationd not running:"; tail -20 /tmp/yastationd.log; exit 1; }
tail -n +\$((l0 + 1)) /tmp/yastationd.log | grep -E 'level=(ERROR|WARN)' | tail -3 | sed 's/^/  log: /'
echo "  ok"
EOF
done
