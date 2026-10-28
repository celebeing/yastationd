#!/bin/sh
# exit 0 if hw:0,1 capture is free (no arecord, daemon doesn't hold pcmC0D1c)
ps w | grep -v grep | grep -q arecord && { echo "busy: arecord running"; ps w | grep -v grep | grep arecord; exit 1; }
# The daemon, wherever it runs from (<cfw>/bin/, or a test copy in /tmp/): argv[0] ends in /yastationd.
for p in $(ps w | awk '$3 ~ /\/yastationd$/ {print $1}'); do
  ls -l /proc/$p/fd 2>/dev/null | grep -q pcmC0D1c && { echo "busy: yastationd pid $p holds pcmC0D1c"; exit 1; }
done
[ "$(cat /proc/asound/card0/pcm1c/sub0/status)" = closed ] || { echo "busy: pcm1c open by someone"; exit 1; }
echo free
