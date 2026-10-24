#!/bin/sh
# Boot smoke test of an emulated speaker:
#   fresh images -> maintenance boot + provision (auto) -> normal boot (headless) -> SSH ->
#   checks -> logs to emu/work/<board>/results/<stamp>/ -> PASS/FAIL.
#
#   emu/test/boot-smoke.sh <board> [--power-cycle] [--stable S] [--boot-timeout S]
#                                  [--ssh-port N] [--no-fresh] [--keep-running]
#
#   --power-cycle    after the first round of checks, cut the power (powercut.sh), boot again
#                    and check again
#   --stable S       how long yastationd's PID must stay the same (default 120)
#   --boot-timeout S how long to wait for SSH after power-on (default 900: TCG is slow)
#   --no-fresh       reuse the current flash images (skip --fresh and the provisioning boot)
#   --keep-running   leave QEMU running at the end
#
# Needs a seed (emu/seed-from-unit.sh <board> ...). SSH uses the seed's emulator key.
set -eu
. "$(cd "$(dirname "$0")" && pwd)/../lib/common.sh"

[ $# -ge 1 ] || { sed -n '2,16p' "$0"; exit 1; }
load_board "$1"; shift
POWERCYCLE=0 STABLE=120 BOOT_TIMEOUT=900 FRESH=1 KEEP=0 SSH_PORT=
while [ $# -gt 0 ]; do
	case $1 in
		--power-cycle) POWERCYCLE=1 ;;
		--stable) STABLE=$2; shift ;;
		--boot-timeout) BOOT_TIMEOUT=$2; shift ;;
		--ssh-port) SSH_PORT=$2; shift ;;
		--no-fresh) FRESH=0 ;;
		--keep-running) KEEP=1 ;;
		*) die "unknown option $1" ;;
	esac
	shift
done
if [ -z "$SSH_PORT" ]; then
	case $BOARD in bergamot) SSH_PORT=2222 ;; pickle) SSH_PORT=2223 ;; *) SSH_PORT=2224 ;; esac
fi
[ -f "$SEED/provision.sh" ] || die "no seed in $SEED (run emu/seed-from-unit.sh $BOARD <ssh-target>)"

OUT=$RESULTS/$(date +%Y%m%d-%H%M%S)$([ "$POWERCYCLE" = 1 ] && echo -powercycle)
mkdir -p "$OUT"
FAILS=0
pass() { echo "PASS  $*" | tee -a "$OUT/summary.txt"; }
fail() { echo "FAIL  $*" | tee -a "$OUT/summary.txt"; FAILS=$((FAILS + 1)); }
info() { echo "      $*" | tee -a "$OUT/summary.txt"; }

g() {
	ssh -p "$SSH_PORT" -i "$SEED/emu_ssh_key" -o IdentitiesOnly=yes -o BatchMode=yes \
		-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
		-o ConnectTimeout=10 root@127.0.0.1 "$@"
}

stop_qemu() { "$EMU/powercut.sh" "$BOARD" --no-restart >/dev/null 2>&1 || true; }
trap '[ "$KEEP" = 1 ] || stop_qemu' EXIT
qemu_pid >/dev/null && die "$BOARD is already running; stop it first (emu/powercut.sh $BOARD --no-restart)"

echo "== $BOARD smoke test -> $OUT"

# --- 1. fresh flash + provisioning in maintenance mode ---
if [ "$FRESH" = 1 ]; then
	if timeout 900 "$EMU/run.sh" "$BOARD" --fresh --maint --auto --headless \
		--ssh-port "$SSH_PORT" --log "$OUT/maint.log" >"$OUT/maint.run.txt" 2>&1 \
		&& grep -q '\[maint\] RESULT: OK' "$OUT/maint.log"; then
		pass "maintenance boot: images created, seed provisioned"
	else
		fail "maintenance boot/provision (see maint.log, maint.run.txt)"
		exit 1
	fi
fi

# --- 2. normal boot and checks ---
wait_ssh() { # $1 = serial log
	t0=$(date +%s)
	while :; do
		if g true 2>/dev/null; then
			echo $(( $(date +%s) - t0 ))
			return 0
		fi
		qemu_pid >/dev/null || { echo "qemu exited"; return 1; }
		[ $(( $(date +%s) - t0 )) -lt "$BOOT_TIMEOUT" ] || { echo "timeout"; return 1; }
		sleep 5
	done
}

