# emu/kernel — guest kernel for the QEMU speaker emulator

One arm64 kernel (Linux **5.4.283**) for QEMU `virt` that boots the **vendor rootfs from the NAND
dumps** of bergamot (Station Mini 3) and pickle (Station Mini 3 Pro). The vendor init and the CFW
hooks run on it. It is **not** a SoC emulation. The flash is emulated faithfully enough for UBI/UBIFS.
The board hardware is reduced to stubs that have the right names in sysfs/devfs.

```
emu/kernel/build.sh          # fetch + verify + patch + configure + build -> emu/work/out/Image
emu/kernel/build.sh config   # stop after configuring (emu/work/build/.config)
emu/kernel/build.sh clean    # drop emu/work/src + emu/work/build (keeps the download)
```

| Path | What |
|---|---|
| `build.sh` | Pins the version and the tarball sha256. Also checks the hash against kernel.org's `sha256sums.asc` (and its signature, when the kernel.org autosigner key is in your keyring). Re-extracts the tree whenever `patches/` changes. Build metadata is fixed (`KBUILD_BUILD_*`). |
| `emu.config` | Config fragment applied on top of `allnoconfig` (`KCONFIG_ALLCONFIG`, with `EXPERT` off, so the standard syscalls/tty/block layer keep their default-on values). The script fails if any line in the fragment did not survive dependency resolution. |
| `patches/0001-block2mtd-…` | block2mtd: per-device name + writesize, several devices when built in, configurable wait, write-through. |
| `patches/0002-snd-dummy-…` | snd-dummy: 1..8 channels (vendor capture paths are 8ch / 4ch). |
| `patches/0003-emuboard-kbuild.patch` | Hooks `drivers/misc/emuboard/` into Kbuild. |
| `emuboard/` | The board stub driver, copied into the tree by `build.sh` on every run. |

Outputs (gitignored): `emu/work/out/{Image,config,System.map}`. Sources: `emu/work/src/`.
Build dir: `emu/work/build/`. Toolchain: `aarch64-linux-gnu-` (override with `CROSS_COMPILE`). Everything is built in. `CONFIG_MODULES` is off because the vendor rootfs has no modules for this kernel, so its `modprobe` calls fail harmlessly.

## Differences from the real kernels

- **bergamot runs 4.9.68 on the real unit, and that kernel has no overlayfs.** This kernel has
  `OVERLAY_FS=y`. The runner can't hide it, so **bergamot tests must not rely on overlayfs**. pickle's real
  5.4.283 kernel does have it.
- 32-bit EL0 (`COMPAT`) plus the deprecated-instruction emulation (SWP, CP15 barriers, SETEND) and kuser
  helpers. These cover bergamot's armhf userland. QEMU `-cpu cortex-a53` provides AArch32 EL0 under TCG.
  There is no compat vDSO (it needs a 32-bit cross compiler), so 32-bit time calls fall back to syscalls.
- No seccomp: bergamot's real kernel has none either (`dhcpcd` logs "sandbox unavailable").
- The root DT `compatible`/`model` are **rewritten by emuboard** at boot, so
  `/proc/device-tree/compatible` reads `yandex,bergamot-evt1 yandex,bergamot amlogic, axg`
  (pickle: `yandex,pickle-evt yandex,pickle amlogic, a4`). That is how yastationd auto-detects the board.
  The rest of the tree is QEMU's `virt` tree.

## Flash: one virtio disk per MTD partition

Each MTD partition of the real board is a **raw file** attached as a `virtio-blk-pci` disk. The patched
built-in `block2mtd` turns them into `mtd0..mtdN`, with the real names, erase size 256 KiB, and **writesize
4096**. The dumps' UBI images have VID header 4096, data offset 8192, and UBIFS min I/O 4096. Stock
block2mtd reports writesize 1, which UBI would turn into a 64-byte VID offset, so these images would not attach
without the patch. With the patch, `ubi0: min./max. I/O unit sizes: 4096/4096, VID header offset: 4096, data offset: 8192`, the same as on the
units.

