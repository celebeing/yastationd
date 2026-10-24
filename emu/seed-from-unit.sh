#!/bin/sh
# Builds emu/work/<board>/seed/ — what turns a stock dump into a twin of a real unit — from that
# unit, READ-ONLY: the speaker only runs find/md5sum/cat/tar to stdout. Nothing is written,
# restarted or played on it.
#
#   emu/seed-from-unit.sh <board> <ssh-target> [--broker] [--keep-wake] [-- <ssh options>]
#
#   e.g. emu/seed-from-unit.sh bergamot root@<speaker-ip> -- -i <key> -o IdentitiesOnly=yes
#
#   --broker      point the twin's yastationd at a test broker on 10.0.2.100:1883 (the guest
#                 side of run.sh --broker HOSTPORT) instead of running without MQTT
#   --keep-wake   keep the unit's wake_enabled (default: off, the emulator has no mics)
#
# What it collects:
#   rootfs.tar      rootfs files the CFW added or changed (the unit's files vs the dump's rootfs,
#                   REF_ROOTFS in the board file) plus, always, /etc/init.d/S*z*, the gated
#                   /system/vendor/quasar/run.sh(.orig) and device_ota_update.sh
#   rootfs.remove   files the dump has and the unit no longer has (e.g. init hooks moved away)
#   rootfs.mkdir    directories only the unit has
#   rw.tar          <cfw> plus SEED_RW_EXTRA from the board file (minus Wi-Fi credentials)
#   yastationd.conf the unit's config with emulator overrides (device_id emu-<board>, no
#                   broker, no wake word)
#   emu_ssh_key     a key pair made here; provision adds the public half to root's
#                   authorized_keys, so the smoke test needs no hardware key
#   unit/           the unit's manifest, cmdline, process list, init.d listing (for comparison)
#   provision.sh    run by the maintenance initramfs (emu/run.sh <board> --maint --auto)
#
# The seed holds secrets (host keys, broker credentials, authorized_keys): it stays in the
# gitignored emu/work/ and must never be committed or shared.
set -eu
. "$(cd "$(dirname "$0")" && pwd)/lib/common.sh"

[ $# -ge 2 ] || { sed -n '2,30p' "$0"; exit 1; }
load_board "$1"; TARGET=$2; shift 2
BROKER=0 KEEPWAKE=0
while [ $# -gt 0 ]; do
	case $1 in
		--broker) BROKER=1 ;;
		--keep-wake) KEEPWAKE=1 ;;
		--) shift; break ;;
		*) die "unknown option $1" ;;
	esac
	shift
done
SSHOPTS="$* -o BatchMode=yes -o ConnectTimeout=10"

# Read-only remote command. Everything that runs on the speaker goes through here.
on_unit() {
	# shellcheck disable=SC2086
	ssh $SSHOPTS "$TARGET" "$1"
}

TMP=$SEED.new
rm -rf "$TMP"
mkdir -p "$TMP/unit"

note "$BOARD: reading the unit's rootfs manifest (read-only)"
on_unit 'find / -xdev -type f -exec md5sum {} + | sed "s/^/F /"
find / -xdev -type l | while read -r l; do echo "L $(readlink "$l") $l"; done
find / -xdev -type d | sed "s/^/D - /"
awk "{print \"M - \" \$2}" /proc/mounts' > "$TMP/unit/manifest"
on_unit 'cat /proc/cmdline; echo; cat /proc/mtd; echo; ls -l /etc/init.d; echo; ps w; echo; cat /proc/mounts' \
	> "$TMP/unit/info.txt"
on_unit "ls -la $CFW_ROOT" > "$TMP/unit/cfw-listing.txt"

