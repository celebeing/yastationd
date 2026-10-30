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
# docker): yastationd, hostapd, and per userland arch in the list sendspin-host and spotter-host
# (its sysroot from tools/build.env, else fetched off the first speaker of that arch). Native
# helpers are loaded from spotter-host/build/<arch>/ and sendspin-host/build/<arch>/; hostapd
# from hostapd/build/hostapd, else from a recorded build in your vendor tree ($VENDOR/builds/hostapd/<version>/armv7/hostapd, the newest); with neither, a
# speaker that has no hostapd yet gets a warning (its Wi-Fi setup mode cannot start).
#
# dropbear (the speaker's SSH server, dropbear/) is replaced only on a first deploy (no yastationd
# on the speaker yet) or with DROPBEAR=1, and only where it runs from <cfw>/bin/dropbearmulti. The
# new one is tried on port 2222 first and swaps itself in from there; the old one stays as
# dropbearmulti.prev. DROPBEAR=only does just that: no build or update of anything else.
#
# SSH=openssh | dropbear picks the speaker's SSH server (docs/ssh.md): it is tried on port 2222
# first and switched to on port 22 from there; unset, the server stays as it is. SSH_HOOK=1 makes
# the boot start the chosen one (a rootfs write: only with the owner's OK). With DROPBEAR=only,
# SSH= / SSH_HOOK= do just that.
#
# Env: SSHKEY (optional: the key to use; unset, ssh picks one from the agent and ~/.ssh/config),
# VENDOR (optional, the vendor-tree checkout), DROPBEAR=1 | only (replace dropbear, above), NATIVE=1 (build yastationd with the host's
# toolchain, ship the helpers as they are). Speakers are updated one at a time; the script stops
# at the first failure so a broken build never reaches the rest.
#
# `make deploy` in yastationd/ runs this script for local.mk's one speaker.
#
# What it does per speaker: upload the binary next to the live one (and the supervisor script,
# bin/diag.sh and the helpers), then run stop -> swap -> start as a script on stdin, keeping the
# old binary as yastationd.prev for the supervisor's crash-loop rollback. Keep it that way: the
# init hook's stop matches processes by the binary's path, and an ssh command line that mentions
# the path would be killed along with the daemon.
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
# ssh_2222: the test server on port 2222, checked against the speaker's port-22 known_hosts
# entry (its host key must be the same; known_hosts has no line for 2222).
ssh_2222() { ssh_ -p 2222 -o HostKeyAlias="${target#*@}" "$target" "$@"; }

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
# A userland arch without a spotter-host sysroot gets one from the first speaker of that arch in
# the list (tools/fetch-vendor.sh: read-only, into vendor-local/), so a fresh checkout deploys
# without setting anything up. NATIVE=1 and DROPBEAR=only build no spotter-host.
if [ "${NATIVE:-0}" != 1 ] && [ "${DROPBEAR:-0}" != only ]; then
	for a in $arches; do
		"$here/tools/build.sh" "has-sysroot:$a" && continue
		grep -v '^[[:space:]]*\(#\|$\)' "$hosts" | while read -r t platform _ arch; do
			if [ -z "${arch:-}" ] || [ "$arch" = "-" ]; then
				arch=$(profile_value "$here/yastationd/internal/board/profiles/$platform.conf" platform.userland_arch 2>/dev/null || true)
			fi
			[ "$arch" = "$a" ] || continue
			echo "== no $a sysroot: fetching one from $t ($platform)"
			"$here/tools/fetch-vendor.sh" "$t" "$platform" || fail "fetching the $a sysroot from $t"
			break
		done
	done
fi

echo "== build"
if [ "${DROPBEAR:-0}" = only ]; then
	"$here/tools/build.sh" dropbear
	[ "${SSH:-}" = openssh ] && "$here/tools/build.sh" openssh
elif [ "${NATIVE:-0}" = 1 ]; then
	(cd "$here/yastationd" && make -s test build)
else
	targets="yastationd hostapd dropbear"
	[ "${SSH:-}" = openssh ] && targets="$targets openssh"
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
dbm=$here/dropbear/build/dropbearmulti

# DB_ARGS: on the speaker, the live dropbear listener's command line (its pid file is the init
# hook's /var/run/dropbear.pid).
DB_ARGS='args=$(tr "\0" " " </proc/$(cat /var/run/dropbear.pid)/cmdline)'

