# Deploying the CFW (agent guide)

How to put the CFW on a speaker, update it and take it off. **Assumption:** you have root SSH to
the speaker (how that is obtained is out of scope) and the board is supported (or you're porting
it: [porting-a-speaker.md](../porting-a-speaker.md)). Read the safety doctrine in [CLAUDE.md](../../CLAUDE.md) first:
don't touch Wi-Fi/boot without a fallback, and make sound only on the unit the user names.

## What ends up on the device

`<cfw>` = `/bkupgrade/cfw` on bergamot and orion (where `/bkupgrade` is the firmware's own symlink
to `/ota`) and `/data/cfw` on pickle. Never create `/bkupgrade` on a model that lacks it — stock
scripts branch on its existence.

| Path | What |
|---|---|
| `<cfw>/bin/yastationd` | the agent (static armv7, same binary for every board) |
| `<cfw>/bin/spotter-host` | the voice front-end host, built for the device's userland arch; with `voice_id` a second process of it (`spotter-host voice`) makes the voiceprints |
| `<cfw>/bin/sendspin-host` | the Music Assistant player (optional, `sendspin = true`), built for the userland arch |
| `<cfw>/bin/dropbearmulti`, `<cfw>/bin/scp` (a link to it), `dropbearmulti.prev` | the SSH server, our build of dropbear (static aarch64, with scp; [dropbear/README.md](../../dropbear/README.md)), when the speaker already runs dropbear from there; replaced on a first deploy or with `DROPBEAR=1`, the one before kept as `.prev` |
| `<cfw>/openssh/{sshd,sshd-session,sshd-auth,sftp-server,scp,dropbearconvert}` (+ `.prev`), `ssh_host_ed25519_key`; `<cfw>/init.d/ssh.sh`; `<cfw>/ssh_server` (+ `.prev`) | the SSH server (the default; not with `SSH=dropbear`): OpenSSH, dropbear staying as the fallback, its host key converted from dropbear's, the script that starts the server `<cfw>/ssh_server` names; the owner's `trusted_user_ca_keys`, `principals/`, `sshd_config.local` next to it ([ssh.md](../ssh.md)) |
| `<cfw>/bin/hostapd` | the access point for the Wi-Fi setup mode (static armv7, same binary for every board; [wifi-setup.md](../wifi-setup.md)) |
| `<cfw>/sendspin/state.json` | its identity (stable client_id) and pairing records, 0600 |
| `<cfw>/yastationd.conf` | config (generic `yastationd/configs/yastationd.conf.example`; Orion bring-up `orion.conf.example`) |
| `<cfw>/board.conf` | optional per-unit overrides of board-profile keys |
| `<cfw>/yastationd.state` | persisted UI state (ring, display, volume, brightness, ambilight effects…), written by the daemon |
| `<cfw>/sounds/*.wav` | decoded vendor voice lines and call sounds (`make sounds`) |
| `<cfw>/vendor/vqe_models/noise_reducer/model.nnet` | the VQE's call-mode noise reducer, for the intercom (`make vqe-models`) |
| `<cfw>/vendor/bio_lingware/` | voice ID's voiceprint model for a unit whose firmware has only the old one (Mini 3 0.6.x; `make voice-models VOICE_MODELS=<a newer firmware's bio_lingware>`; [voice-id.md](../voice-id.md)) |
| `<cfw>/voices/log.jsonl`, `<cfw>/voices/edits.json` | only on the speaker with `voice_store`: every speaker's voice requests (voiceprint, transcript, verdict; the newest 3000, ~3 KB each) and the owner's names and edits of the voices. Biometric data: 0600, never leaves the home ([voice-id.md](../voice-id.md)) |
| `<cfw>/clips/*.scled` | the firmware's clock animations converted to text (`make clips`, firmware 0.369 only; others ship text clips the daemon reads in place) |
| `<cfw>/scenarios/*.json` | your own display/ring scenarios (optional) |
| `<cfw>/bin/yastationd.prev` | the previous agent, kept by every update; the supervisor swaps it back in if the new one crash-loops (the bad one is then kept as `.bad`) |
| `<cfw>/egress.sh` | the egress lock, IPv4 + IPv6 (`make deploy-egress`; §0); `<cfw>/iptables/` = a bundled iptables-legacy where the firmware has none |
| `<cfw>/yastationd.conf.bak`, `<cfw>/config.trial` | the config before the last change (web app, sync, calibration), and the trial marker while a change that needed a restart is on trial ([settings.md](../settings.md)) |
| `<cfw>/packs/<id>/` | asset packs: installed ones and the owner's own (their moments, sounds and animations, written by the web app's Behaviour tabs; [asset-packs.md](../asset-packs.md)); an old `<cfw>/moments.json` is folded into `packs/custom/` and renamed `.old` |
| `<cfw>/web.secret` | the web app's token secret: 32 random bytes, written once, 0600 (a login then survives restarts; deleting it logs everyone out) |
| `<cfw>/sync.pending`, `<cfw>/sync.rejected` | shared settings a running trial came from, and those whose trial failed here and are not taken again until they change ([settings.md](../settings.md), "Sync") |
| `<cfw>/wakes/` | only with the test option `wake_capture`: the audio around each wake word (WAV + JSON, the newest 200, ~22 MB) ([wake-word-boundary.md](../wake-word-boundary.md)) |
| `<cfw>/log-spool` | log lines waiting for the log server while it is unreachable (`log_remote`, [storage.md](../storage.md)) |
| `<cfw>/yastationd.conf` `frametap_addr` | optional, development only: the LED preview stream on its own port without a login (the host simulator); the web app has the same preview behind its login ([led-preview.md](../led-preview.md)) |
| runtime device-tree overlay `yastationd-ambilight` | orion: applied at start to give the ambilight framebuffer a white channel (configfs, until the next boot; nothing on flash; [ambilight-stream.md](../ambilight-stream.md)) |
| UDP 4048 (`ddp_port`) | boards with an ambilight: LED frames from LedFx / WLED tools ([ambilight-stream.md](../ambilight-stream.md)); `0` = no receiver |
| `<cfw>/yastationd_off` | kill switch: the supervisor stops respawning while it exists |
| `/etc/init.d/S52zyastationd` | the only rootfs file: boot hook that starts the supervisor (copy kept in `<cfw>/init.d/`) |
| `<cfw>/init.d/supervise.sh` | the supervisor (see below); without it the hook falls back to a plain 2 s respawn loop |
| `<cfw>/bin/diag.sh` | the diagnostics collector (busybox sh, read-only; uploaded by `tools/deploy.sh`, nothing runs it by itself): one redacted archive in `/tmp` to send when something is wrong ([diagnostics.md](diagnostics.md)) |
| `/tmp/yastationd.log` | log (tmpfs — gone on reboot), copy-truncated at 1 MB with one `.1` kept |

The vendor's own files stay untouched, except the two reversible guards below.

### Supervisor

`yastationd/init.d/supervise.sh` keeps the daemon up (its header has the full contract). An agent exit
with status 0 (SIGTERM/SIGINT) is deliberate and restarted after 1 s. Anything else is a crash:
restart after 2 s, doubling up to 60 s. After 5 crashes in a row, each within 5 min of the start, it
swaps in `yastationd.prev`. If that crash-loops too, it keeps retrying every 5 min. There is no
fallback to the vendor stack: the speaker stays without an agent rather than wake the vendor code.
The log shows `=== start`, `=== exit <rc> after <n>s` and `=== supervise: …` lines.

### Going back

Only this automatic swap of the daemon has been exercised. Every deploy keeps the
daemon it replaced as `yastationd.prev`; `spotter-host`, `sendspin-host` and `hostapd` are
overwritten with no copy kept. Putting the previous daemon back by hand is the same swap; send it as
a script on stdin, never as one ssh command line (the init script's `stop` matches the binary's
path, which would be on your own ssh session's command line too, §4):

```sh
ssh root@<speaker> 'sh -s' <<'EOF'
/etc/init.d/S52zyastationd stop
mv <cfw>/bin/yastationd.prev <cfw>/bin/yastationd
/etc/init.d/S52zyastationd start
EOF
```

Going back to an older release as a whole (its daemon and its helpers) has not been worked through:
deploying a checkout of the older tag with `tools/deploy.sh` should do it, but nobody has tried, and
a config written by a newer web app may carry keys the older daemon skips (or, before v0.18.0,
refuses).

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
  `lo`, and `unbound` listens on `::1`). A unit without a system iptables uses the bundled Debian
  iptables-legacy in `<cfw>/iptables/` (the install kit's; copies in `<vendor-repo>`): armhf on
  bergamot fw 0.6.1.4, whose `xtables-legacy-multi` is also its `ip6tables`; aarch64 on orion,
  whose `bin/iptables` is a multi-call binary that needs a `bin/ip6tables -> iptables` link
  (`ln -s iptables <cfw>/iptables/bin/ip6tables`). Put one architecture's bundle in the directory,
  never both. `egress.sh status` shows both families; `open` lifts both, temporarily. On the
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
  physical fallback ready and the user's go-ahead. Deploy the setup mode first — `hostapd` in
  `<cfw>/bin` plus `wifi_setup = true` ([wifi-setup.md](../wifi-setup.md)); the key combo is the one
  recovery path that does not depend on the network you are about to change.

rootfs edits: `mount -o remount,rw /` … `sync; mount -o remount,ro /`.

## 1. Build

Everything is built in pinned containers ([building.md](../building.md)); the host needs docker:

```sh
tools/build.sh            # web app, yastationd, hostapd, sendspin-host and spotter-host for both arches
```

`tools/deploy.sh` (§3a) builds what it ships by itself; this step is for a first install by hand.

`spotter-host` links the vendor front-end library, so it needs a sysroot: the vendor `/lib` and
`/system/vendor/quasar/libs` of the firmware the speaker runs. Copy it off the speaker (read-only)
into the gitignored `vendor-local/`, which also names it in `tools/build.env`:

```sh
tools/fetch-vendor.sh root@<speaker-ip> <board>
```

`tools/deploy.sh` does that by itself for an arch with no sysroot yet. A `<vendor-repo>` checkout
(`vendor/<board>/<version_full>/`) works as well, named in `tools/build.env`
(`tools/build.env.example`; [building.md](../building.md), "The sysroot").

`ARCH` = `armhf` (bergamot) or `aarch64` (pickle, orion; one build serves both) — `file /system/vendor/quasar/maind` tells you.

`sendspin-host` (the music player, optional) and `hostapd` (the Wi-Fi setup mode's access point)
need no device files; their first build fetches their pinned sources.

**Build all three helpers before you start the install** (`tools/build.sh` does). They are only ever
uploaded from the host; the speaker is offline by design and cannot fetch or compile anything later. A missing
helper is not fatal at runtime, which is what makes it easy to miss: without `spotter-host` the
daemon logs `wake word disabled: spotter binary missing` and runs push-to-talk only; without
`sendspin-host`, `sendspin = true` in the config is ignored.

The exact toolchain versions and a container that pins them: [building.md](../building.md).

## 2. First install

```sh
cd yastationd
# local.mk (gitignored): DEVICE = root@<speaker>, SSHKEY = <key> (optional), PLATFORM = <codename>
make platform-info                                 # confirms derived <cfw> and helper arch
make stage                                         # creates <cfw>: binary + init hook copy (nothing starts)
make sounds                                        # vendor sounds -> <cfw>/sounds/*.wav (decoded in a container)
make vqe-models [VQE_MODEL=<model_call.nnet>]      # intercom: the call-mode NR model (from the unit's firmware, else the file; orion has none yet)
make clips                                         # firmware 0.369 only: its binary clock clips -> <cfw>/clips/*.scled
```

Config: copy `configs/yastationd.conf.example` to `<cfw>/yastationd.conf`; for Orion, start from
`configs/orion.conf.example`. Set at least `device_id` (unique per speaker: it names the MQTT
topics and HA device), `device_name`, `mqtt_host`, the `probe` lines (broker + HA, for the health
state; leave them out on a unit with no HA), and `wake_enabled`. The Orion example pins `board = orion`; its stock «Алиса» model runs
with the NPU verifier on the unit.

A config line the binary does not take (an unknown key, a value it refuses) is skipped since
v0.18.0: the daemon logs it, the key keeps its default and the web app's settings show the line
([settings.md](../settings.md)). Before v0.18.0 it was **fatal** (`load config err="…: unknown
key"`, exit 1, five times, then the supervisor swaps in `yastationd.prev`). Either way it means a
config newer than the binary (the examples in the tree track the current source). Check which
build you just put there:

```sh
ssh root@<speaker> '<cfw>/bin/yastationd -version'
```

It must print the same `git describe` string that `make build` printed. `frametap_addr` for one
needs v0.8.1 or newer ([led-preview.md](../led-preview.md)); drop the key, or update the binary
(`tools/deploy.sh`), until they match.

**Time.** The Minis have no RTC: the clock starts at 1970 and every TLS and timestamped thing
waits for a sync (Station 3's RTC is battery-backed, `platform.rtc_backed`; it still syncs). The egress lock (§0) drops the vendor's `ntp.yantp.net`, and plenty of
routers do not answer NTP, so the default fallback candidates fail and the log shows
`time sync failed tried="ntp.yantp.net…: operation not permitted; <gateway>…: i/o timeout"`. Give
the speaker a time source on the LAN — either:

- `ntp_server = <lan-ntp>` in `<cfw>/yastationd.conf` (repeatable; tried before the rest), or
- `server <lan-ntp> iburst` in `/etc/chrony.conf`, on firmware where chronyd runs and then owns
  the sync (`time_sync` only verifies it).

Anything speaking SNTP works: `chrony`/`ntpd` on the router or HA host, or `dnsmasq`'s built-in
server when enabled. Until the clock is confirmed the front display shows only its blinking colon
([status-indication.md](../status-indication.md)); orion, whose RTC keeps time, shows it at once.

Dry run in the foreground before autostart. Ring and clock will light up:

```sh
ssh root@<speaker> '<cfw>/bin/yastationd -config <cfw>/yastationd.conf'   # Ctrl-C to stop
```

Expect `board profile=<name>`, `mqtt connected`, `wyoming listening`, `health state=online`. At
this point `wake word disabled: spotter binary missing` and `music player disabled: sendspin-host
missing` are expected: the helpers come with the deploy below.

On the ring, a **red pulse after ~40 s** means the first connection did not happen
(`boot_connect_timeout`): the broker is not reachable, or HA has not taken the Wyoming satellite
connection yet — add the integration in §3 and it goes away. A **dim red pulse** after that means a
`probe` host does not answer; the speaker still works. For a local-only unit (no HA at all), leave
`mqtt_host` empty and remove the `probe` lines, otherwise the health state can never be satisfied.

Then:

```sh
make install-initd                                 # copies the hook into /etc/init.d (remount rw/ro)
DROPBEAR=1 ../tools/deploy.sh <hosts-file>         # this speaker's line (§4)
```

The deploy builds everything in docker, ships the helpers (`spotter-host`, `sendspin-host` when
built, `hostapd`), starts the daemon through the hook and checks that it runs; `DROPBEAR=1` also
puts our SSH server in (§4, optional). Now `wake: spotter ready` shows in the log if the wake word
is on.

Reboot once (with the user's OK) and check that it all comes back on its own. On pickle, the first
start after boot fails once (the ring LEDs appear late) and the supervisor restarts it 2 s later
(one crash, far below the rollback threshold).

The hook in `/etc/init.d` changes rarely (the supervisor logic lives in `<cfw>/init.d/`), but when
it does, run `make install-initd` again: a deploy updates only `<cfw>`.

## 3. Home Assistant side

First-time voice setup (STT/TTS servers, agent, pipeline): [ha-assistant-setup.md](ha-assistant-setup.md).

- **MQTT:** nothing to do. Entities appear through MQTT Discovery under one device per
  `device_id`.
- **Voice:** add a Wyoming integration for `<speaker-ip>:10700` (UI, or the config-flow API:
  `POST /api/config/config_entries/flow {"handler":"wyoming"}` then `{"host":…, "port":10700}`).
  Then pick the Assist pipeline on the satellite's `…_assistant` select and put the device in its
  area, so room-relative commands work.
- **This step is what makes the speaker "ready".** The daemon counts itself connected only when the
  broker is up *and* HA holds the Wyoming satellite link; until then the log says
  `status: no connection to HA yet after=40s` and `voice: no HA satellite connection` (a wake word
  is detected and then dropped), and the ring pulses red. A fresh install that looks broken is
  usually just missing this integration — add it before diagnosing anything else.
- HA offers the Wyoming integration and, when you first build a pipeline, a Wyoming/piper add-on.
  Only the satellite runs on the speaker: STT, TTS and the conversation agent are HA-side or on
  another host ([ha-assistant-setup.md](ha-assistant-setup.md)).
- `hass/scripts.json`: the Assist-callable scripts (ring animation, show text). Create them once per
  HA instance.

## 3a. The web app

Every speaker serves the app at `http://<speaker-ip>/` (port 80, `http_addr`). Log in with
**admin / yastation** — the documented default; change it under Account (it is written into that
speaker's `yastationd.conf` as `web_user` and a `web_password` hash; each speaker has its own). It
lists the other speakers by itself (they announce themselves over MQTT), shows sensors and the
runtime controls, the LED preview, moments, the saved Wi-Fi networks (add only), and edits the
config with a validated save: the speaker restarts with it on trial and goes back to the old one if
it gets worse ([web-ui.md](../web-ui.md)). Nothing to install: the app is embedded in `yastationd`
and every deploy updates it. Port 80 is open to the LAN; the egress lock only filters outgoing
traffic.

## 4. Updating

`tools/deploy.sh` with a `tools/deploy.hosts` list (see `tools/deploy.hosts.example`); for some
of the speakers, a hosts file with just their lines. Each line is `<ssh-target> <platform>
[cfw-root override] [helper-arch override]`; the board profile supplies omitted values.

It builds once in docker (yastationd, and per userland arch `spotter-host` and `sendspin-host`;
`hostapd` is a local build, else the newest recorded one in `$VENDOR/builds/hostapd/`, and a
speaker left without one gets a warning). Then, one speaker at a time, it uploads aside (the
daemon, `supervise.sh`, `diag.sh`, the helpers), stops → keeps the old binary as `.prev` → swaps →
starts, checks that the daemon runs and prints its new WARN/ERROR lines, and `ok`. It stops at the
first failing speaker, so a broken build never reaches the rest.

**dropbear.** On a first deploy (no `yastationd` on the speaker yet; after `make stage` there is
one, hence `DROPBEAR=1` in §2), or with `DROPBEAR=1`,
`tools/deploy.sh` also replaces the speaker's SSH server with ours (`dropbear/`), where it runs from
`<cfw>/bin/dropbearmulti`. The swap cannot lock you out on its own: the new binary first runs as a
test server on port 2222 with the live listener's arguments (same host key); only a login through
it does the swap, keeping the old binary as `dropbearmulti.prev`, and restarts the port-22
listener (sessions in progress stay up). A login on port 22 then confirms it and stops the test
server; if port 22 does not answer, the old binary is put back through port 2222 and the run
stops. `DROPBEAR=only` does only this, without building or updating anything else (for
speakers that should keep their running build). The boot hook starts the same path, so a reboot keeps the new one. To go back by hand:
`mv <cfw>/bin/dropbearmulti.prev <cfw>/bin/dropbearmulti` and restart the listener (or reboot).

**OpenSSH on port 22.** Every deploy then makes OpenSSH the server on port 22 (`SSH=dropbear`
keeps dropbear; with `DROPBEAR=only`, nothing else is updated), the same careful way: tried on
port 2222, a login through it switches, a login on 22 confirms, else the previous one comes back.
After the first switch the boot hook is made to start it too (a rootfs write, once; the kit's
hook kept as the fallback). A speaker already on the same build is left alone. [ssh.md](../ssh.md).

**Don't** improvise the swap as one ssh command line that contains the binary's path. The init
hook's `stop` kills processes by that path and takes your ssh session down with it, leaving the
daemon stopped. Both tools run the swap as a script on stdin for this reason.

The web app comes with the binary: a deploy updates it. `make build` (which `tools/deploy.sh`
runs) builds it from `web/` in the pinned builder container whenever its source changed, so the
build machine needs docker ([docs/building.md](../building.md)).

Config changes: from the web app (Config tab: validated, restarted on trial, rolled back if worse),
or edit `<cfw>/yastationd.conf` and `/etc/init.d/S52zyastationd restart`. Profile
tweaks without a rebuild: `<cfw>/board.conf` (+ restart).

## 5. Disabling and removing

- Pause: `touch <cfw>/yastationd_off; /etc/init.d/S52zyastationd stop` (rm the flag + `start` to resume).
- Remove: stop it, delete `/etc/init.d/S52zyastationd` (rootfs remount), then `<cfw>/bin/*` and
  `<cfw>/yastationd.*`. HA keeps the MQTT device until you delete it there (or clear the retained
  `homeassistant/+/yastation_<id>/#` configs).
- Re-enabling the vendor stack (removing `maind_off`, restoring `run.sh.orig` / the update script)
  also brings back its internet use and updates. Don't, unless the user asks for exactly that.

## 6. Verification checklist

- [ ] `ps w`: one `yastationd`, one `spotter-host` (wake on), no vendor stack processes;
      one `sendspin-host` when `sendspin = true`.
- [ ] `<cfw>/bin/` holds what the config asks for: `spotter-host` for wake, `sendspin-host` for
      music, `hostapd` for the Wi-Fi setup mode (a missing helper only shows as a log line).
- [ ] `yastationd -version` equals the build you deployed (a config key from a newer tree is
      skipped and logged; a build before v0.18.0 exits 1 on it instead).
- [ ] Log clean after start: profile, MQTT, Wyoming, health online, clock confirmed/in sync.
- [ ] HA: device with ring light, display, volume, brightness, mic mute, illuminance, supply voltage;
      the Wyoming satellite shows idle.
- [ ] Keys: +/− change volume (shown on the display), the voice key starts a session, mute works.
- [ ] Voice: push-to-talk → answer spoken; then the wake word (sound — only on the agreed unit).
- [ ] Reboot survives (with the user's OK); no internet from the device; the HA/broker probes are fine.
- [ ] `<cfw>/egress.sh status`: IPv4 `LOCKED` and `ipv6: LOCKED` (or `OFF`); `ota_script.pub` differs
      from `ota_script.pub.orig`.

## 7. Diagnostics and gotchas

- **Collect first, then ask.** `<cfw>/bin/diag.sh` (or `ssh root@<speaker> 'sh -s' <
  yastationd/scripts/diag.sh` on a unit without it) packs versions, the config (redacted), the
  logs, `dmesg`, the network, the vendor guards and the ALSA state into one archive;
  `tools/diag-build.sh` does the same for a failing build or deploy on the host
  ([diagnostics.md](diagnostics.md)). **Never read every ALSA mixer control** (`amixer contents`,
  `controls`, `scontents`, or a loop over them) on bergamot: on 2026-10-28 that froze the kernel
  inside the codec driver and the unit needed a power cycle. Read named controls with `cget`, and
  the stream state from `/proc/asound`.
- **A config line the daemon skips** (logged at start, and the settings pages show it), or, on a
  build before v0.18.0, **`load config err="…: unknown key"`, exit 1, over and over.** The config
  knows a key the binary does not — the binary is older than the tree the config was copied from.
  Since v0.18.0 the line is only skipped (the key keeps its default), so check it is not one you
  meant to set; before, it was fatal, the supervisor moved `yastationd.prev` back (just as old)
  and kept retrying every 5 min, and `touch <cfw>/yastationd_off` stopped that. Fix the pair, don't edit around it: `yastationd -version`
  tells you the binary, and `tools/deploy.sh` brings it up to the source. Known
  dates: `frametap_addr` and the `video_*` keys need v0.8.1+, and anything merged since the last tag
  exists only in a build from `main`. Both examples in `configs/` follow the current source, so an
  old binary plus a fresh example is exactly this failure.
- **`time sync failed tried="ntp.yantp.net…: operation not permitted; <gateway>…: i/o timeout"`.**
  Not a bug: the egress lock (§0) drops the vendor server, and the gateway is not an NTP server.
  Set `ntp_server = <lan-ntp>` in the config, or a `server` line in `/etc/chrony.conf` where chronyd
  runs (§2). Meanwhile the front display shows a bare blinking colon and anything comparing
  timestamps is wrong.
- **`wake word disabled: spotter binary missing path=<cfw>/bin/spotter-host`.** The helper was never
  built or deployed; the daemon deliberately runs on (push-to-talk still works). Build it against a
  sysroot from `<vendor-repo>` and deploy (`tools/deploy.sh` ships it; §1, §4). Same story for
  `sendspin-host` and music, and for `hostapd` and the Wi-Fi setup mode.
- **Music does not play although HA shows the player.** Check the helper first:
  `<cfw>/bin/sendspin-host` exists (`sendspin = true` without it does nothing) and
  `ps w | grep sendspin`. Then the two volumes: with `sendspin_volume = linked` the Music
  Assistant player's level *is* the speaker's, so a speaker at 5 % is inaudible; with `separate`
  they are independent. Music Assistant discovers the player by itself — no entry to add by hand.
- **Front display shows a blinking digit instead of the clock.** A blinking *colon* is the
  "time not confirmed" face ([status-indication.md](../status-indication.md)); a lone blinking digit
  is not a documented state. If `date` is right and the log says the clock is confirmed, capture
  the display-related lines from the start and compare with a second unit on the same tag — and
  check `auto_brightness`: a lux reading near 0 (sensor behind the grille) drives brightness to
  the low end of `auto_brightness_curve`.
- **Ring red on a fresh install.** Red pulse = no first connection within `boot_connect_timeout`
  (broker unreachable, or HA has not added the Wyoming satellite yet); dim red pulse = a `probe`
  target does not answer. Both are status, not failure — see §2 and
  [status-indication.md](../status-indication.md).
- **Two speakers, different loudness at the same %?** Check, in order: the same `yastationd`
  version on both (HA's Version sensor; deploy with `tools/deploy.sh` rather than by hand — the
  volume curve is in the board profile compiled into the binary); the same model (each board has
  its own stock curve; orion's since v0.8.3); the same firmware — the boot script
  `S90acm8625-settings` loads a per-firmware amplifier tuning, and a Mini 3 on 0.369 ends up with
  `main Acm8625 AMP Post Gain` = 13 where 0.6.1.4 has 0 (`amixer -c0 cget name='main Acm8625 AMP
  Post Gain'`), so the 0.369 unit is louder at every level; no `audio.volume_map` or
  `volume_max` override in one of them (`<cfw>/board.conf`, `yastationd.conf`). The loudness
  does not depend on what the vendor stack did before: it never runs, and ALSA state is not
  stored across boots. On the unit itself: `amixer -c 0 sget 'Master Playback Volume'` (0..255,
  what the curve gave), every `… Acm8625 AMP mute status` must be 0, and `ps w | grep hardvold`
  (without it the master control does not reach the amps). A different curve without a rebuild:
  `audio.volume_map = 0:0, 4:20, 8:60, 12:120, 20:255` in `<cfw>/board.conf` (levels 0..20, raw up
  to `audio.volume_max`).
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
