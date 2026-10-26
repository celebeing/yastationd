# Storage and logs on the speakers

What the CFW writes where, how much flash wear that costs, and what happens to the logs. Measured
on all four units on 2026-10-26 (two bergamot, one pickle, one orion), v0.9.0.

## The flash

| Board | Flash | Writable partition (ours) | Free | RAM |
|---|---|---|---|---|
| bergamot | raw NAND, 256 KiB erase blocks, UBI | `/bkupgrade` (UBIFS, ~200 MB) → `<cfw>` | ~170 MB | 256 / 512 MB (units differ) |
| pickle | raw NAND, 256 KiB erase blocks, UBI | `/data` (UBIFS, ~157 MB) → `<cfw>` | ~139 MB | 512 MB |
| orion | eMMC, ~7.7 GB | `/ota` (ext4, 1.4 GB, `noatime`) → `<cfw>`; `/data` (ext4, 2.6 GB) unused | 1.3 GB + 2.5 GB | 2 GB |

The rootfs is read-only everywhere (UBIFS on the minis, ext4 on orion) and is written only on an
install.

**Wear.** UBI counts erases per block: the highest erase count on the writable UBI device is
**4-7** on the three minis, over the units' whole life (vendor years included). UBI spreads
writes over all ~750-930 blocks, and SLC NAND is rated for tens of thousands of cycles: the
minis' flash is idle. On orion the eMMC reports `life_time 0x01 0x01` (0-10 % used) and
`pre_eol_info 0x01` (normal). Measured steady writes on orion: **0 bytes in 2 min** of normal
running; everything written since boot (~490 MB in 13 h) was deploys. A deploy writes the binary
twice (the upload and the `.prev` copy, ~17 MB each).

**What the daemon writes to flash at runtime** (everything else goes to tmpfs):

| File | When |
|---|---|
| `<cfw>/yastationd.state` (~350 B, written to a temp file and renamed) | an HA-set or user-set value changes: ring/ambilight colour, display mode, volume, brightness…; debounced 2 s, so turning the volume ring is one write |
| `<cfw>/sendspin/state.json` (sendspin-host) | a Music Assistant server pairs |
| `wpa_supplicant.conf` | only from the Wi-Fi setup page |
| `<cfw>/intercom-*.raw` | only with `intercom_record_dir` set (testing) |

Not written: the light-sensor readings, auto-brightness, room sound levels, the heartbeat
(`/tmp/yastationd.heartbeat`), the once-per-boot markers (in `/tmp`).

Clutter worth removing by hand: orion's `<cfw>/iptables.mixed-armhf` (8.7 MB, the broken first
bundle, boards/orion.md), old `yastationd.conf.*` copies and `initd-backup-*.tar` on the minis.

## Logs

| Log | Where | Rotation | After a reboot |
|---|---|---|---|
| `yastationd` (stdout/stderr, slog text) | `/tmp/yastationd.log` (tmpfs) | the supervisor copy-truncates it at 1 MB every 60 s and on each start, one `.1` kept: ≤ 2 MB of RAM | gone |
| syslog + kernel (busybox `syslogd -n`, `klogd -n`) | `/var/log/messages` (`/var/log` is a symlink to `/tmp`) | busybox's default: 200 KB, one `.0` | gone |
| `egress.sh`, adb-over-USB helper | `/tmp/yastation-*.log` | none (a few lines per boot) | gone |
| `dmesg` | the kernel ring buffer; `ramoops` pstore for a crash's console | — | gone. The pstore is in RAM: it survives a warm reset (a panic, the watchdog), not a power cycle, and `S98pstore-clear` wipes it after a `normal` reboot (deploying.md, "Diagnostics") |

Nothing persists across a reboot. That cost us on 2026-10-26: a Mini 3 lost Wi-Fi for two hours
and had to be power-cycled, and every trace on the speaker was gone (yastationd/README.md, item
7). Whatever the next cause is, a hang ends in a power cycle, so evidence must already be on
flash when it happens.

## Options for persistent logs (not built)

- **orion:** plenty of room and an eMMC with its own wear levelling: keep the daemon's log (and
  syslog) on `/data` or `<cfw>/log/` with size-capped rotation. A few MB a day is nothing next to
  a single deploy.
- **minis:** don't put the full log on NAND, but its warnings are cheap: append WARN/ERROR lines
  (and the supervisor's own lines) to a capped file on `<cfw>`, flushed every few minutes. That
  is ~one UBI block erase per flush spread over ~900 blocks, years before anything wears.
  A tail of the full log could also be saved on a clean stop (not on a hang, which ends in a
  power cycle).
- In both cases the RAM log stays as it is.
