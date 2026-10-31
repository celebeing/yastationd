# Backing up a speaker and putting it back

Two scripts on the host, ssh only: `tools/backup-unit.sh` takes everything a speaker needs to come
back exactly as it was, and `tools/restore-unit.sh` writes it back onto a speaker you can log in to.
The point is that Home Assistant and Music Assistant notice nothing: same entities, same player,
same voices, same address. Take a backup before any experiment that can cost the unit its state (a
factory reset, rootfs work, [research/permanent-install.md](../research/permanent-install.md)), and
now and then anyway.

A backup holds the unit's Wi-Fi password, its SSH host keys, tokens and the web login, so it goes
to the gitignored `dumps/` and never anywhere public.

## Taking one

```sh
tools/backup-unit.sh root@<speaker-ip>              # -> dumps/backups/<device_id>/<UTC time>/
SYSTEM=1 tools/backup-unit.sh root@<speaker-ip>     # also the whole rootfs image (orion, ~1 GiB)
```

It only reads on the speaker (cat, find, `tar c`, gzip, `dd` from partitions) and makes no sound.
About 30 s for a Station 3, 1.5 min for a Mini 3 (slower flash), 20-65 MB.

| File | What | Why it matters |
|---|---|---|
| `rw.tar.gz` | every writable flash partition: orion `/ota` (= `/bkupgrade`) and `/data`, bergamot `/bkupgrade`, pickle `/data` | `<cfw>` with `yastationd.conf` (`device_id`: the MQTT client id and every HA entity's unique id), `yastationd.state` (the options set from HA and the web app), `sendspin/state.json` (Music Assistant's player id), `voices/` (voice ID's People album), packs, sounds, `web.secret`, the SSH host keys and the CA files; the Wi-Fi config; Bluetooth pairings |
| `rootfs.tar.gz`, `rootfs.sha256` | the rootfs files changed since the firmware was built (newer than its `os-release`) | the install hooks in `/etc/init.d`, the maind gate (`run.sh`), the OTA stub, `/root/.ssh/authorized_keys` and the `.orig` copies |
| `rootfs.list` | every rootfs path with size and mode | to see later what was there, deleted files included |
| `part-*.img.gz` | the small raw partitions: `env`, `misc`, `reserved` (eMMC: orion), `nand_env`, the `nv` mtd (NAND: bergamot) | U-Boot's settings and the unit's keys; orion's MAC and serial are probably in `reserved`, so a speaker that lost them gets another DHCP address. Each image is checked against a checksum taken on the speaker |
| `info.txt`, `SHA256SUMS` | board, firmware, mounts, free space, time; checksums of the rest | |

The "changed since the firmware was built" rule catches edits nobody listed, at the cost of a few
stock files on bergamot whose dates are later than its `os-release` (a few MB, harmless: restore
skips what is already the same).

## Putting it back

```sh
tools/restore-unit.sh -n root@<speaker-ip> dumps/backups/<id>/<time>    # dry run: what would change
tools/restore-unit.sh root@<speaker-ip> dumps/backups/<id>/<time>       # rootfs, then rw
tools/restore-unit.sh root@<speaker-ip> <dir> rw                        # only one part
REBOOT=1 tools/restore-unit.sh root@<speaker-ip> <dir>                  # and reboot at the end
```

- **rootfs** writes only the files whose checksum (or symlink target) differs on the speaker, with
  `remount,rw` … `sync; remount,ro`. The speaker's versions of those files are saved into the backup
  directory first (`replaced-<time>.tar.gz`), so the step can be undone. The hooks take effect on
  the next boot.
- **rw** stops the daemon, unpacks the partitions over what is there (nothing is deleted; files
  newer than the backup stay) and starts it again: music and voice on that unit pause for a few
  seconds. **The Wi-Fi config is kept** unless the speaker's has no network in it or `WIFI=1`: a
  wrong one locks you out on the next boot.
- A golden image's own files (`/usr/share/cfw-seed`, `S00zcfwseed`, `/etc/cfw-image*`) are left out of
  both a backup and a restore: they belong to the image, not the unit.
- It refuses a backup of another board (`FORCE=1` overrides) and checks `SHA256SUMS` first.
- **The raw partitions are never written by the script.** `env` and `reserved` hold U-Boot's
  settings and the unit's identity; a bad write there can stop the boot. If one ever has to go
  back, it is a deliberate `dd` by hand, with the user present.

The speaker must be reachable over SSH. After a factory reset into stock firmware it is not: the
CFW's SSH is gone and getting back in is the install kit's job (out of this repo's scope); restore
right after that.

## Tried

2026-10-31: backups of both Station 3s and both Mini 3s; on a Station 3, a dry run, a rootfs restore
of one file (the remount, the saved copy, back to `ro`) and a full `rw` restore of its own backup
(the daemon back in seconds, HA and Music Assistant connected). A restore after a real factory reset:
on a Mini 3 Pro and two Station 3s through a [golden image](golden-image.md) (2026-10-31), each came
back whole, voices included. A restore reopens network ADB on orion: the marker is in `/data`.
