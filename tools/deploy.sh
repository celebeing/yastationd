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
# It builds what it ships first, in the pinned builder containers (tools/build.sh; host needs
# docker): yastationd, hostapd, and per userland arch in the list sendspin-host and (with a sysroot
# in tools/build.env) spotter-host. Native helpers are loaded from spotter-host/build/<arch>/ and
# sendspin-host/build/<arch>/; hostapd from hostapd/build/hostapd, else from a recorded build in
# your vendor tree ($VENDOR/builds/hostapd/<version>/armv7/hostapd, the newest); with neither, a
# speaker that has no hostapd yet gets a warning (its Wi-Fi setup mode cannot start).
#
# Env: SSHKEY (optional: the key to use; unset, ssh picks one from the agent and ~/.ssh/config),
# VENDOR (optional, the vendor-tree checkout), NATIVE=1 (build yastationd with the host's
# toolchain, ship the helpers as they are). Speakers are updated one at a time; the script stops
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
[ -f "$hosts" ] || {
	echo "no hosts file: $hosts (see the header)" >&2
	exit 1
}

keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$@"; }

# fail: stop the whole run (a speaker that could not be updated stops the rest). Every ssh call
# below checks its own status explicitly: set -e does not stop on a failure inside an && list or
# an if, and the per-host loop runs in a pipeline's subshell.
fail() {
	echo "  FAILED: $*" >&2
	exit 1
}
# put FILE REMOTE_CMD: stream a file to the speaker.
put() { ssh_ "$target" "$2" <"$1" || fail "upload of $(basename "$1") to $target"; }


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


# Build what goes out, each in its pinned container (tools/build.sh): yastationd, hostapd, and
# sendspin-host / spotter-host for every userland arch in the hosts file (spotter-host only where
# tools/build.env names a sysroot; else the last build is shipped). NATIVE=1 builds yastationd
# with the host's own toolchain instead and ships the helpers as they are.
arches=$(grep -v '^[[:space:]]*\(#\|$\)' "$hosts" | while read -r _ platform _ arch; do
	if [ -z "${arch:-}" ] || [ "$arch" = "-" ]; then
		arch=$(profile_value "$here/yastationd/internal/board/profiles/$platform.conf" platform.userland_arch 2>/dev/null || true)
	fi
	echo "$arch"
done | sort -u)
echo "== build"
if [ "${NATIVE:-0}" = 1 ]; then
	(cd "$here/yastationd" && make -s test build)
else
	targets="yastationd hostapd"
	for a in $arches; do targets="$targets sendspin:$a spotter-if:$a"; done
	# shellcheck disable=SC2086 # targets is a list
	"$here/tools/build.sh" $targets
fi
bin=$here/yastationd/build/yastationd
hap=$here/hostapd/build/hostapd
if [ ! -x "$hap" ] && [ -n "${VENDOR:-}" ]; then
	hap=$(ls -1d "$VENDOR"/builds/hostapd/*/armv7/hostapd 2>/dev/null | sort -V | tail -1)
fi
[ -n "$hap" ] && [ -x "$hap" ] && echo "hostapd: $hap" || hap=

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
	# Reach it first, so an ssh failure (auth, timeout) says so and is not taken for a missing
	# install: 255 is ssh's own error, anything else is the remote test.
	rc=0
	ssh_ "$target" "test -d $cfw/bin" </dev/null || rc=$?
	case $rc in
	0) ;;
	255) fail "ssh to $target (login refused, unreachable or no key: try ssh $target)" ;;
	*) fail "$cfw/bin missing on $target — not installed here?" ;;
	esac

	put "$bin" "cat > $cfw/bin/yastationd.upload"
	put "$here/yastationd/init.d/supervise.sh" "mkdir -p $cfw/init.d && cat > $cfw/init.d/supervise.sh.new && mv $cfw/init.d/supervise.sh.new $cfw/init.d/supervise.sh"
	if [ -n "${arch:-}" ]; then
		sp=$here/spotter-host/build/$arch/spotter-host
		[ -x "$sp" ] || fail "no $sp — set SPOTTER_SYSROOT_* in tools/build.env (docs/building.md)"
		put "$sp" "cat > $cfw/bin/spotter-host.upload"
		ss=$here/sendspin-host/build/$arch/sendspin-host
		if [ -x "$ss" ]; then
			put "$ss" "cat > $cfw/bin/sendspin-host.upload"
		fi
	fi
	if [ -n "$hap" ]; then
		put "$hap" "cat > $cfw/bin/hostapd.upload"
	elif ! ssh_ "$target" "test -x $cfw/bin/hostapd" </dev/null; then
		echo "  warning: no hostapd here and none to install (tools/build.sh hostapd, or set VENDOR) — the Wi-Fi setup mode cannot start" >&2
	fi

	# The swap runs from stdin; it restarts the daemon, which restarts spotter-host with it.
	ssh_ "$target" 'sh -s' <<EOF || fail "update on $target (see above)"

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
