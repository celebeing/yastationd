# Permanent install — taking over the factory reset

Research (2026-10-24; orion 2026-10-31) into making the CFW survive a hard reset, i.e. replacing the image the
speaker restores on a factory reset with our own, while keeping a convenient way back. Done with
static analysis of the local dumps and **read-only** reads on the units (`/proc/mtd`,
`fw_printenv`, the header of the recovery partition). Nothing was written to any device.

**Verdict:**
- The vendor hard reset is **the brick safety net for rootfs work**. Anything we can break from
  Linux (deleting `maind`, assets or libs, bad init hooks) lives in the `system` partition. The reset
  path (bootloader → recovery → restore payload) lives in other partitions that Linux never writes
  unless we write `/dev/mtdN` directly. So a broken rootfs is always recoverable by the mute+power
  gesture. The only question is *what* it restores.
- On **pickle** the restore payload is an unsigned Amlogic burn package in a plain NAND partition, so
  we **can** make the reset land in a known-good CFW instead of stock. That turns "we bricked the
  rootfs" into a two-minute round trip. This is the recommended goal.
- On **bergamot** the payload is inside the signed and encrypted recovery image and cannot be
  replaced. The reset always lands in stock. Heavy rootfs experiments belong on pickle.
- On **orion** (2026-10-31) the payload is the same unsigned burn package as on pickle, with
  ~900 MB of headroom and an unverified ext4 rootfs, so both a minimal "comes back reachable"
  image and a full baked-in CFW are possible. What the reset itself does there is unknown; see
  [orion](#orion--the-restore-payload-is-replaceable-too).
- A second gesture in U-Boot is not doable. A soft-reset gesture in early Linux init is possible,
  but it does **not** protect against a broken rootfs. It is a convenience on top, not the safety net.

## How a hard reset works (both models)

- User gesture (vendor manual): unplug, hold the **mic-mute button**, plug in, hold ~5 s until the
  ring turns yellow. This happens **before Linux**: U-Boot / the always-on CPU sees the button and
  boots the `recovery` partition. `maind` has no flash-level reset. Its `factoryReset` only resets
  configs, counters, Zigbee and Matter state, so gating maind does not disable the gesture.
- U-Boot env is reachable from Linux (`fw_printenv`/`fw_setenv`, `/etc/fw_env.config` →
  `/dev/nand_env 0x0 0x10000 0x200`, one copy with no redundant one). Relevant variables (both
  models): `switch_bootmode` = `get_rebootmode; if reboot_mode = factory_reset → run
  recovery_from_flash; if update → USB burning`, and `recovery_from_flash` = `imgread kernel recovery
  … ; bootm`. bergamot additionally runs a compiled-in `yandex_io_check_recovery` from `bootcmd`.
- Secure boot is on (`AMLSECU!` headers; pickle env `forUpgrade_secureBoot=true`). U-Boot, the
  kernel, the DTB and the **recovery kernel + ramdisk** are signed and encrypted with the SoC key.
  They cannot be read or replaced. The **rootfs (UBIFS) is not authenticated** (no dm-verity), which
  is why rootfs edits work at all.
- OTA does not touch the recovery (the Station 2's (`yandexmidi`) `sw-description`: rootfs, boot, dtb and U-Boot only). OTA
  `.swu` files are RSA-signed (`sw-description.sig`, `ota_script.pub`), so the swupdate-in-recovery
  path cannot install our image either.

## pickle — the restore payload is replaceable

- `aml_sysrecovery` (mtd6, 128 MiB) holds an **Amlogic image v2 burn package** (magic `0x27b51956`,
  113 392 448 bytes, the rest of the partition is `0xff`). The recovery partition is only 15 MB of
  kernel and ramdisk, so the 80 MB system image can only come from this package.
- Package items (19): `DDR/UBOOT(.USB, _ENC)`, `_aml_dtb`, `aml_sysrecovery` (self-reference, used
  by the USB burn tool), `boot`, `bootloader`, `meson1(_ENC).dtb`, `platform.conf`, `recovery`,
  **`system`** (a ubinize image, 79 953 920 B), `usb_flow.aml`, plus one `VERIFY` item per partition.
- Integrity is **all recomputable**: each `VERIFY` item is the text `sha1sum <sha1 of the item>`, and
  the header CRC is `~crc32(image[4:size])` (zlib polynomial, inverted). There is **no signature
  item**. The signed and encrypted items (boot, recovery, bootloader, DTB) stay byte-identical.
- Evidence that the reset reflashes system from this package: the live `system` UBI `image_seq`
  equals the package's. `boot.PARTITION` and `recovery.PARTITION` are byte-identical prefixes of
  mtd8 and mtd7.
- Headroom: 128 MiB − 113.4 MB ≈ **20 MB** for a bigger `system` item (the CFW today is ~16.5 MB
  uncompressed and much smaller zlib-compressed).
- UBI/UBIFS geometry of the package `system`: PEB 256 KiB, min I/O 4096, VID header 4096, data
  offset 8192, LEB 253 952; UBIFS `-m 4096 -e 253952 -c 1120`, zlib, fanout 8, log 4 LEBs; one
  volume `rootfs`, flag **autoresize**. 0 bad blocks on mtd6 and mtd9 on our unit. The kernel has no
  SELinux (no selinuxfs, despite `androidboot.selinux=enforcing`), so no xattr labels are needed.
- The reset **wipes `/data`** (`S00rw-partitions` recreates the volume). Everything the golden image
  needs to boot into a working CFW must live in the rootfs; `/data` holds only per-unit state.

### Unknowns (encrypted, cannot be read statically)
- What the recovery program checks before it starts erasing. The package format has no signature,
  but the recovery *could* contain a hard-coded hash. We consider that unlikely, but it is the one
  residual risk of the takeover.
- Whether the restore also rewrites `bootloader` from the package. That is stock behaviour either
  way, and we keep that item byte-identical.
- How to force Amlogic USB-burn (ADNL) mode with no working Linux: a boot strap or pad. From a
  running Linux it is `reboot update` → `usb_burning=adnl 1200`.

## bergamot — the restore payload is not replaceable

- The recovery is **one Android boot image spanning `recovery` + `recovery_fs`** on the 0.369-layout
  unit: kernel 5.8 MB + ramdisk 30 MB, the ramdisk running into `recovery_fs`. It carries an
  `AMLSECU!` header and is encrypted (entropy 8.0). Cmdline: `rootfstype=ramfs ubi.mtd=system
  ubi.mtd=bkupgrade`. Whatever factory image it restores is inside that encrypted ramdisk. No
  plaintext copy exists on the flash (the rest of `recovery_fs` is `0xff`; `/bkupgrade` holds only
  `cfw` and `data`).
- The 0.6.x-layout unit has **a different partition table**: one `recovery` of 58.75 MiB and no
  `recovery_fs` (a later factory layout). What its reset restores (which firmware version) is not
  verified.
- Inside the recovery, `S01swupdate` installs `/bkupgrade/ota/yandex_io_ota.swu` when
  `boot_to_recovery=1` (`apply_packed_ota`). This needs a Yandex signature.
- What a reset does on bergamot (a full system reflash, or only a `/bkupgrade` wipe) is **unknown**.
  The vendor says "all software updates are removed", which points to a full reflash to the factory
  version.
- One possible lever, **parked**: rewrite `recovery_from_flash` in the env to boot the normal kernel
  with a `cfw_safe=1` argument, turning the mute+power gesture into a CFW safe mode. Problems: it
  only works if the compiled-in `yandex_io_check_recovery` goes through that variable (unknown); a
  bad env write can stop the boot (there is no redundant env copy); and it removes the vendor
  escape hatch. Not recommended without a UART console as the backstop.

## orion — the restore payload is replaceable too

Static analysis of the eMMC dump (firmware factory-13) and read-only reads on one unit
(2026-10-31: `/proc/cmdline`, `/proc/mounts`, `/proc/partitions`, `fw_printenv`). Nothing was
written.

**Layout.** One eMMC, partitions by name under `/sys/class/block`: `bootloader` (+ `mmcblk0boot0/1`),
`reserved` (64 MiB), `cache`, `env` (8 MiB), `misc`, `tee` (48 MiB, mounted `ro` at `/tee`),
`aml_sysrecovery` (1.5 GiB), `boot` (64 MiB), `recovery` (192 MiB), `system` (1 GiB, ext4, the
root, `root=PARTLABEL=system ro`), `ota` (1.5 GiB ext4, `/ota` = `/bkupgrade`), `data` (2.8 GiB ext4).
Only `system`, `ota`, `data` and `tee` are mounted.

**The restore package.** `aml_sysrecovery` holds an Amlogic image v2 package of 629 930 824 bytes,
the same 19 items as pickle's (`DDR`/`UBOOT` and their `_ENC`, `_aml_dtb`, `boot`, `bootloader`,
`meson1(_ENC)` dtb, `platform` conf, `recovery`, `system`, `usb_flow`, one `VERIFY` per partition
item). Checked on the dump:
- the header CRC is `~crc32(image[4:size])` and every `VERIFY` is `sha1sum <sha1>`, both
  recomputed and matching. There is no signature item;
- `system` is an Android sparse image (file type 254, 262 144 blocks of 4 KiB) of a 1 GiB ext4;
- `boot` and `recovery` are byte-identical prefixes of the live partitions;
- the dumped `system` partition, unsparsed, equals the package's `system` except two bytes (a
  journal feature flag set by the first mount). Consistent with "the factory flashes `system`
  from this package"; it does not prove that the *reset* does;