# Runs the checks against the running guest; $1 = label for the result files.
check_round() {
	label=$1 log=$2
	if r=$(wait_ssh "$log"); then
		pass "[$label] SSH up ${r}s after power-on"
	else
		fail "[$label] no SSH ($r)"
		return 0
	fi
	boots=$(grep -c 'Booting Linux on physical CPU' "$log" || true)
	[ "$boots" -le 1 ] && pass "[$label] booted once (no reboot loop)" || fail "[$label] $boots kernel boots in the serial log"
	grep -q 'Kernel panic' "$log" && fail "[$label] kernel panic in the serial log"

	# Vendor stack must stay gated.
	n=$(g 'ps w | grep -c "[m]aind\|[q]uasar_launcher"; true' || echo "?")
	[ "$n" = 0 ] && pass "[$label] vendor stack not running" || fail "[$label] vendor stack processes: $n"

	# yastationd: alive, PID stable, no respawn loop.
	pidcmd="for p in /proc/[0-9]*; do [ \"\$(readlink \$p/exe 2>/dev/null)\" = \"$CFW_ROOT/bin/yastationd\" ] && echo \${p#/proc/}; done"
	t0=$(date +%s); pid1=
	while [ -z "$pid1" ] && [ $(( $(date +%s) - t0 )) -lt 120 ]; do
		pid1=$(g "$pidcmd" | head -1) || true
		[ -n "$pid1" ] || sleep 5
	done
	if [ -z "$pid1" ]; then
		fail "[$label] yastationd not running"
	else
		info "[$label] yastationd pid $pid1, watching for ${STABLE}s"
		sleep "$STABLE"
		pid2=$(g "$pidcmd" | head -1) || true
		[ "$pid1" = "$pid2" ] && pass "[$label] yastationd PID stable for ${STABLE}s" \
			|| fail "[$label] yastationd PID changed ($pid1 -> ${pid2:-none})"
	fi
	# Which CFW hooks ran: each leaves a trace. Checked after the stability wait, so rcS has had
	# time to reach the last hooks (dropbear comes up long before S99).
	g "ls /etc/init.d" > "$OUT/$label.initd.txt" 2>&1 || true
	for h in $(grep -E '^S[0-9][0-9]z' "$OUT/$label.initd.txt"); do
		case $h in
			S42zdropbear) pass "[$label] $h ran (we are connected through it)" ;;
			S52zyastationd)
				if g '[ -f /tmp/yastationd-sup.pid ] && kill -0 $(cat /tmp/yastationd-sup.pid)'; then
					pass "[$label] $h ran (supervisor alive)"
				else
					fail "[$label] $h: no live supervisor"
				fi ;;
			S99zzegress)
				if g '[ -f /tmp/yastation-egress.log ]'; then pass "[$label] $h ran (rcS reached the end)"; info "$(g cat /tmp/yastation-egress.log | head -3)"
				else fail "[$label] $h did not run"; fi ;;
			S42zadbusb)
				g '[ -f /tmp/yastation-adbusb.log ]' && pass "[$label] $h ran" || fail "[$label] $h did not run" ;;
			*) info "[$label] $h present (no trace check)" ;;
		esac
	done

	g 'cat /tmp/yastationd.log.1 /tmp/yastationd.log 2>/dev/null' > "$OUT/$label.yastationd.log" 2>&1 || true
	starts=$(grep -c '^=== start' "$OUT/$label.yastationd.log" || true)
	exits=$(grep -c '^=== exit' "$OUT/$label.yastationd.log" || true)
	sup=$(grep -c '^=== supervise:' "$OUT/$label.yastationd.log" || true)
	if [ "$exits" -le 1 ] && [ "$sup" = 0 ]; then
		pass "[$label] no respawn loop (starts=$starts exits=$exits)"
	else
		fail "[$label] respawns: starts=$starts exits=$exits supervisor-notes=$sup"
	fi
	grep -m3 -iE 'panic|fatal' "$OUT/$label.yastationd.log" | while read -r l; do info "[$label] log: $l"; done

	# Collect.
	g dmesg > "$OUT/$label.dmesg.txt" 2>&1 || true
	g 'ps w' > "$OUT/$label.ps.txt" 2>&1 || true
	g 'logread 2>/dev/null || cat /var/log/messages' > "$OUT/$label.syslog.txt" 2>&1 || true
	g 'cat /proc/mounts; echo; cat /proc/mtd; echo; cat /proc/cmdline; echo; uptime' > "$OUT/$label.system.txt" 2>&1 || true
	g "ls -la $CFW_ROOT; cat $CFW_ROOT/.emu-provisioned" > "$OUT/$label.cfw.txt" 2>&1 || true
}

"$EMU/run.sh" "$BOARD" --headless --ssh-port "$SSH_PORT" --log "$OUT/serial.log" >"$OUT/run.txt" 2>&1 \
	|| { fail "QEMU did not start (see run.txt)"; exit 1; }
check_round boot1 "$OUT/serial.log"

if [ "$POWERCYCLE" = 1 ]; then
	info "power cut"
	"$EMU/powercut.sh" "$BOARD" --off 2 >"$OUT/powercut.txt" 2>&1 || fail "powercut.sh failed (see powercut.txt)"
	check_round boot2 "$OUT/serial.log"
fi

echo
if [ "$FAILS" = 0 ]; then
	echo "RESULT: PASS ($BOARD)" | tee -a "$OUT/summary.txt"
else
	echo "RESULT: FAIL ($BOARD, $FAILS failed checks)" | tee -a "$OUT/summary.txt"
	exit 1
fi
