#!/bin/sh
# orion-mic-recheck.sh - pure-capture mic retest for orion (Yandex Station 3).
# Created after a 2026-10-24 single 4 s clip showed mic idx 5 at RMS 3061 vs
# 33-57 on its neighbors. The initial run of this script cleared it: two 30 s
# captures 150 s apart put idx 5 at 1.0x its peers, so the spike was transient
# (local acoustic/electrical event or transport glitch), not a dead capsule.
# Keep the script for repeating any suspicious channel capture. Verdict is
# host-side (numpy FFT) after the pull.
#
# Host-side usage (USB ADB), run from the repo root:
#   adb push tools/orion-mic-recheck.sh /data/local/tmp/orion-mic-recheck.sh
#   adb shell sh /data/local/tmp/orion-mic-recheck.sh
#   adb pull /data/local/tmp/orion-mic-recheck dumps/orion/orion/mic-recheck
#
# PURE CAPTURE: plays nothing, writes no mixer control, opens no PDM device.
# Needs the capture devices free - vendor stack stopped (tools/orion-stack-stop.sh)
# and no CFW voice session/wake listener holding hw:0,3 - else devices answer
# "busy" and the script says so instead of guessing.
#
# Runtime ~4 min: 30 s capture A, 150 s pause, 30 s capture B. Keep the room
# quiet and the mic-mute switch OFF (mics live). The speaker emits nothing.
# Each capture is 74,880,000 bytes (13ch x 96 kHz x S16 x 30 s); ~150 MB total
# to pull. On-device RMS is not possible (this fw's busybox od cannot dump S16,
# see the verify2 log) - host-side analysis after the pull is the truth.

OUT=""
for BASE in /data/local/tmp /tmp /var/tmp /dev/shm /mnt; do
	[ -d "$BASE" ] && [ -w "$BASE" ] && OUT="$BASE/orion-mic-recheck" && break
done
[ -n "$OUT" ] || {
	echo "orion-mic-recheck: no writable output dir" >&2
	exit 1
}
rm -rf "$OUT"
mkdir -p "$OUT" || {
	echo "orion-mic-recheck: cannot create $OUT" >&2
	exit 1
}
LOG="$OUT/log.txt"
: >"$LOG"
log() { echo "$*" | tee -a "$LOG"; }
sect() { echo "===== $* =====" | tee -a "$LOG"; }

DUR=30
GAP=150
EXPECT=$((DUR * 96000 * 13 * 2))

sect "preflight"
log "date: $(date)"
log "uptime: $(cut -d' ' -f1 /proc/uptime 2>/dev/null)"
for T in arecord amixer dmesg; do
	command -v "$T" >/dev/null 2>&1 && log "ok: $T" || log "MISSING: $T"
done
log "mic-mute switch OFF (mics live), room quiet, speaker untouched"

sect "capture-relevant processes (vendor stack must be down)"
ps w >>"$LOG" 2>&1

getval() { amixer -c0 cget "$1" 2>/dev/null | sed -n 's/^[[:space:]]*: values=//p' | tail -n 1; }
sect "mixer context (read-only)"
log "Loopback datain source = $(getval "name='Loopback datain source'")"
log "Loopback PDM Gain      = $(getval "name='Loopback PDM Gain'")"

sect "capture device probe (13ch 96 kHz, 1 s)"
CAP=""
for D in mroute mcapt default; do
	if arecord -q -D "$D" -c 13 -r 96000 -f S16_LE -t raw -d 1 "$OUT/probe.raw" >>"$LOG" 2>&1; then
		CAP="$D"
		log "capture device: $CAP"
		break
	else
		log "probe $D failed"
	fi
done
rm -f "$OUT/probe.raw"
if [ -z "$CAP" ]; then
	log "NO CAPTURE DEVICE AVAILABLE. If the errors say 'Device or resource busy',"
	log "the vendor stack (or a CFW wake listener) holds the loopback - run"
	log "tools/orion-stack-stop.sh first and retry."
	exit 1
fi

capture_one() {
	L="$1"
	F="$OUT/cap-silence-$L.raw"
	sect "capture $L (${DUR}s silence via $CAP)"
	if arecord -q -D "$CAP" -c 13 -r 96000 -f S16_LE -t raw -d "$DUR" "$F" >>"$LOG" 2>&1; then
		B=$(wc -c <"$F")
		log "bytes: $B (expected $EXPECT)"
		[ "$B" = "$EXPECT" ] || log "WARN: size mismatch - truncated/overrun; keep the file, host analysis must slice safely"
	else
		log "capture $L FAILED (see log above)"
		return 1
	fi
	log "dmesg tail:"
	dmesg 2>/dev/null | grep -iE "pcm|loopback|pdm|xrun|overrun" | tail -10 >>"$LOG" 2>&1
	return 0
}

capture_one A || exit 1
log "waiting $GAP s - keep the room quiet; the gap spans another minute of appliance cycles"
sleep $GAP
capture_one B || exit 1

sect "done"
ls -l "$OUT" >>"$LOG" 2>&1
log "pull with: adb pull $OUT <host-dir> (e.g. dumps/orion/orion/mic-recheck)"
log "verdict: host-side per-channel RMS/FFT on both captures (numpy); compare A vs B"
