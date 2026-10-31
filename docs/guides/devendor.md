# Removing the vendor's software

`tools/devendor.sh` takes the vendor's stack off a speaker for good, instead of keeping it from
starting. What goes, what stays and why: [research/vendor-removal.md](../research/vendor-removal.md).

| Board | State |
|---|---|
| **pickle** (Mini 3 Pro) | `remove` and `undo` proven on a unit, voice, music, HA working. **`commit` has not been run on a unit yet.** |
| **orion** (Station 3) | No list: the tool refuses. Its [golden image](golden-image.md) is untried. |
| **bergamot** (Mini 3) | Impossible: no golden image, so no way back. |

**A golden image is required, and the tool checks.** The reset is the way back from here: the
package in the speaker's `aml_sysrecovery` must match a `golden.txt` that `tools/golden.sh` left in
`dumps/golden/`, or `remove` refuses. There is no override. Never write a devendored rootfs into the
restore partition: a broken one would then be what the reset restores.

## Steps

```sh
tools/backup-unit.sh root@<speaker-ip>
tools/devendor.sh remove root@<speaker-ip> pickle   # moves it to <cfw>/../vendor-held, reboots, checks
tools/devendor.sh status root@<speaker-ip>
```

Live with it for a while; nothing is deleted yet and the egress lock is on. Then one of:

```sh
tools/devendor.sh undo root@<speaker-ip>     # puts it back, reboots
tools/devendor.sh commit root@<speaker-ip>   # deletes it and opens the egress lock
```

`commit` deletes what was held, sets `<cfw>/egress_disabled` (the boot hook then leaves the lock off)
and opens the lock at once. `<cfw>/vendor_removed` says when and in which state. A block on your router
is separate: lifting it is your decision, and the speaker works the same with it.

## Afterwards

- **Way back:** a factory reset. It restores the golden image, vendor files included, and the Wi-Fi
  setup mode ([golden-image.md](golden-image.md)); then `tools/restore-unit.sh`, then this tool again.
- **Updates:** nothing on the unit can fetch or apply one any more.
- `tools/deploy.sh` and `tools/restore-unit.sh` work as before. A restore of an older backup puts back
  a few small rootfs edits (`run.sh`, the update stub); they do nothing there.
- USB and network ADB are gone with `adbd`. SSH is the way in; the setup page adds keys.