- headroom: 1.5 GiB − 630 MB ≈ **900 MB**. The ext4 itself has ~350 MB free, against ~115 MB
  for the whole CFW in `<cfw>` today.

**What stays sealed.** `recovery` and `boot` are Android boot images with plaintext headers, but
their kernel and ramdisk are encrypted (entropy 8.0); the bootloader too. Env:
`forUpgrade_secureBoot=true`, `bootloader_wp=1`, `avb2=0`, no dm-verity on the cmdline, so the
rootfs is not authenticated (the install kit's rootfs edits boot fine).

**The reset path in the env.** `storeboot` → `switch_bootmode`: `reboot_mode=factory_reset` runs
`fr`, a command compiled into U-Boot whose behaviour cannot be read; `update` and
`recovery_quiescent` run `recovery_from_flash` (boots the recovery kernel), not USB burn. The env
also has `readmute=gpio read 24 mute 1` (unused by any env script, likely what the compiled-in
gesture check reads) and `rabbit_hole_debug=0` → `androidboot.rh_unlock=0` (meaning unknown).
`usb_burning=adnl 1200` exists, but how to reach burn mode with no working Linux is unknown.

**How stock ADB comes back, and why it does not survive a reset.** `onboot.sh` (from `S97quasar`,
before `run.sh`, so it runs even with maind gated) calls `activate_adb.sh check`, which turns ADB on
when a marker exists in `/data` (`quasar_activate_adb_{sound,quasmodrom,glagol}`, plus
`quasar_activate_network_adb` for network ADB). A reset that empties `/data` loses them. maind
is not needed for ADB, and none is needed for us either: an SSH server started from a rootfs hook
does the same job.

**What a reset empties.** `S00rw-partitions` runs `mkfs` on any `rw` partition from `/etc/fstab`
that is not mounted (i.e. failed to mount), so an erased `data` or `ota` comes back empty. Stock
`S40wifi` copies `/etc/wpa_supplicant.conf` into `/bkupgrade/data/wpa_supplicant.conf` when the
latter is missing or empty, so **a network baked into the rootfs's `/etc/wpa_supplicant.conf` is
joined after a reset with no onboarding**. Whether `fr` reflashes `system`, wipes `data`/`ota`, or
both, is the main unknown and needs one deliberate reset on a backed-up unit.

### Why the vendor's updates leave the package alone

An update never passes through anything that knows partitions: maind downloads the `.swu` and
runs `applyOtaScript` (`device_ota_update.sh`), which stops the stack and calls
`yandex-apply-ota`; that moves the file to `/ota/yandex_io_ota.swu` and runs
`yandex-reboot update`; U-Boot boots the recovery, whose `S01swupdate` installs it. Which
partitions get written is decided by the `sw-description` inside each signed `.swu`. Neither
maind nor any script or binary in the rootfs's `/usr/bin`, `/usr/sbin` or `/etc` mentions
`aml_sysrecovery`, `swupdate` or `sw-description` (maind's `/factory_reset` string belongs to its
Zigbee code).

So it *could*: the Station 2's `sw-description` (the only one we have) writes the rootfs, `boot`,
`dtb` and every bootloader copy, so swupdate in the recovery can write any partition it is told
to. It is just never told to write this one. That is the usual Amlogic design: the package is the
factory "golden" image, a reset returns to the factory version and the unit then updates itself,
and rewriting the one image that rescues a failed update on every update would put it at risk
(and make each update hundreds of MB heavier). Orion's own `sw-description` is unseen (no orion
`.swu` on hand; it is normally plaintext in the cpio even when the images are encrypted). Our unit
runs `…factory-13` and its `system` equals the package's, so it has never been updated and says
nothing about this either way.

