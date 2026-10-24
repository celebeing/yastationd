#!/bin/sh
# orion-led-probe.sh - on-device LED bring-up probe for orion (Yandex Station 3).
# Part of the LED ring bring-up (CFW, docs/boards/orion.md).
#
# Host-side usage (network ssh; there is no scp on the device - stream it):
#   ssh root@<speaker> 'cat > /tmp/orion-led-probe.sh' < tools/orion-led-probe.sh
#   ssh root@<speaker> 'sh /tmp/orion-led-probe.sh'
#
# What it does (~2 min, the ring and ambilight will visibly flash - run this only on
# the unit the user names, when they expect the lights to move):
#   1. Static state: triggers, max_brightness, sync LED semantics (read-only).
#   2. Sysfs ring control test, pixel by pixel: takes the ring off the ledfb trigger
#      (trigger=none) and walks one red pixel ring0 -> ring35 - answers the questions
#      the dump analysis left open:
#        a) does a brightness write show up on the glass without touching :sync
#           (bergamot's logo sync LED is never poked by the CFW and works),
#        b) which physical position lights up for each ring{i} (device order),
#        c) is the walking direction clockwise or counter-clockwise,
#        d) do the three AW21036 chips (ringA/ringB/ringC :sync) map to the
#           i<12 / 12<=i<24 / i>=24 thirds (watch for the step in brightness).
#   3. Colour sanity: one pixel in full red / green / blue (labels vs perceived colour).
#   4. Ambilight scope probe: same walk over the 12 ambilight{i}a positions plus the
#      white channel, to decide if it is worth wiring up later.
#   5. Framebuffer cross-check: writes 1 px to /dev/fb2 (ring) - only if it is not
#      mmap-busy - to confirm the fb mirror of the same pixels.
#
# Reversible: every trigger it changes is restored to the saved value in the trap
# handler, LEDs are blanked at exit. Nothing persistent is touched. The vendor stack
# (if still running) may fight for the LEDs - the ring trigger is on ledfb-ring; if a
# maind animation is running, stop the stack first (tools/orion-stack-stop.sh) or
# expect flicker.

LED=/sys/class/leds
SAVED=$(mktemp)
trap 'sh "$SAVED" 2>/dev/null; rm -f "$SAVED"; exit 0' INT TERM EXIT

save_trigger() { # $1 = led name
	t=$(sed -n 's/.*\[\(.*\)\].*/\1/p' "$LED/$1/trigger" 2>/dev/null)
	# Skip if the trigger read failed: an empty restore line would be a no-op echo into sysfs.
	[ -n "$t" ] && echo "echo $t > $LED/$1/trigger" >>"$SAVED"
}
set_led() { # $1 = led name, $2 = value
	printf '%s' "$2" >"$LED/$1/brightness" 2>/dev/null || echo "write $1 failed"
}
hold() {
	echo "$1"
	sleep "${2:-1}"
}

