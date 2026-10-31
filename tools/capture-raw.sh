#!/bin/sh
# capture-raw.sh - pull an interleaved raw mic-array capture over SSH.
#
# Usage:
#   tools/capture-raw.sh <ssh-target> <board> <seconds> <output.raw>
#
# Examples:
#   tools/capture-raw.sh root@<speaker-ip> bergamot 10 captures/bergamot.raw
#   tools/capture-raw.sh root@<speaker-ip> pickle 10 captures/pickle.raw
#   tools/capture-raw.sh root@<speaker-ip> orion 10 captures/orion.raw
#
# The output is S16_LE PCM in the board's native interleaved channel layout.
# A JSON metadata file is written beside it. This script plays nothing and does
# not change mixer state.

set -eu

usage() {
	echo "usage: $0 <ssh-target> <bergamot|pickle|orion> <seconds> <output.raw>" >&2
	exit 2
}

[ "$#" -eq 4 ] || usage

target=$1
board=$2
duration=$3
output=$4

case "$duration" in
'' | *[!0-9]*)
	echo "duration must be a positive integer number of seconds" >&2
	exit 2
	;;
esac
[ "$duration" -gt 0 ] || {
	echo "duration must be greater than zero" >&2
	exit 2
}

case "$board" in
bergamot)
	# 8-channel S16_LE, 48 kHz: hw:0,1.
	device=hw:0,1
	channels=8
	rate=48000
	;;
pickle)
	# 4-channel S16_LE, 48 kHz: hw:0,2.
	device=hw:0,2
	channels=4
	rate=48000
	;;
orion)
	# 13-channel S16_LE, 96 kHz: the stock mroute capture path.
	device=mroute
	channels=13
	rate=96000
	;;
*)
	echo "unsupported board: $board" >&2
	exit 2
	;;
esac

case "$output" in
*.raw) meta=${output%.raw}.json ;;
*) meta=$output.json ;;
esac

mkdir -p "$(dirname "$output")"
umask 077

expected_bytes=$((duration * rate * channels * 2))

echo "target:   $target"
echo "board:    $board"
echo "device:   $device"
echo "channels: $channels"
echo "rate:     $rate Hz"
echo "duration: $duration s"
echo "output:   $output"
echo "expected: $expected_bytes bytes"
echo
echo "Starting capture. Remote arecord diagnostics are printed below."
echo "Press Ctrl-C to stop early."

# The remote command is assembled only from fixed board values and validated
# numeric arguments. PCM goes to stdout; arecord diagnostics remain on stderr.
ssh "$target" "exec arecord -q \\
	-D '$device' \\
	-c '$channels' \\
	-r '$rate' \\
	-f S16_LE \\
	-t raw \\
	--buffer-time=1000000 \\
	--period-time=20000 \\
	-d '$duration' \\
	-" >"$output"

actual_bytes=$(wc -c <"$output" | tr -d '[:space:]')
if [ "$actual_bytes" -ne "$expected_bytes" ]; then
	echo "warning: byte count differs from expected" >&2
	echo "  expected: $expected_bytes" >&2
	echo "  received: $actual_bytes" >&2
fi

cat >"$meta" <<EOF
{
  "board": "$board",
  "device": "$device",
  "format": "S16_LE",
  "channels": $channels,
  "rate_hz": $rate,
  "duration_s": $duration,
  "expected_bytes": $expected_bytes,
  "actual_bytes": $actual_bytes,
  "interleaved": true
}
EOF

echo
echo "raw:  $output ($actual_bytes bytes)"
echo "meta: $meta"
