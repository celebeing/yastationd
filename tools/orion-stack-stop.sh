#!/bin/sh
# orion-stack-stop.sh - test-only stop of the Yandex quasar stack on orion (Yandex
# Station 3). Part of the orion sound bring-up (yastation_cfw, docs/boards/orion.md).
#
# Why: maind's audiod holds every ALSA capture device (mroute/mcapt/hw:0,3), blocking
# the mic half of tools/orion-verify.sh. The playback half is already verified on the
# live stack; capture needs the stack down, which is also the intended end state
# (maind_off conversion), so this run doubles as a rehearsal.
#
# What dies (matched by command line, not by name - busybox killall matches the comm
# and would miss begemot, whose comm is renamed to NehTFunc, and rootkeeper.sh, whose
# comm is 'sh'):
#   - quasar_launcher2  - the supervisor; killed first so it cannot respawn maind
#   - rootkeeper.sh     - process running from /data/quasar/rootkeeper.sh. Its job is
#                         NOT confirmed: it may be a vendor watchdog that restarts the
#                         stack, or a keeper installed by other tooling (the path is the
#                         same either way). Killed here because it may restart the stack;
#                         after this run, check whether anything came back on its own.
#   - maind (all 5 facets, audiod inside platformd), begemot, bulbik
# What stays: hardvold (Master->ACM volume mapping, needed by our own beep test),
# bluealsa/bluetoothd, wpa_supplicant/dhcpcd (wifi), adbd (this shell).
#
# Fully reversible and non-persistent: no init.d files, flags or configs are touched;
# a plain reboot restores the full vendor stack. USB-ADB is independent of the quasar
# processes, so there is no lockout path.
#
# Intended flow:
#   adb push tools/orion-stack-stop.sh /data/local/tmp/orion-stack-stop.sh
#   adb shell sh /data/local/tmp/orion-stack-stop.sh
#   adb shell sh /data/local/tmp/orion-verify.sh
#   adb pull /data/local/tmp/orion-verify orion/verify2
#   adb shell reboot          # when you want the vendor stack back

echo "== before =="
ps w | grep -E 'quasar|maind|begemot|bulbik|hardvold' | grep -v grep

# Pass 1: polite TERM for everything 'quasar' (launcher first by pid order, so maind
# has no supervisor left to respawn it while we are still killing).
for p in $(ps w | awk '/quasar/ && !/awk|grep/ {print $1}'); do
	kill "$p" 2>/dev/null && echo "TERM $p"
done
sleep 5

# Pass 2: KILL survivors and anything that came back in the meantime (from any source).
for p in $(ps w | awk '/quasar/ && !/awk|grep/ {print $1}'); do
	kill -9 "$p" 2>/dev/null && echo "KILL $p"
done

echo "== after (empty below = capture devices free) =="
ps w | grep -E 'quasar|maind|begemot|bulbik' | grep -v grep
echo "hardvold (must still be running):"
ps w | grep hardvold | grep -v grep
echo ""
echo "Next: sh /data/local/tmp/orion-verify.sh  (expect two beeps: vol 90, then 40)"
echo "Restore the vendor stack anytime with: adb shell reboot"
