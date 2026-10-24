#!/bin/sh
# "Pulls the plug" on a running emulated speaker: SIGKILLs its QEMU (no guest shutdown, no
# flush of the guest's caches; what QEMU already wrote to the flash images stays, like NAND
# pages already programmed) and boots it again with the same files and options.
#
#   emu/powercut.sh <board> [--no-restart] [--off S]
#
#   --no-restart   only cut the power
#   --off S        seconds to stay "unplugged" before restarting (default 1)
#
# The restart uses emu/work/<board>/last-run.args (written by run.sh on a normal boot), always
# headless so the caller gets control back.
set -eu
. "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

[ $# -ge 1 ] || { sed -n '2,12p' "$0"; exit 1; }
load_board "$1"; shift
RESTART=1 OFF=1
while [ $# -gt 0 ]; do
	case $1 in
		--no-restart) RESTART=0 ;;
		--off) OFF=$2; shift ;;
		*) die "unknown option $1" ;;
	esac
	shift
done

if p=$(qemu_pid); then
	kill -9 "$p"
	i=0
	while kill -0 "$p" 2>/dev/null && [ $i -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
	kill -0 "$p" 2>/dev/null && die "QEMU pid $p survived SIGKILL"
	note "$BOARD: power cut (QEMU pid $p killed)"
else
	note "$BOARD: not running"
fi
rm -f "$BDIR/qemu.pid" "$BDIR/qmp.sock"

[ "$RESTART" = 1 ] || exit 0
sleep "$OFF"
ARGS=$(cat "$BDIR/last-run.args" 2>/dev/null || true)
case " $ARGS " in *" --headless "*) ;; *) ARGS="$ARGS --headless" ;; esac
# Keep the previous serial log: the restart appends a new one.
LOGF=$(echo " $ARGS " | sed -n 's/.* --log \([^ ]*\) .*/\1/p')
[ -n "$LOGF" ] || LOGF=$BDIR/serial.log
[ -f "$LOGF" ] && mv "$LOGF" "$LOGF.before-powercut"
note "$BOARD: power on"
# shellcheck disable=SC2086
exec "$EMU/run.sh" "$BOARD" $ARGS