# update_dropbear: swap the speaker's dropbear for ours (see the header). A test server on port
# 2222 runs the new binary with the live one's arguments; a login through it does the swap and
# restarts the port-22 listener (sessions in progress stay up); a login on 22 confirms it, else the
# old binary goes back. Anything failing before the swap leaves everything as it was.
update_dropbear() {
	[ -x "$dbm" ] || { echo "  dropbear: no $dbm (tools/build.sh dropbear), not replaced" >&2; return 0; }
	if ! ssh_ "$target" "test -x $cfw/bin/dropbearmulti && test -f /var/run/dropbear.pid" </dev/null; then
		echo "  dropbear: not run from $cfw/bin/dropbearmulti here, not replaced" >&2
		return 0
	fi
	put "$dbm" "cat > $cfw/bin/dropbearmulti.upload && chmod 755 $cfw/bin/dropbearmulti.upload"
	if ! ssh_ "$target" 'sh -s' <<EOF; then
set -e
$DB_ARGS
\$(echo "\$args" | sed -e 's|/dropbearmulti |/dropbearmulti.upload |' -e 's| -p [0-9]*| -p 2222|' \
	-e 's| -P [^ ]*| -P /var/run/dropbear-test.pid|')
EOF
		echo "  dropbear: the new one did not start, not replaced" >&2
		ssh_ "$target" "rm -f $cfw/bin/dropbearmulti.upload" </dev/null || true
		return 0
	fi
	sleep 1
	if ! ssh_2222 'sh -s' <<EOF; then
set -e
D=$cfw/bin
$DB_ARGS
cp -p \$D/dropbearmulti \$D/dropbearmulti.prev
mv \$D/dropbearmulti.upload \$D/dropbearmulti
ln -sf dropbearmulti \$D/scp
kill \$(cat /var/run/dropbear.pid)
sleep 1
\$args
EOF
		ssh_ "$target" "kill \$(cat /var/run/dropbear-test.pid); rm -f $cfw/bin/dropbearmulti.upload" </dev/null || true
		echo "  dropbear: no login through the new one on port 2222, not replaced" >&2
		return 0
	fi
	sleep 1
	if v=$(ssh_ "$target" "kill \$(cat /var/run/dropbear-test.pid); $cfw/bin/dropbearmulti dropbear -V 2>&1 | head -1" </dev/null); then
		echo "  dropbear: $v"
		return 0
	fi
	# Port 22 does not answer with the new binary: put the old one back through the test server.
	ssh_2222 'sh -s' <<EOF || true
D=$cfw/bin
$DB_ARGS
mv \$D/dropbearmulti.prev \$D/dropbearmulti
kill \$(cat /var/run/dropbear.pid)
sleep 1
\$args
kill \$(cat /var/run/dropbear-test.pid)
EOF
	fail "dropbear on $target: port 22 did not answer with the new binary; the old one is back"
}

# OpenSSH (openssh/): the files that go to <cfw>/openssh, and the switch between the servers.
ossh=$here/openssh/build
OSSH_BINS="sshd sshd-session sshd-auth sftp-server scp"

# switch_ssh SERVER: make SERVER (openssh | dropbear) the speaker's SSH server on port 22, through
# <cfw>/init.d/ssh.sh (docs/ssh.md). New files go up beside the live ones as .new; SERVER runs on
# port 2222 from them first; a login through 2222 copies them into place, sets <cfw>/ssh_server
# and restarts port 22 (sessions in progress stay up); a login on 22 confirms SERVER answers
# there, else 2222 (still running from the .new files) puts the previous server back. Nothing
# about boot changes: the init hook decides that (SSH_HOOK=1).
switch_ssh() {
	srv=$1
	if [ "$srv" = openssh ]; then
		for b in $OSSH_BINS dropbearconvert; do
			[ -x "$ossh/$b" ] || fail "no $ossh/$b (tools/build.sh openssh)"
		done
		ssh_ "$target" "mkdir -p $cfw/openssh && chmod 755 $cfw/openssh" </dev/null || fail "mkdir $cfw/openssh on $target"
		for b in $OSSH_BINS; do
			put "$ossh/$b" "cat > $cfw/openssh/$b.new && chmod 755 $cfw/openssh/$b.new"
		done
		put "$ossh/dropbearconvert" "cat > $cfw/openssh/dropbearconvert && chmod 755 $cfw/openssh/dropbearconvert"
	fi
	put "$here/yastationd/init.d/ssh.sh" "mkdir -p $cfw/init.d && cat > $cfw/init.d/ssh.sh.new"
	ssh_ "$target" "SERVER=$srv sh $cfw/init.d/ssh.sh.new test 2222 .new" </dev/null ||
		fail "ssh: $srv did not start on port 2222 of $target; nothing changed"
	sleep 1
	if ! ssh_2222 'sh -s' <<EOF; then
set -e
C=$cfw
for b in $OSSH_BINS; do
	[ -f \$C/openssh/\$b.new ] || continue
	[ -f \$C/openssh/\$b ] && cp -p \$C/openssh/\$b \$C/openssh/\$b.prev
	cp -p \$C/openssh/\$b.new \$C/openssh/\$b
done
cp \$C/init.d/ssh.sh.new \$C/init.d/ssh.sh
if [ -f \$C/ssh_server ]; then cp -p \$C/ssh_server \$C/ssh_server.prev; else echo dropbear > \$C/ssh_server.prev; fi
echo $srv > \$C/ssh_server
sh \$C/init.d/ssh.sh stop
sleep 1
sh \$C/init.d/ssh.sh start
EOF
		ssh_ "$target" "kill \$(cat /var/run/sshd-2222.pid)" </dev/null || true
		fail "ssh: no login through $srv on port 2222 of $target; port 22 unchanged"
	fi
	sleep 1
	case $srv in
	openssh) pid=/var/run/sshd-22.pid ;;
	*) pid=/var/run/dropbear.pid ;;
	esac
	if ssh_ "$target" "test -f $pid && kill -0 \$(cat $pid) && kill \$(cat /var/run/sshd-2222.pid); rm -f $cfw/openssh/*.new $cfw/init.d/ssh.sh.new; true" </dev/null; then
		echo "  ssh: $srv on port 22"
		return 0
	fi
	# Port 22 does not answer with SERVER: the previous one back, through 2222.
	ssh_2222 'sh -s' <<EOF || true
