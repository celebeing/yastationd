#!/bin/sh
# orion-verify.sh - on-device sound-profile verification for orion (Yandex Station 3).
# Part of the orion sound bring-up (yastation_cfw, docs/boards/orion.md).
#
# Host-side usage (USB ADB):
#   adb push tools/orion-verify.sh /data/local/tmp/orion-verify.sh
#   adb shell sh /data/local/tmp/orion-verify.sh        # prefix "su 0 " or "su -c" if adbd is not root
#   adb pull /data/local/tmp/orion-verify orion/verify
#
# What it does (~90 s, two soft beeps from the speaker - volume 90 during the tone
# capture, then 40 for the by-ear test):
#   - dumps board identity, mixer, power-supply, IIO and process state (read-only);
#   - probes the vendor capture devices for a 13ch 96 kHz open (mroute/mcapt/default/hw:0,3);
#   - records 4 s of silence and 4 s while playing a generated 1 s 1 kHz tone
#     (this needs the capture devices free - run tools/orion-stack-stop.sh first,
#     else vendor maind keeps them busy and the capture sections are skipped);
#   - measures arecord CPU at 96 kHz vs 48 kHz (decimation/resample cost);
#   - probes hw:0,2 for reference (the direct-PDM device: ENXIO while the vendor stack
#     holds the loopback, which owns the single PDM slot; opens with the stack stopped).
# Only runtime mixer state is modified, and everything is restored at the end. Nothing
# persistent is written and no vendor daemon is touched by THIS script. Run it while
# the speaker is idle and nobody expects sound from it.

OUT=""
for BASE in /data/local/tmp /tmp /var/tmp /dev/shm /mnt; do
	[ -d "$BASE" ] && [ -w "$BASE" ] && OUT="$BASE/orion-verify" && break
done
[ -n "$OUT" ] || {
	echo "no writable output base found" >&2
	exit 1
}
mkdir -p "$OUT" || {
	echo "cannot create $OUT" >&2
	exit 1
}
LOG="$OUT/log.txt"
: >"$LOG"
log() { echo "$*" | tee -a "$LOG"; }
sect() {
	echo "" | tee -a "$LOG"
	echo "===== $* =====" | tee -a "$LOG"
}

sect "preflight"
for T in amixer arecord aplay awk sed od base64 tr; do
	command -v "$T" >/dev/null 2>&1 && log "ok: $T" || log "MISSING: $T (sections needing it will be skipped/limited)"
done

sect "identity"
{
	id
	echo
	tr '\0' '\n' </proc/device-tree/compatible
	echo
	uname -a
	echo
	cat /proc/asound/cards
	echo
	cat /proc/asound/pcm
} >>"$LOG" 2>&1
cp /proc/bus/input/devices "$OUT/input-devices.txt" 2>>"$LOG"

sect "sound-related processes"
ps w >>"$LOG" 2>&1

getval() { amixer -c0 cget "$1" 2>/dev/null | sed -n 's/^[[:space:]]*: values=//p' | tail -n 1; }
setval() { amixer -c0 cset "$1" "$2" >>"$LOG" 2>&1; }

# A full mixer dump (`amixer contents`) froze a bergamot's kernel in its codec driver
# (docs/guides/diagnostics.md, "Never in diag.sh"). It ran fine on orion during the bring-up, but it is
# opt-in now: FULL_MIXER=1 sh orion-verify.sh.
sect "mixer BEFORE (full)"
if [ "${FULL_MIXER:-0}" = 1 ]; then
	amixer -c0 contents >"$OUT/amixer-before.txt" 2>&1 || log "amixer contents failed (root? card?)"
else
	log "full mixer dump skipped (FULL_MIXER=1 to take it)"
fi

sect "power_supply ya_pd"
PD=/sys/class/power_supply/ya_pd
if [ -d "$PD" ]; then
	for f in "$PD"/*; do
		[ -f "$f" ] || continue
		printf '%s = ' "${f##*/}" >>"$LOG"
		cat "$f" >>"$LOG" 2>&1
		echo >>"$LOG"
	done
else
	log "ya_pd missing; power supplies present:"
	ls /sys/class/power_supply >>"$LOG" 2>&1
fi

sect "iio devices"
for d in /sys/bus/iio/devices/iio:device*; do
	[ -d "$d" ] || continue
	{
		echo "$d  name=$(cat "$d/name" 2>/dev/null)"
		ls "$d"
		echo
	} >>"$LOG" 2>&1
	for f in "$d"/in_intensity*raw "$d"/in_illuminance*raw; do
		[ -f "$f" ] || continue
		printf '%s: ' "${f#$d/}" >>"$LOG"
		i=0
		while [ $i -lt 3 ]; do
			tr '\n' ' ' <"$f" >>"$LOG" 2>/dev/null
			sleep 1
			i=$((i + 1))
		done
		echo >>"$LOG"
	done