One consequence for us: the package also carries `bootloader`. If updates raise the bootloader
and a reset put the factory one back, anti-rollback would get in the way, which suggests `fr`
leaves the bootloader alone; unverified. Hence a golden package changes only `system` and keeps
the bootloader, `boot` and `recovery` items byte-identical.

### The recovery itself cannot be changed, only its inputs

`recovery` is the program (kernel + ramdisk, ~23 MB in a 192 MiB partition); `aml_sysrecovery` is
the data it restores from. What it wipes and writes is decided in its ramdisk, which is encrypted
(cannot be read) and signed with the SoC key under secure boot (a changed image is not booted, and
the reset gesture would then lead nowhere). Its inputs:

| Input | Signed | Usable |
|---|---|---|
| the package in `aml_sysrecovery` | no | **yes, the lever** |
| `/ota/yandex_io_ota.swu` (how updates are installed) | yes, checked with a key inside the recovery | no: the install kit's `ota_script.pub` swap is in the rootfs, which the recovery does not read |
| U-Boot env (`recovery_from_flash`, `bootargs`) | no | in theory; one copy only, a mistake can stop the boot. Not touched |
| `misc` (the Android channel for recovery commands like wipe/update) | no | unknown whether this recovery reads it; zeros on the unit |

Learning its logic without decrypting it: the `cache` partition, where Android recoveries keep
logs of past runs, and `misc` are both empty on the unit (read 2026-10-31), so there is no record
of earlier runs. That leaves the deliberate reset with marker files (above), and **UART**: the
recovery kernel almost certainly logs to `ttyS0` at 921600 (the env's `silent=1` silences U-Boot
only). A serial console attached during a reset would show what it checks, which partitions it
writes, and whether it rejects a modified package, the one residual risk of the takeover. How to
reach orion's UART (case, pads) is not researched yet.

