#!/bin/sh
# diag.sh — collect everything needed to debug the CFW on this speaker into one archive.
# Runs ON the speaker as root (busybox ash). Installed as <cfw>/bin/diag.sh by tools/deploy.sh;
# also runs from a checkout without installing anything:
#
#   ssh root@<speaker> 'sh -s' < yastationd/scripts/diag.sh                 # archive left in /tmp
#   ssh root@<speaker> 'sh -s -- -' < yastationd/scripts/diag.sh > diag.tar.gz
#   <cfw>/bin/diag.sh [-r] [-x] [-o DIR] [-]
#
#   -o DIR   write diag-<device_id>-<YYYYmmdd-HHMMSS>.tar.gz into DIR (default /tmp); its path is
#            the last line on stdout
#   -        write the tar.gz to stdout instead (progress goes to stderr)
#   -r       raw: no redaction (for your own use; never attach a raw archive to a public issue)
#   -x       also take live readings from drivers: the IIO sensors' raw values, every LED's
#            brightness and trigger, the USB-PD power_supply uevent. Each is read by the daemon
#            or was read safely by hand, but not all of them together; off by default, for a
#            unit you can power-cycle.
#
# Never added here, whatever the flag (2026-10-28, see docs/diagnostics.md):
#   - `amixer contents` / `controls` / `scontents`, or any loop over every mixer control: reading
#     every control froze bergamot's kernel in the codec driver (after the SPDIF "Channel A
#     Status" control); the unit needed a power cycle. ALSA facts come from /proc/asound only.
#   - `wpa_cli signal_poll` (and `iw`) and /proc/net/wireless: they reach cfg80211 calls the
#     vendor Wi-Fi driver does not support; all were in the run that froze bergamot
#     (docs/boards/bergamot.md). `wpa_cli status` / `list_networks` are wpa_supplicant's own
#     state and stay.
#   - efuse and pcba_ids reads (serials, and not proven harmless).
#
# Strictly read-only: it reads files, runs status commands and asks the daemon over local HTTP;
# it writes only its work directory in /tmp and the archive. It never remounts, never changes the
# config, Wi-Fi, the egress lock or dropbear, never starts vendor binaries, never makes a sound or
# drives an LED. Every command is time-boxed and its output capped; a failing one is noted and
# the run goes on. docs/diagnostics.md has what is inside and what redaction does.
#
# Redaction (default; the archive is meant to be sent to someone else, maybe a public issue):
# secrets (passwords, password hashes, PSKs, tokens, intercom/MQTT credentials), Wi-Fi SSIDs, the
# hostnames named in the config, MAC addresses (the vendor half is kept), serial numbers, the
# speaker's hostname, global IPv6 addresses, spoken text and replies in the log, moment names.
# LAN IPv4 addresses stay (needed for debugging). Never collected, even with -r: web.secret,
# the SSH host keys, Wi-Fi PSKs, audio (wakes/ is listed, not copied).

PATH=/usr/sbin:/sbin:/usr/bin:/bin:$PATH
export PATH LC_ALL=C

OUTDIR=/tmp
STDOUT=0
RAW=0
HW=0
while [ $# -gt 0 ]; do
	case $1 in
	-o) [ $# -ge 2 ] || { echo "diag.sh: -o needs a directory" >&2; exit 2; }; OUTDIR=$2; shift ;;
	-r) RAW=1 ;;
	-x) HW=1 ;;
	-) STDOUT=1 ;;
	-h | --help) sed -n '2,40p' "$0" 2>/dev/null || echo "usage: diag.sh [-r] [-x] [-o DIR] [-]"; exit 0 ;;
	*) echo "diag.sh: unknown argument '$1' (usage: diag.sh [-r] [-x] [-o DIR] [-])" >&2; exit 2 ;;
	esac
	shift
done
[ "$STDOUT" = 1 ] || [ -d "$OUTDIR" ] || { echo "diag.sh: no directory $OUTDIR" >&2; exit 2; }

# The CFW root, found the way the boot hook and the supervisor find it.
# YASTATIOND_CFW / YASTATIOND_LOG override them for host dry runs, as in supervise.sh.
CFW=${YASTATIOND_CFW:-/bkupgrade/cfw}; [ -d "$CFW" ] || CFW=/data/cfw
CONF=$CFW/yastationd.conf
LOG=${YASTATIOND_LOG:-/tmp/yastationd.log}
Q=/system/vendor/quasar
T0=$(cut -d. -f1 /proc/uptime)

