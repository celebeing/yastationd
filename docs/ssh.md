# SSH on the speaker: OpenSSH, dropbear the failsafe

Status (2026-11-06): built. **OpenSSH is the speaker's SSH server**, the only one to choose:
`tools/deploy.sh` makes it so first thing on every speaker, and every later step of an update
copies its files with scp (SFTP). dropbear stays on the speaker only as the failsafe that starts
when OpenSSH can't, and serves the first install until the switch. OpenSSH as the default since
2026-10-31, tried on bergamot and orion, a reboot included. How a speaker got SSH in the first
place is out of scope ([CLAUDE.md](../CLAUDE.md)).

## The two servers

| | dropbear ([dropbear/](../dropbear/README.md)) | OpenSSH ([openssh/](../openssh/README.md)) |
|---|---|---|
| Version | 2026.94 | 10.6p1 on OpenSSL 3.5.9 (LTS) |
| Binary | one static aarch64 `dropbearmulti` in `<cfw>/bin` (0.6 MB) | static aarch64 `sshd`, `sshd-session`, `sshd-auth`, `sftp-server`, `scp` in `<cfw>/openssh` (~10 MB: each carries libcrypto) |
| Key exchange | mlkem768x25519, sntrup761x25519, curve25519, ECDH, DH | the same, OpenSSH's order (mlkem768x25519 first) |
| Ciphers | chacha20-poly1305, AES-GCM, AES-CTR | the same |
| User keys | ed25519, ECDSA, RSA, FIDO (`sk-`) | the same, plus **certificates** |
| User certificates (a trusted CA) | no: dropbear has no OpenSSH certificates | yes (`TrustedUserCAKeys`, principals) |
| File copy | `scp` (the legacy protocol) | `scp` both ways and SFTP (OpenSSH 9+ clients use SFTP for `scp`) |
| Tried by | every unit | the dev units, 2026-10-31 |

Both run on every board: the minis' armhf userland runs static aarch64 binaries on their 64-bit
kernel. They share the host key (OpenSSH's is dropbear's ed25519 key, converted once by
`dropbearconvert`), so a client sees the same server after a switch and `known_hosts` stays.

## What starts

`yastationd/init.d/ssh.sh`, installed as `<cfw>/init.d/ssh.sh`:

- `ssh.sh start` starts OpenSSH on port 22, when its files are there and `sshd -t` accepts the
  config it writes (`/tmp/sshd_config.22`, log `/tmp/sshd-22.log`). Otherwise it starts dropbear,
  so a broken OpenSSH never leaves the speaker without SSH.
- `ssh.sh test PORT [SUFFIX]` starts OpenSSH on a test port beside the live server;
  `ssh.sh config` prints the sshd_config.
- `<cfw>/ssh_server` (`openssh` or `dropbear`) chose the server before v0.29; the switch below
  removes it.

**What relies on OpenSSH.** Regular work on an installed speaker: `tools/deploy.sh` after its SSH
step, `tools/golden.sh flash`, the Orion capture scripts copy files with `scp`, which OpenSSH 9+
runs over SFTP (dropbear serves no SFTP). **What doesn't:** the first-install steps (`make stage`,
`make sounds`, `make install-initd` and the rest of `yastationd/Makefile`) and
`tools/restore-unit.sh` stream files over plain ssh (`cat >`, `tar`), as they may meet a speaker
that runs only the install kit's dropbear; so do the SSH step itself and the backups' `tar`/`dd`
streams.

The OpenSSH config it writes: root by key only (no passwords), `/root/.ssh/authorized_keys` as
dropbear uses, no DNS lookups, SFTP, `StrictModes no` (the only login is root and every file is
root's, but bergamot's `/bkupgrade` belongs to a vendor uid, which StrictModes refuses for the
principals files), and `PerSourcePenalties max:60s`: OpenSSH 10 shuts out an address after failed
logins for up to 10 minutes by default, too long when SSH is the only way in.

**The owner's files** (none ship with the CFW), read when present:

| File | What |
|---|---|
| `<cfw>/openssh/trusted_user_ca_keys` | CA public keys; a user certificate signed by one of them is accepted |
| `<cfw>/openssh/principals/<user>` | the principals a certificate must name to log in as `<user>` (`root`); without the file a certificate must name the user itself |
| `<cfw>/openssh/sshd_config.local` | extra sshd_config lines, included first, so they win (for example `PubkeyAuthentication` limits, or `AuthorizedKeysFile none` to accept certificates only) |