### The two images

- **Minimal ("comes back reachable"):** stock `system` plus the rootfs edits the install kit and
  `tools/deploy.sh` already make: the maind gate in `run.sh`, the OTA stub, the egress lock hook,
  the SSH hook with dropbear/OpenSSH in the rootfs and `/root/.ssh/authorized_keys` (already on the
  rootfs on orion), and the home network in `/etc/wpa_supplicant.conf`. After a reset the unit
  joins Wi-Fi, serves SSH with the owner's key, does not start the vendor stack and has no
  internet; restoring `<cfw>` from a backup brings the rest back. With that in place, network
  ADB `:5555` ([backlog.md](../backlog.md)) can be closed. If the reset turns out not to touch
  `system`, the same edits on the live rootfs give this without touching the package.
- **Full:** the same with the CFW baked into the rootfs (the golden layer of the design below).

Build: unsparse the package `system`, edit the ext4 in a container (e2fsprogs `debugfs -w`, or
`mke2fs -d` from a tree), re-sparse, replace the item, recompute its `VERIFY` sha1 and the header
CRC; the encrypted items stay byte-identical. Write it with `dd` to `aml_sysrecovery` (normal
boot does not read it), read back and compare. The image carries the Wi-Fi PSK and the owner's
key: built locally, never committed. Same residual risk as pickle: the encrypted recovery might
check something after it starts erasing; and never put an image there that has not booted live.