done

sect "test tone"
# 1 s of 1 kHz at -11 dBFS, 48 kHz mono S16: a WAV header plus one 48-sample period, repeated
# 1000 times (10 x a 100-period block) - no tone file needs shipping.
base64 -d >"$OUT/tone.wav" <<'B64'
UklGRiR3AQBXQVZFZm10IBAAAAABAAEAgLsAAAB3AQACABAAZGF0YQB3AQA=
B64
base64 -d >"$OUT/period.raw" <<'B64'
AAC1BFYJzg0JEvYVghmfHD4fVCHYIsQjEyTEI9giVCE+H58cghn2FQkSzg1WCbUEAABL+6r2MvL37QrqfuZh48LgrN4o3Tzc7ds83CjdrN7C4GHjfuYK6vbtMvKq9kv7
B64
i=0
while [ $i -lt 100 ]; do cat "$OUT/period.raw"; i=$((i + 1)); done >"$OUT/block.raw"
i=0
while [ $i -lt 10 ]; do cat "$OUT/block.raw"; i=$((i + 1)); done >>"$OUT/tone.wav"
rm -f "$OUT/period.raw" "$OUT/block.raw"
TBYTES=$(wc -c <"$OUT/tone.wav" 2>/dev/null || echo 0)
if [ "$TBYTES" = "96044" ]; then
	log "tone.wav decoded ok ($TBYTES bytes: 1 s, 48 kHz mono S16, 1 kHz at -11 dBFS)"
else
	log "tone decode size mismatch: got $TBYTES want 96044 - file may be unusable"
fi

sect "current mixer values (saved for restore)"
V_FR1=$(getval "name='fr1 Acm8625 AMP mute status'")
V_FR2=$(getval "name='fr2 Acm8625 AMP mute status'")
V_WF=$(getval "name='wf Acm8625 AMP mute status'")
V_MV=$(getval "name='Master Playback Volume'")
log "fr1='$V_FR1' fr2='$V_FR2' wf='$V_WF' vol='$V_MV'"
[ -n "$V_MV" ] || log "WARN: 'Master Playback Volume' not found - see amixer-before.txt"

restore_mixer() {
	[ -n "$V_FR1" ] && setval "name='fr1 Acm8625 AMP mute status'" "$V_FR1"
	[ -n "$V_FR2" ] && setval "name='fr2 Acm8625 AMP mute status'" "$V_FR2"
	[ -n "$V_WF" ] && setval "name='wf Acm8625 AMP mute status'" "$V_WF"
	[ -n "$V_MV" ] && setval "name='Master Playback Volume'" "$V_MV"
	log "mixer restored (fr1=$V_FR1 fr2=$V_FR2 wf=$V_WF vol=$V_MV)"
}

sect "capture device probe (96 kHz)"
CAP=""
CCH=13
for D in mroute mcapt default "hw:0,3"; do
	CH=13
	[ "$D" = "hw:0,3" ] && CH=14
	if arecord -q -D "$D" -c "$CH" -r 96000 -f S16_LE -t raw --buffer-time=1000000 --period-time=20000 -d 1 "$OUT/probe.raw" >>"$LOG" 2>&1; then
		CAP="$D"
		CCH=$CH
		log "capture OK via $D ($CH ch): $(wc -c <"$OUT/probe.raw") bytes"
		break
	else
		log "device $D refused 96 kHz / $CH ch (details in log)"
	fi
done
rm -f "$OUT/probe.raw"
[ -n "$CAP" ] || log "ALL CAPTURE DEVICES FAILED. If the errors say 'Device or resource busy',"
[ -n "$CAP" ] || log "the vendor audio stack still holds them (maind on) - that is a result too."

