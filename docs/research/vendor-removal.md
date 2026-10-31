# Taking the vendor's software off a speaker

Research (2026-10-31) on a Mini 3 Pro (`pickle`), the first board with a way back: a
[golden image](../guides/golden-image.md) in its restore partition. The question: can the vendor's
software be deleted rather than gated, so that the egress lock stops being needed? The tool that came
out of it: [guides/devendor.md](../guides/devendor.md).

**Verdict:** on pickle yes, and easily. Everything the vendor's stack did that the CFW needs fits in
one line. What must stay are two libraries and some models, and those have no network code. The
rootfs goes from 80 MB to 44 MB.

**Status.** Moved aside and booted, several times, with voice, music, MQTT and Home Assistant working;
`undo` puts it all back. **The final step (deleting for good and opening the egress lock) has not been
run on the unit**, so nothing below describes a speaker running with the lock off.

## What the CFW needs from the vendor

On a running unit, of everything under `/system/vendor/quasar`, only these four are open or mapped:
`libs/libyandex_evil.so`, `libs/libtflite.so`, and the `alisa` and `command` models. Voice ID adds
`bio_lingware` (the speaker's identity, loaded when it runs), the display `ledpatterns/clock`.

**Those libraries can't reach the network on their own.** `libyandex_evil.so` and `libtflite.so`
import no `socket`, `connect`, `sendto` or `getaddrinfo`. They do import `syscall` and `dlopen` (the
one library name in there besides libc's is `libhifi4rpc*`, which this firmware doesn't have), so the
import table is strong evidence, not a proof. On a live Station 3, `spotter-host` holds pipes, model
files and `/dev/galcore`, and no socket.

`hardvold` (volume) stays too, for now: vendor-written, nothing to do with the network. So do the
small `yandex-*` shell and binary helpers the boot scripts call (`yandex-wifichipmodel`,
`yandex-waitfile`, `yandex-apply-dtb-overlay`, `yandex-get-localization`, `yandex-wifireset`).

## What the boot needs

The only reference to `/system/vendor/quasar` in the rest of the rootfs is `S99quasar`, which runs
`onboot.sh`. Of what that does:

| In `onboot.sh` | Needed? |
|---|---|
| `vm.overcommit_memory = 1` | kept: the replacement `S99quasar` is this line |
| `/data/quasar` directories | only for the stack |
| `yio_iptables_init.sh` (the `yio_*` chains) | no; INPUT and OUTPUT policies are ACCEPT, `egress.sh` builds its own |
| `set_monotonic_time.sh`, `q save-last-monotonic-time` | only for the stack |
| `activate_adb.sh check` | no |
| `run.sh` | the stack itself |

`yandex-boot-completed` (it would set `boot_completed` in the env) has no caller and no config here,
and the variable is empty: no boot counter waits for it.

## What can reach the network

Everything in the rootfs that imports `connect`, `sendto`, `sendmsg` or `getaddrinfo`, with what
becomes of it:

| Program | After removal |
|---|---|
| `maind` | gone |
| `adbd`, `chronyd`, `chronyc`, `unbound*` | gone or no longer started. chrony's stock server is the vendor's pool; unbound starts unasked on every boot and answers only if `/data/unbound_enabled` routes DNS to it, which only maind sets |
| `wpa_supplicant`, `dhcpcd`, `wpa_cli` | needed; the local network only |
| `busybox` (`wget`, `ping`...) | stays; runs only when something runs it |
| `bluetoothd`, `bluealsa`, BlueZ tools | stay; Bluetooth, no IP |
| `mpg123`, `dbus-cleanup-sockets`, `gio-launch-desktop` | stay; local sockets, nothing starts them |

The CFW's own programs talk to the LAN only. The web app's update check is made by the viewer's
browser, not by the speaker.

## Does the egress lock still do anything?

Counters of its final DROP rule, gated units with `maind` not running:

| Unit | Up | Dropped |
|---|---|---|
| Station 3 | 20 h | 0 |
| Mini 3 | 3 days | 2315 packets, 78 bytes on average |
| Mini 3 | 10 h | 803 packets, 78 bytes on average |
| Mini 3 Pro, vendor moved aside | minutes | 0 |

So with the stack gated, a Station 3 tries nothing at all. The Mini 3's trickle has the size of NTP;
what sends it was not traced and it is not on pickle (the install points chrony at the LAN). Keeping
the lock is cheap, and a block on the router stays independent of all this: the case for lifting them
is that nothing is left that would use the hole, not that the hole is harmless.

## What is different elsewhere

Not looked at. The list of what goes is per board (`tools/devendor/<board>.conf`), and bergamot has
no golden image, so no way back: the tool refuses. orion has a proven [golden image](../guides/golden-image.md#orion), but no list yet.
