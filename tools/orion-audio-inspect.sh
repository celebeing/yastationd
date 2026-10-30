#!/bin/sh
# orion-audio-inspect.sh - bounded, read-only Orion ALSA/mixer inspection.
#
# Usage from the host:
#   ssh root@<speaker-ip> 'sh -s' < tools/orion-audio-inspect.sh | tee captures/orion-audio-inspect.log
#
# This script does not play sound, open capture devices, or write mixer state.
# It deliberately does not run `amixer contents` or `aplay -L`.

set -u

TMP=/tmp/orion-audio-inspect-$$
mkdir -p "$TMP" 2>/dev/null || true
RUN_PID=

cleanup() {
	[ -n "${RUN_PID:-}" ] && kill "$RUN_PID" 2>/dev/null || true
	rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

section() {
	echo
	echo "===== $* ====="
}

# Run one direct external command with a three-second watchdog. The command is
# passed as argv, not through a shell, so the watchdog can kill the actual probe.
bounded() {
	label=$1
	shift
	file="$TMP/$label"
	"$@" >"$file" 2>&1 &
	RUN_PID=$!
	seconds=0
	while kill -0 "$RUN_PID" 2>/dev/null; do
		if [ "$seconds" -ge 3 ]; then
			kill "$RUN_PID" 2>/dev/null || true
			wait "$RUN_PID" 2>/dev/null || true
			echo "TIMEOUT after 3 seconds: $*"
			cat "$file" 2>/dev/null || true
			RUN_PID=
			return 124
		fi
		sleep 1
		seconds=$((seconds + 1))
	done
	status=0
	wait "$RUN_PID" || status=$?
	cat "$file" 2>/dev/null || true
	RUN_PID=
	return "$status"
}

section "identity"
id 2>&1 || true
uname -a 2>&1 || true
printf 'compatible: '
tr '\0' ' ' </proc/device-tree/compatible 2>/dev/null || true
echo

section "available commands"
for command in amixer aplay arecord; do
	if command -v "$command" >/dev/null 2>&1; then
		echo "$command: $(command -v "$command")"
	else
		echo "$command: MISSING"
	fi
done

section "ALSA cards"
cat /proc/asound/cards 2>&1 || true

section "ALSA PCMs"
cat /proc/asound/pcm 2>&1 || true

section "playback device list"
bounded playback-list aplay -l || true

section "known mixer controls"
i=0
for control in \
	"name=Master Playback Volume" \
	"name=fr1 Acm8625 AMP mute status" \
	"name=fr2 Acm8625 AMP mute status" \
	"name=wf Acm8625 AMP mute status" \
	"name=main Acm8625 AMP mute status" \
	"name=speaker Acm8625 AMP mute status"; do
	echo "--- $control"
	bounded "mixer-$i" amixer -c0 cget "$control" || true
	i=$((i + 1))
done

section "sound sysfs"
for path in /sys/class/sound/*; do
	[ -e "$path" ] || continue
	printf '%s: ' "$path"
	cat "$path/id" 2>/dev/null || echo unknown
done

section "audio-related processes"
bounded process-list ps w || true

section "recent audio kernel messages"
bounded kernel-log dmesg || true
if [ -s "$TMP/kernel-log" ]; then
	grep -iE 'acm8625|audio|alsa|asoc|pcm|mroute|mcapt|speaker|spk_onoff|xrun|overrun|underrun' "$TMP/kernel-log" | tail -80 || true
fi

echo
echo "inspection complete"