say() { echo "diag: $*" >&2; }

# tmo SECS CMD...: CMD with a time limit. It runs in its own session, so the whole process group
# (a pipeline, wpa_cli waiting on a socket) is killed when time is up.
tmo() {
	_t=$1
	shift
	setsid "$@" &
	_p=$!
	(sleep "$_t"; kill -TERM -"$_p" 2>/dev/null; sleep 1; kill -KILL -"$_p" 2>/dev/null) >/dev/null 2>&1 &
	_w=$!
	wait "$_p"
	_r=$?
	kill "$_w" 2>/dev/null
	return $_r
}

# conf_get KEY [FILE]: a key's value from a key = value file (the first one).
conf_get() {
	[ -f "${2:-$CONF}" ] || return 0
	awk -F= -v k="$1" '{x=$1; gsub(/^[ \t]+|[ \t]+$/,"",x)} x==k {v=substr($0,index($0,"=")+1); gsub(/^[ \t]+|[ \t]+$/,"",v); print v; exit}' "${2:-$CONF}"
}

DEVID=$(conf_get device_id | tr -c 'A-Za-z0-9_.\n-' '_' | cut -c1-40)
[ -n "$DEVID" ] || DEVID=$(hostname 2>/dev/null | tr -c 'A-Za-z0-9_.\n-' '_')
[ -n "$DEVID" ] || DEVID=speaker
STAMP=$(date +%Y%m%d-%H%M%S)
NAME=diag-$DEVID-$STAMP
WORK=$(mktemp -d /tmp/diag.XXXXXX) || { echo "diag.sh: mktemp failed" >&2; exit 1; }
D=$WORK/$NAME
mkdir -p "$D"
trap 'rm -rf "$WORK"' EXIT
trap 'exit 130' INT TERM HUP

# Output helpers. sec FILE starts appending to $D/FILE; run "CMD" [SECS] [LINES] appends the
# command line and its output (stderr too), time-boxed (5 s) and capped (400 lines); show FILE
# [LINES] appends a file's last lines; note TEXT appends a line.
sec() { F=$D/$1; : >>"$F"; }
note() { printf '%s\n' "$*" >>"$F"; }
run() {
	printf '\n$ %s\n' "$1" >>"$F"
	tmo "${2:-5}" sh -c "$1" 2>&1 </dev/null | head -n "${3:-400}" >>"$F"
}
show() {
	if [ -f "$1" ]; then
		printf '\n== %s (%s bytes, last %s lines)\n' "$1" "$(wc -c <"$1" 2>/dev/null)" "${2:-400}" >>"$F"
		tail -n "${2:-400}" "$1" 2>&1 | cut -c1-2000 >>"$F"
	else
		printf '\n== %s: absent\n' "$1" >>"$F"
	fi
}
# binfo PATH: size, date and sha256 of a binary (never its content).
binfo() {
	if [ -e "$1" ]; then
		note "$(ls -la "$1" 2>&1)"
		[ -f "$1" ] && note "  sha256 $(sha256sum "$1" 2>/dev/null | cut -d' ' -f1)"
	else
		note "$1: absent"
	fi
}

say "collecting into $D (cfw $CFW)"

