# Diagnostics: archives to send when something is wrong

Two scripts collect what is needed to debug a problem remotely, each into one `.tar.gz`. They are
written so that someone else's speaker or build machine can be debugged from the archive alone,
and so that the archive can be attached to a public issue as it is: redaction is on by default.

| Script | Runs on | For |
|---|---|---|
| `yastationd/scripts/diag.sh` → `<cfw>/bin/diag.sh` | the speaker, as root (busybox sh) | the CFW misbehaves on a unit |
| `tools/diag-build.sh` | the developer's machine (Linux, WSL, macOS; POSIX sh) | `tools/build.sh` or `tools/deploy.sh` fails |

Both are read-only. Neither builds, deploys, changes a setting or deletes anything; each writes
only its work directory in `/tmp` and the archive.

## On the speaker: diag.sh

```sh
ssh root@<speaker-ip> <cfw>/bin/diag.sh                      # archive in /tmp, its path printed last
ssh root@<speaker-ip> '<cfw>/bin/diag.sh -' > diag.tar.gz    # straight to the host
ssh root@<speaker-ip> 'sh -s -- -' < yastationd/scripts/diag.sh > diag.tar.gz   # a unit without it
```

`tools/deploy.sh` and `make deploy` upload it to `<cfw>/bin/diag.sh` with every deploy; nothing
runs it on its own. The third form needs nothing installed (a CFW older than the script, or a broken deploy).

| Flag | |
|---|---|
| `-o DIR` | write `diag-<device_id>-<YYYYmmdd-HHMMSS>.tar.gz` there instead of `/tmp` |
| `-` | write the tar.gz to stdout (progress goes to stderr) |
| `-r` | raw: no redaction, for your own use only |
| `-x` | also take live readings from drivers: the IIO sensors' raw values, every LED's brightness and trigger, the USB-PD `power_supply` uevent. Off by default; use it on a unit you can power-cycle |

It finds `<cfw>` the way the boot hook does (`/bkupgrade/cfw`, else `/data/cfw`; never creates
either). Every command is time-boxed (5–8 s, in its own process group, so a whole pipeline is
killed) and its output capped; a failing one is noted and the run goes on.

**Inside** (`diag-<device_id>-<stamp>/`):