### Backup and restore first

Before any reset experiment each unit needs a backup that restores it with Home Assistant and
Music Assistant noticing nothing. What they depend on lives almost entirely in `/ota`:

| What | Where | Why it matters |
|---|---|---|
| `device_id` and all config | `<cfw>/yastationd.conf` | the MQTT client id, HA entities' unique ids, intercom, wake arbitration |
| options | `<cfw>/yastationd.state` | everything set from HA or the web app |
| Music Assistant player identity | `<cfw>/sendspin/state.json` | the stable `client_id`; without it MA sees a new player |
| voices | `<cfw>/voices/` | voice ID's People album |
| `web.secret`, packs, sounds | `<cfw>/` | web login, themes |
| SSH host key, CA, principals | `<cfw>/`, `<cfw>/openssh/` | no host-key warning, certificate login |
| Wi-Fi | `/ota/data/wpa_supplicant.conf` | |
| Bluetooth | `/data/bt_mac`, `/data/bluez_data` | pairings |
| rootfs edits | the vendor repo's per-unit `paths` list | hooks, gate, OTA stub |
| MAC, serial | probably `reserved` (Amlogic unifykey, read by `keyman` in the env's `cmdline_keys`); backed up by `tools/backup-unit.sh` | same MAC → same DHCP lease and address |

Built (2026-10-31): `tools/backup-unit.sh` and `tools/restore-unit.sh`
([guides/backup.md](../guides/backup.md)); every reachable unit backed up. Getting back in after a
stock reset needs the install kit (out of this repo's scope); with it, "reset → kit → restore"
brings a unit back as it was.

### Status: the takeover is parked (2026-10-31)

**No escape hatch without opening the case.** The case is clipped and glued (sealed), and the owner
will not open it, so UART and board pads are out. What is left from outside:
- The vendor gesture (hold **mute**, the only hardware key, while plugging in the USB-C power; the
  ring runs a yellow animation, then the unit boots the factory image and waits for the vendor
  onboarding) is the **recovery** path (`aml_sysrecovery`), not USB burn. The env's
  `readmute=gpio read 24 mute 1` suggests U-Boot itself reads the key.
- **USB burn (ADNL)** has a U-Boot command (`usb_burning=adnl 1200`), but no env script runs it; the
  boot ROM enters USB mode by itself only with a broken bootloader or a board strap. Reaching it
  from outside would take an env edit (the single env copy: a boot risk taken for a safety net) or
  feeding the unit its 20 V PD from its own supply while passing USB data to a computer (it is picky
  about USB-C power; a PC port does not power it). The USB device controller is there
  (`crgudc2`), so data does reach the port. Untried.
- **Any** change to the package carries the same risk: if the encrypted recovery checks a hash of
  it after erasing `system`, a changed byte bricks the unit as surely as a new image. So a probe
  proves nothing a real image would not; the first write should be the minimal golden image (its
  rootfs edits already run live), and only once a way back exists.

**Without that way back the takeover is not worth it now**: "reset → kit → restore" already brings
a unit back, and the golden package would save only the kit step. Why it stays on the table: the
kit's way in partly depends on vendor services, which may not work forever; a golden package (or a
working USB burn) would make recovery independent of them.

**Still useful at stock risk, not done yet:** one stock reset of a dev unit with marker files in
the rootfs, `/ota` and `/data` (placed on one Station 3, with partition hashes, ext4 UUIDs and the
env recorded before), to learn what besides `system` is wiped, whether the MAC survives, and to
rehearse the restore. orion keeps a **ramoops** console log of the previous boot
(`/sys/fs/pstore/console-ramoops-0`): the first normal boot after a reset may hold the recovery
kernel's log (what it mounted, formatted and wrote), provided nothing reboots the unit again before
it is read.

## Our own reset gesture — in early Linux init, not in U-Boot (a convenience, not a safety net)

A second gesture in U-Boot ("vendor gesture → factory, our gesture → our image") is **not doable**:
U-Boot is signed and encrypted, so we cannot add code. We control only the env scripts. A gesture
there would need a key or GPIO command in this U-Boot build, and its command set is unknown: the
binary is unreadable and the console is `silent=1` with `bootdelay` 0/1. Any env experiment risks
the boot, and the env has no redundant copy. On pickle the vendor gesture itself is probably caught
by the always-on CPU, outside U-Boot.

The same result is available where we already own the code: **the rootfs init**. The kernel is
stock and starts our `S0x` hooks a few seconds after power-on, so:

- **vendor gesture** (mute held while plugging in) stays untouched → stock factory image (pickle:
  `aml_sysrecovery` stays original, so there is **no restore-engine risk at all**);
- **our gesture**, checked by an early hook (before Wi-Fi and before any `<cfw>` code runs), resets to
  the golden CFW. Example gestures: a different key held while plugging in (vol−, or the Alice touch
  key; never mute, which is the vendor gesture), or the mute switch toggled N times within T seconds
  after boot. Read the key state with a tiny probe (`yastationd --probe-keys`, EVIOCGKEY/EVIOCGSW).
  The touch keys (`aw933xx`) and `gpio-keys`/`mute` are kernel-probed before `rcS`;
- the golden CFW lives **read-only in the rootfs**. Free space: pickle ~34 MB, bergamot ~88 MB,
  against ~16.5 MB of CFW. Our reset wipes `<cfw>` (the dev overlay, config, `wpa_supplicant.conf`)
  and lets the golden hooks boot into AP mode;
- **limit:** it only covers failures that leave the rootfs and its early init intact. It does not
  help once we have edited the rootfs itself (deleted vendor files, broken busybox, libc or init).
  That case needs the hard reset, which is why the takeover below matters;
- the same mechanism works on bergamot, where replacing the restore image is impossible anyway.

## Snapshot the current system as the restore image (pickle)

This is the simplest form of the takeover: once the onboard CFW is finished, take the running
system, clear the per-unit state (`<cfw>` config, `wpa_supplicant.conf`, host keys) and make that
the restore payload. It is the same pipeline as in the plan below, with the **live rootfs** as the
source: a file-level copy, then `mkfs.ubifs` + `ubinize`. A raw mtd9 dump does not work: a live UBI
partition fills all 128 MiB with data spread across PEBs by wear levelling, so it cannot be
truncated, and it would not fit next to the other package items.

## Working on the rootfs safely

- Never write `mtd0`–`mtd8` (pickle) or `mtd0`–`mtd2`, `mtd4`, `mtd6`, `mtd7` (bergamot). The reset
  path lives there. `aml_sysrecovery` is written only in the deliberate takeover step.
- "Delete" without deleting first: hide a directory with `mount --bind <empty-dir> <dir>` from a
  hook or by hand. Reboot and check. Only then remove it for real in a `remount,rw` session, with a
  tarball of what was removed kept on the host.
- Do heavy rootfs experiments (freeing space, removing the vendor stack) on pickle once its reset
  lands in the golden CFW. On bergamot the reset lands in stock, and on the 0.6.x-layout unit what it
  lands in is unknown.

## Hardening rootfs work: layers, snapshots, boot counting

Kernel facts (read on the units 2026-10-24):

| | pickle | bergamot |
|---|---|---|
| overlayfs | **yes** | **no** |
| squashfs, loop | yes | yes |
| fuse | no | yes |
| `kernel.panic` | 5 (reboot 5 s after a panic) | 5 |
| free on the rw partition | `/data` ~139 MB | `/bkupgrade` ~169 MB |

UBIFS has no snapshots, and both UBI devices have no free LEBs for a second volume, so all of the
below works at the file level.

1. **Overlay root (pickle).** Change one rootfs file, once: `/sbin/init` becomes a tiny busybox-only
   wrapper (the original is kept as `init.orig`). The wrapper mounts `ubi1:data`, mounts overlayfs
   with lower = the read-only stock rootfs and upper = `/data/overlay/upper`, runs `pivot_root`, and
   then `exec`s the real init. From then on every change, including deleting `maind` or assets, is
   a write or a whiteout in the upper layer, and **the stock rootfs is never modified again**. On any
   error the wrapper `exec`s `init.orig` without the overlay. Rollback = drop the upper dir. This does
   not free space in the lower layer, and it does not need to: our files live in `/data`.
   `S00rw-partitions` skips `/data` when it is already mounted.
2. **Snapshots = copies of the upper layer.** It is small: `/data/overlay/snap-<date>` plus a copy on
   the host. Rolling back to one means telling the wrapper "use snap-X as upper on the next boot".
3. **Boot counter with automatic rollback.** The wrapper increments a counter in `/data` on every
   boot. The CFW clears it once the system is healthy (yastationd up, network up, N minutes
   uptime). After 3 failed boots in a row the wrapper boots **without** the overlay (stock plus the
   SSH hook) or with the last good snapshot. Kernel panics reboot by themselves (`panic=5`), so boot
   loops count too. Side effect: "pull the power 3 times in a row" becomes a manual rollback
   gesture that works with any broken userland, as long as the wrapper itself is intact.
4. **bergamot: hide, don't delete.** No overlayfs, so vendor files are hidden by a hook on every boot
   (`mount --bind <empty-dir> <dir>`), which lasts only until the next reboot. The same init wrapper
   and boot counter decide whether our hooks apply. Space is not an issue (88 MB free in the
   rootfs).
5. **Smaller aids.** Remote syslog (busybox `syslogd -R <log-host>`) or netconsole shows where a boot
   died without SSH. Test a new tree in a `chroot` on `/data` before booting it. UART is the last
   resort for debugging (needs opening the case).

**What stays vulnerable.** The init wrapper is the single rootfs edit. Break it and the kernel finds
no init and boot-loops, which only the hard reset cures (stock on pickle unless the takeover is
done). Keep it tiny, busybox-only, with the `init.orig` fallback, and install it once. The reset
wipes `/data`, snapshots included, so keep copies on the host.

**Recommendation.** pickle: init wrapper + overlay + boot counter + host-side snapshots. That covers
"we deleted maind and bricked it" without touching `aml_sysrecovery`. The `aml_sysrecovery` takeover
becomes an optional later stage whose only gain is that a hard reset lands in the CFW instead of
stock. bergamot: bind-mount hiding + the same wrapper with the boot counter.

None of this is implemented or tried on a device yet.

## Design: three layers

| Layer | Where | What gets you there |
|---|---|---|
| **dev** | `<cfw>` on the rw partition (`/data/cfw`, `/bkupgrade/cfw`) — today's `tools/deploy.sh` | normal |
| **golden** | CFW baked into the rootfs (both models) **and** into `aml_sysrecovery` (pickle) | safe-mode boot (both); hard reset (pickle, wipes `/data`) |
| **stock** | the original dumps on the host (`dumps/pickle/mtd6_aml_sysrecovery.img`, …) | write the original mtd6 back and hard-reset (pickle); hard reset (bergamot) |

After a reset into stock the unit runs the vendor firmware, with the vendor stack, its updater and
no CFW. Keep it off the internet (the router block) until the CFW is reinstalled; see
[deploying.md](../guides/deploying.md).

Golden-rootfs requirements, all of which must work with an **empty** rw partition:
- the maind gate becomes **opt-in** (`run.sh` launches quasar only if `<cfw>/maind_on` exists), not
  `maind_off`-on-`/data`;
- `device_ota_update.sh` = `exit 0`, the `egress.sh lock` hook and the dropbear hook with the
  owner's `authorized_keys` all live in the rootfs. Host keys are generated on first boot into
  `<cfw>`;
- the baked binaries are in `/usr/lib/cfw` (or similar). The init hooks prefer `<cfw>/bin/*` (dev)
  unless safe mode is on;
- **safe mode**: the mute switch ON at boot (read by `yastationd --probe-mute` or similar), or a
  `<cfw>/safe` flag, makes the hooks ignore `<cfw>/bin` and the dev config overrides. This is the
  cheap rollback and keeps Wi-Fi and config;
- with no Wi-Fi config, the CFW's own AP-mode setup takes over.

The golden image contains vendor files and the owner's key: it is **built locally and never
committed or published**. Only the build tooling goes into the repo.

## Plan (pickle first)

**Phase 0 — tooling, host only, no device writes**
1. `tools/amlimg` — parse and repack Amlogic v2 packages. Acceptance: repacking the original items
   reproduces `mtd6_aml_sysrecovery.img` **byte-for-byte**, including the CRC.
2. `tools/mkgolden` — unpack the package `system` (ubi_reader under `fakeroot`), lay the CFW overlay
   on top, `mkfs.ubifs` + `ubinize` with the parameters above (mtd-utils on the host), then rebuild
   the package. Acceptance: building the **unmodified** rootfs through the pipeline gives the same
   file list, modes, owners, links and contents as the original (compared with a read-only
   `find / -xdev` listing from the device); the superblock and volume-table parameters match.
3. Golden-rootfs changes from the list above (opt-in gate, baked hooks, safe mode, AP fallback).

**Phase 1 — prove the overlay on the live rootfs (already common practice)**
4. Apply exactly the golden overlay file set to pickle's live rootfs with `remount,rw` (keep `.orig`
   files), reboot, and verify the hooks with an emptied `<cfw>` (move it aside, not delete) and the
   safe-mode gesture.