# ---------------------------------------------------------------- versions
sec versions.txt
note "CFW root: $CFW"
run "$CFW/bin/yastationd -version"
run "cat $Q/version_full $Q/VERSION"
run "cat /etc/os-release" 5 30
run "uname -a"
run "cat /proc/device-tree/model; echo; tr '\\0' ' ' </proc/device-tree/compatible; echo"
run "cat /proc/cmdline"
run "ldd --version 2>&1 | head -1; ls /lib/libc-* /lib/ld-* 2>/dev/null"
run "file $Q/maind 2>/dev/null || od -An -tx1 -N5 $Q/maind"
note ""
note "== binaries in $CFW/bin"
for b in "$CFW"/bin/*; do binfo "$b"; done
run "$CFW/bin/dropbearmulti dropbear -V"
run "$CFW/bin/hostapd -v"
note ""
note "== the boot hook and the supervisor"
binfo /etc/init.d/S52zyastationd
binfo "$CFW/init.d/S52zyastationd"
binfo "$CFW/init.d/supervise.sh"
binfo "$CFW/egress.sh"

# ---------------------------------------------------------------- system
sec system.txt
run "date; date -u"
run "cat /proc/uptime; uptime"
run "cat /proc/loadavg"
run "cat /proc/meminfo" 5 60
run "free"
run "cat /proc/cpuinfo" 5 80
run "for c in /sys/devices/system/cpu/cpu[0-9]*; do echo \"\${c##*/}: cur=\$(cat \$c/cpufreq/scaling_cur_freq 2>/dev/null) max=\$(cat \$c/cpufreq/scaling_max_freq 2>/dev/null) gov=\$(cat \$c/cpufreq/scaling_governor 2>/dev/null)\"; done"
run "for z in /sys/class/thermal/thermal_zone*; do echo \"\${z##*/} \$(cat \$z/type 2>/dev/null) \$(cat \$z/temp 2>/dev/null)\"; done; for c in /sys/class/thermal/cooling_device*; do echo \"\${c##*/} \$(cat \$c/type 2>/dev/null) cur=\$(cat \$c/cur_state 2>/dev/null)/\$(cat \$c/max_state 2>/dev/null)\"; done"
run "df -k" 8
run "cat /proc/mounts"
run "cat /proc/mtd"
run "ubinfo -a" 5 120
run "for u in /sys/class/ubi/ubi[0-9]*; do [ -f \$u/max_ec ] && echo \"\${u##*/} max_ec=\$(cat \$u/max_ec) bad=\$(cat \$u/bad_peb_count 2>/dev/null) avail_ebs=\$(cat \$u/avail_eraseblocks 2>/dev/null) total_ebs=\$(cat \$u/total_eraseblocks 2>/dev/null)\"; done"
run "for m in /sys/class/mmc_host/mmc*/mmc*; do [ -f \$m/life_time ] && echo \"\${m##*/} life_time=\$(cat \$m/life_time) pre_eol=\$(cat \$m/pre_eol_info 2>/dev/null)\"; done"
run "cat /sys/class/aml_info/reboot_reason"
run "ls -la /sys/fs/pstore"
run "lsmod" 5 80
run "ipcs"
run "ulimit -a"

# ---------------------------------------------------------------- processes
sec processes.txt
run "ps w" 5 300
run "top -b -n 1" 8 60
for p in "$CFW/bin/yastationd" "$CFW/bin/spotter-host" "$CFW/bin/sendspin-host" "$CFW/bin/hostapd" "$CFW/bin/dropbearmulti"; do
	for pid in $(ps w | awk -v b="$p" '$3 == b {print $1}'); do
		note ""
		note "== $p pid $pid: $(grep -E '^(State|VmRSS|VmSize|Threads)' /proc/$pid/status 2>/dev/null | tr -s ' \t' ' ' | tr '\n' ';') fds=$(ls /proc/$pid/fd 2>/dev/null | wc -l)"
	done
done
run "ps w | grep -E 'maind|quasar_launcher|quasar_client|metrics_collector|updater' | grep -v grep || echo 'no vendor stack processes'"
run "cat /tmp/yastationd-sup.pid; echo; kill -0 \$(cat /tmp/yastationd-sup.pid) && echo supervisor alive"

# ---------------------------------------------------------------- the CFW tree
sec cfw.txt
run "ls -la $CFW"
run "ls -la $CFW/bin $CFW/init.d"
note ""
note "== flags and markers"
for f in maind_off yastationd_off config.trial sync.pending sync.rejected netwatch.count log-spool moments.json board.conf web.secret; do
	if [ -e "$CFW/$f" ]; then note "$f: present ($(wc -c <"$CFW/$f" 2>/dev/null) bytes, $(date -r "$CFW/$f" '+%F %T' 2>/dev/null))"; else note "$f: absent"; fi
