#!/bin/sh
# supervise.sh — keeps yastationd running until <cfw>/yastationd_off exists. Started (under setsid)
# by /etc/init.d/S52zyastationd; lives in <cfw>/init.d/ so changing it needs no rootfs write.
#
# Exit status of the agent: 0 = deliberate exit (SIGTERM/SIGINT), restarted after 1 s and not
# counted as a crash unless it ran < MINUP s. Anything else (1 = init/run error, 2 = Go panic,
# 128+n = killed by signal n) is a crash. A "short crash" is one after a run of < LONG s; only a
# run of >= LONG s resets the count. Restart delay after a crash: BACKOFF_MIN s, doubling up to
# BACKOFF_MAX. After CRASHES consecutive short crashes the previous binary (bin/yastationd.prev,
# kept by `make deploy` / tools/deploy.sh) is swapped in and the bad one kept as .bad. If that
# crash-loops too, it keeps retrying every GIVEUP s: there is no vendor stack to fall back to.
#
# The log is copy-truncated at MAXLOG bytes (one .1 kept): the agent keeps its O_APPEND descriptor,
# so a rename would not stop the growth.
#
# YASTATIOND_CFW / YASTATIOND_LOG and the tunables below override the device values (host dry runs).

CFW=${YASTATIOND_CFW:-/bkupgrade/cfw}; [ -d "$CFW" ] || CFW=/data/cfw
BIN=$CFW/bin/yastationd
CONF=$CFW/yastationd.conf
OFF=$CFW/yastationd_off
LOG=${YASTATIOND_LOG:-/tmp/yastationd.log}
MAXLOG=${MAXLOG:-1000000}
LONG=${LONG:-300}
CRASHES=${CRASHES:-5}
MINUP=${MINUP:-5}
BACKOFF_MIN=${BACKOFF_MIN:-2}
BACKOFF_MAX=${BACKOFF_MAX:-60}
GIVEUP=${GIVEUP:-300}

# Seconds since boot: unaffected by clock steps (the agent's own SNTP step, chronyd).
uptime_s() { read -r u _ < /proc/uptime; echo "${u%%.*}"; }

rotate() {
	if [ -f "$LOG" ] && [ "$(wc -c < "$LOG")" -gt "$MAXLOG" ]; then
		cp "$LOG" "$LOG.1" && : > "$LOG"
	fi
}

say() { echo "=== supervise: $* ($(date))" >> "$LOG"; }

# Sleeps and waits in the background: busybox ash runs a trap only after the foreground command
# returns, while `wait` is interrupted by it, so `stop` takes effect at once.
nap() { sleep "$1" & wait $!; }

# Puts bin/yastationd.prev in place of a crash-looping binary (kept as .bad). Fails when there is
# no distinct .prev.
swap_prev() {
	[ -f "$BIN.prev" ] || return 1
	[ -f "$BIN" ] && cmp -s "$BIN" "$BIN.prev" && return 1
	[ -f "$BIN" ] && mv -f "$BIN" "$BIN.bad"
	cp -p "$BIN.prev" "$BIN.tmp" && chmod +x "$BIN.tmp" && mv -f "$BIN.tmp" "$BIN"
}

( while sleep 60; do rotate; done ) &
ROT=$!
CHILD=
trap 'kill $CHILD $ROT 2>/dev/null; exit 143' TERM INT HUP

fails=0 swapped=0 backoff=$BACKOFF_MIN
while [ ! -f "$OFF" ]; do
	rotate
	if [ ! -x "$BIN" ]; then
		if [ "$swapped" = 0 ] && swap_prev; then
			swapped=1 fails=0
			say "$BIN missing or not executable; restored .prev"
		else
			say "$BIN missing or not executable; retrying in ${GIVEUP}s"
			nap "$GIVEUP"
			continue
		fi
	fi

	echo "=== start $(date)" >> "$LOG"
	t0=$(uptime_s)
	"$BIN" -config "$CONF" >> "$LOG" 2>&1 &
	CHILD=$!
	wait $CHILD
	rc=$?
	CHILD=
	up=$(($(uptime_s) - t0))
	echo "=== exit $rc after ${up}s $(date)" >> "$LOG"

	if [ "$up" -ge "$LONG" ]; then
		fails=0 swapped=0 backoff=$BACKOFF_MIN
		[ "$rc" -eq 0 ] && nap 1 || nap "$backoff"
		continue
	fi
	if [ "$rc" -eq 0 ] && [ "$up" -ge "$MINUP" ]; then
		nap 1
		continue
	fi

	fails=$((fails + 1))
	if [ "$fails" -ge "$CRASHES" ]; then
		if [ "$swapped" = 0 ] && swap_prev; then
			swapped=1 fails=0 backoff=$BACKOFF_MIN
			say "$CRASHES short crashes in a row; swapped in .prev (bad binary kept as .bad)"
			continue
		fi
		say "$CRASHES short crashes in a row, no other binary to try; retrying in ${GIVEUP}s"
		fails=0 backoff=$BACKOFF_MIN
		nap "$GIVEUP"
		continue
	fi
	say "crash $fails/$CRASHES (rc $rc after ${up}s); restarting in ${backoff}s"
	nap "$backoff"
	backoff=$((backoff * 2)); [ "$backoff" -gt "$BACKOFF_MAX" ] && backoff=$BACKOFF_MAX
done
say "$OFF present; stopping"
kill $ROT 2>/dev/null
