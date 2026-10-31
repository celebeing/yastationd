# Permanent install: taking over the factory reset

Research, 2026-10-24 to 2026-10-31: can the CFW survive a hard reset, by replacing the image the
speaker restores with our own? What was done on real units is marked as such; the rest is static
analysis of the local dumps and read-only reads. How to do it:
[guides/golden-image.md](../guides/golden-image.md).

**Verdict:**

| Board | Restore image | Result |
|---|---|---|
| **pickle** (Mini 3 Pro) | unsigned burn package in `aml_sysrecovery` | **Done.** A reset took a rebuilt package, on one unit, two rounds |
| **orion** (Station 3) | the same design | **Done.** The same, one round on each of two units |
| **bergamot** (Mini 3) | inside the signed, encrypted recovery | **Impossible.** The reset always lands in the vendor's firmware |

With a golden image the vendor gesture becomes a way back from anything that leaves the reset path
alone: whatever we break from Linux (deleted vendor files, bad hooks) lives in `system`, and the reset
path (bootloader, recovery, package) in partitions Linux doesn't write unless told to. It is also
what makes [vendor removal](vendor-removal.md) possible. It does not protect against a bad image:
see [Hazards](#hazards).

## How a hard reset works

- **The gesture:** unplug, hold the **mic-mute button**, plug in, hold ~5 s until the ring turns yellow.
  It happens before Linux: U-Boot or the always-on CPU sees the button. orion's env has
  `readmute=gpio read 24 mute 1`, used by no env script, likely what the compiled-in check reads.
  `maind` has no flash-level reset (its `factoryReset` covers configs, counters, Zigbee and Matter),
  so gating it does not disable the gesture.
- **The env** is reachable from Linux (`fw_printenv`/`fw_setenv`; pickle: `/dev/nand_env`, a single
  copy, no redundant one). `storeboot` → `switch_bootmode` → `get_rebootmode`:
  - pickle: `reboot_mode=factory_reset` runs `recovery_from_flash` (`imgread kernel recovery … ; bootm`);
  - orion: it runs **`fr`**, a command compiled into U-Boot; `update` and `recovery_quiescent` run
    `recovery_from_flash`;
  - bergamot additionally runs a compiled-in `yandex_io_check_recovery` from `bootcmd`.
- **Secure boot** is on (`AMLSECU!` headers, `forUpgrade_secureBoot=true`). U-Boot, the kernel, the DTB
  and the recovery's kernel and ramdisk are signed and encrypted with the SoC key: they cannot be read
  or replaced. **The rootfs is not authenticated** (no dm-verity; orion: `avb2=0`), which is why rootfs
  edits work at all.

### What a reset does, measured

By markers in the rootfs and the writable partitions, partition hashes and UUIDs before and after:

| | pickle | orion | bergamot |
|---|---|---|---|
| `system` | rewritten from the package | rewritten from the package | very likely kept (below) |
| writable partition | `/data` wiped | `/ota` and `/data` reformatted, new UUIDs | `/data` kept; pairings survive |
| `aml_sysrecovery` | untouched | untouched | n/a |
| `boot`, `recovery`, env | unchanged | unchanged (`misc` too) | not measured |
| `bootloader` | unchanged (`mtd0`–`mtd5`) | changed on one unit of two | not measured |
| `reserved` | n/a | two copies of a 182 KB encrypted-looking block rewritten (`0x413600`, `0x453600`), both units | not measured |
| Wi-Fi MAC | kept | kept | not measured |
| dhcpcd's DUID, Bluetooth | regenerated | regenerated | kept |
| network ADB marker | gone | gone | not measured |
| time to Linux | ~2 min | a few minutes (at most ~3) | not measured |

Whether a reset with the stock package also touches `bootloader` and `reserved` on orion is not
known: both resets there ran on rebuilt packages, and their bootloader items are byte-identical to
stock. The second-to-last row matters: the reset **closes** network ADB, and a restore brings it back
with the rest of `/data`.

`/sys/fs/pstore` was empty after a reset: the gesture is a cold start, and ramoops doesn't outlive
one. `cache` and `misc` hold no record of earlier runs either. The recovery's own log stays unseen.

**bergamot's** column is from file dates, without a test. Both units were reset at least once, yet
the oldest vendor files in `/data` date from the factory day on one and the first setup on the
other, pairings included, so `/data` is not reformatted. One unit runs firmware that came by an
update and was reset after it, yet still runs it, so the reset doesn't return to the factory image.
Whether rootfs edits survive was not seen. The 0.6.x-layout unit is unverified: don't reset it.

The published notes disagree with one another and with us. Community notes say a button reset clears
`/data` and keeps rootfs edits (fits bergamot); colleagues report the rootfs comes back stock (fits
pickle and orion). The models differ, nobody named them.

## The restore package

Both boards, the same design, checked on dumps of each: an **Amlogic image v2 burn package** (magic
`0x27b51956`) of 19 items: `DDR`/`UBOOT` and their `_ENC`, `_aml_dtb`, `aml_sysrecovery` (a
self-reference: offset 0, the whole size), `boot`, `bootloader`, `meson1(_ENC)` dtb, `platform` conf,
`recovery`, **`system`**, `usb_flow`, one `VERIFY` per partition item.

- **Integrity recomputes:** each `VERIFY` is the text `sha1sum <sha1 of the item>`, the header CRC is
  `~crc32(image[4:size])`. There is no signature item. The signed items stay byte-identical.
- `boot` and `recovery` are byte-identical prefixes of the live partitions.
- Items after `system` are kept on 4 bytes, no more (orion's `VERIFY` sits at `…b74`).

| | pickle | orion |
|---|---|---|
| Partition | `mtd6`, 128 MiB NAND | `aml_sysrecovery`, 1.5 GiB eMMC |
| Stock package | 113 392 448 B | 629 930 824 B (identical on both units) |
| `system` item | UBI, 79 953 920 B | Android sparse (file type 254), 262 144 blocks of 4 KiB, a 1 GiB ext4 |
| Geometry | PEB 256 KiB, min I/O 4096, VID 4096, data 8192, LEB 253 952; UBIFS `-c 1120`, zlib, fanout 8, log 4, `fmt 4`; volume `rootfs`, autoresize | features, UUID, size read from the stock image |
| Golden image | 127 MB of 134 | 661 MB of 1610 |
| xattrs | none needed (no SELinux in the kernel, despite the cmdline) | none in stock |

Hence `tools/golden.sh` reads the geometry from the item it replaces and writes nothing about a board.

**Updates leave the package alone.** maind downloads a `.swu`, `device_ota_update.sh` →
`yandex-apply-ota` → `yandex-reboot update`, and the recovery's `S01swupdate` installs it. What gets
written is decided by the `sw-description` inside each signed `.swu`: the Station 2's, the only one on
hand, writes the rootfs, `boot`, `dtb` and the bootloader copies. No script or binary in the rootfs
mentions `aml_sysrecovery`. Amlogic's design: the package is the factory image, a reset returns to it
and the unit then updates itself. Our units have never been updated (`system` equalled the
package's). Whether an update could rewrite the package is unseen; the update blocks stay regardless.

**The recovery can't be changed, only its inputs:**

| Input | Signed | Usable |
|---|---|---|
| the package in `aml_sysrecovery` | no | **yes: the lever** |
| `/ota/yandex_io_ota.swu` | yes, by a key inside the recovery | no: the kit's `ota_script.pub` swap is in the rootfs, which the recovery doesn't read |
| U-Boot env | no | in theory; one copy, a mistake can stop the boot. Not touched |
| `misc` | no | unknown whether it is read; zeros on the unit |

**What others published** (searched 2026-10-31). Amlogic's public U-Boot (old, not these SoCs): its
`aml_sysrecovery` command checks only magic, version and item count, the whole-image check is a
stub, it skips `bootloader` by default and verifies each item's sha1 after writing it. No public
report of anyone replacing this package, success or brick. Our runs are the first we know of.

## What was done

### The design: a seed

The rootfs gets additions, not changes: the CFW in `/usr/share/cfw-seed` and a hook `S00zcfwseed`
right after `S00rw-partitions`, which fills an empty `<cfw>`. Everything else (the SSH hook, the maind
gate, the egress lock) finds `<cfw>` as usual, so a golden image needs no other logic. Stock `S40wifi`
copies an empty `/etc/wpa_supplicant.conf` into the Wi-Fi config, and the daemon then finds no
network and enters setup mode by itself. That is also why the "minimal image" idea fell away: a unit
without Wi-Fi would need the vendor's BLE onboarding, which needs the internet. Only what is in the
package survives a reset, so a golden image worth having is the whole CFW.

### The takeover on pickle, tried (2026-10-31)

**First round, a snapshot of the unit** (keys, Wi-Fi, identity in the seed): by the owner's choice,
with no way back from outside if the recovery refused. Each step before the next:

1. a backup; hashes of `mtd0`–`mtd8` and the env, all matching the dump;
2. hook and seed on the live rootfs, then a real boot with `/data/cfw` and Wi-Fi moved aside: SSH back
   in 9 s from the seed;
3. a package from that rootfs, differing only in a marker and a new UBI `image_seq`, which says at a
   glance where `system` came from;
4. the packer first reproducing the stock package byte for byte; superblock and UBI headers matching
   stock's; the result unpacked and compared entry by entry;
5. `flash_erase` + `nandwrite -p`, a read-back equal; then the reset.

The recovery took it. So it checks no hash beyond what we recompute, and a `system` of another size
is fine (305 → 334 PEBs; autoresize grew the volume from 332 to 468 LEBs on first attach).

What did not come back was what lives in `/data` outside `<cfw>`. The MAC stayed, yet the router gave
another lease: dhcpcd's client id is `ff` + IAID + DUID, and the DUID is regenerated on an empty
`/data`. HA then couldn't reach the satellite, which showed as "no connection to HA".

**Second round, an image for a board** (no key, Wi-Fi or settings): that is `tools/golden.sh`. The
daemon enters setup mode with no saved network, and the page takes SSH keys. First-boot logic had
changed and a mistake there can't be cured by a reset, so it was proved **while `mtd6` still held a
working image**: the new hook and seed on the live rootfs, `<cfw>` and Wi-Fi moved aside, a temporary
hook collecting the result, putting everything back and rebooting by itself after 2 minutes. Then the
write and a reset: the access point came up, a phone added a key and the network, and a backup brought
the unit's identity back with no change on the HA side.

### orion (2026-10-31)

The same on two units, the second the one with `voice_store`: a live first-boot test (with the egress
lock seen `LOCKED` in the access point), the owner's key copied to `<cfw>/authorized_keys`, the write
with `dd`, a normal reboot unchanged, the reset, the access point, a key and Wi-Fi from a phone, a
restore. Differences found on the way: `system` is rebuilt with `mke2fs -d` + `img2simg` from stock's
features and UUID, with raw and fill chunks where stock also has one "don't care" chunk; `debugfs
rdump` loses setuid and sticky bits, so the check reads those from the image; and the guard for an
unmounted `<cfw>` must look at `/ota`, not `/data`.

Details and the steps: [guides/golden-image.md](../guides/golden-image.md#orion).

## orion — the restore payload is replaceable too

**Layout.** One eMMC, partitions by name under `/sys/class/block`: `bootloader` (+ `mmcblk0boot0/1`),
`reserved` (64 MiB), `cache`, `env` (8 MiB), `misc`, `tee` (48 MiB, `ro` at `/tee`),
`aml_sysrecovery` (1.5 GiB), `boot` (64 MiB), `recovery` (192 MiB), `system` (1 GiB ext4, the root,
`root=PARTLABEL=system ro`), `ota` (1.5 GiB ext4, `/ota` = `/bkupgrade`), `data` (2.8 GiB ext4).

**What else the env holds.** `bootloader_wp=1`, `rabbit_hole_debug=0` → `androidboot.rh_unlock=0`
(meaning unknown), `usb_burning=adnl 1200`.

**ADB.** `onboot.sh` (from the boot hook, before `run.sh`, so even with maind gated) calls
`activate_adb.sh check`, which turns ADB on when a marker exists in `/data`
(`quasar_activate_adb_{sound,quasmodrom,glagol}`, `quasar_activate_network_adb` for the network).
maind isn't needed for it. A reset empties `/data` and closes it; a restore of `/data` reopens it.

## bergamot — the restore payload is not replaceable

- The recovery is one Android boot image spanning `recovery` + `recovery_fs` on the 0.369-layout unit:
  kernel 5.8 MB + ramdisk 30 MB, `AMLSECU!`, encrypted (entropy 8.0), cmdline `rootfstype=ramfs
  ubi.mtd=system ubi.mtd=bkupgrade`. Whatever it restores is in that ramdisk; no plaintext copy on
  the flash.
- The 0.6.x-layout unit has another table: one `recovery` of 58.75 MiB, no `recovery_fs`.
- Inside the recovery, `S01swupdate` installs `/bkupgrade/ota/yandex_io_ota.swu` when
  `boot_to_recovery=1`. This needs a Yandex signature.
- One lever, parked: rewrite `recovery_from_flash` in the env to boot the normal kernel with a safe
  flag. It works only if the compiled-in check goes through that variable, a bad env write can stop
  the boot, and it removes the vendor's escape hatch.

## Hazards

- **Both copies bad.** A golden image that gives no way in is restored by every reset, and there is no
  other way to write one. Never write an image whose first boot hasn't been proved live on that
  board, while a good package is still in the partition.
- **No USB burn.** Nothing has reached burn mode from outside. `usb_burning=adnl 1200` exists, no env
  script runs it, the boot ROM enters USB mode only with a broken bootloader or a strap; from a
  running Linux it would be `reboot update`. On orion, reaching it takes an env edit, or feeding the
  unit 20 V PD from its own supply while passing USB data to a computer (a PC port doesn't power it).
  The case is glued, so UART and pads are out: the recovery's logic stays unread.
- **The recovery might differ between firmware versions.** Two boards and three units took a rebuilt
  package; every one runs the firmware of its own package.
- **Power loss** while writing the partition leaves the live system intact: write again. During the
  reset itself it is a vendor-level risk, with a long window.
- **Bad blocks** (NAND): `flash` refuses a partition with any.
- **Host keys and the DUID change** with a reset, until a restore: expect the SSH warning, and a new
  lease if the router binds by client id.
- **A restore reintroduces what a backup holds**, an older `<cfw>`, network ADB, keys since removed.
- **The update blocks stay mandatory:** an update would replace the rootfs, and whether it can touch
  the package is unseen.

## Working on the rootfs safely

- Never write pickle's `mtd0`–`mtd5`, `mtd7`, `mtd8`, or bergamot's `mtd0`–`mtd2`, `mtd4`, `mtd6`,
  `mtd7`. `aml_sysrecovery` is written only by `tools/golden.sh flash`.
- "Delete" without deleting first: hide a directory with `mount --bind <empty-dir> <dir>`, reboot and
  check; then remove it in a `remount,rw` session with a tarball on the host.
- Heavy rootfs work belongs on a unit with a golden image. On bergamot the reset lands in stock.

## Ideas not built

Kept for the reasoning; none was needed for what is above.

- **A second gesture in U-Boot:** not doable. U-Boot is signed, its command set unknown, the env single.
- **A gesture in early Linux init:** a different key held at power-on, read by a hook before Wi-Fi,
  resetting `<cfw>` to a read-only copy in the rootfs. With the seed that copy exists, but it covers
  only failures that leave the rootfs intact, and the Wi-Fi setup combo and SSH cover most of those.
- **A safe mode** (the mute switch at boot makes hooks ignore `<cfw>/bin`): the same limit.
- **An overlay root on pickle:** `/sbin/init` as a tiny wrapper mounting overlayfs with the rootfs below
  and `/data/overlay/upper` above, so the rootfs is never written again, with a boot counter rolling back
  after 3 failed boots. pickle's kernel has overlayfs, bergamot's doesn't. The wrapper itself is the one
  edit that can brick. A golden image gives the same safety by a longer road, so it stayed on paper.
- **bergamot:** the same counter with bind-mount hiding, which lasts until the next reboot.

Kernel facts behind these (2026-10-24): overlayfs on pickle only; squashfs and loop on both; fuse on
bergamot only; `kernel.panic` 5 on both. UBIFS has no snapshots, and neither UBI device has free LEBs
for a second volume.
