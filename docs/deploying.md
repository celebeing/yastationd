# Deploying the CFW (agent guide)

How to put the CFW on a speaker, update it and take it off. **Assumption:** you have root SSH to
the speaker (how that is obtained is out of scope) and the board is supported (or you're porting
it: [porting-a-speaker.md](porting-a-speaker.md)). Read the safety doctrine in `CLAUDE.md` first:
don't touch Wi-Fi/boot without a fallback, and make sound only on the unit the user names.

## What ends up on the device

`<cfw>` = `/bkupgrade/cfw` on bergamot and `/data/cfw` on pickle/orion. Never create
`/bkupgrade` on a model that lacks it — stock scripts branch on its existence.

| Path | What |
|---|---|
| `<cfw>/bin/yastationd` | the agent (static armv7, same binary for every board) |
| `<cfw>/bin/spotter-host` | the voice front-end host, built for the device's userland arch |
| `<cfw>/bin/sendspin-host` | the Music Assistant player (optional, `sendspin = true`), built for the userland arch |
| `<cfw>/bin/hostapd` | the access point for the Wi-Fi setup mode (static armv7, same binary for every board; [wifi-setup.md](wifi-setup.md)) |
| `<cfw>/sendspin/state.json` | its identity (stable client_id) and pairing records, 0600 |
| `<cfw>/yastationd.conf` | config (generic `yastationd/configs/yastationd.conf.example`; Orion bring-up `orion.conf.example`) |
| `<cfw>/board.conf` | optional per-unit overrides of board-profile keys |
| `<cfw>/yastationd.state` | persisted UI state (ring, display, volume, brightness…), written by the daemon |
| `<cfw>/sounds/*.wav` | decoded vendor voice lines and call sounds (`make sounds`) |
| `<cfw>/vendor/vqe_models/noise_reducer/model.nnet` | the VQE's call-mode noise reducer, for the intercom (`make vqe-models`) |
| `<cfw>/clips/*.scled` | the firmware's clock animations converted to text (`make clips`, firmware 0.369 only; others ship text clips the daemon reads in place) |
| `<cfw>/scenarios/*.json` | your own display/ring scenarios (optional) |
| `<cfw>/bin/yastationd.prev` | the previous agent, kept by every update; the supervisor swaps it back in if the new one crash-loops (the bad one is then kept as `.bad`) |
| `<cfw>/egress.sh` | the egress lock, IPv4 + IPv6 (`make deploy-egress`; §0); `<cfw>/iptables/` = a bundled iptables-legacy where the firmware has none |
| `<cfw>/yastationd_off` | kill switch: the supervisor stops respawning while it exists |
| `/etc/init.d/S52zyastationd` | the only rootfs file: boot hook that starts the supervisor (copy kept in `<cfw>/init.d/`) |
| `<cfw>/init.d/supervise.sh` | the supervisor (see below); without it the hook falls back to a plain 2 s respawn loop |
| `/tmp/yastationd.log` | log (tmpfs — gone on reboot), copy-truncated at 1 MB with one `.1` kept |

The vendor's own files stay untouched, except the two reversible guards below.

**Supervisor** (`yastationd/init.d/supervise.sh`; the header has the full contract). An agent exit
with status 0 (SIGTERM/SIGINT) is deliberate and restarted after 1 s. Anything else is a crash:
restart after 2 s, doubling up to 60 s. After 5 crashes in a row, each within 5 min of the start, it
swaps in `yastationd.prev`. If that crash-loops too, it keeps retrying every 5 min. There is no
fallback to the vendor stack: the speaker stays without an agent rather than wake the vendor code.
The log shows `=== start`, `=== exit <rc> after <n>s` and `=== supervise: …` lines. Manual rollback:
`/etc/init.d/S52zyastationd stop; mv <cfw>/bin/yastationd.prev <cfw>/bin/yastationd;
/etc/init.d/S52zyastationd start` (as a script on stdin, see §4).

## 0. Keep the vendor offline and asleep (once per speaker)

The CFW assumes the vendor assistant stack does not run and the speaker cannot reach the internet.
Check, and set up what's missing (keep an `.orig` of every file you change):

- **Gate the vendor stack.** In `/system/vendor/quasar/run.sh`, before it starts
  `quasar_launcher2`: if `<cfw>/maind_off` exists, exit without launching. Then `touch
  <cfw>/maind_off`. Check after a reboot: `ps w | grep -c [m]aind` → 0. Leave `onboot.sh` and the
  init chain alone. The init layer (Wi-Fi, DHCP, ALSA, LEDs) does not depend on the stack (verified
  on bergamot and pickle; see their board docs). Never delete vendor binaries.
- **Block updates.** `/system/vendor/quasar/device_ota_update.sh` → `exit 0`. A third, independent
  guard: `make ota-block` replaces the OTA signing key `/system/vendor/quasar/ota_script.pub` with
  `yastationd/init.d/ota-block.pub`, a public key whose private half was never kept, so no update
  package can pass the signature check. The vendor key stays as `ota_script.pub.orig` (restore:
  copy it back, rootfs remount). The key is used only by the update path, never at boot.