done
run "ls -la $CFW/*.bak* $CFW/*.pre-* $CFW/*.prev $CFW/bin/*.prev $CFW/bin/*.bad $CFW/bin/*.upload 2>/dev/null"
run "find $CFW/vendor $CFW/sendspin $CFW/iptables -maxdepth 3 2>/dev/null | head -150; du -sk $CFW/vendor $CFW/iptables $CFW/sounds $CFW/clips $CFW/scenarios $CFW/media 2>/dev/null"
run "ls $CFW/sounds | wc -l; ls $CFW/clips 2>/dev/null | wc -l"
if [ -d "$CFW/wakes" ]; then
	run "echo \"files: \$(ls $CFW/wakes | wc -l), \$(du -sk $CFW/wakes | cut -f1) KB\"; ls -la $CFW/wakes | tail -40"
else
	note ""
	note "wakes/: absent (wake_capture off)"
fi
run "du -sk $CFW; df -k $CFW"

# ---------------------------------------------------------------- config and state
# The config is redacted key by key below (and once more by the generic pass at the end).
sec config.txt
redact_conf() {
	if [ "$RAW" = 1 ]; then cat "$1"; return; fi
	awk -F= '
		/^[ \t]*#/ || index($0, "=") == 0 { print; next }
		{
			k = $1; gsub(/^[ \t]+|[ \t]+$/, "", k)
			v = substr($0, index($0, "=") + 1); gsub(/^[ \t]+|[ \t]+$/, "", v)
			if (tolower(k) ~ /(pass|secret|psk|token|key|user)/ && v != "") print k " = <redacted>"
			else print
		}' "$1"
}
for f in "$CONF" "$CFW/board.conf" "$CFW/sync.pending" "$CFW/sync.rejected"; do
	note ""
	if [ -f "$f" ]; then
		note "== $f ($(wc -c <"$f") bytes, $(date -r "$f" '+%F %T'))"
		redact_conf "$f" | head -n 400 >>"$F"
	else
		note "== $f: absent"
	fi
done
if [ -f "$CONF.bak" ] && [ -f "$CONF" ]; then
	note ""
	note "== the last config change ($CONF.bak -> $CONF)"
	redact_conf "$CONF.bak" >"$WORK/old.conf"
	redact_conf "$CONF" >"$WORK/new.conf"
	diff -u "$WORK/old.conf" "$WORK/new.conf" 2>&1 | head -n 120 >>"$F"
	rm -f "$WORK/old.conf" "$WORK/new.conf"
fi

sec state.txt
show "$CFW/yastationd.state" 50
if [ -f "$CFW/moments.json" ]; then
	note ""
	note "== $CFW/moments.json ($(wc -c <"$CFW/moments.json") bytes)"
	if [ "$RAW" = 1 ]; then head -c 20000 "$CFW/moments.json" >>"$F"; else
		head -c 20000 "$CFW/moments.json" | sed -E 's/("name"[[:space:]]*:[[:space:]]*)"[^"]*"/\1"<redacted>"/g' >>"$F"
	fi
	note ""
fi
show "$CFW/netwatch.count" 5
note ""
note "== the heartbeat ($(cat /tmp/yastationd.heartbeat 2>/dev/null || echo absent))"
[ -f /tmp/yastationd.heartbeat ] && note "   last written $(($(date +%s) - $(date -r /tmp/yastationd.heartbeat +%s))) s ago"
run "ls -la /tmp/yastationd* /tmp/yastation-* 2>/dev/null"
show /tmp/yastationd.logship 5
[ -f "$CFW/log-spool" ] && show "$CFW/log-spool" 50
[ -f "$CFW/sendspin/state.json" ] && note "" && note "sendspin/state.json: $(wc -c <"$CFW/sendspin/state.json") bytes (content not collected: pairing records)"

# ---------------------------------------------------------------- the daemon over HTTP
sec daemon-http.txt
if grep -q '^[[:space:]]*http_addr[[:space:]]*=' "$CONF" 2>/dev/null; then HA=$(conf_get http_addr); else HA=:80; fi
if [ -z "$HA" ]; then
	note "http_addr is empty: the web server is off"
else
	HPORT=${HA##*:}
	HHOST=${HA%:*}
	case $HHOST in "" | 0.0.0.0 | "[::]" | "*") HHOST=127.0.0.1 ;; esac
	U=http://$HHOST:$HPORT
	note "http_addr = $HA -> $U"
	run "wget -q -T 5 -O - $U/api/info; echo" 7 50
	run "wget -q -T 5 -O - $U/metrics" 7 1500
fi