Plain keys in `authorized_keys` keep working beside certificates unless the owner turns them off.

## Switching: `tools/deploy.sh`

The **first** thing a deploy does on each speaker, before it compares or uploads anything else, is
to make OpenSSH the server on port 22 (`DROPBEAR=only`: this and nothing else). The step streams
its files over plain ssh, which dropbear takes too. A speaker already running the same OpenSSH
binaries and `ssh.sh` is left alone; otherwise, in steps that can't lock you out:

1. The files go up beside the live ones as `.new` (OpenSSH's binaries, `ssh.sh`), and
   `dropbearconvert`.
2. OpenSSH starts on port **2222** from the `.new` files. The login to it is checked against the
   speaker's port-22 `known_hosts` entry (`HostKeyAlias`), so it must have the same host key.
3. A login through 2222 copies the files into place (the old ones as `.prev`) and restarts port
   22 through `ssh.sh` (sessions in progress stay up).
4. A login on port 22 must find OpenSSH running there. Then the test server stops and the `.new`
   files and `<cfw>/ssh_server` go. If not, the test server, still running from the `.new` files,
   puts the previous files back (no previous OpenSSH: its binaries go, and `ssh.sh` starts
   dropbear) and the run stops.

The steps were tried on bergamot and orion (dropbear → OpenSSH → dropbear → OpenSSH, each confirmed
on port 22, nothing left on 2222). `SSH=dropbear` is refused since v0.29: dropbear is no longer a
server to choose.

**dropbear as the failsafe** stays in `<cfw>/bin`, updated on a first deploy and with
`DROPBEAR=1` / `only`: when it is not running, the new binary must serve a login on port 2222
before it replaces the old one (kept as `.prev`). On a first install, where it still serves port
22, the swap goes through 2222 the same way (guides/deploying.md, "Updating").

`<cfw>/authorized_keys` holds the keys added from the Wi-Fi setup page ([wifi-setup.md](wifi-setup.md));
OpenSSH reads it next to `/root/.ssh/authorized_keys`.

**Open ADB is a break-glass, not a hole, while either holds:**

- a reset of the unit lands in the factory firmware;
- the unit has not yet joined its target Wi-Fi and been logged into over SSH.

Past both, it goes. So a unit with a golden image, once reached over SSH, has no business with ADB.

A speaker can run on certificates alone: with the CA and principals in place, empty
`authorized_keys`, and on orion remove `/data/quasar_activate_*` and `yandex-debugctl stop` to close
ADB. **Check a login by certificate only first** (`ssh -F /dev/null -i <key> -o IdentityAgent=none`;
your own `~/.ssh/config` may hide what really logged in). The way back without a certificate is the
key combo's setup page, which adds a key; dropbear, the fallback, takes no certificates. A restore of
an older backup brings its keys and ADB markers back.

## Boot

The install kit's `/etc/init.d/S42zdropbear` starts dropbear and knows nothing of OpenSSH.
Once OpenSSH answers on port 22, `tools/deploy.sh` replaces that hook (once:
a rootfs write, remount rw/ro) with one that runs `<cfw>/init.d/ssh.sh start`, and the kit's
original, kept as `<cfw>/init.d/S42zdropbear.orig`, when `ssh.sh` is missing or starts nothing.
`ssh.sh` itself starts dropbear when OpenSSH can't start. A speaker without that hook file (not
seen yet) keeps it; it then boots into whatever its own hook starts.

Checked on orion: the new hook run as at boot starts OpenSSH; with `sshd` renamed away it starts
dropbear; and through real reboots the speaker came back on OpenSSH (2026-10-31).

## Trying it by hand

Without `deploy.sh`, nothing persistent: copy the `openssh/build/` files and `ssh.sh` to a
directory on tmpfs, then `SSH_DIR=<dir> sh <dir>/ssh.sh test 2222` (the host key is converted
into `<dir>`). Log in with `-p 2222 -o HostKeyAlias=<speaker>`; stop it with
`kill $(cat /var/run/sshd-2222.pid)`. A certificate test needs a key that is not in
`authorized_keys`, or the login proves nothing.

## Related

[guides/deploying.md](guides/deploying.md) ("What ends up on the device", "Updating"),
[building.md](building.md), [dropbear/README.md](../dropbear/README.md),
[openssh/README.md](../openssh/README.md).
