# emu — QEMU emulator for CFW speakers

Boots the **vendor rootfs from our own NAND dumps** under `qemu-system-aarch64 -M virt` with a
substitute kernel ([`kernel/`](kernel/)), runs its stock BusyBox init and our CFW init hooks, and
checks that `yastationd` comes up and stays up. It exists to rehearse boot-path experiments
(init wrapper, overlay root, boot counters, power cuts; see
[permanent-install.md](../docs/permanent-install.md)) before anyone tries them on a real speaker.
Boards: **bergamot** (Mini 3, fw 0.369) and **pickle** (Mini 3 Pro).

Nothing here talks to a real speaker except `seed-from-unit.sh`, and that only reads.

## Quick start

```sh
# once: the kernel (emu/kernel/README.md) and the maintenance initramfs
emu/kernel/build.sh                      # -> emu/work/out/Image
emu/maint/build.sh                       # -> emu/work/maint/initramfs.cpio.gz (run.sh does it too)

# a twin of a real unit (read-only on the speaker)
emu/seed-from-unit.sh bergamot root@<speaker-ip> -- -i <key> -o IdentitiesOnly=yes

# smoke test: fresh flash -> provision -> boot -> checks -> PASS/FAIL
emu/test/boot-smoke.sh bergamot                 # add --power-cycle for the power-cut variant

# by hand
emu/run.sh bergamot --fresh --maint --auto      # fresh flash + install the seed
emu/run.sh bergamot                             # boot it; serial on this terminal (Ctrl-A x quits)
ssh -p 2222 -i emu/work/bergamot/seed/emu_ssh_key root@127.0.0.1
emu/powercut.sh bergamot                        # pull the plug and power on again
```

Host needs: `qemu-system-aarch64` (Debian/Ubuntu `qemu-system-arm`), `aarch64-linux-gnu-gcc`,
`cpio`, `python3`, `ssh`. No root.

## Pieces

| Path | What |
|---|---|
| `boards/<board>.conf` | partition table (name, size, source dump), UBI/root args, RAM, userland arch, `<cfw>`, rw volume, reference rootfs for the seed diff |
| `lib/common.sh` | shared helpers; **the kernel cmdline contract** (block2mtd/emuboard syntax) lives in one block there |
| `run.sh` | creates the flash images on first use (`--fresh` recreates them), builds the cmdline, starts QEMU |
| `powercut.sh` | SIGKILLs QEMU (no guest shutdown, guest caches lost) and boots again with the same files |
| `maint/` | maintenance initramfs: static BusyBox 1.36.1 (tarball sha256-pinned in `build.sh`) + `/init` |
| `seed-from-unit.sh` | read-only copy of a unit's CFW state into `work/<board>/seed/` |
| `lib/rootfs_diff.py` | unit rootfs manifest vs the dump's rootfs → what the CFW added, changed, removed |
| `lib/provision.sh` | copied into the seed; installs it from maintenance mode |
| `test/boot-smoke.sh` | the smoke test (optionally with a power cut) |
| `work/` | **gitignored**: kernel build, images, seeds, results |

### Flash

One raw file per MTD partition, `work/<board>/flash/mtd<N>-<name>.img`, in the board's
`/proc/mtd` order, each attached as a `virtio-blk-pci` disk at a fixed PCI slot (so `/dev/vda` is
always mtd0). The kernel's patched `block2mtd` turns each disk into an MTD device with the real
name, 256 KiB erase blocks and 4 KiB pages, so the stock cmdline (`ubi.mtd=system …
root=ubi0:rootfs`) and the fstab (`/dev/ubi1_0`, `ubi1:data`) work unchanged.

- `system` is a copy of the dump (bergamot `dumps/mtd/mtd3.img`, pickle
  `dumps/pickle/mtd9_system.img`). `dumps/` is never written.
- Everything else is blank NAND (`0xff`). The rw partition (bergamot `bkupgrade`, pickle `data`)
  starts empty: maintenance mode creates its volume the way the vendor does (`ubimkvol -N <name> -m`),
  and on pickle `S00rw-partitions` would do it too on a normal boot.