Rules for the runner:

- **File size = partition size, exactly** (a multiple of 256 KiB). Pad partial dumps with `0xff`, never
  `0x00`: `0xff` is erased flash. An all-`0xff` file is an empty partition (UBI auto-formats it as an empty UBI
  device with no volumes).
- Give the disks **ascending PCI slots in mtd order** (`addr=0x2`, `0x3`, …). PCI probe order assigns
  `vda`, `vdb`, … in slot order, so the slot order fixes the `/dev/vdX` → mtdN mapping. Other virtio-blk disks (scratch, etc.) go
  in higher slots. Don't use virtio-mmio disks for flash: their enumeration order is reversed.
- block2mtd registers the devices in **command-line order**, and nothing else in this kernel registers an MTD
  (no physmap/CFI), so mtdN matches the real board.
- Writes are **write-through** (`block2mtd.sync=1`, the default). An erase or write is on the virtio disk,
  and so in the host file, before it returns. Killing QEMU (`kill -9`, `timeout -s KILL`) therefore behaves
  like a power cut after the last completed flash operation. UBIFS replays its journal on the next mount
  ("recovery needed / recovery completed"). Use the default QEMU `cache=writeback`: the host page cache
  survives a QEMU kill.
- `block2mtd.timeout=<s>` (default 3) is how long each device is waited for, polled every 100 ms. With
  `virtio-blk-pci` the disks already exist when block2mtd's `late_initcall` runs, so this is only a backstop.
  block2mtd runs before `ubi_init` (same initcall level, earlier in link order), so `ubi.mtd=<name>`
  always finds the partitions.

block2mtd parameter syntax (repeat once per partition, in mtd order):

```
block2mtd.block2mtd=<dev>,<erasesize>,<name>,<writesize>      e.g. /dev/vdd,256KiB,system,4096
```

### bergamot (8 partitions)

| mtd | name | size | dump |
|---|---|---|---|
| 0 | bootloader | 0x00400000 (4 MiB) | `dumps/mtd/mtd0.img` is only 520 KiB (NAND-protected); pad with 0xff |
| 1 | tpl | 0x00600000 | `mtd1.img` |
| 2 | boot | 0x01400000 | `mtd2.img` |
| 3 | system | 0x0b000000 (176 MiB) | `mtd3.img`: UBI, volume `rootfs` |
| 4 | nv | 0x00200000 | `mtd4.img`: jffs2, mounted at `/tee` (`/dev/mtdblock4`) |
| 5 | bkupgrade | 0x0e880000 (232 MiB) | not dumped. UBI volume `ubi1_0` → `/bkupgrade` (the CFW root) |
| 6 | recovery | 0x00800000 | `mtd6.img` |
| 7 | recovery_fs | 0x03380000 | `mtd7.img` |

```
console=ttyAMA0 emuboard=bergamot
block2mtd.block2mtd=/dev/vda,256KiB,bootloader,4096 block2mtd.block2mtd=/dev/vdb,256KiB,tpl,4096
block2mtd.block2mtd=/dev/vdc,256KiB,boot,4096 block2mtd.block2mtd=/dev/vdd,256KiB,system,4096
block2mtd.block2mtd=/dev/vde,256KiB,nv,4096 block2mtd.block2mtd=/dev/vdf,256KiB,bkupgrade,4096
block2mtd.block2mtd=/dev/vdg,256KiB,recovery,4096 block2mtd.block2mtd=/dev/vdh,256KiB,recovery_fs,4096
ubi.mtd=system ubi.mtd=bkupgrade root=ubi0:rootfs rootfstype=ubifs ro
snd_dummy.pcm_devs=2 mac80211_hwsim.radios=0 panic=5
```

### pickle (11 partitions)

