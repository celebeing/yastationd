#!/bin/sh
# board-survey: read-only hardware inventory of a Yandex Station, to write its yastationd board
# profile (yastationd/internal/board/profiles/<codename>.conf) and docs/boards/<codename>.md.
# Busybox-only, changes nothing. Output contains per-device identifiers (MACs, serials) — keep it
# under the gitignored dumps/, never commit it.
#
#   ssh root@<speaker> 'sh -s' < tools/board-survey.sh > dumps/survey/<codename>-$(date +%F).txt
#   ssh root@<speaker> 'cd /system/vendor/quasar && tar cf - ledconfigs ledpatterns' > dumps/survey/<codename>-leds.tar

sec() { printf '\n===== %s =====\n' "$*"; }
run() { printf '\n$ %s\n' "$*"; sh -c "$*" 2>&1; }

sec identity
run 'tr "\0" " " < /proc/device-tree/model; echo'
run 'tr "\0" " " < /proc/device-tree/compatible; echo'
run 'cat /etc/hostname; uname -a'
run 'cat /system/vendor/quasar/VERSION 2>/dev/null'
run 'grep -o "\"deviceType\"[^,]*" /system/vendor/quasar/quasar.cfg'
run 'cat /proc/cpuinfo | grep -E "processor|model name|Hardware|CPU part" | sort | uniq -c'
run 'free; df -h; mount'

sec leds
for d in /sys/class/leds/*; do
	printf '%s max=%s b=%s trig=%s\n' "${d##*/}" "$(cat "$d/max_brightness" 2>/dev/null)" \
		"$(cat "$d/brightness" 2>/dev/null)" "$(sed -n 's/.*\[\(.*\)\].*/\1/p' "$d/trigger" 2>/dev/null)"
done
run 'ls /sys/class/leds | sed "s/[0-9]*$//" | sort | uniq -c'
run 'ls -la /dev/fb* /sys/class/graphics 2>/dev/null; cat /sys/class/graphics/fb*/virtual_size /sys/class/graphics/fb*/name 2>/dev/null'
run 'ls /system/vendor/quasar/ledconfigs /system/vendor/quasar/ledpatterns 2>/dev/null'

sec input
run 'cat /proc/bus/input/devices'

sec audio
run 'cat /proc/asound/cards /proc/asound/pcm'
run 'for f in /etc/asound*.conf /etc/alsa/conf.d/*; do echo "--- $f"; cat "$f"; done'
# Never `amixer contents` / `controls` / `scontents` (or a loop reading every control): dumping all
# mixer controls froze a bergamot's kernel in its codec driver (docs/guides/diagnostics.md, "Never in
# diag.sh"). The cards' own /proc files are safe; read the named controls you need by hand.
for c in /proc/asound/card[0-9]*; do run "ls $c; cat $c/id"; done
run 'cat /etc/hardvold.config 2>/dev/null'
run 'for f in /etc/init.d/S4*alsa* /etc/init.d/S4*audio* /etc/init.d/S4*hardvol* /etc/init.d/S9*; do echo "--- $f"; cat "$f"; done'
run 'ls -la /tmp/amp_* 2>/dev/null; grep -l . /sys/bus/i2c/devices/*/name 2>/dev/null | while read n; do echo "$n: $(cat $n)"; done'

sec sensors
for d in /sys/bus/iio/devices/iio:device*; do
	echo "--- $d $(cat "$d/name")"
	for f in "$d"/in_*; do [ -f "$f" ] && echo "${f##*/}=$(cat "$f" 2>/dev/null)"; done
done

sec radios-and-buses
run 'ls -la /sys/class/tty/ | grep -v -E "tty[0-9]+ |ptyp|ttyp"'
run 'for t in /sys/class/tty/tty[A-Z]*; do [ -e "$t/device/driver" ] && echo "${t##*/} -> $(readlink "$t/device/driver")"; done'
run 'ls /sys/class/net; ls /sys/class/bluetooth 2>/dev/null'
run 'lsmod'
run 'ls /dev | grep -i -E "spi|i2c|ttyS|ttyAML|zigbee|thread|802154|hci"'

sec vendor-voice
run 'ls -la /system/vendor/quasar/ /system/vendor/quasar/libs'
run 'ls -laR /system/vendor/quasar/spotter_models /system/vendor/quasar/vqe_models 2>/dev/null | head -100'
run 'for d in /data/quasar/data/spotter_model/*/; do echo "--- $d"; ls "$d"; grep -h -E "^--(thresholds|subthresholds|sample-rate|model-version|words)" "$d"/flags.txt 2>/dev/null; done'
run 'sed -n "/\"audiod\"/,/^   },/p" /system/vendor/quasar/quasar.cfg'
run 'sed -n "/\"leds\"/,/^   },/p" /system/vendor/quasar/quasar.cfg'
run 'strings -a /system/vendor/quasar/libs/libyandex_evil.so 2>/dev/null | grep -i -E "preset|bergamot|cucumber|lemon|plum|mini|pro" | sort -u | head -80'

sec init
run 'ls -la /etc/init.d'
run 'ps w'
run 'dmesg | tail -200'