- The images persist between runs: that *is* the flash. `--fresh` resets them to the dumps.

### Maintenance mode = the "UART"

`run.sh <board> --maint` boots `maint/` as an initramfs (`rdinit=/init`) on the same flash. It
mounts the rootfs volume **rw** on `/mnt/root` and the rw volume on `/mnt/rw` (bind-mounted at
its real path inside `/mnt/root`, so `chroot /mnt/root /bin/sh` gives the vendor userland), mounts
the 9p share `work/<board>/seed` on `/mnt/seed`, runs `seed/provision.sh` if present, and then
either gives a shell on the console or, with `--auto` (`emu.auto=1`), syncs and powers off. Use it
to edit or inspect the emulated flash, e.g. install an init wrapper before a normal boot.

### Seed = a twin of a real unit

`seed-from-unit.sh <board> <ssh-target>` runs only `find`/`md5sum`/`readlink`/`cat`/`tar c` on the
speaker, writing to stdout. It compares the unit's rootfs (file hashes, links) with the dump's
rootfs (`REF_ROOTFS`, a tar or an extracted tree) and takes:

- the rootfs files the CFW added or changed, and always `/etc/init.d/S??z*`, the gated
  `/system/vendor/quasar/run.sh` (+ `.orig`) and `device_ota_update.sh`; files the unit no longer
  has are removed (e.g. hooks moved to `<cfw>/disabled-initd/`);
- `<cfw>` and the board's `SEED_RW_EXTRA` (the rest of the rw partition), without
  `wpa_supplicant.conf*`;
- the unit's `yastationd.conf` with emulator overrides: `device_id = emu-<board>`, no broker
  (`mqtt_host =`, no probes), `wake_enabled = false` (no mics; `--keep-wake` keeps it),
  `sendspin_server`/`ntp_server` dropped. `--broker` points it at `10.0.2.100:1883` instead,
  which `run.sh --broker <hostport>` forwards to a test broker on the host's loopback;
- a fresh emulator SSH key pair; provision adds the public half to root's `authorized_keys`.

`provision.sh` refuses to finish unless `run.sh` is gated, `<cfw>/maind_off` exists and the OTA
script is `exit 0`, as on the real unit. **The seed holds secrets** (host keys, `authorized_keys`,
any broker credentials): it lives in gitignored `work/` and is never committed or shared.

### Runner

