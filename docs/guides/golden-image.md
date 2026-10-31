# Golden image: a factory reset that lands in the CFW

On boards with an unsigned restore package, `tools/golden.sh` writes a new one into the
`aml_sysrecovery` partition. The vendor gesture (mute held at power-on) then brings the unit back
into the CFW, in the Wi-Fi setup mode, instead of the vendor's firmware.

| Board | State |
|---|---|
| **pickle** (Mini 3 Pro) | **Proven:** two full rounds on one unit, 2026-10-31: the reset took a rebuilt package, a clean image came up with its access point, a backup put the unit back as it was. |
| **orion** (Station 3) | **Nothing proven.** An image builds and passes every check on the host (below), and that is all: none has been written to a unit, none has been through a reset, and it is not known what a reset does there. Read [Orion](#orion-untried) before anything. `tools/golden.sh flash` refuses it unless told otherwise. |
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

On a board other than pickle `flash` stops unless `UNTRIED=<board>` is set ([Orion](#orion-untried)).

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

## Orion: untried

**Nobody has done this. Everything in this section is reasoning, not experience.** A brick here means
a Station 3 with no way back: its case is glued, USB burn is unreached.

What carries over from pickle: the package format, and the fact that a rebuilt package can pass a
recovery of this family. What does not:

- **Another reset path.** pickle's env sends `factory_reset` to `recovery_from_flash`; orion's runs `fr`,
  a command inside U-Boot that we cannot read.
- **Another `system` format.** An Android sparse ext4 image, so another code path writes it. Ours is
  made by `mke2fs` + `img2simg`, with stock's features, UUID and size; it has raw and fill chunks
  where stock also has one "don't care" chunk.
- **Unknown wipe.** Whether `/ota` (where `<cfw>` and the Wi-Fi config live) goes with `/data`.

What each outcome would mean:

| The reset | Result |
|---|---|
| rewrites `system`, empties `/ota` | as on pickle: the access point, a key, a restore |
| rewrites `system`, keeps `/ota` | the old `<cfw>` stays, the hook does nothing, the unit rejoins Wi-Fi. **The rootfs's own key is gone** (`/root/.ssh/authorized_keys` points to `<cfw>/authorized_keys`): SSH refuses until a key is in that file, hence the step below; else the key combo's setup page can add one |
| empties `/data` only | the CFW stays as it is; a golden image is not needed; Bluetooth pairings go |
| refuses the package after erasing | a brick |

Before a write on an orion, beyond the steps above:

1. Copy your key where a golden image reads it, so the second outcome doesn't lock you out:
   `cat /root/.ssh/authorized_keys >> <cfw>/authorized_keys`.
2. Prove the first boot live, as was done on pickle ([research](../research/permanent-install.md#the-takeover-on-pickle-tried-2026-10-31)):
   the hook and seed on the live rootfs, `<cfw>` and Wi-Fi moved aside, a temporary hook that collects
   the result, puts everything back and reboots by itself. Automatic entry into setup mode and the
   page's SSH keys have never run on this board either (the access point itself has, by the key combo).
3. `UNTRIED=orion tools/golden.sh flash root@<speaker-ip> <image>` writes it with `dd`, untried too.
   The stock package is on the unit only: `tools/golden.sh build` leaves it as `stock.img` in the work
   dir; keep that file.

`/usr/share/cfw-seed` comes out of the fixed list, so a donor's `media`, `packs` and voices stay out
(checked on a real build); the speaker runs on the built-in theme until a backup or you bring them.

## Limits

- A new image carries the donor's firmware. An update can't reach a unit (the blocks stay), so this
  is the same firmware as the package's on our units; the builder doesn't check that it matches the
  `boot` item, so don't build from a donor whose firmware differs from its own package.
- Room: on pickle the image takes 127 MB of 134 (128 MiB) with 25 MB of CFW in it; on orion 661 MB of
  1.5 GiB. `flash` refuses what doesn't fit.
- Bad blocks in `aml_sysrecovery` (NAND, pickle): `flash` refuses; our unit has none.