**Phase 2 — escape hatch before touching mtd6** (needs the user present, USB cable)
5. Get an Amlogic ADNL host tool working. Confirm that `reboot update` brings the unit into burn mode
   and that the tool identifies it, **without writing**. Find the strap or pad for ROM USB mode in
   case Linux is dead (optional but strongly preferred).
6. Verify the safety net itself: a **stock** hard reset with a marker file in the rootfs. Expect
   stock factory-14, the marker gone and `/data` empty. This proves the reset rewrites `system` from
   `aml_sysrecovery` (so it recovers any rootfs brick) and shows the trigger, timing and wipe
   semantics, all at stock risk. Cost: the unit is back on stock firmware until the CFW is
   reinstalled.

**Phase 3 — the takeover**
7. Stage the golden package in `/data`, check its sha256, keep the original mtd6 dump on the host
   (sha256 in `dumps/pickle/MANIFEST.txt`), then `flash_erase /dev/mtd6 0 0` and
   `nandwrite -p /dev/mtd6 pkg.img`. Read it back and compare hashes. Normal boot does not read
   mtd6, so a failure here is still harmless.
8. Hard reset → expect the golden CFW, empty `/data`, AP mode, SSH with the baked key.
   Rollback: rewrite the original mtd6 and hard-reset; if Linux is dead, USB burn of the original
   package.

