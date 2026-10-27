# CLAUDE.md — yastationd (the CFW)

Custom firmware layer (CFW) that turns owned smart speakers (`bergamot` = Station Mini 3,
`pickle` = Station Mini 3 Pro, …) into offline Home Assistant speakers: the on-device agent
`yastationd` + the voice front-end host `spotter-host`. What it does: `README.md`. Porting it to a
new model: `docs/porting-a-speaker.md`. Installing/updating it on a speaker: `docs/deploying.md`. **Connection details: `ACCESS.local.md` (gitignored).**

**Scope.** The CFW's functionality, researching new speakers and adapting the CFW to them, and
keeping the vendor's code from running or talking to the internet. **Out of scope:** how shell access
to a speaker is obtained — we assume the user already has legitimate SSH access to their own unit.
Don't add material on that here.

> **Visibility:** **Public** (GitHub `celebeing/yastationd`, MIT). Everything committed is world-readable.

## ⚠️ Working with the speaker — safety doctrine (READ BEFORE ANY DEVICE CHANGE)

Our only way into a speaker is the access the user set up (network SSH; on some units also a
physical USB fallback — see `ACCESS.local.md`). Losing it, or a speaker that no longer boots, may
not be recoverable by us. Hence the hard rules:

- **Never sever your own access.** Any change to Wi-Fi (`wpa_supplicant.conf`), the egress firewall
  (`<cfw>/egress.sh`), dropbear, routing, or the LAN path can lock you out. Before such a change make
  sure a physical fallback exists and the user is ready to use it. Prefer reversible flags over
  destructive edits. When in doubt, don't — ask the user.
- **Never risk the boot.** rootfs is UBIFS mounted `ro`; edit only via `mount -o remount,rw /` …
  `sync; mount -o remount,ro /`, keep `.orig` backups, keep init.d hooks reversible, and take a
  backup before any risky rootfs write. Do not touch the bootloader, the kernel, or anything that
  could change the boot chain.
- **We don't trust the vendor: its code must not run, and the speaker must not reach the internet.**
  The vendor assistant stack (`maind` / `quasar_launcher2`) is kept from starting by a reversible gate
  (`<cfw>/maind_off`, checked in `/system/vendor/quasar/run.sh`; original kept as `run.sh.orig`). Do
  not remove the flag and do not run the vendor binaries or `onboot.sh` in ways that start the
  stack. Reusing the vendor voice front-end through our own host that `dlopen`s
  `libyandex_evil.so` is fine (it does not start the stack). You may add a second guard
  (`chmod -x maind`), but **never delete** the vendor blobs: the front-end library is needed, deletion
  loses reversibility, and a bad delete can break boot. Internet egress stays blocked at two
  independent layers (on-device `egress.sh lock` + the router), and the CFW talks only to the LAN.
- **Never allow a firmware update.** An update replaces the rootfs, removes the CFW and brings the
  vendor stack back. The update blocks (`device_ota_update.sh` = `exit 0`, the vendor updater never
  starting with the stack gated, no internet) must stay.
- **Confirm before destructive or outward-facing actions.** Approval for one action does not carry to
  the next. Making a speaker emit sound / flash / demand attention: only when the user expects it,
  and only on the unit they name.