# ---------------------------------------------------------------- logs
sec log-yastationd.txt
show "$LOG.1" 1500
show "$LOG" 4000

sec log-system.txt
show /var/log/messages.0 800
show /var/log/messages 2500
command -v logread >/dev/null 2>&1 && run "logread" 5 1500
for f in /tmp/yastation-*.log /var/log/dropbear.log "$CFW"/adbusb.log; do [ -f "$f" ] && show "$f" 200; done

sec kernel.txt
run "dmesg" 8 3000
for f in /sys/fs/pstore/*; do [ -f "$f" ] && show "$f" 300; done

# ---------------------------------------------------------------- network
sec network.txt
run "hostname"
run "ip addr"
run "ip route; ip -6 route"
run "ip rule"
run "cat /proc/net/dev"
run "cat /etc/resolv.conf; ls -la /etc/resolv.conf"
run "cat /etc/hosts" 5 40
# No `wpa_cli signal_poll` / `iw` / /proc/net/wireless: the vendor Wi-Fi driver's cfg80211 does not support them (header).
run "wpa_cli -i wlan0 status"
run "wpa_cli -i wlan0 list_networks"
WPA=$(ps w | awk '/[w]pa_supplicant/ {for (i = 1; i <= NF; i++) { if ($i == "-c") { print $(i+1); exit } if ($i ~ /^-c\//) { print substr($i, 3); exit } }}')
note ""
if [ -n "$WPA" ] && [ -f "$WPA" ]; then
	# The PSKs and EAP passwords never leave the speaker, even with -r.
	note "== $WPA (passwords and PSKs removed)"
	sed -E 's/^([[:space:]]*(psk|password|wep_key[0-9]|private_key_passwd|sae_password)=).*/\1<removed>/' "$WPA" | head -n 200 >>"$F"
else
	note "== wpa_supplicant config: ${WPA:-not found}"
fi
run "netstat -tlnup" 5 60
run "netstat -tnp" 5 60
run "cat /proc/net/arp"
run "cat /etc/chrony.conf 2>/dev/null | grep -v '^#' | grep -v '^\$'" 5 40
run "ps w | grep -E '[c]hronyd|[d]hcpcd|[u]dhcpc|[u]nbound|[n]tp'"

sec egress.txt
if [ -x "$CFW/egress.sh" ]; then
	# status only: it lists the CFW_EGRESS chains (iptables -C / -L), nothing is changed.
	run "$CFW/egress.sh status" 8
else
	note "$CFW/egress.sh: absent"
fi
run "ls -la /etc/init.d/S99zzegress; cat /etc/init.d/S99zzegress" 5 40
run "cmp $CFW/egress.sh $CFW/egress.sh.prev && echo 'egress.sh = egress.sh.prev'"

# ---------------------------------------------------------------- the vendor guards
sec vendor-guards.txt
if [ -e "$CFW/maind_off" ]; then note "maind_off: present (vendor stack gated)"; else note "maind_off: ABSENT - the vendor stack is not gated"; fi
run "grep -n maind_off $Q/run.sh; ls -la $Q/run.sh $Q/run.sh.orig"
run "head -n 5 $Q/device_ota_update.sh; ls -la $Q/device_ota_update.sh*"
run "sha256sum $Q/ota_script.pub $Q/ota_script.pub.orig; cmp $Q/ota_script.pub $Q/ota_script.pub.orig >/dev/null 2>&1 && echo 'ota_script.pub = .orig: the OTA key guard is NOT in place' || echo 'ota_script.pub differs from .orig (or no .orig)'"
run "ls -la $Q/maind $Q/quasar_launcher2"
run "ps w | grep -E 'maind|quasar|updater' | grep -v grep || echo 'none running'"

# ---------------------------------------------------------------- boot
sec boot.txt
run "ls -la /etc/init.d"
run "cmp /etc/init.d/S52zyastationd $CFW/init.d/S52zyastationd && echo 'the boot hook = the copy in <cfw>/init.d'"
run "crontab -l"
run "ls -la /etc/crontabs /var/spool/cron/crontabs"
run "cat /etc/inittab" 5 40

