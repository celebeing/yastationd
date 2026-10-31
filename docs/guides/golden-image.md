# Golden image: a factory reset that lands in the CFW

On boards with an unsigned restore package, `tools/golden.sh` writes a new one into the
`aml_sysrecovery` partition. The vendor gesture (mute held at power-on) then brings the unit back
into the CFW, in the Wi-Fi setup mode, instead of the vendor's firmware.

| Board | State |
|---|---|
| **pickle** (Mini 3 Pro) | **Proven:** two full rounds on one unit, 2026-10-31: the reset took a rebuilt package, a clean image came up with its access point, a backup put the unit back as it was. |
| **orion** (Station 3) | **Proven once:** one full round on one unit, 2026-10-31: a reset took a rebuilt package, a clean image came up with its access point, a backup put the unit back. See [Orion](#orion). |
| **bergamot** (Mini 3) | **Impossible:** its restore image is inside the signed recovery. |

Why it works and what was measured: [research/permanent-install.md](../research/permanent-install.md).

**The risk, on any board.** If an image can't give you a way in, the reset restores the same image,
and from outside there is no way to put another one on (no USB burn yet). Keep the order below: an
image is built from a unit that works, and first-boot changes are proved live while the last good
package is still in the partition.

## What is in the image

The donor's rootfs with the CFW's edits (the maind gate, the update blocks, the boot hooks), plus
the CFW's programs in `/usr/share/cfw-seed` and the hook `S00zcfwseed`, which copies them to `<cfw>`
on a first boot.

**Not in it:** SSH keys, host keys, Wi-Fi networks, the config, state, Music Assistant identity,
voices, LAN addresses. The builder takes a fixed list of `<cfw>` (`bin init.d openssh egress.sh
sounds clips vendor iptables`), refuses a seed with anything that looks like an owner's file, and
drops the home router from `/etc/chrony.conf`.

It does hold vendor files and the sounds decoded from them, so `dumps/golden/` is gitignored and
**an image is never published**.

## Building and writing

Deploy first: the image carries what the donor runs.

```sh
tools/deploy.sh <hosts-file>                                # the donor runs the CFW you want baked
tools/backup-unit.sh root@<speaker-ip>                      # the unit you are about to reset
tools/golden.sh build root@<speaker-ip> <board>             # -> dumps/golden/<board>-<time>/golden.img
tools/golden.sh flash root@<speaker-ip> dumps/golden/<board>-<time>/golden.img
```

`build` changes nothing on the speaker. It checks that it understands the package (the CRC and every
`VERIFY` recompute, repacking reproduces it byte for byte), keeps every item but `system`, takes
`system`'s geometry from the item it replaces, and unpacks the result with another tool and compares
every entry (`ubi_reader` on NAND, `debugfs` on ext4). `flash` checks the board's partition, room to stage the file (the partition's
parent, else `/tmp`), uploads, writes, and compares a read-back. A normal boot never reads this
partition, so a write by itself changes nothing until the next reset.

On a board other than pickle and orion `flash` stops unless `UNTRIED=<board>` is set.

**Going back to the vendor's image:** `tools/golden.sh flash root@<speaker-ip> <stock package>`,
then a reset. Keep a dump of the stock package off the speaker; the package on the unit is gone once
overwritten.

## After a reset (as on pickle)

1. Mute held at power-on, about 5 s, until the ring turns yellow; then blue, then the start animation.
2. About a minute later the speaker is an open access point `yastation-<board>-<serial tail>`, the
   ring running a yellow comet. It enters setup mode by itself because it has no saved network
   ([wifi-setup.md](../wifi-setup.md)).
3. Join it; the page opens by itself, or go to `http://10.42.0.1/`.
4. **SSH keys:** paste your public key.
5. Add your Wi-Fi network, fill in the home settings, press **Connect now**.

Until someone does that, anyone in Wi-Fi range can do it: the network is open, as setup mode always
is. There is no USB or network ADB on this path, deliberately.

**Your old unit back:** `tools/restore-unit.sh root@<speaker-ip> <backup-dir>` then a reboot
(`REBOOT=1`): config, identity, Sendspin, voices, host keys, DUID, Bluetooth pairings
([backup.md](backup.md)). The host key changes until then, so ssh needs telling once. The backup
brings the CFW binaries of its day: deploy again afterwards.

The Wi-Fi MAC survives a reset. A router that binds its lease to the client id will give another
address, since dhcpcd's DUID is new; a lease by MAC keeps it.

## Orion

One real round on a Station 3, 2026-10-31. Before it: the first boot proved live with `<cfw>` and
Wi-Fi moved aside (the seed, setup mode 3 s after the daemon, the access point 14 s later, a key through
the page's API), a login by a key only in `<cfw>/authorized_keys`, the write and a normal reboot.

**What the reset did** (mute held at power-on), by markers put there first:

- `system` was rewritten from the package: the rootfs marker gone, the golden image's in place;
- `/ota` and `/data` were both reformatted: new UUIDs, markers gone. So on orion, as on pickle, a reset
  takes the CFW and Wi-Fi, and network ADB with them;
- `aml_sysrecovery`, `boot`, `recovery`, `misc` and the env kept their hashes, and the Wi-Fi MAC stayed;
- **`bootloader` and `reserved` changed**, unlike on pickle: `bootloader`'s hash differs from before, and
  two copies of an encrypted-looking 182 KB block in `reserved` (at `0x413600`, `0x453600`) were
  rewritten. Whether a stock reset does the same is not known; the unit boots and its identity holds. A
  Station 3 that was not reset differs from it only in 48 bytes at `0x160` of `bootloader`.
- `/sys/fs/pstore` was empty on the first boot: no log of the recovery (the gesture is a cold start, and
  ramoops doesn't outlive one).

**It takes long:** a few minutes before Linux is up (at most about 3 here, by the clock; a 618 MB `system`
goes to eMMC), with nothing to show for it but the ring, and it feels longer. Leave the power alone.

Then the access point, a key, the Wi-Fi network; a restore brought the unit's identity back with no
change on the HA side. A restore also brings back the `/data` marker that opens **network ADB on
`:5555`**, which the reset had closed ([backlog.md](../backlog.md)).

Before a write on an orion, copy your key where a golden image reads it, in case a reset ever keeps
`/ota` (the rootfs's file is replaced by a link to `<cfw>/authorized_keys`):
`cat /root/.ssh/authorized_keys >> <cfw>/authorized_keys`. Keep `stock.img` from the work dir: it is
the way back, and both our Station 3s' packages are byte-identical.

`/usr/share/cfw-seed` comes out of the fixed list, so a donor's `media`, `packs` and voices stay out; the
speaker runs on the built-in theme until a backup or you bring them. **The unit with `voice_store`:**
restore its `voices/` along with its config, or the empty album clears every speaker's household
([voice-id.md](../voice-id.md)).

## Limits

- A new image carries the donor's firmware. An update can't reach a unit (the blocks stay), so this
  is the same firmware as the package's on our units; the builder doesn't check that it matches the
  `boot` item, so don't build from a donor whose firmware differs from its own package.
- Room: on pickle the image takes 127 MB of 134 (128 MiB) with 25 MB of CFW in it; on orion 661 MB of
  1.5 GiB. `flash` refuses what doesn't fit.
- Bad blocks in `aml_sysrecovery` (NAND, pickle): `flash` refuses; our unit has none.