**bergamot**: phase 0 step 3 and phase 1 apply as they are (golden overlay in the rootfs, safe mode).
Do **not** hard-reset the 0.6.x-layout unit: what it restores is unknown, and getting the CFW back
onto that firmware is unverified.

## Hazards

- **Both copies bad.** A golden image that does not boot, sitting in mtd6, makes the hard reset
  restore the same broken image. Never put an image in mtd6 that has not been proven live first
  (phase 1). The image pipeline must pass the identity test in phase 0.
- **Restore engine rejects the modified package after it has started erasing** (unreadable recovery
  logic): this is the residual risk. The only cure is USB burn → phase 2 first.
- **Power loss** while writing mtd6 leaves the live system intact; just rewrite. Losing power during
  the reset itself is a vendor-level risk.
- **Env writes** (bergamot lever): a single copy and no redundancy. Avoid.
- **Bad blocks**: mtd6 has none today. `nandwrite -p` skips bad blocks the same way the Amlogic store
  driver reads them. Verify by read-back, never assume.
- **Sizes**: the package must fit in 128 MiB minus bad blocks. The `system` item must keep the same
  UBI geometry. The rootfs volume has autoresize, so a smaller image is fine.
- **Host key change** after every reset: expect the SSH host-key warning.
- The OTA blocks remain mandatory. An OTA would replace the rootfs and possibly the recovery path
  semantics. Whether a pickle OTA rewrites `aml_sysrecovery` is unknown.