# ---------------------------------------------------------------- audio
sec audio.txt
run "cat /proc/asound/cards /proc/asound/pcm"
run "cat /proc/asound/devices; for i in /proc/asound/card*/pcm*/info; do echo \"== \$i\"; cat \$i; done" 5 200
run "for s in /proc/asound/card*/pcm*/sub*; do echo \"\$s: \$(head -n 1 \$s/status 2>/dev/null)\"; grep -v '^closed' \$s/hw_params 2>/dev/null; done"
# Never `amixer contents` / `controls` / `scontents`: reading every mixer control froze
# bergamot's kernel inside the codec driver on 2026-10-28 (power cycle needed). /proc/asound only.
run "cat /etc/asound.conf" 5 200
run "ps w | grep -E '[h]ardvold|[a]play|[a]record|[b]luealsa'"

# ---------------------------------------------------------------- hardware
sec hardware.txt
run "cat /proc/bus/input/devices" 5 200
if [ "$HW" = 1 ]; then
	run "for d in /sys/bus/iio/devices/iio:device*; do echo \"== \$d \$(cat \$d/name 2>/dev/null)\"; for f in \$d/in_*_raw \$d/in_*_input \$d/in_*scale \$d/in_*integration_time \$d/in_*hardwaregain; do [ -f \$f ] && echo \"  \${f##*/} = \$(cat \$f 2>/dev/null)\"; done; done"
fi
run "for d in /sys/bus/iio/devices/iio:device*; do echo \"\${d##*/} \$(cat \$d/name 2>/dev/null): \$(ls \$d | grep -c '^in_')  in_* files\"; done"
run "ls /sys/class/leds | wc -l; ls /sys/class/leds" 5 600
[ "$HW" = 1 ] && run "for l in /sys/class/leds/*; do echo \"\${l##*/} \$(cat \$l/brightness 2>/dev/null)/\$(cat \$l/max_brightness 2>/dev/null) \$(sed -n 's/.*\\[\\(.*\\)\\].*/\\1/p' \$l/trigger 2>/dev/null)\"; done" 8 600
run "ls -la /dev/fb* /dev/ledfb* 2>/dev/null; cat /sys/class/graphics/fb*/virtual_size 2>/dev/null"
run "ls -la /sys/class/power_supply/"
[ "$HW" = 1 ] && run "for p in /sys/class/power_supply/*; do echo \"== \$p\"; cat \$p/uevent 2>/dev/null; done"
run "lsusb"

# ---------------------------------------------------------------- redaction
# Pseudonyms first (exact strings: serials, the hostname, SSIDs, the hostnames the config names),
# then the patterns (MACs, IPv6, key = secret pairs, spoken text).
if [ "$RAW" = 0 ]; then
	M=$WORK/map.sed
	: >"$M"
	esc() { printf '%s\n' "$1" | sed -e 's/[]\/$*.^[]/\\&/g'; }
	# map STRING REPLACEMENT [MINLEN]
	map() {
		[ "${#1}" -ge "${3:-3}" ] || return 0
		grep -qxF "s/$(esc "$1")/" "$M.seen" 2>/dev/null && return 0
		echo "s/$(esc "$1")/" >>"$M.seen"
		echo "s/$(esc "$1")/$2/g" >>"$M"
	}
	for s in $(tr ' ' '\n' </proc/cmdline | sed -nE 's/^androidboot\.(serialno|deviceid|cpuid|usid)=//p'); do map "$s" '<serial>' 6; done
	s=$(sed -n 's/^Serial[[:space:]]*:[[:space:]]*//p' /proc/cpuinfo | head -1); [ -n "$s" ] && [ "$(echo "$s" | tr -d 0)" != "" ] && map "$s" '<serial>' 6
	s=$(cat /proc/device-tree/serial-number 2>/dev/null | tr -d '\0'); map "$s" '<serial>' 6
	map "$(hostname 2>/dev/null)" '<hostname>' 4
	n=0
	{
		[ -n "$WPA" ] && sed -n 's/^[[:space:]]*ssid="\(.*\)"[[:space:]]*$/\1/p; s/^[[:space:]]*ssid=\([0-9A-Fa-f]*\)[[:space:]]*$/\1/p' "$WPA" 2>/dev/null
		tmo 5 wpa_cli -i wlan0 status 2>/dev/null </dev/null | sed -n 's/^ssid=//p'
		tmo 5 wpa_cli -i wlan0 list_networks 2>/dev/null </dev/null | awk -F'\t' 'NR > 1 && $2 != "" {print $2}'
	} | sort -u >"$WORK/ssids"
	while IFS= read -r s; do n=$((n + 1)); map "$s" "<wifi-ssid-$n>"; done <"$WORK/ssids"
	n=0
	{
		for k in mqtt_host log_remote ntp_server sendspin_server probe; do
			grep -E "^[[:space:]]*$k[[:space:]]*=" "$CONF" 2>/dev/null | cut -d= -f2-
		done
		sed -nE 's/^[[:space:]]*(search|domain)[[:space:]]+//p' /etc/resolv.conf 2>/dev/null
		sed -nE 's/^[[:space:]]*(server|pool)[[:space:]]+([^[:space:]]*).*/\2/p' /etc/chrony.conf 2>/dev/null
	} | tr ' \t=@/:,' '\n\n\n\n\n\n\n' | grep -E '^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$' | grep -vE '^[0-9.]+$' | awk '{print length, $0}' | sort -rn | cut -d' ' -f2 | uniq >"$WORK/hosts"
	while IFS= read -r s; do n=$((n + 1)); map "$s" "<host-$n>"; done <"$WORK/hosts"

	cat >"$WORK/pat.sed" <<'EOF'