echo "== 1. static state =="
for d in "$LED"/orion:*; do
	n=${d##*/}
	printf '%-28s max=%s b=%s trig=%s\n' "$n" "$(cat "$d/max_brightness" 2>/dev/null)" \
		"$(cat "$d/brightness" 2>/dev/null)" "$(sed -n 's/.*\[\(.*\)\].*/\1/p' "$d/trigger" 2>/dev/null)"
done | sort
echo "-- sync LEDs before pokes"
for s in A B C; do
	save_trigger "orion:ring${s}:sync"
	echo "ring${s}:sync brightness=$(cat "$LED/orion:ring${s}:sync/brightness")"
done
echo "-- framebuffers"
cat /sys/class/graphics/fb*/name 2>/dev/null
ls -la /dev/fb* 2>/dev/null

echo ""
echo "== 2. sysfs ring walk (watch the ring) =="
echo "Taking all 108 ring colour LEDs off the ledfb trigger (restored at exit)."
for i in $(seq 0 35); do
	for c in red green blue; do
		save_trigger "orion:$c:ring$i"
	done
done
for i in $(seq 0 35); do
	for c in red green blue; do
		printf 'none' >"$LED/orion:$c:ring$i/trigger" 2>/dev/null
	done
done
sleep 1
echo "All off for 2 s:"
for i in $(seq 0 35); do for c in red green blue; do set_led "orion:$c:ring$i" 0; done; done
hold "blank" 2
echo "Now ONE red pixel walks ring0..ring35 (expected one visible dot moving; note direction, pauses of 0.5 s):"
for i in $(seq 0 35); do
	set_led "orion:red:ring$i" 128
	hold "ring$i lit" 1
	set_led "orion:red:ring$i" 0
done
echo "Chip-thirds check: px 0, 12, 24 lit for 3 s each (should look equally bright if the chips share current settings):"
for i in 0 12 24; do
	set_led "orion:red:ring$i" 128
	hold "px $i" 3
	set_led "orion:red:ring$i" 0
done

echo ""
echo "== 3. colour sanity on ring0 =="
for c in red green blue; do
	set_led "orion:$c:ring0" 255
	hold "$c" 2
	set_led "orion:$c:ring0" 0
done
echo "ring0 back to ledfb-ring trigger now; if the vendor stack is stopped the ring stays dark - fine."

echo ""
echo "== 4. ambilight probe (12 positions + white; behind the grille) =="
for i in $(seq 0 11); do
	for c in red green blue white; do
		save_trigger "orion:$c:ambilight${i}a"
		printf 'none' >"$LED/orion:$c:ambilight${i}a/trigger" 2>/dev/null
	done
done
for i in $(seq 0 11); do
	for c in red green blue white; do set_led "orion:$c:ambilight${i}a" 0; done
done
echo "One white pixel walks ambilight0a..ambilight11a; then red px0a vs px0b (the two dies), 2 s each:"
for i in $(seq 0 11); do
	set_led "orion:white:ambilight${i}a" 255
	hold "amb${i}a" 2
	set_led "orion:white:ambilight${i}a" 0
done
set_led "orion:red:ambilight0a" 200
hold "0a red" 2
set_led "orion:red:ambilight0a" 0
set_led "orion:red:ambilight0b" 200
hold "0b red" 2
set_led "orion:red:ambilight0b" 0
echo "a vs b fins: if they are two stacked rows of the 24 dies, note which is outer."

echo ""
echo "== 4b. restore ring triggers (so the fb mirror test below can reach the glass) =="
sh "$SAVED" 2>/dev/null
: >"$SAVED" # restored: nothing left to undo for the ring
echo "ring + ambilight triggers back to their boot values (expected: ring stays dark - the"
echo "stack is stopped and fb2 is all-zero, or a vendor animation takes it over if it runs)."

echo ""
echo "== 5. framebuffer mirror (ring /dev/fb2) =="
echo "Filling fb2 with 36 red pixels if writable (px layout check against sysfs):"
if printf 'AAAAAAAA' >/dev/fb2 2>/dev/null; then
	# 36 px, 24bpp per the ring fb geometry: RGB triplets per px, byte order like bergamot's fb0.
	i=0
	while [ $i -lt 36 ]; do
		printf '\xff\x00\x00' >/dev/fb2
		i=$((i + 1))
	done
	hold "fb2 red written (if the ring went red: the fb mirror drives the same pixels, byte order RGB)" 3
	# Blank fb2 again BEFORE exit: with the trigger restored, leftover fb content would keep
	# re-lighting the ring after the sysfs cleanup below.
	i=0
	while [ $i -lt 36 ]; do
		printf '\x00\x00\x00' >/dev/fb2
		i=$((i + 1))
	done
	echo "fb2 blanked again."
else
	echo "fb2 not writable directly (vendor holds it mmap-busy) - fine, sysfs is our path."
fi

echo ""
echo "== done: restoring triggers, blanking =="
for i in $(seq 0 35); do
	for c in red green blue; do set_led "orion:$c:ring$i" 0; done
done
for i in $(seq 0 11); do
	for c in red green blue white; do set_led "orion:$c:ambilight${i}a" 0; done
done
echo "Report back: (a) did each sysfs write show on the glass with no sync poke, (b) walk direction,"
echo "(c) pixel-number-to-position mapping if you can tell (e.g. 0 = front/top?), (d) brightness of 0/12/24 equal,"
echo "(e) ambilight a-vs-b difference, (f) which ledconfigs colour the case came with (red.json/blue.json on this unit)."
