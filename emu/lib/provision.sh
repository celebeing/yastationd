#!/bin/sh
# provision.sh — installs a seed (emu/seed-from-unit.sh) into the emulated flash. Copied into
# emu/work/<board>/seed/ and run by the maintenance initramfs (emu/maint/init) with:
#   /mnt/root  the rootfs volume, mounted rw
#   /mnt/rw    the rw volume, also bind-mounted at /mnt/root$RW_PATH
#   /mnt/seed  this directory (9p)
# Runs with the initramfs's static BusyBox, never with the vendor userland. Idempotent.
set -eu
R=/mnt/root
S=/mnt/seed
. "$S/seed.env"

say() { echo "[provision] $*"; }

mountpoint -q "$R$RW_PATH" || { say "rw partition is not mounted on $R$RW_PATH"; exit 1; }

say "rootfs: removing $(wc -l < "$S/rootfs.remove") paths, adding $(wc -l < "$S/rootfs.list")"
while IFS= read -r p; do
	[ -n "$p" ] && rm -f "$R$p" && say "  rm $p"
done < "$S/rootfs.remove"
while IFS= read -r p; do
	[ -n "$p" ] && [ ! -e "$R$p" ] && [ ! -L "$R$p" ] && mkdir -p "$R$p"
done < "$S/rootfs.mkdir"
tar xf "$S/rootfs.tar" -C "$R"

say "rw partition: $(tar tf "$S/rw.tar" | wc -l) entries into $RW_PATH"
tar xf "$S/rw.tar" -C "$R"

say "emulator config -> $CFW_ROOT/yastationd.conf"
cp "$S/yastationd.conf" "$R$CFW_ROOT/yastationd.conf"
chmod 644 "$R$CFW_ROOT/yastationd.conf"

say "emulator ssh key -> /root/.ssh/authorized_keys"
mkdir -p "$R/root/.ssh"
chmod 700 "$R/root/.ssh"
key=$(cat "$S/emu_ssh_key.pub")
touch "$R/root/.ssh/authorized_keys"
grep -qxF "$key" "$R/root/.ssh/authorized_keys" || echo "$key" >> "$R/root/.ssh/authorized_keys"
chmod 600 "$R/root/.ssh/authorized_keys"

# Safety checks: the twin must keep the vendor stack gated and updates blocked, exactly like the
# real unit. Refuse to finish otherwise.
ok=1
grep -q "maind_off" "$R/system/vendor/quasar/run.sh" || { say "run.sh is not gated"; ok=0; }
[ -f "$R$CFW_ROOT/maind_off" ] || { say "$CFW_ROOT/maind_off missing"; ok=0; }
grep -qx 'exit 0' "$R/system/vendor/quasar/device_ota_update.sh" || { say "OTA script not blocked"; ok=0; }
[ "$ok" = 1 ] || exit 1

date -u +%FT%TZ > "$R$CFW_ROOT/.emu-provisioned"
sync
say "done"