s/(([0-9A-Fa-f]{2}[:-]){3})[0-9A-Fa-f]{2}([:-])[0-9A-Fa-f]{2}[:-][0-9A-Fa-f]{2}/\1xx\3xx\3xx/g
s/(^|[^0-9A-Fa-f:])[23][0-9A-Fa-f]{3}(:[0-9A-Fa-f]{0,4}){2,7}/\1<ipv6-global>/g
s/fe80::[0-9A-Fa-f:]+/fe80::<masked>/g
s/(^|[^A-Za-z])(pass|passwd|password|passphrase|secret|psk|token|api_?key|auth_?key|bearer)("?[[:space:]]*[=:][[:space:]]*)("[^"]*"|[^][:space:],;&"'}{)]+)/\1\2\3<redacted>/Ig
s/(Authorization:[[:space:]]*Bearer[[:space:]]+)[^[:space:]]+/\1<redacted>/Ig
s/pbkdf2-sha256\$[^[:space:]"]*/<redacted-hash>/g
s/(^|[^A-Za-z_])ssid=.*/\1ssid=<ssid>/
s/(^|[[:space:]])(text|phrase_text|reply|transcript)=("([^"\\]|\\.)+"|[^[:space:]"]+)/\1\2=<redacted>/g
s/("message"[[:space:]]*:[[:space:]]*)"[^"]*"/\1"<redacted>"/g
EOF
	for f in "$D"/*; do
		[ -s "$M" ] && sed -i -f "$M" "$f"
		sed -i -E -f "$WORK/pat.sed" "$f"
	done
fi

# ---------------------------------------------------------------- summary (first in the archive)
F=$D/summary.txt
: >"$F"
yv=missing; [ -x "$CFW/bin/yastationd" ] && yv=$(tmo 5 "$CFW/bin/yastationd" -version 2>&1 </dev/null | head -n 1)
up=$(cut -d. -f1 /proc/uptime)
running() { ps w | awk -v b="$CFW/bin/$1" '$3 == b' | wc -l; }
note "yastationd diagnostics — $NAME"
note "collected: $(date '+%F %T %Z') (uptime $((up / 86400))d $((up % 86400 / 3600))h $((up % 3600 / 60))m), took $((up - T0)) s"
if [ "$RAW" = 1 ]; then
	note "redaction: OFF (-r). Contains secrets, SSIDs, hostnames, MACs, serials: keep it private."
else
	note "redaction: ON (secrets, SSIDs, config hostnames, MACs, serials, hostname, spoken text)."
	note "           LAN IPv4 addresses are kept: the archive shows your local network layout."
fi
note ""
note "device_id     $DEVID"
note "model         $(cat /proc/device-tree/model 2>/dev/null | tr -d '\0')"
note "compatible    $(cat /proc/device-tree/compatible 2>/dev/null | tr '\0' ' ')"
note "firmware      $(cat $Q/version_full 2>/dev/null)"
note "kernel        $(uname -r) $(uname -m)"
note "cfw root      $CFW"
note "yastationd    ${yv:-?}"
note "processes     yastationd=$(running yastationd) spotter-host=$(running spotter-host) sendspin-host=$(running sendspin-host) hostapd=$(running hostapd)"
note "helpers       $(for h in spotter-host sendspin-host hostapd dropbearmulti diag.sh; do [ -x "$CFW/bin/$h" ] && printf '%s ' "$h" || printf '%s(missing) ' "$h"; done)"
vs=$(ps w | grep -cE '[m]aind|[q]uasar_launcher')
note "vendor stack  maind_off=$([ -e "$CFW/maind_off" ] && echo yes || echo NO) vendor processes=$vs ota_update=$(head -n 3 $Q/device_ota_update.sh 2>/dev/null | grep -q '^exit 0' && echo blocked || echo NOT-BLOCKED)"
note "egress        $(tmo 8 "$CFW/egress.sh" status 2>&1 </dev/null | grep -E '^(state|ipv6):' | tr '\n' ' ')"
note "off switch    $([ -e "$CFW/yastationd_off" ] && echo 'yastationd_off PRESENT (daemon disabled)' || echo none)  config on trial: $([ -e "$CFW/config.trial" ] && echo yes || echo no)"
note "clock         $(date '+%F %T') $([ "$(date +%Y)" -lt 2025 ] && echo '(NOT SET: no time sync yet)')"
note "load / mem    $(cut -d' ' -f1-3 /proc/loadavg) / $(awk '/^MemAvailable/ {a=$2} /^MemTotal/ {t=$2} END {printf "%d of %d MB available", a/1024, t/1024}' /proc/meminfo)"
note "flash         $(df -k "$CFW" 2>/dev/null | awk 'NR == 2 {printf "%s KB free of %s on %s", $4, $2, $6}')"
note "temperature   $(for z in /sys/class/thermal/thermal_zone*/temp; do [ -f "$z" ] && printf '%s ' "$(cat "$z")"; done)"
note "reboot reason $(cat /sys/class/aml_info/reboot_reason 2>/dev/null || echo n/a)"
if [ -f /tmp/yastationd.heartbeat ]; then note "heartbeat     written $(($(date +%s) - $(date -r /tmp/yastationd.heartbeat +%s))) s ago"; else note "heartbeat     absent"; fi
note "driver probes $([ "$HW" = 1 ] && echo 'ON (-x: sensor, LED, PD readings)' || echo 'off (no -x)')"
if [ -f "$LOG" ]; then
	note "log           $(wc -l <"$LOG") lines; ERROR $(grep -c 'level=ERROR' "$LOG"), WARN $(grep -c 'level=WARN' "$LOG"); starts $(grep -c '^=== start' "$LOG")"
	note ""
	note "== the supervisor's last lines"
	grep '^=== ' "$LOG" | tail -n 12 >>"$F"
	note ""
	note "== the last errors and warnings"
	grep -E 'level=(ERROR|WARN)' "$LOG" | tail -n 15 | cut -c1-300 >>"$F"
else
	note "log           $LOG absent"
fi
note ""
note "== files"
for f in "$D"/*; do [ "$f" = "$F" ] || note "$(printf '%-22s %8s bytes' "${f##*/}" "$(wc -c <"$f")")"; done
if [ "$RAW" = 0 ]; then
	[ -s "$M" ] && sed -i -f "$M" "$F"
	sed -i -E -f "$WORK/pat.sed" "$F"
fi

# ---------------------------------------------------------------- the archive
cd "$WORK" || exit 1
list="$NAME/summary.txt $(ls "$NAME" | grep -v '^summary\.txt$' | sed "s|^|$NAME/|" | tr '\n' ' ')"
if [ "$STDOUT" = 1 ]; then
	# shellcheck disable=SC2086 # a list of names without spaces
	tar cf - $list | gzip -c
	say "done in $(($(cut -d. -f1 /proc/uptime) - T0)) s"
else
	out=$OUTDIR/$NAME.tar.gz
	# shellcheck disable=SC2086
	tar cf - $list | gzip -c >"$out" || { echo "diag.sh: writing $out failed" >&2; exit 1; }
	say "done in $(($(cut -d. -f1 /proc/uptime) - T0)) s, $(wc -c <"$out") bytes"
	echo "$out"
fi