`run.sh <board> [--maint [--auto]] [--fresh] [--ssh-port N] [--headless] [--log FILE] [--fwd H:G]
[--broker H] [--dry-run]`. The cmdline is `console=ttyAMA0 emuboard=<board>` + the block2mtd
parameters + the board's UBI/root arguments + its `KERNEL_EXTRA` (dummy PCM count, no fake Wi-Fi) +
`ip=dhcp panic=5`. QEMU gets `-cpu cortex-a53`
(AArch32 EL0 for bergamot's armhf userland), the board's RAM, an `i6300esb` watchdog, `virtio-rng`,
virtio-net and a QMP socket. SSH: `127.0.0.1:2222` (bergamot), `:2223` (pickle). `--headless` sends the
serial console to `work/<board>/serial.log` (or `--log`) and backgrounds QEMU. The vendor console
getty is locked, so use SSH or maintenance mode, not a console login.

`powercut.sh <board> [--no-restart] [--off S]` kills QEMU with SIGKILL. Writes QEMU already made to
the image files survive (like programmed NAND pages); whatever the guest still had in its own
caches is lost. It then boots again (headless) with the last normal-boot options.

### Smoke test

`test/boot-smoke.sh <board> [--power-cycle] [--stable S] [--boot-timeout S] [--no-fresh]
[--keep-running]`: fresh images → maintenance boot with provision → normal boot → wait for SSH →
checks → results in `work/<board>/results/<stamp>/` (`summary.txt`, serial and maintenance logs,
guest `dmesg`, `ps`, syslog, the yastationd log, mounts). Checks:

- one kernel boot in the serial log (no reboot loop), no kernel panic;
- every `S??z*` hook left its trace (dropbear: we are connected; yastationd: live supervisor;
  egress: its log, so rcS reached the end; adbusb: its log);
- no vendor stack processes (`maind`, `quasar_launcher2`);
- `yastationd` (found via `/proc/*/exe`) alive with the same PID for `--stable` seconds
  (default 120) and at most one exit in its log, no supervisor rollback notes.

`--power-cycle` then cuts the power, boots again and repeats the checks.

## What is stubbed, what is not

The kernel side (`kernel/`) provides block2mtd flash, `emuboard` (board LEDs and input devices,
`/sys/class/aml_info/reboot_reason`, debugfs controls), `snd-dummy`, the `i6300esb` watchdog,
DHCP and 9p. Not emulated: Wi-Fi/BT chips (vendor Wi-Fi/BT/dhcpcd scripts fail; the guest has a
virtio NIC instead), the real audio codec/amp and its mixer controls, the mics, TEE/OP-TEE,
`unifykeys`, the USB-C PD controller, the light sensor, the LED framebuffer (`ledfb`), the USB
gadget, the U-Boot env and the bootloader/recovery path (those partitions are blank). Real timing
is not emulated either: TCG is several times slower than the SoC.

## Safety

- **Network is `-netdev user,restrict=on` only.** The guest reaches neither the internet nor the
  LAN; the only paths are the host-loopback forwards run.sh sets up (SSH, `--fwd`, `--broker`).
  The vendor code must never reach the internet, and the twin must never talk to the real broker
  or Home Assistant (it would collide with the real unit's entities). Do not loosen this.
- The vendor stack stays gated exactly as on the unit (provision checks it).
- Images, seeds and results stay in gitignored `work/`. They contain vendor firmware and
  per-unit secrets: never commit, upload or share them.
- `dumps/` is read only; images are copies.

## Verified (2026-10-24, QEMU 6.2 TCG)

Both boards, seeded from their real units: `test/boot-smoke.sh <board> --power-cycle` passes.
Maintenance + provision takes ~11 s; SSH is up 7–12 s (bergamot) / ~21 s (pickle) after power-on;
rcS runs to `S99zzegress`; `yastationd` auto-detects its board (emuboard sets the DT compatible),
runs local-only and keeps its PID for 120 s with no restart; after a SIGKILL power cut UBIFS
replays the rw volume's journal ("recovery needed / completed") and the second boot passes the
same checks. With a default route added by hand in the guest, every outbound connection (internet,
LAN, the host) is refused by QEMU's restricted network.

What the vendor init does under emulation (all non-fatal): Wi-Fi/BT scripts fail (no SDIO chip,
no `pcba_ids`), `unifykeys` writes fail (`S61leds_brightness`, pickle `S80light-sensor`,
`yandex-elevator`), `S90acm8625-settings` finds no amp on I2C, `unbound` fails to start, `modprobe`
finds no modules, `dhcpcd` cannot write its state to the read-only rootfs and leaves **no IPv4
default route**, and `onboot.sh` runs `maind` twice as a tool (`maind --build-timestamp` in
`set_monotonic_time.sh`, `q save-last-monotonic-time`). Both abort with "Can not obtain deviceId"
(on the real unit they presumably succeed). The `maind_off` gate only stops the
`quasar_launcher2` stack, not these two tool calls.

## Known gaps

- bergamot's real kernel is 4.9 without overlayfs; the emulator kernel is 5.4 for both boards and has
  overlayfs. A bergamot rehearsal must not rely on overlayfs.
- The bergamot reference rootfs is `dumps/rootfs-full.tar`, taken minutes before the mtd3 dump; a
  file changed in between and identical on the unit could be missed (the always-copied hooks and
  guards are not affected).
- Flash semantics are simplified (kernel README): block2mtd is write-through, so a power cut lands
  between whole MTD operations; there are no torn pages, no bad blocks, and a write overwrites
  instead of AND-ing bits. Power-cut tests therefore show UBI/UBIFS journal recovery, not NAND
  corner cases.
- The watchdog daemon (`watchdog -t 1 -T 5`) feeds the i6300esb: a TCG stall over 5 s resets the
  guest.
