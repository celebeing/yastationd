#!/bin/sh
# orion-safe-tone-capture.sh - low-level Orion playback/reference test.
#
# Usage:
#   tools/orion-safe-tone-capture.sh <ssh-target> <output.raw> [volume-raw] [tone-dbfs] [capture-device] [channels] [loopback-source]

# The optional volume is bounded to raw values 1..134. The tone may be -60 dBFS
# or -40 dBFS; increase only one parameter per test. The only permitted
# temporary loopback source is TDMOUTB, restored after capture.
#
# The speaker emits one 1-second, 1 kHz mono tone while Master Playback Volume
# is forced to the requested raw value. Previous mixer values are restored even
# when the remote command exits with an error.
#
# This does not stop yastationd or the vendor stack. The selected capture
# device must already be free. It writes only temporary files under /tmp.
# Default output is 4 seconds of 13-channel S16_LE, 96 kHz, interleaved raw PCM.

set -eu

[ "$#" -ge 2 ] && [ "$#" -le 7 ] || {
	echo "usage: $0 <ssh-target> <output.raw> [volume-raw 1..134] [tone-dbfs -60|-40] [capture-device] [channels] [loopback-source TDMOUTB]" >&2
	exit 2
}

target=$1
output=$2
volume_raw=${3:-1}
tone_dbfs=-60
[ "$#" -ge 4 ] && tone_dbfs=$4
capture_device=${5:-mroute}
capture_channels=${6:-13}
loopback_source=${7:-}
case "$loopback_source" in
'' | TDMOUTB) ;;
*)
	echo "loopback-source must be TDMOUTB or omitted" >&2
	exit 2
	;;
esac
case "$capture_device" in
mroute | hw:0,3) ;;
*)
	echo "capture-device must be mroute or hw:0,3" >&2
	exit 2
	;;
esac
case "$capture_channels" in
1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14) ;;
*)
	echo "channels must be an integer from 1 through 14" >&2
	exit 2
	;;
esac
expected=$((4 * 96000 * capture_channels * 2))
case "$volume_raw" in
'' | *[!0-9]*)
	echo "volume-raw must be an integer from 1 through 134" >&2
	exit 2
	;;
esac
[ "$volume_raw" -ge 1 ] && [ "$volume_raw" -le 134 ] || {
	echo "volume-raw must be an integer from 1 through 134" >&2
	exit 2
}
case "$tone_dbfs" in
-60)
	period_b64=AAAEAAgADAAQABMAFwAZABwAHgAfACAAIAAgAB8AHgAcABkAFwATABAADAAIAAQAAAD8//j/9P/w/+3/6f/n/+T/4v/h/+D/4P/g/+H/4v/k/+f/6f/t//D/9P/4//z/
	;;
-40)
	period_b64=AAArAFUAfgCkAMgA6AAEARwBLwE9AUUBSAFFAT0BLwEcAQQB6ADIAKQAfgBVACsAAADV/6v/gv9c/zj/GP/8/uT+0f7D/rv+uP67/sP+0f7k/vz+GP84/1z/gv+r/9X/
	;;
*)
	echo "tone-dbfs must be -60 or -40" >&2
	exit 2
	;;
