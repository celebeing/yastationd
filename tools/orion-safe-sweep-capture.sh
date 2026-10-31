#!/bin/sh
# orion-safe-sweep-capture.sh - bounded, low-level Orion acoustic measurement.
#
# Usage:
#   tools/orion-safe-sweep-capture.sh <ssh-target> <output.raw>
#
# Captures 7 seconds of direct 14-channel LOOPBACK-A audio while playing one
# faded 100 Hz -> 10 kHz logarithmic sweep. Playback is fixed at raw master
# volume 134 and -40 dBFS peak PCM. The prior mixer values are restored even
# when the remote command exits with an error.
#
# This does not stop yastationd or the vendor stack. The caller must first free
# hw:0,3. It writes only temporary files under /tmp on the speaker.

set -eu

[ "$#" -eq 2 ] || {
	echo "usage: $0 <ssh-target> <output.raw>" >&2
	exit 2
}

target=$1
output=$2
case "$output" in
/*) ;;
*) output=$(pwd)/$output ;;
esac
case "$output" in
*.raw) meta=${output%.raw}.json ;;
*) meta=$output.json ;;
esac
log_output=${meta%.json}.log
mkdir -p "$(dirname "$output")"
umask 077

master=134
peak_dbfs=-40
capture_seconds=7
capture_rate=96000
capture_channels=14
expected=$((capture_seconds * capture_rate * capture_channels * 2))
remote_dir=/tmp/yastationd-safe-sweep-$$
local_sweep=$(mktemp "${TMPDIR:-/tmp}/orion-sweep-XXXXXX.raw")
trap 'rm -f "$local_sweep"' EXIT HUP INT TERM

# The host contract includes awk. Generate locally because the speaker's BusyBox
# awk omits math support; PCM is later streamed to the speaker over SSH.
awk -v rate=48000 -v seconds=4 -v f0=100 -v f1=10000 -v peak=328 '
BEGIN {
	pi = 4 * atan2(1, 1)
	n = rate * seconds
	fade = rate / 10
	lr = log(f1 / f0)
	for (i = 0; i < n; i++) {
		t = i / rate
		env = i < fade ? i / fade : (i >= n - fade ? (n - 1 - i) / fade : 1)
		phase = 2 * pi * f0 * seconds / lr * (exp(lr * t / seconds) - 1)
		v = peak * env * sin(phase)
		s = v >= 0 ? int(v + 0.5) : int(v - 0.5)
		u = s < 0 ? s + 65536 : s
		printf "%c%c", u % 256, int(u / 256)
	}
}' </dev/null >"$local_sweep"
[ "$(wc -c <"$local_sweep" | tr -d '[:space:]')" -eq 384000 ] || {
	echo "failed to generate the expected 384000-byte sweep" >&2
	exit 1
}

printf 'local raw:  %s\nlocal meta: %s\nlocal log:  %s\n' "$output" "$meta" "$log_output"
ssh "$target" "mkdir -p '$remote_dir'" </dev/null
ssh "$target" "cat > '$remote_dir/sweep.raw'" <"$local_sweep"

ssh "$target" "sh -s" <<EOF
set -eu
D='$remote_dir'
CAP=\$D/capture.raw
SWEEP=\$D/sweep.raw
LOG=\$D/remote.log

getval() {
	amixer -c0 cget "\$1" 2>/dev/null |
		sed -n 's/^[[:space:]]*: values=//p' |
		tail -n 1
}
setval() {
	if amixer -c0 cset "\$1" "\$2" >>"\$LOG" 2>&1; then
		STATUS=0
	else
		STATUS=\$?
	fi
	printf 'cset %s=%s exit=%s\\n' "\$1" "\$2" "\$STATUS" >>"\$LOG"
	[ "\$STATUS" -eq 0 ]
}

V_FR1=\$(getval "name='fr1 Acm8625 AMP mute status'")
V_FR2=\$(getval "name='fr2 Acm8625 AMP mute status'")
V_WF=\$(getval "name='wf Acm8625 AMP mute status'")
V_MV=\$(getval "name='Master Playback Volume'")
V_LB=\$(getval "name='Loopback tdmin lb source'")
[ -n "\$V_MV" ] && [ -n "\$V_LB" ] || {
	echo "required Orion mixer control not found" >&2
	exit 1
}
{
	echo "mixer before test:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	amixer -c0 cget "name='fr2 Acm8625 AMP mute status'"
	amixer -c0 cget "name='wf Acm8625 AMP mute status'"
	amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1

restore() {
	[ -n "\${V_FR1:-}" ] && setval "name='fr1 Acm8625 AMP mute status'" "\$V_FR1" || true
	[ -n "\${V_FR2:-}" ] && setval "name='fr2 Acm8625 AMP mute status'" "\$V_FR2" || true
	[ -n "\${V_WF:-}" ] && setval "name='wf Acm8625 AMP mute status'" "\$V_WF" || true
	[ -n "\${V_MV:-}" ] && setval "name='Master Playback Volume'" "\$V_MV" || true
	[ -n "\${V_LB:-}" ] && setval "name='Loopback tdmin lb source'" "\$V_LB" || true
}
RP=
RESTORED=0
cleanup() {
	[ -n "\${RP:-}" ] && kill "\$RP" 2>/dev/null || true
	if [ "\$RESTORED" -eq 0 ]; then
		restore
	fi
	return 0
}
trap cleanup EXIT INT TERM


setval "name='fr1 Acm8625 AMP mute status'" 0
setval "name='fr2 Acm8625 AMP mute status'" 0
setval "name='wf Acm8625 AMP mute status'" 0
setval "name='Master Playback Volume'" $master
setval "name='Loopback tdmin lb source'" TDMOUTB
{
	echo "mixer after test setup:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1

arecord -q -D hw:0,3 -c $capture_channels -r $capture_rate -f S16_LE -t raw \\
	--buffer-time=1000000 --period-time=20000 -d $capture_seconds "\$CAP" >>"\$LOG" 2>&1 &
RP=\$!
sleep 1
aplay -q -D default -t raw -f S16_LE -r 48000 -c 1 "\$SWEEP" >>"\$LOG" 2>&1
wait "\$RP"
RP=
restore
RESTORED=1
{
	echo "mixer after restore:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1
printf 'capture bytes: '
wc -c <"\$CAP"
printf 'expected bytes: $expected\\n'
cat "\$LOG" >&2
EOF

echo "transferring raw capture..."
if ! ssh "$target" "cat '$remote_dir/capture.raw'" >"$output"; then
	echo "failed to transfer remote capture; leaving $remote_dir on $target" >&2
	exit 1
fi
echo "raw transfer complete"
if ! ssh "$target" "cat '$remote_dir/remote.log'" >"$log_output"; then
	echo "failed to transfer remote diagnostics; raw capture was saved" >&2
	exit 1
fi
echo "diagnostic log transfer complete"
actual=$(wc -c <"$output" | tr -d '[:space:]')
ssh "$target" "rm -rf '$remote_dir'" </dev/null || true
if [ "$actual" -ne "$expected" ]; then
	echo "warning: expected $expected bytes, received $actual" >&2
fi

cat >"$meta" <<EOF
{
  "board": "orion",
  "test": "logarithmic_sweep",
  "sweep_start_hz": 100,
  "sweep_end_hz": 10000,
  "sweep_duration_s": 4,
  "fade_duration_ms": 100,
  "tone_peak_dbfs": $peak_dbfs,
  "hardware_volume_raw": $master,
  "loopback_tdmin_source": "TDMOUTB",
  "capture_device": "hw:0,3",
  "format": "S16_LE",
  "channels": $capture_channels,
  "rate_hz": $capture_rate,
  "duration_s": $capture_seconds,
  "expected_bytes": $expected,
  "actual_bytes": $actual,
  "interleaved": true
}
EOF

echo "raw:  $output ($actual bytes)"
echo "meta: $meta"