| mtd | name | size | dump (`dumps/pickle/`) |
|---|---|---|---|
| 0 | bootloader | 4 MiB | `mtd0_bootloader.img` |
| 1 | bl2e | 2 MiB | `mtd1_bl2e.img` |
| 2 | bl2x | 2 MiB | `mtd2_bl2x.img` |
| 3 | ddrfip | 2 MiB | `mtd3_ddrfip.img` |
| 4 | tpl | 12 MiB | `mtd4_tpl.img` |
| 5 | tee | 2 MiB | `mtd5_tee.img` (all 0xff) |
| 6 | aml_sysrecovery | 128 MiB | `mtd6_aml_sysrecovery.img` |
| 7 | recovery | 16 MiB | `mtd7_recovery.img` |
| 8 | boot | 16 MiB | `mtd8_boot.img` |
| 9 | system | 128 MiB | `mtd9_system.img`: UBI `rootfs` |
| 10 | data | 188 MiB (197132288) | `mtd10_data.img`: UBI `data` → `/data` (the CFW root). It was dumped while mounted, so the first mount replays the journal. |

```
console=ttyAMA0 emuboard=pickle
block2mtd.block2mtd=/dev/vda,256KiB,bootloader,4096 block2mtd.block2mtd=/dev/vdb,256KiB,bl2e,4096
block2mtd.block2mtd=/dev/vdc,256KiB,bl2x,4096 block2mtd.block2mtd=/dev/vdd,256KiB,ddrfip,4096
block2mtd.block2mtd=/dev/vde,256KiB,tpl,4096 block2mtd.block2mtd=/dev/vdf,256KiB,tee,4096
block2mtd.block2mtd=/dev/vdg,256KiB,aml_sysrecovery,4096 block2mtd.block2mtd=/dev/vdh,256KiB,recovery,4096
block2mtd.block2mtd=/dev/vdi,256KiB,boot,4096 block2mtd.block2mtd=/dev/vdj,256KiB,system,4096
block2mtd.block2mtd=/dev/vdk,256KiB,data,4096
ubi.mtd=system root=ubi0:rootfs rootfstype=ubifs ro ubi.mtd=data
snd_dummy.pcm_devs=3 mac80211_hwsim.radios=0 panic=5
```

QEMU side (both boards):

```
qemu-system-aarch64 -M virt -cpu cortex-a53 -smp 2 -m 256|512 -nographic -device i6300esb \
  -kernel emu/work/out/Image -append "<cmdline>" \
  -drive file=bootloader.img,if=none,format=raw,id=bootloader -device virtio-blk-pci,drive=bootloader,addr=0x2 \
  -drive file=tpl.img,if=none,format=raw,id=tpl -device virtio-blk-pci,drive=tpl,addr=0x3  ...
```

`-device i6300esb` gives `/dev/watchdog`. bergamot's `S01watchdog` runs `watchdog -t 1 -T 5` on it, so a
guest that hangs for more than 5 s gets reset. Use `-no-reboot` to turn that reset into a QEMU exit.

## Other cmdline knobs

| Parameter | Default | Meaning |
|---|---|---|
| `emuboard=<bergamot\|pickle>` | none | Selects the board stubs. Without it emuboard does nothing. |
| `emuboard.reboot_reason=<s>` | `cold_boot` | Initial `/sys/class/aml_info/reboot_reason`. The real unit reports `normal` after a software reboot; `S98pstore-clear` keys off it. The runner sets it on warm restarts. |
| `snd_dummy.pcm_devs=<n>` | 1 | PCM devices on card 0. bergamot needs 2 (`hw:0,1` mics), pickle 3 (`hw:0,2`). |
| `mac80211_hwsim.radios=<n>` | 2 | Fake Wi-Fi radios (`wlan0`…). 0 = no Wi-Fi (phase 1). |
| `block2mtd.timeout=<s>` / `block2mtd.sync=<0\|1>` | 3 / 1 | See above. |
| `panic=<s>` | 5 (built in) | Reboot after a panic. |

