#!/bin/sh
# devendor.sh — take the vendor's software off a speaker for good, and with it the need for an egress
# lock (docs/guides/devendor.md). Only with a golden image in the restore partition: the reset is
# the way back from here, and this tool refuses to run without one.
#
#   tools/devendor.sh remove <ssh-target> <board>   move it aside, reboot, check the speaker
#   tools/devendor.sh undo   <ssh-target>           put it back, reboot (before commit)
#   tools/devendor.sh commit <ssh-target>           delete it for good and open the internet
#   tools/devendor.sh status <ssh-target>
#
# Boards: those with a tools/devendor/<board>.conf (what goes differs by board).
# Env: SSHKEY as for deploy.sh; GOLDEN_DIR (default dumps/golden) where tools/golden.sh built images.
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
GOLDEN_DIR=${GOLDEN_DIR:-$here/dumps/golden}
keyopt=
[ -n "${SSHKEY:-}" ] && keyopt="-i $SSHKEY -o IdentitiesOnly=yes"
# shellcheck disable=SC2086 # keyopt is two options or none
ssh_() { ssh $keyopt -o BatchMode=yes -o ConnectTimeout=5 "$target" "$@"; }
die() { echo "devendor: $*" >&2; exit 1; }

COMMON='
CFW=/bkupgrade/cfw; [ -d /bkupgrade ] || CFW=/data/cfw
H=$(dirname "$CFW")/vendor-held
FLAG=$CFW/vendor_removed
Q=/system/vendor/quasar
'

# golden_gate: the package in aml_sysrecovery must be one tools/golden.sh built for this board.
golden_gate() {
	sum=$(ssh_ 'n=$(sed -n "s/^\(mtd[0-9]*\):.*\"aml_sysrecovery\"$/\1/p" /proc/mtd 2>/dev/null)
D=/dev/$n; [ -n "$n" ] || D=/dev/aml_sysrecovery
[ -e "$D" ] || exit 3
head -c "$(od -An -tu8 -j12 -N8 "$D" | tr -d " ")" "$D" | sha256sum | cut -d" " -f1' </dev/null) ||
		die "no restore partition on $target: no golden image, no way back"
	for t in "$GOLDEN_DIR"/"$board"-*/golden.txt; do
		if [ -f "$t" ] && grep -qx "sha256 $sum" "$t"; then
			echo "golden image in place: ${t%/golden.txt}"
			return 0
		fi
	done
	die "the restore partition of $target holds no golden image built here ($sum; see tools/golden.sh). This tool does not run without one"
}

wait_back() {
	sleep 20
	i=0
	until ssh_ true </dev/null 2>/dev/null; do
		i=$((i + 1))
		[ $i -lt 40 ] || die "$target did not come back. Reset it (mute held at power-on): the golden image restores it"
		sleep 3
	done
}

# check: the speaker runs what matters. The first start of the daemon can fail on pickle (the ring's
# bus isn't Linux's yet) and the supervisor starts it again, so this waits.
check() {
	echo "== checking"
	sleep 30
	ssh_ '
ps w | grep -q "[y]astationd -config" || { echo "no yastationd"; exit 1; }
ps w | grep -q "[s]potter-host serve" || { echo "no spotter-host"; exit 1; }
grep -q "wake: spotter ready" /tmp/yastationd.log || { echo "the wake word did not start"; exit 1; }
grep -E "msg=health" /tmp/yastationd.log | tail -1 | sed "s/.*state=/health: /"' </dev/null ||
		die "the speaker came back without its daemon or voice: tools/devendor.sh undo $target"
}

remove() {
	[ $# -eq 2 ] || { echo "usage: $0 remove <ssh-target> <board>" >&2; exit 2; }
	target=$1 board=$2
	conf=$here/tools/devendor/$board.conf
	[ -f "$conf" ] || die "no $conf: nobody has worked out what goes on a $board"
	. "$conf"
	compat=$(ssh_ 'tr "\0" " " </proc/device-tree/compatible' </dev/null) || die "ssh to $target"
	case $compat in *"yandex,$board"*) ;; *) die "$target is '$compat', not $board" ;; esac
	golden_gate
	echo "== moving the vendor's software aside"
	# shellcheck disable=SC2086 # GO is flattened to one line
	ssh_ 'sh -s' <<EOF || die "moving failed; the rootfs may be partly moved: tools/devendor.sh undo $target"
set -eu
$COMMON
KEEP="$KEEP"
GO="$(echo $GO)"
[ ! -e "\$FLAG" ] || { echo "already done here: \$(cat "\$FLAG")" >&2; exit 1; }
[ -x "\$CFW/bin/yastationd" ] || { echo "no CFW here" >&2; exit 1; }
need=\$(du -sk "\$Q" | cut -f1)
free=\$(df -k "\$(dirname "\$CFW")" | awk 'END{print \$(NF-2)}')
[ "\$free" -gt "\$((need + 8192))" ] || { echo "\$free KB free for \$need KB" >&2; exit 1; }
hold() { mkdir -p "\$H\$(dirname "\$1")"; mv "\$1" "\$H\$1"; }
mount -o remount,rw /
trap 'sync; mount -o remount,ro /' EXIT
for s in S95yandex-debugctl S50keymaster-svc S48tee-supplicant; do
	[ -x /etc/init.d/\$s ] && /etc/init.d/\$s stop >/dev/null 2>&1 || true
done
for f in \$Q/*; do
	b=\${f##*/}
	case " \$KEEP " in
	*" \$b "*) continue ;;
	*" \$b/"*)
		for g in \$f/*; do
			case " \$KEEP " in *" \$b/\${g##*/} "*) ;; *) hold "\$g" ;; esac
		done ;;
	*) hold "\$f" ;;
	esac