- **Block the internet at two layers.** On the device, `<cfw>/egress.sh lock` (source:
  `yastationd/init.d/egress.sh`, installed and applied by `make deploy-egress`; the running copy is
  kept as `egress.sh.prev`), run at boot by `/etc/init.d/S99zzegress` after the vendor's own
  iptables init. IPv4: loopback, RFC 1918, link-local, multicast, broadcast, DHCP and established
  flows pass, everything else is dropped. IPv6, the same: loopback, `fe80::/10`, `fc00::/7`,
  `ff00::/8` and established pass, global addresses are dropped; without any `ip6tables` it switches
  IPv6 off on every interface but `lo` instead (not via `conf/all`, which the kernel copies onto
  `lo`, and `unbound` listens on `::1`). A unit without a system iptables (bergamot fw 0.6.1.4) uses
  the bundled Debian iptables-legacy in `<cfw>/iptables/`, whose `xtables-legacy-multi` is also its
  `ip6tables`. `egress.sh status` shows both families; `open` lifts both, temporarily. On the
  router, drop the speaker's MAC towards the WAN (make sure the rule covers IPv6 as well, if the
  network ever gets it). Check from the device: `wget -q -T 5 -O- http://1.1.1.1` must fail, while
  the HA host and the broker answer.
- **DNS.** The firmware's resolver is `unbound` on `127.0.0.1:53` (`resolv.conf` points at it on
  bergamot); it forwards to the router and to Yandex's DNS (`77.88.8.x`), and `quasar.cfg` carries a
  `DnsFallbackMap` of hard-coded addresses for when DNS fails — so DNS alone never isolates a
  speaker. The egress lock drops the Yandex resolvers and the fallback addresses like any other
  public address.
- **Wi-Fi.** `wpa_supplicant.conf` should list only the networks you mean it to use: a vendor
  stack that once ran may have stored extra ones. Editing it can cut your access, so only with a
  physical fallback ready and the user's go-ahead.

rootfs edits: `mount -o remount,rw /` … `sync; mount -o remount,ro /`.

## 1. Build

```sh
cd yastationd && make test build                  # -> build/yastationd
```

`spotter-host` links the vendor front-end library, so it needs a sysroot from the device itself:
its `/lib` and `/system/vendor/quasar/libs`. Pull them into the gitignored `dumps/`:

```sh
mkdir -p dumps/sysroot-<board> && ssh root@<speaker> 'tar cf - /lib /system/vendor/quasar/libs' | tar xf - -C dumps/sysroot-<board>
ARCH=aarch64 SYSROOT=dumps/sysroot-<board> OUT=spotter-host/build/aarch64/spotter-host sh spotter-host/build.sh
```

`ARCH` = `armhf` (bergamot) or `aarch64` (pickle) — `file /system/vendor/quasar/maind` tells you.

`sendspin-host` (the music player, optional) needs no device files: `make -C sendspin-host` builds
both arches with zig 0.14.1 (see its README).