Other features needed by the boot paths: initramfs (`BLK_DEV_INITRD`, gzip/xz/lz4), 9p over virtio
(`-virtfs …,mount_tag=…`) for the maintenance share, `IP_PNP_DHCP`, virtio-net/rng/console, ext4, loop,
squashfs, tmpfs, configfs, debugfs, pstore (no backend), devtmpfs automount, uevent helper (mdev).
Netfilter is legacy x_tables: iptables + ip6tables `filter`/`nat`/`mangle`, conntrack/state, REJECT,
LOG, multiport, addrtype, iprange, owner, comment, limit, mark, MASQUERADE. The vendor
`yio_iptables_init.sh` and pickle's `S35iptables` run cleanly.

## emuboard — what is stubbed

Inactive without `emuboard=`. For the selected board:

| Interface | bergamot | pickle |
|---|---|---|
| `/proc/device-tree/{compatible,model}` | `yandex,bergamot-evt1 yandex,bergamot amlogic, axg` / `Yandex Bergamot EVT1` | `yandex,pickle-evt yandex,pickle amlogic, a4` / `Yandex Pickle EVT` |
| LEDs (`/sys/class/leds`, max 255) | 52: `bergamot:{red,green,blue}:logo0..5`, `bergamot:logo:sync`, `bergamot:white:clock{A..G}{0..3}`, `clockDH/DL/DM/DEGREE`, `cucumber:sync:clock-0` | 106: `pickle:{red,green,blue}:logo0..5`, `pickle:{green,red,blue}:ring0..15`, `pickle:logo:sync`, `pickle:ring:sync0/1`, `pickle:white:clock{A..H}{0..3}`, `clockDH/DL/DM/DEGREE`, `pickle:sync:clock` |
| LED triggers | no-op `ledfb-logo`, `ledfb-logo-sync` (logo LEDs default to them, like the real DT) | + `ledfb-ring`, `ledfb-ring-sync` |
| `…/leds/<led>/device/brightness_scale` | stored string (written by `S61leds_brightness`) | same |
| Input, in this order | `event0` `gpio-keys` (BUS_HOST 1/1/0x100, KEY_MUTE 113, SW_MUTE_DEVICE 14), `event1` `aw933xx` (bus 0, keys 115/114/582, MSC_SERIAL+MSC_SCAN) | `event0` `input_btrcu` (116, 133), `event1` `vad_keypad` (116, BTN_0), `event2` `mute` (113, SW 14), `event3` `aw933xx` (115/114/582) |
| `/sys/class/aml_info/reboot_reason` | rw, from `emuboard.reboot_reason=` | same |
| `/sys/class/power_supply/ya_pd` | `voltage_now`/`current_now` in **mV/mA** (vendor units) 12000/2000, `health` Good (Dead when the voltage is 0), `voltage_now` writable (PDO select), `device/name` = `husb238_pd` | same |
| IIO `ltr308als01` | `in_intensity_{raw,input,scale,offset,hardwaregain,integration_time}`; input = raw × scale | same |
| Framebuffers | `fb0` `logo` (6×1, 24 bpp) under `/devices/platform/logo-framebuffer`, with a backlight | + `fb1` `ring` (16×1). pickle's `S93i2ctop` waits for `/dev/fb0` forever without it. |
| ALSA | card 0 `Dummy` (snd-dummy, 1..8 ch, S16/U8, ≤48 kHz) | same |

`/proc/bus/input/devices` matches the on-device surveys line for line (names, bus ids, phys, sysfs
parents, capability bitmaps). The input devices are registered before anything else creates one:
`KEYBOARD_GPIO`, `GPIO_PL061` and `VIRTIO_INPUT` are off on purpose. Don't add `virtio-keyboard`/`virtio-tablet`
devices, because they would shift the event numbers.

### debugfs control: `/sys/kernel/debug/emuboard/`