C=$cfw
mv \$C/ssh_server.prev \$C/ssh_server
sh \$C/init.d/ssh.sh stop
sleep 1
sh \$C/init.d/ssh.sh start
kill \$(cat /var/run/sshd-2222.pid)
EOF
	fail "ssh: $srv did not answer on port 22 of $target; the previous server is back"
}

# ssh_hook: make the boot start <cfw>/init.d/ssh.sh (the server <cfw>/ssh_server names, falling
# back to dropbear) instead of the install kit's dropbear-only hook. A rootfs write (remount rw,
# the original kept as <cfw>/init.d/S42zdropbear.orig and used when ssh.sh is missing or fails);
# only with SSH_HOOK=1, after the owner agreed (docs/ssh.md, "Boot").
ssh_hook() {
	ssh_ "$target" 'sh -s' <<EOF || fail "ssh boot hook on $target"
set -e
C=$cfw
H=/etc/init.d/S42zdropbear
[ -f \$H ] || { echo "  ssh hook: no \$H here, not changed" >&2; exit 0; }
grep -q 'init.d/ssh.sh' \$H && { echo "  ssh hook: already in place"; exit 0; }
[ -f \$C/init.d/ssh.sh ] || { echo "  ssh hook: no \$C/init.d/ssh.sh yet (switch with SSH= first)" >&2; exit 1; }
mkdir -p \$C/init.d
[ -f \$C/init.d/S42zdropbear.orig ] || cp -p \$H \$C/init.d/S42zdropbear.orig
mount -o remount,rw /
trap 'sync; mount -o remount,ro /' EXIT
cat > \$H.new <<'HOOK'
#!/bin/sh
# yastation: the speaker's SSH server, dropbear or OpenSSH (<cfw>/init.d/ssh.sh; docs/ssh.md).
# The install kit's dropbear-only original is <cfw>/init.d/S42zdropbear.orig: it runs when
# ssh.sh is missing or starts nothing.
[ "\$1" = "start" ] || exit 0
CFW=/bkupgrade/cfw; [ -d "\$CFW" ] || CFW=/data/cfw
[ -f "\$CFW/init.d/ssh.sh" ] && sh "\$CFW/init.d/ssh.sh" start && exit 0
exec sh "\$CFW/init.d/S42zdropbear.orig" start
HOOK
chmod 755 \$H.new
mv \$H.new \$H
echo "  ssh hook: \$H now runs \$C/init.d/ssh.sh"
EOF
}

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

	if [ "${DROPBEAR:-0}" = only ]; then
		if [ -n "${SSH:-}" ]; then switch_ssh "$SSH"; else update_dropbear; fi
		[ "${SSH_HOOK:-0}" = 1 ] && ssh_hook
		continue
	fi
	first=0
	ssh_ "$target" "test -e $cfw/bin/yastationd" </dev/null || first=1

	put "$bin" "cat > $cfw/bin/yastationd.upload"
	put "$here/yastationd/init.d/supervise.sh" "mkdir -p $cfw/init.d && cat > $cfw/init.d/supervise.sh.new && mv $cfw/init.d/supervise.sh.new $cfw/init.d/supervise.sh"
	# The diagnostics collector (docs/guides/diagnostics.md); nothing runs it on deploy.
	put "$here/yastationd/scripts/diag.sh" "cat > $cfw/bin/diag.sh.new && chmod 755 $cfw/bin/diag.sh.new && mv $cfw/bin/diag.sh.new $cfw/bin/diag.sh"
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
	if [ "$first" = 1 ] || [ "${DROPBEAR:-0}" = 1 ]; then
		update_dropbear
	fi
	[ -n "${SSH:-}" ] && switch_ssh "$SSH"
	[ "${SSH_HOOK:-0}" = 1 ] && ssh_hook
	true
done