done
for f in \$GO; do
	[ -e "\$f" ] && hold "\$f"
done
hold /etc/init.d/S99quasar
cat >/etc/init.d/S99quasar <<'HOOK'
#!/bin/sh
# yastation: the vendor stack is gone from this unit (docs/guides/devendor.md). What its boot script
# did that anything here needs: allow overcommit, for fork in processes with big maps.
[ "\$1" = "start" ] && echo 1 > /proc/sys/vm/overcommit_memory
exit 0
HOOK
chmod 755 /etc/init.d/S99quasar
{ date; echo held; } >"\$FLAG"
EOF
	ssh_ '(sleep 1; reboot) >/dev/null 2>&1 &' </dev/null
	wait_back
	check
	echo "ok: the vendor's software is aside. Look at the speaker; then commit, or undo."
}

undo() {
	[ $# -eq 1 ] || { echo "usage: $0 undo <ssh-target>" >&2; exit 2; }
	target=$1
	ssh_ 'sh -s' <<EOF || die "undo failed"
set -eu
$COMMON
[ -d "\$H" ] || { echo "nothing held at \$H (after commit only a reset brings it back)" >&2; exit 1; }
mount -o remount,rw /
trap 'sync; mount -o remount,ro /' EXIT
(cd "\$H" && find . -type f -o -type l) | while read -r f; do
	p=\${f#.}
	mkdir -p "\$(dirname "\$p")"
	rm -f "\$p"
	mv "\$H\$p" "\$p"
done
rm -rf "\$H" "\$FLAG"
EOF
	ssh_ '(sleep 1; reboot) >/dev/null 2>&1 &' </dev/null
	wait_back
	check
	echo "ok: back as it was"
}

commit() {
	[ $# -eq 1 ] || { echo "usage: $0 commit <ssh-target>" >&2; exit 2; }
	target=$1
	ssh_ 'sh -s' <<EOF || die "commit failed"
set -eu
$COMMON
grep -qx held "\$FLAG" 2>/dev/null || { echo "run remove first" >&2; exit 1; }
rm -rf "\$H"
touch "\$CFW/egress_disabled"
sh "\$CFW/egress.sh" open >/dev/null
{ date; echo committed; } >"\$FLAG"
sync
sh "\$CFW/egress.sh" status | head -1
EOF
	echo "ok: gone for good; a factory reset is what brings it back. A block on your router is yours to lift."
}

status() {
	[ $# -eq 1 ] || { echo "usage: $0 status <ssh-target>" >&2; exit 2; }
	target=$1
	ssh_ "$COMMON"'cat "$FLAG" 2>/dev/null | tr "\n" " "; echo; du -sm "$H" 2>/dev/null; df -h / | tail -1; sh "$CFW/egress.sh" status | head -1' </dev/null
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
remove) remove "$@" ;;
undo) undo "$@" ;;
commit) commit "$@" ;;
status) status "$@" ;;
*) sed -n '2,12p' "$0" >&2; exit 2 ;;
esac