note "$BOARD: comparing with $REF_ROOTFS"
python3 -I "$EMU/lib/rootfs_diff.py" "$REPO/$REF_ROOTFS" "$TMP/unit/manifest" > "$TMP/unit/diff.txt"
# Always carried, whatever the reference says: the CFW's init hooks and the vendor guards.
{
	awk '$1 == "copy" { print $2 }' "$TMP/unit/diff.txt"
	grep -E '^[FL] ' "$TMP/unit/manifest" | awk '{ print $NF }' | grep -E \
		'^/etc/init\.d/S[0-9][0-9]z|^/system/vendor/quasar/(run\.sh|run\.sh\.orig|device_ota_update\.sh)$'
} | sort -u > "$TMP/rootfs.list"
awk '$1 == "remove" { print $2 }' "$TMP/unit/diff.txt" > "$TMP/rootfs.remove"
awk '$1 == "mkdir" { print $2 }' "$TMP/unit/diff.txt" > "$TMP/rootfs.mkdir"
grep -q '^/system/vendor/quasar/run.sh$' "$TMP/rootfs.list" || die "unit has no run.sh?"
note "$BOARD: $(wc -l < "$TMP/rootfs.list") rootfs paths to copy, $(wc -l < "$TMP/rootfs.remove") to remove"

note "$BOARD: fetching the changed rootfs files"
on_unit "cd / && tar cf - $(sed 's|^/||' "$TMP/rootfs.list" | tr '\n' ' ')" > "$TMP/rootfs.tar"

note "$BOARD: fetching $CFW_ROOT${SEED_RW_EXTRA:+ and $SEED_RW_EXTRA}"
RWPATHS=$(for p in $CFW_ROOT $SEED_RW_EXTRA; do printf '%s ' "${p#/}"; done)
on_unit "cd / && tar cf - --exclude 'wpa_supplicant.conf*' --exclude ash_history $RWPATHS" > "$TMP/rw.tar"
tar tf "$TMP/rw.tar" > "$TMP/unit/rw.list"
tar tf "$TMP/rootfs.tar" > "$TMP/unit/rootfs.list"

note "$BOARD: making the emulator config"
on_unit "cat $CFW_ROOT/yastationd.conf" | awk -v board="$BOARD" -v broker="$BROKER" -v keepwake="$KEEPWAKE" '
	BEGIN { split("board device_id device_name mqtt_host mqtt_port mqtt_user mqtt_pass probe sendspin_server ntp_server", d, " ")
	        for (i in d) drop[d[i]] = 1
	        if (!keepwake) drop["wake_enabled"] = 1 }
	{ k = $0; sub(/[ \t]*=.*/, "", k); gsub(/^[ \t]+/, "", k)
	  if (k in drop) { print "# emu: dropped " k; next } print }
	END {
	  print ""
	  print "# --- emulator overrides (emu/seed-from-unit.sh) ---"
	  print "device_id   = emu-" board
	  print "device_name = Emulated " board
	  if (broker) {
	    print "mqtt_host   = 10.0.2.100"
	    print "mqtt_port   = 1883"
	    print "probe       = broker=10.0.2.100:1883@broker"
	  } else {
	    print "mqtt_host   ="
	  }
	  if (!keepwake) print "wake_enabled = false"
	}' > "$TMP/yastationd.conf"

# Emulator SSH key: kept across re-seeds so known_hosts/agents stay stable.
if [ -f "$SEED/emu_ssh_key" ]; then
	cp -p "$SEED/emu_ssh_key" "$SEED/emu_ssh_key.pub" "$TMP/"
else
	ssh-keygen -q -t ed25519 -N '' -C "emu-$BOARD" -f "$TMP/emu_ssh_key"
fi

cat > "$TMP/seed.env" <<EOF
BOARD=$BOARD
CFW_ROOT=$CFW_ROOT
RW_PATH=$RW_PATH
SEEDED_FROM_DATE=$(date -u +%FT%TZ)
EOF
cp "$EMU/lib/provision.sh" "$TMP/provision.sh"

rm -rf "$SEED.old"
[ -d "$SEED" ] && mv "$SEED" "$SEED.old"
mv "$TMP" "$SEED"
rm -rf "$SEED.old"
chmod 700 "$SEED"
note "$BOARD: seed ready in $SEED (local only: contains secrets)"
note "next: emu/run.sh $BOARD --fresh --maint --auto   (installs it), then emu/run.sh $BOARD"
