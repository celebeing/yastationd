# Golden image: a factory reset that lands in the CFW

On **pickle** and **orion** the image a factory reset restores is an unsigned package in the
`aml_sysrecovery` partition. `tools/golden.sh` builds a new one from a speaker running the CFW and
writes it there. From then on the vendor gesture (mute held at power-on) brings the unit back into
the CFW, in the Wi-Fi setup mode, instead of the vendor's firmware.

**Status (2026-10-31):** two full rounds on a Mini 3 Pro: the reset accepted a rebuilt package, a
clean image came up with its access point, and a backup put the unit back as it was. **orion: the
tool handles it and nothing was tried there** (no image built, no reset). bergamot can't do this: its
restore image is inside the signed recovery. Why it works and what was measured:
[research/permanent-install.md](../research/permanent-install.md).

**The risk.** If an image can't give you a way in, the reset restores the same image, and from
outside there is no way to put another one on (no USB burn yet; opening the case is not an option on
our units). Keep the order below: an image is built from a unit that works and the last good package
stays until the new one is proven.

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
tools/golden.sh build root@<speaker-ip> pickle              # -> dumps/golden/pickle-<time>/golden.img
tools/golden.sh flash root@<speaker-ip> dumps/golden/pickle-<time>/golden.img
```

`build` changes nothing on the speaker. It checks that it understands the package (the CRC and every
`VERIFY` recompute, repacking reproduces it byte for byte), keeps every item but `system`, takes
`system`'s geometry from the item it replaces, and on NAND unpacks the result with another tool and
compares every entry. `flash` checks the board's partition, room to stage the file (the partition's
parent, else `/tmp`), uploads, writes, and compares a read-back. A normal boot never reads this
partition, so a write by itself changes nothing until the next reset.

**Going back to the vendor's image:** `tools/golden.sh flash root@<speaker-ip> <stock package>`,
then a reset. Keep a dump of the stock package off the speaker; the package on the unit is gone once
overwritten.

## After a reset

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

## Limits

- A new image carries the donor's firmware. An update can't reach a unit (the blocks stay), so this
  is the same firmware as the package's on our units; the builder doesn't check that it matches the
  `boot` item, so don't build from a donor whose firmware differs from its own package.
- The image takes most of pickle's partition: 127 MB of 134 (128 MiB) with 25 MB of CFW in it.
  `flash` refuses what doesn't fit.
- Bad blocks in `aml_sysrecovery`: `flash` refuses; our unit has none.
