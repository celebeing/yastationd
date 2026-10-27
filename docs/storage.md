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
| `<cfw>/log-spool` | only while the log server is unreachable, once a minute, capped (`log_spool_kb`) |
| `<cfw>/web.secret` (32 B) | once, the first time the web server starts |
| `<cfw>/netwatch.count` | a network-watchdog reboot (one write) |

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

## Logs off the speaker (built 2026-10-26)

With `log_remote = <syslog-host>:<port>` in `yastationd.conf` (off by default) the daemon ships
logs to a syslog server on the LAN (`internal/logship`):

- **What:** new lines of `/tmp/yastationd.log` (the daemon and its supervisor) and of
  `/var/log/messages` (busybox syslogd: the kernel, dropbear, the vendor's boot scripts). After a
  reboot both are read from their start, so the boot's kernel lines go too; a daemon restart
  resumes where it left (positions in `/tmp/yastationd.logship`).
- **How:** RFC 5424 over TCP, newline-framed, each line with its own time and zone (slog's
  `time=`, syslogd's stamp; a stamp before 2026, a boot before the clock is set, gets the time it
  was read). HOSTNAME is the `device_id`; app is `yastationd` or the syslog tag (`kernel`,
  `dropbear`…), severity from the line. Any syslog server that takes TCP works; the author's is
  VictoriaLogs.
- **While the server is unreachable:** lines wait in memory and once a minute are appended to
  `<cfw>/log-spool` on flash, capped at `log_spool_kb` (default 1024; the oldest lines go first).
  When the server is back the spool is sent first, then deleted. Flash is written only during an
  outage, once a minute.
- **A dead link** (the Wi-Fi hang: writes into the socket still succeed, nothing is
  acknowledged): the connection has a 20 s TCP user timeout, and data unacknowledged for 20 s
  (`SIOCOUTQ`) drops it; the unacknowledged lines go back to the queue and from there to the spool.
  Some may arrive twice; none is lost. Tested on orion by blocking the port for 90 s: the lines
  written meanwhile were spooled on flash and arrived after.

The **network watchdog** (`net_watchdog_min`, default 10; 0 = off) goes with it. "Down" is the
broker connection closed and the default gateway not answering a ping (checked every 30 s; not
in Wi-Fi setup mode). It climbs the ladder the firmware's own `WifiWatcher` climbs (in `maind`:
`wpa_cli reconfigure`, `ip link set wlan0 down/up`, `yandex-wifireset`, then a reboot; its
`wifiReseter` acts after 120 s without internet, `quasar.cfg`):

1. down `net_wifi_reconfigure_min` (2): `wpa_cli -i wlan0 reconfigure`;
2. down `net_wifi_reset_min` (4), then every `net_wifi_reset_every_min` (10): the board's
   `platform.wifi_reset` — the firmware's
   `/usr/bin/yandex-wifireset`, which stops wpa_supplicant and Bluetooth, unloads the Wi-Fi
   driver, cycles the SDIO power (`wifi_power 0/1`), loads the driver and starts them again. This
   is what a hung Wi-Fi chip needs ("fw recovery not finish" in the kernel log: the black Mini
   2026-10-27, the chip's firmware died and the driver could not bring it back);
3. down `net_watchdog_min`: it logs the kernel's last 40 lines, flushes the spool and reboots.
   Each watchdog reboot doubles the next wait (10 → 20 → 40 min … up to 4 h, counted in
   `<cfw>/netwatch.count`, measured in uptime because a mini's clock is stale after a reboot); an
   hour of good network resets it.

`net_wifi_recovery = false` skips both steps (a 0 skips one). Each step and the commands' results are logged; the lines reach the log server once the network
is back (the spool). **Until v0.16.4 the watchdog never fired on a speaker with a broker
configured:** it took paho's `IsConnected()` for "connected", and that is also true while the
client keeps reconnecting — so the network always looked up (the lilac Mini 2026-10-27 stayed off
the LAN for half an hour; the black one until it was power-cycled).

## Options for persistent logs on the speaker itself (not built)


- **orion:** plenty of room and an eMMC with its own wear levelling: keep the daemon's log (and
  syslog) on `/data` or `<cfw>/log/` with size-capped rotation. A few MB a day is nothing next to
  a single deploy.
- **minis:** don't put the full log on NAND, but its warnings are cheap: append WARN/ERROR lines
  (and the supervisor's own lines) to a capped file on `<cfw>`, flushed every few minutes. That
  is ~one UBI block erase per flush spread over ~900 blocks, years before anything wears.
  A tail of the full log could also be saved on a clean stop (not on a hang, which ends in a
  power cycle).
- In both cases the RAM log stays as it is.