if [ -n "$CAP" ]; then
	ARR="--buffer-time=1000000 --period-time=20000"

	sect "4 s silence capture via $CAP"
	arecord -q -D "$CAP" -c "$CCH" -r 96000 -f S16_LE -t raw $ARR -d 4 "$OUT/cap-silence.raw" >>"$LOG" 2>&1 &&
		log "cap-silence.raw: $(wc -c <"$OUT/cap-silence.raw" 2>/dev/null || echo 0) bytes" ||
		log "silence capture failed"

	sect "capture while playing tone at volume 90 (second beep)"
	setval "name='fr1 Acm8625 AMP mute status'" 0
	setval "name='fr2 Acm8625 AMP mute status'" 0
	setval "name='wf Acm8625 AMP mute status'" 0
	setval "name='Master Playback Volume'" 90
	arecord -q -D "$CAP" -c "$CCH" -r 96000 -f S16_LE -t raw $ARR -d 4 "$OUT/cap-tone.raw" >>"$LOG" 2>&1 &
	RP=$!
	sleep 2
	aplay -q -D default "$OUT/tone.wav" >>"$LOG" 2>&1 || log "aplay failed"
	wait $RP
	log "cap-tone.raw: $(wc -c <"$OUT/cap-tone.raw" 2>/dev/null || echo 0) bytes"
	restore_mixer

	sect "arecord CPU cost via $CAP (6 s per rate)"
	for R in 96000 48000; do
		arecord -q -D "$CAP" -c "$CCH" -r "$R" -f S16_LE -t raw /dev/null >>"$LOG" 2>&1 &
		P=$!
		sleep 1
		U1=$(awk '{print $14+$15}' /proc/$P/stat 2>/dev/null)
		sleep 5
		U2=$(awk '{print $14+$15}' /proc/$P/stat 2>/dev/null)
		kill $P 2>/dev/null
		wait $P 2>/dev/null
		log "rate $R Hz: $((${U2:-0} - ${U1:-0})) jiffies of 500 (CLK_TCK=$(getconf CLK_TCK 2>/dev/null || echo '?'))"
	done
fi

sect "unmute + first beep (volume 40) - by-ear test"
if [ -n "$V_MV" ]; then
	setval "name='fr1 Acm8625 AMP mute status'" 0
	setval "name='fr2 Acm8625 AMP mute status'" 0
	setval "name='wf Acm8625 AMP mute status'" 0
	setval "name='Master Playback Volume'" 40
	aplay -q -D default "$OUT/tone.wav" >>"$LOG" 2>&1 || log "aplay failed"
	{
		amixer -c0 cget "name='Master Playback Volume'"
		amixer -c0 cget "name='fr1 Acm8625 AMP mute status'"
	} >>"$LOG" 2>&1
	dmesg 2>/dev/null | grep -iE "acm8625|spk_onoff|speaker" | tail -20 >>"$LOG" 2>&1
	restore_mixer
else
	log "skipped: no Master Playback Volume control"
fi

sect "hw:0,2 direct-PDM probe (6 ch 96 kHz; needs the loopback closed and the PDM toddr free - i.e. only with the vendor stack stopped)"
if arecord -q -D hw:0,2 -c 6 -r 96000 -f S16_LE -t raw -d 1 "$OUT/cap-pdm.raw" >>"$LOG" 2>&1; then
	log "hw:0,2 opened: $(wc -c <"$OUT/cap-pdm.raw") bytes (direct PDM copy - carries DC and a hotter floor than the loopback; reference only)"
else
	log "hw:0,2 6ch/96k failed (with the stack running this is the expected ENXIO - the loopback owns the PDM)"
	rm -f "$OUT/cap-pdm.raw"
fi

sect "per-channel RMS (best-effort; pulled raws are the source of truth)"
log "13ch frame map: idx 0-5 = loopback ch 0,2,3,4,5,6 (mics expected),"
log "                idx 6-10 = loopback ch 8,9,11,12,13 (speaker refs), idx 11-12 = ultrasound ch 0,1"
ODFLAGS=""
if [ -f "$OUT/cap-silence.raw" ]; then
	if od -An -v -t d2 -N 64 "$OUT/cap-silence.raw" >/dev/null 2>&1; then
		ODFLAGS="-An -v -t d2"
	elif od -An -v -d -N 64 "$OUT/cap-silence.raw" >/dev/null 2>&1; then
		ODFLAGS="-An -v -d"
	fi
fi
if [ -n "$ODFLAGS" ]; then
	for F in cap-silence cap-tone cap-pdm; do
		[ -f "$OUT/$F.raw" ] || continue
		CH=13
		[ "$F" = "cap-pdm" ] && CH=6
		od $ODFLAGS "$OUT/$F.raw" 2>/dev/null | awk -v chs="$CH" -v tag="$F" '
			{ for (i = 1; i <= NF; i++) { k = n++ % chs; s[k] += $i * $i; c[k]++ } }
			END { printf "%s msq (mean-square; sqrt it on the host for RMS):", tag;
			      for (j = 0; j < chs; j++) if (c[j]) printf " ch%d=%d", j, int(s[j] / c[j]);
			      printf "\n" }' | tee -a "$LOG"
	done
else
	log "od cannot dump S16 here - RMS will be computed on the host after pull"
fi

sect "mixer AFTER + dmesg"
[ "${FULL_MIXER:-0}" = 1 ] && amixer -c0 contents >"$OUT/amixer-after.txt" 2>&1
dmesg 2>/dev/null | grep -iE "acm8625|pcm|loopback|pdm" | tail -30 >>"$LOG" 2>&1

sect "done"
{
	echo "files in $OUT:"
	ls -la "$OUT"
	du -sk "$OUT"
} | tee -a "$LOG"
log "pull this folder with: adb pull $OUT"
