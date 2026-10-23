#!/bin/sh
# On-device live wake/command test: hw:0,1 -> vendor VQE -> «Алиса» + command spotters.
# Copy next to spotter-host in /tmp/spotter/ and run:  sh /tmp/spotter/listen.sh [SECONDS] [extra listen flags]
#   extra flags: -b (BNR input <- vqe_0), -x 0,0,1 (skip VQE), -g (x50 gain into the command spotter)
# Output: "<HH:MM:SS> <stream_s> WAKE|CMD <EVENT> [phrase]" and a LEVEL line per second.
# Refuses to start if hw:0,1 is busy (yastationd owns the mic when it captures). Ctrl-C to stop.
D=${1:-600}; [ $# -gt 0 ] && shift
H=$(dirname "$0")
M=/data/quasar/data/spotter_model
"$H/hwfree.sh" >/dev/null || { "$H/hwfree.sh"; exit 1; }
export LD_LIBRARY_PATH=/system/vendor/quasar/libs
arecord -q -D hw:0,1 -c 8 -r 48000 -f S16_LE -t raw -d "$D" --buffer-time=1000000 --period-time=20000 |
  "$H/spotter-host" listen -v "$@" \
    -a $M/8cf52356-a4f7-4ef5-9100-e95067abdc0a \
    -k $M/a23e7ada-6e9e-4739-87a2-c31e04f088ea 2>/dev/null |
  while read -r l; do echo "$(date +%T) $l"; done