| File | What |
|---|---|
| `summary.txt` (first) | device, board, firmware, the `yastationd` version, which helpers run, the vendor-stack gate, OTA block, egress state, the off switch and config trial, clock, load, memory, flash, temperatures, reboot reason, heartbeat age, log counts, the supervisor's last lines, the last warnings and errors, the file list |
| `versions.txt` | `yastationd -version`, `version_full`, `os-release`, `uname`, the device-tree model, `/proc/cmdline`, glibc, size + date + sha256 of everything in `<cfw>/bin`, dropbear and hostapd versions, the boot hook and supervisor checksums |
| `system.txt` | uptime, load, meminfo, cpuinfo, cpufreq, thermal zones, `df`, mounts, `/proc/mtd`, `ubinfo -a`, UBI erase counts, eMMC life (orion), `reboot_reason`, pstore listing, `lsmod`, `ipcs` (the dmix semaphores), limits |
| `processes.txt` | `ps w`, a `top` snapshot, RSS/threads/fds of our processes, vendor-stack processes, the supervisor's pid |
| `cfw.txt` | the `<cfw>` listing; flags and markers (`maind_off`, `yastationd_off`, `config.trial`, `sync.*`, `netwatch.count`, `log-spool`…: presence, size, date); `.bak` / `.pre-*` / `.prev` / `.bad` / `.upload` files (names and sizes only); `vendor/`, `iptables/`, sounds and clips; `wakes/` (count, size, the newest names — never the audio) |
| `config.txt` | `yastationd.conf`, `board.conf`, `sync.pending`, `sync.rejected`, and the diff from `yastationd.conf.bak` (the last change) |
| `state.txt` | `yastationd.state`, `moments.json`, the heartbeat, `/tmp/yastationd*` markers, log-ship positions, the log spool |
| `daemon-http.txt` | `GET /api/info` and `GET /metrics` from the daemon on `127.0.0.1` (port from `http_addr`, default 80; both need no login) |
| `log-yastationd.txt` | the tails of `/tmp/yastationd.log` (4000 lines) and `.1` (1500) |
| `log-system.txt` | `/var/log/messages` and `.0`, `logread` where it exists, `/tmp/yastation-*.log`, dropbear's log |
| `kernel.txt` | `dmesg` (3000 lines), any pstore records |
| `network.txt` | `ip addr/route/rule`, interface counters, `resolv.conf`, `/etc/hosts`, `wpa_cli status` and `list_networks`, the `wpa_supplicant.conf` without PSKs or passwords, listening sockets, ARP, chrony and DHCP processes |
| `egress.txt` | `<cfw>/egress.sh status` (it only lists the chains), the boot hook, `egress.sh` vs `.prev` |
| `vendor-guards.txt` | `maind_off`, the gate in `run.sh`, `device_ota_update.sh`, `ota_script.pub` vs `.orig`, vendor processes |
| `boot.txt` | `/etc/init.d`, the boot hook against its copy in `<cfw>/init.d`, crontabs, `inittab` |
| `audio.txt` | `/proc/asound` (cards, devices, PCM info, every substream's status and hw_params: what is open and at which rate), `/etc/asound.conf`, audio processes |
| `hardware.txt` | input devices, IIO device names, LED names, framebuffers, power supplies, `lsusb` (and the `-x` readings) |

Not yet measured end to end on a speaker since the change below. On bergamot the collection took
about 2 s, and an archive is expected to be tens of KB (mostly the logs). A host dry run under
busybox (`YASTATIOND_CFW=<dir> YASTATIOND_LOG=<file> sh diag.sh`, as for `supervise.sh`) takes
under a second.

### Never in diag.sh (nor in any script that runs on a speaker)

- **Reading every ALSA mixer control:** `amixer contents`, `amixer controls`, `amixer
  scontents`, or a loop that `cget`s them all. On 2026-10-28 that froze the kernel of a
  bergamot (fw 0.6.1.4) inside the codec driver: the last kernel lines were the driver's
  `snd_*_get` prints, after the SPDIF "Channel A Status" control. The unit needed a power cycle.
  The daemon and hardvold read a few named controls, which is fine. A diagnostic reads the
  stream state from `/proc/asound` instead.
- **`wpa_cli signal_poll`, `iw`, `/proc/net/wireless`:** they reach cfg80211 calls the vendor
  Wi-Fi driver does not support (`get_channel`, `get_tx_power`). `wpa_cli status` and
  `list_networks` only report wpa_supplicant's own state.
- **efuse, `pcba_ids`**: serial numbers, and nobody has shown that reading them is harmless.
- Anything that writes: no remount, no config change, nothing to Wi-Fi, the egress lock or
  dropbear, no vendor binaries started, no sound, no LEDs.

## On the host: diag-build.sh

```sh
tools/diag-build.sh            # archive in the current directory, its path printed last
tools/diag-build.sh -s -o /tmp # with an ssh check of every speaker in tools/deploy.hosts
```

| Flag | |
|---|---|
| `-o DIR` | where to write `diag-build-<host>-<YYYYmmdd-HHMMSS>.tar.gz` (redacted: `<host>` is the platform, `linux`, `wsl` or `macos`) |
| `-r` | raw: no redaction |
| `-s` | `ssh -o BatchMode=yes -o ConnectTimeout=5 <target> true` for each speaker in `tools/deploy.hosts`. A key that needs a hardware tap or a PIN fails in BatchMode; that is reported, not retried |

**Inside:** `summary.txt` (platform, checkout, docker, the builder images' state, sysroots,
artifacts); `host.txt` (OS, WSL, CPUs, memory, disk, which tools exist and their versions);
`git.txt` (describe, branch, status, log, remotes, submodules, `core.autocrlf`, CR bytes and the
executable bit of the scripts — a Windows checkout with CRLF breaks them); `docker.txt` (`docker
version`, `info`, `system df`, buildx, the builder images); `build.txt` (the build cache, the keys
in `tools/build.env`, each sysroot's key files and firmware, `vendor-local/`, the built
artifacts with size, date and hash, whether the web app is stale, the sendspin-host CMake caches'
directory); `deploy.txt` (the hosts file's entry count and platforms, the ssh check).

**The builder images.** For every `docker/<name>.Dockerfile` it computes what `tools/build.sh`'s
`image()` computes — the first 16 hex digits of the file's sha256 — and compares it with the
`yastation.dockerfile` label of the `yastation-<name>` image: `ok`, `STALE` (the next build
rebuilds it) or `MISSING`. An image that is rebuilt on every run although its line says `ok` is
docker's cache, not the script (`docker builder prune`; [building.md](building.md)). Images left
from older builders (`yastation-build`, `yastation-web`) show up in the list and can go.

`tools/build.sh` writes no log of its own: send the failing command's output together with the
archive.

## Redaction

On by default in both, because an archive may be attached to a public issue. `-r` turns it off.

**diag.sh:**

- secrets: the config keys whose name contains `pass`, `secret`, `psk`, `token`, `key` or `user`
  (`mqtt_pass`, `mqtt_user`, `intercom_secret`, `wifi_setup_psk`, `web_password`…); anywhere,
  `pass` / `password` / `secret` / `psk` / `token` / `api_key` followed by `=` or `:`, bearer
  tokens, `pbkdf2-sha256$…` hashes;
- Wi-Fi SSIDs (from `wpa_supplicant.conf` and `wpa_cli`) → `<wifi-ssid-N>` everywhere, and
  every `ssid=` line;
- the hostnames the config names (`mqtt_host`, `probe`, `log_remote`, `ntp_server`,
  `sendspin_server`), the resolver's search domain and chrony's servers → `<host-N>`;
- MAC addresses → the vendor half kept (`aa:bb:cc:xx:xx:xx`); global IPv6 addresses; the
  interface part of link-local ones;
- the serial number (kernel command line, `cpuinfo`, the device tree) → `<serial>`; the
  speaker's hostname, which ends in part of it → `<hostname>`;
- what was said and answered: the log's `text=` values (transcripts, TTS replies), the display
  `message` in the state file, moment names.

**Kept:** LAN IPv4 addresses (needed to debug the network), `device_id` and `device_name`, the
vendor's own hostnames. The summary says the archive shows the local network layout.

**Never collected, even with `-r`:** `web.secret`, the SSH host keys, the Wi-Fi PSKs and EAP
passwords, `sendspin/state.json`'s content (pairing records), audio.

**diag-build.sh:** remote URLs reduced to the host (anything but a public forge →
`<private-host>`), `$HOME` → `~`, the user and host names replaced, `tools/deploy.hosts` reduced
to a count (the ssh check labels hosts `host#N`), `tools/build.env` values dropped except the
`SPOTTER_SYSROOT_*` paths.

Redaction is pattern-based: look through the archive before posting it publicly.

## From the web app

Status → Log → "Download diagnostics": `GET /api/diag` (behind the login; `?raw=1` with "keep
secrets and names" ticked) runs `<cfw>/bin/diag.sh -` (or `-r -`) and hands its tar.gz to the
browser as `diag-<device_id>-<time>.tar.gz` (`apiDiag`, `yastationd/internal/app/diag.go`). One run
at a time (409 while one goes), two minutes at most; a speaker without the script (an older
deploy) answers 404.