## Hardware / environment facts
- Per-model facts live in `docs/boards/<codename>.md`. bergamot: armhf userland (glibc 2.30) on an
  arm64 kernel, CFW root `/bkupgrade/cfw`. pickle: aarch64 userland (glibc 2.36), CFW root
  `/data/cfw` (no `/bkupgrade` — never create one). orion: aarch64 userland, CFW root `/bkupgrade/cfw`
  (the firmware's own `/bkupgrade` is a symlink to the writable ext4 `/ota`). `yastationd` is static armv7 and runs on all three; anything
  linking vendor libs (`spotter-host`) must match the userland.
- No python / compiler / MQTT client on the devices; `aplay/arecord/amixer`, busybox tools are
  there. **Missing:** `scp` (stream files via `ssh … 'cat > f' < f`), `timeout`, `pkill`/`pgrep`
  (find pids via `ps w`; don't match by substring of a command line — it hits your own ssh session).
- Interfaces driven from the shell: LEDs/display (`/sys/class/leds/*`), ALSA, evdev input, IIO
  light sensor, USB-C PD (`/sys/class/power_supply/ya_pd`). Details in the board docs,
  `docs/device-teardown.md`, `docs/wakeword-mic-array.md`, `docs/yastationd-design.md`.

## Repo hygiene
- `dumps/` and `ACCESS.local.md` are **sensitive and gitignored** — never commit or share them.
- Remotes: `origin` = GitHub `celebeing/yastationd` (public; every push needs the user's key tap —
  push only while the user is around, never as the last step of a long run), `gitea` = the private
  Gitea mirror (touchless; its pre-publication history lives on branch `history`). Push to both.
- Deployment-specific values (LAN addresses, hostnames, SSID, key paths, private repo names, people's
  names) are **placeholders** in committed files; the real values live in gitignored
  `PRIVATE.local.md` (placeholder → value map) and `yastationd/local.mk` (build target). Keep it so.

## Directives

- All configuration (URLs, API keys, DB credentials, hostnames) **must** come from configuration files, never hardcoded. Prefer stable file-based formats (yaml, toml) for config; use environment variables **only** when there is genuinely no other way (e.g. a secret that must not touch disk, or a platform that injects config solely via env). Use generic placeholders in examples.
- **Read `docs/` before planning** — always consult existing research, findings, and design docs before proposing changes or starting new work. Start from the docs index (`docs/index.md`) to find the relevant document. The docs contain hard-won knowledge about API quirks, country-specific parsing rules, and known limitations.
- **Update `docs/` before committing** — when implementation reveals new findings, edge cases, or changes to architecture/decisions, update the relevant docs first, and keep the docs index (`docs/index.md`) in sync. Docs must stay in sync with the code. If a doc doesn't exist for a new area, create one and add it to the index.
- **Language** — all committed artifacts (code, comments, identifiers, commit messages, docs, `CLAUDE.md`) are written in **English**. The agent's conversation with the user may be in **any language** — match the user; only the artifacts are English-only. **One exception:** the main `README.md` is Russian; `README.en.md` is its English twin. Change both together, so they say the same thing.

## Changes that ripple: deploy, build, vendor repo

A feature is not done when the code works. In the **same change**, whenever it:

- **puts something new on the speaker** (a file, a binary, a port, a default that changes behaviour)
  → `docs/deploying.md` ("What ends up on the device", install/update steps), and
  `tools/deploy.sh` if it must be uploaded;
- **adds a config key or a board-profile key** → both `yastationd/configs/*.example`, and
  `docs/porting-a-speaker.md` for profile keys;
- **needs a new build step or toolchain** (a helper, a front-end, a pinned tool) → its Makefile
  target, a target in `tools/build.sh` (and a `docker/<toolchain>.Dockerfile` when it needs a
  new toolchain), `docs/building.md`;
- **is something a user would notice** → `README.md` (and `yastationd/README.md` for the daemon),
  with any login, address or default they need;
- **uses or produces a binary we don't rebuild from source each time** (a vendor library, a
  third-party bundle, a build we keep) → record it in `<vendor-repo>` (`tools/record-binary.sh`),
  and after changing what a unit carries, `tools/snapshot-unit.sh <label>` there.

Before every release, walk `docs/releasing.md` → "Before tagging". A deploy that failed (the
script did not print `ok` for a unit) means that unit still runs the old build: check before
testing on it.

## Sibling-project initialization

- If you create a **sibling project** (anything at `../<name>` outside this repo), don't leave it as a bare folder. Give it the same baseline setup this project has, before writing substantial code into it:
  - its own `CLAUDE.md` stating the project's purpose, directives, and intended visibility;
  - a `docs/` folder with an index (`docs/index.md`) for design notes and findings;
  - its own initialized git repository with a sensible `.gitignore`.
- First decide and record the new project's visibility (private vs. public) and apply matching precautions — e.g. keep personal data and internal hostnames/IPs out of anything that may become public.

## Public-repo rules

- **Never commit personal data** in code, comments, examples, commit messages, docs or any file: IP
  addresses, hostnames, domains of personal infra, SSIDs, MACs, device serials/IDs, API
  keys/tokens/passwords, people's names, paths that reveal personal info. Use placeholders
  (`<speaker-ip>`, `<ha-host>`, RFC 5737 `192.0.2.x` in tests). Real values go to the gitignored
  `*.local.md` files or Claude memory.
- **Never commit vendor binaries, firmware, models or audio** (the `.so` libraries, spotter models,
  voice prompts): the CFW uses the copies on the user's own device at runtime.
- **Before every push**, grep the diff for private values (LAN prefixes, the user's hostnames,
  MAC-like strings) — a public push can't be taken back.