`hostapd` (the Wi-Fi setup mode's access point) likewise: `make -C hostapd` builds one static
binary for every board with zig 0.14.1 (see [wifi-setup.md](wifi-setup.md)).

## 2. First install

```sh
cd yastationd
# local.mk (gitignored): DEVICE = root@<speaker>, SSHKEY = <key>, PLATFORM = <codename>
make platform-info                                 # confirms derived <cfw> and helper arch
make stage                                         # binary + init hook copy into <cfw> (no autostart)
make deploy-spotter                                # profile selects build/<helper-arch>/spotter-host
make deploy-sendspin                               # optional; profile selects helper architecture
make sounds                                        # vendor sounds -> <cfw>/sounds/*.wav
make vqe-models [VQE_MODEL=<model_call.nnet>]      # intercom: the call-mode NR model (from the unit's firmware, else the file)
make clips                                         # firmware 0.369 only: its binary clock clips -> <cfw>/clips/*.scled
```

Config: copy `configs/yastationd.conf.example` to `<cfw>/yastationd.conf`; for Orion, start from
`configs/orion.conf.example`. Set at least `device_id` (unique per speaker: it names the MQTT
topics and HA device), `device_name`, `mqtt_host`, the `probe` lines (broker + HA, for the health
state), and `wake_enabled`. The Orion example pins `board = orion` during bring-up and keeps
on-device wake disabled until its NPU verifier is physically tested.

Dry run in the foreground before autostart. Ring and clock will light up:

```sh
ssh root@<speaker> '<cfw>/bin/yastationd -config <cfw>/yastationd.conf'   # Ctrl-C to stop
```

Expect `board profile=<name>`, `mqtt connected`, `wyoming listening`, `health state=online`, and
`wake: spotter ready` if the wake word is on. Then:

```sh
make install-initd                                 # copies the hook into /etc/init.d (remount rw/ro)
ssh root@<speaker> /etc/init.d/S52zyastationd start
```

Reboot once (with the user's OK) and check that it all comes back on its own. On pickle, the first
start after boot fails once (the ring LEDs appear late) and the supervisor restarts it 2 s later
(one crash, far below the rollback threshold).

The hook in `/etc/init.d` changes rarely (the supervisor logic lives in `<cfw>/init.d/`), but when
it does, run `make install-initd` again: `make deploy` updates only `<cfw>`.

## 3. Home Assistant side

First-time voice setup (STT/TTS servers, agent, pipeline): [ha-assistant-setup.md](ha-assistant-setup.md).

- **MQTT:** nothing to do. Entities appear through MQTT Discovery under one device per
  `device_id`.
- **Voice:** add a Wyoming integration for `<speaker-ip>:10700` (UI, or the config-flow API:
  `POST /api/config/config_entries/flow {"handler":"wyoming"}` then `{"host":…, "port":10700}`).
  Then pick the Assist pipeline on the satellite's `…_assistant` select and put the device in its
  area, so room-relative commands work.
- `hass/scripts.json`: the Assist-callable scripts (ring animation, show text). Create them once per
  HA instance.

## 4. Updating

One speaker: `make deploy` (build, upload aside along with `supervise.sh`, stop → keep the old
binary as `.prev` → swap → start, tail the log). Several: `tools/deploy.sh` with a
`tools/deploy.hosts` list (see `tools/deploy.hosts.example`). Each line is `<ssh-target>
<platform> [cfw-root override] [helper-arch override]`; the board profile supplies omitted
values. The script also rolls out `spotter-host` (and `sendspin-host` and `hostapd`, when built) and stops at
the first failing speaker.

**Don't** improvise the swap as one ssh command line that contains the binary's path. The init
hook's `stop` kills processes by that path and takes your ssh session down with it, leaving the
daemon stopped. Both tools run the swap as a script on stdin for this reason.

Config changes: edit `<cfw>/yastationd.conf`, then `/etc/init.d/S52zyastationd restart`. Profile
tweaks without a rebuild: `<cfw>/board.conf` (+ restart).

## 5. Disabling and removing

- Pause: `touch <cfw>/yastationd_off; /etc/init.d/S52zyastationd stop` (rm the flag + `start` to resume).
- Remove: stop it, delete `/etc/init.d/S52zyastationd` (rootfs remount), then `<cfw>/bin/*` and
  `<cfw>/yastationd.*`. HA keeps the MQTT device until you delete it there (or clear the retained
  `homeassistant/+/yastation_<id>/#` configs).
- Re-enabling the vendor stack (removing `maind_off`, restoring `run.sh.orig` / the update script)
  also brings back its internet use and updates. Don't, unless the user asks for exactly that.

## 6. Verification checklist

- [ ] `ps w`: one `yastationd`, one `spotter-host` (wake on), no vendor stack processes.
- [ ] Log clean after start: profile, MQTT, Wyoming, health online, clock confirmed/in sync.
- [ ] HA: device with ring light, display, volume, brightness, mic mute, illuminance, supply voltage;
      the Wyoming satellite shows idle.
- [ ] Keys: +/− change volume (shown on the display), the voice key starts a session, mute works.
- [ ] Voice: push-to-talk → answer spoken; then the wake word (sound — only on the agreed unit).
- [ ] Reboot survives (with the user's OK); no internet from the device; the HA/broker probes are fine.
- [ ] `<cfw>/egress.sh status`: IPv4 `LOCKED` and `ipv6: LOCKED` (or `OFF`); `ota_script.pub` differs
      from `ota_script.pub.orig`.

## 7. Diagnostics and gotchas

- **Why did it reboot?** `cat /sys/class/aml_info/reboot_reason` on bergamot (`cold_boot`, `normal`,
  `watchdog_reboot` = the system hung and the watchdog reset it — almost always a kernel module or
  a boot script). pickle has no such file.
- **The last boot's kernel log is gone.** The firmware keeps a `ramoops` pstore for crash logs, but
  `/etc/init.d/S98pstore-clear` wipes it on a normal boot. Catch a crash live instead:
  `ssh root@<speaker> 'cat /proc/kmsg' | tee kmsg.log` while reproducing it. (If this is ever needed
  often: copy `/sys/fs/pstore/*` into `<cfw>` from a hook that runs before `S98`.)
- **Vendor daemon logs**, should the vendor stack ever be run: `/data/quasar/daemons_logs/`.
- **ALSA does not re-read `/etc/asound.conf` in running players.** alsa-lib reloads its config only
  when the top `/usr/share/alsa/alsa.conf` changes (`asound.conf` is pulled in by a hook from it):
  after editing `asound.conf`, `touch /usr/share/alsa/alsa.conf`, or restart the players.
- **A `softvol` makes its control on the card of its output.** If that is not card 0, a second
  `Master` appears and the vendor volume path (hardvold, our `Master Playback Volume`) stops moving
  the sound: give the `softvol` `control { name "…" card 0 }`.
- **BusyBox:** no `timeout`, `awk` without math (`exp`, `log`), `tar` without `-z` (use
  `gunzip -c | tar x`), `sync` takes no arguments, and `test -w` says "writable" on a read-only
  mount — check `/proc/mounts` instead. A `sync` can hang behind a stuck `mount`; then `fsync`
  single files and power-cycle.