| File | Use |
|---|---|
| `key` (w) | `echo "<code> <0\|1\|2>"` reports one key event on the device that has that key (with MSC_SCAN where the real device sends it). `echo <code>` = press + release. E.g. `echo 582 > key` (Alice button). |
| `switch` (w) | `echo "14 1"` = mic-mute switch on, `"14 0"` = off. |
| `leds` (r) | `name brightness` of every stub LED. |
| `led_log` (rw bool) | `1` = ratelimited `printk` of each LED change. Off by default. |
| `reboot_reason` (rw) | Changes `aml_info/reboot_reason` at runtime. |
| `pd_mv`, `pd_ma` (rw u32) | PD contract. `0` mV = no PD (health `Dead`), which tests the no-power alert. |
| `lux_raw` (rw u32) | Light sensor raw counts. |
| `board` (r) | Selected board. |

The host can drive these through the maintenance channel (serial/ssh in the guest), e.g.
`echo 115 > /sys/kernel/debug/emuboard/key`. debugfs is mounted by the vendor fstab.

## Verified (QEMU 6.2 TCG, cortex-a53)

- bergamot, stock dump set: all 8 MTDs carry the right names and sizes in `/proc/mtd`. `ubi0` attaches
  `system` (704 PEBs, 4096/4096 I/O, VID 4096, data 8192) and mounts `rootfs` read-only. The `nv` jffs2
  mounts. BusyBox init runs `rcS` through `S99quasar` to the getty in about 16 s.
  `/proc/bus/input/devices` is identical to the survey. Key and switch injection reach `/dev/input/event{0,1}`
  as 32-bit compat events. LEDs, aml_info, ya_pd (`S30pd_controller` selects 12 V), iio and the watchdog all work.
- Persistence: a `ubimkvol` + UBIFS write on an empty `bkupgrade` survives poweroff and QEMU restart. A
  write + `sync` followed by `kill -9` of QEMU survives too: UBIFS recovers on the next mount.
- pickle, stock dump set: all 11 MTDs. `system` and `data` attach (`data` replays its journal). Init runs
  through `S35iptables`, `S80light-sensor`, `S93i2ctop` (framebuffer), `yio_iptables_init.sh` (iptables + ip6tables)
  and `S99quasar`.
- On these stock dumps the vendor `maind` starts, because the CFW gate isn't installed in a stock dump.
  It aborts on "Can not obtain deviceId". Boot with `-nic none` (or no route out) unless the image carries
  the CFW gate.

## Known gaps

- **LEDs and framebuffers are not connected.** The ledfb triggers are no-ops, and pixels written to
  `/dev/fbN` don't show up on the LED stubs (tests can read `/dev/fbN` back).
- **ALSA is a dummy card.** The vendor mixer controls don't exist (`'main Acm8625 AMP mute status'`,
  `PDM Gain`, `Loopback Enable`, `Master Playback Volume` from hardvold), so `amixer` calls fail. The
  CFW's `audio.unmute`/`mic_gain` calls fail the same way. Capture returns silence, and nothing is played anywhere.
- Not stubbed: `/sys/class/unifykeys` (the scripts fall back to defaults), `pcba_ids`, sdio Wi-Fi, Bluetooth
  HCI, the TEE, `/dev/ttyS1`, the vendor `aml_*` drivers. Scripts that need them print errors and continue.
- Flash semantics are simplified. A write overwrites; it doesn't AND bits like NAND programming. There are no
  bad blocks, no OOB, and the MTD type is `ram` (`MTD_NO_ERASE` set; JFFS2 therefore uses no write
  buffer). A power cut lands between whole MTD operations, so there are no torn pages.
- `mac80211_hwsim` creates 2 radios unless `mac80211_hwsim.radios=0`; Wi-Fi tests are phase 2.
- TCG speed: a full stock boot takes tens of seconds (pickle's aarch64 init is slower than bergamot's).
  The watchdog's 5 s window has held so far, but on a loaded host use `-device i6300esb` with care or drop
  it.

## License

The `emuboard` driver and the patches in `patches/` are Linux kernel code and are licensed
GPL-2.0 (see the SPDX headers). The rest of this repository, including the scripts here, is MIT.