esac
case "$output" in
/*) ;;
*) output=$(pwd)/$output ;;
esac
case "$output" in
*.raw) meta=${output%.raw}.json ;;
*) meta=$output.json ;;
esac

mkdir -p "$(dirname "$output")"
umask 077

remote_dir=/tmp/yastationd-safe-tone-$$
log_output=${meta%.json}.log

echo "local raw:  $output"
echo "local meta: $meta"
echo "local log:  $log_output"
ssh "$target" "mkdir -p '$remote_dir'" </dev/null

# The selected 48-sample period is repeated 1000 times on the speaker to make
# a one-second tone.
ssh "$target" "printf '%s' '$period_b64' > '$remote_dir/period.b64'"
ssh "$target" "printf '%s' 'UklGRiR3AQBXQVZFZm10IBAAAAABAAEAgLsAAAB3AQACABAAZGF0YQB3AQA=' > '$remote_dir/header.b64'"

ssh "$target" "sh -s" <<EOF
set -eu
D='$remote_dir'
CAP=\$D/capture.raw
TONE=\$D/tone.wav
LOG=\$D/remote.log
LOOPBACK_SOURCE='$loopback_source'

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
	printf 'cset %s=%s exit=%s\n' "\$1" "\$2" "\$STATUS" >>"\$LOG"
	[ "\$STATUS" -eq 0 ]
}

V_FR1=\$(getval "name='fr1 Acm8625 AMP mute status'")
V_FR2=\$(getval "name='fr2 Acm8625 AMP mute status'")
V_WF=\$(getval "name='wf Acm8625 AMP mute status'")
V_MV=\$(getval "name='Master Playback Volume'")
V_LB=
[ -n "\$LOOPBACK_SOURCE" ] && V_LB=\$(getval "name='Loopback tdmin lb source'")
[ -n "\$V_MV" ] || {
	echo "Master Playback Volume control not found" >&2
	exit 1
}
{
	echo "mixer before test:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	amixer -c0 cget "name='fr2 Acm8625 AMP mute status'"
	amixer -c0 cget "name='wf Acm8625 AMP mute status'"
	[ -n "\$LOOPBACK_SOURCE" ] && amixer -c0 cget "name='Loopback tdmin lb source'"
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

base64 -d <"\$D/period.b64" >"\$D/period.raw"
base64 -d <"\$D/header.b64" >"\$TONE"
i=0
while [ "\$i" -lt 1000 ]; do
	cat "\$D/period.raw" >>"\$TONE"
	i=\$((i + 1))
done

# Unmute only for the test, then restore every saved control on exit.
setval "name='fr1 Acm8625 AMP mute status'" 0
setval "name='fr2 Acm8625 AMP mute status'" 0
setval "name='wf Acm8625 AMP mute status'" 0
setval "name='Master Playback Volume'" "$volume_raw"
[ -n "\$LOOPBACK_SOURCE" ] && setval "name='Loopback tdmin lb source'" "\$LOOPBACK_SOURCE"
{
	echo "mixer after test setup:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	amixer -c0 cget "name='fr2 Acm8625 AMP mute status'"
	amixer -c0 cget "name='wf Acm8625 AMP mute status'"
	[ -n "\$LOOPBACK_SOURCE" ] && amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1

arecord -q -D '$capture_device' -c '$capture_channels' -r 96000 -f S16_LE -t raw \\
	--buffer-time=1000000 --period-time=20000 -d 4 "\$CAP" >>"\$LOG" 2>&1 &
RP=\$!
sleep 1
aplay -q -D default "\$TONE" >>"\$LOG" 2>&1
{
	echo "mixer after playback:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	amixer -c0 cget "name='fr2 Acm8625 AMP mute status'"
	amixer -c0 cget "name='wf Acm8625 AMP mute status'"
	[ -n "\$LOOPBACK_SOURCE" ] && amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1
wait "\$RP"
RP=
restore
RESTORED=1
{
	echo "mixer after restore:"
	amixer -c0 cget "name='Master Playback Volume'"
	amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	amixer -c0 cget "name='fr2 Acm8625 AMP mute status'"
	amixer -c0 cget "name='wf Acm8625 AMP mute status'"
	[ -n "\$LOOPBACK_SOURCE" ] && amixer -c0 cget "name='Loopback tdmin lb source'"
} >>"\$LOG" 2>&1

printf 'capture bytes: '
wc -c <"\$CAP"
printf 'expected bytes: $expected\n'
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
expected=$expected

ssh "$target" "rm -rf '$remote_dir'" </dev/null || true

if [ "$actual" -ne "$expected" ]; then
	echo "warning: expected $expected bytes, received $actual" >&2
fi

cat >"$meta" <<EOF
{
  "board": "orion",
  "test": "safe_1khz_tone",
  "tone_peak_dbfs": $tone_dbfs,
  "hardware_volume_raw": $volume_raw,
  "loopback_tdmin_source": "$loopback_source",
  "capture_device": "$capture_device",
  "format": "S16_LE",
  "channels": $capture_channels,
  "rate_hz": 96000,
  "duration_s": 4,
  "expected_bytes": $expected,
  "actual_bytes": $actual,
  "interleaved": true
}
EOF

echo "raw:  $output ($actual bytes)"
echo "meta: $meta"
