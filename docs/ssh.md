# SSH on the speaker: dropbear or OpenSSH

Status (2026-10-31): built. **OpenSSH is the default** that `tools/deploy.sh` puts on a speaker;
dropbear stays on it as the fallback, and `SSH=dropbear` keeps dropbear instead. Tried on bergamot
and orion, a reboot included. How a speaker got SSH in the first place is out of
scope ([CLAUDE.md](../CLAUDE.md)).

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

## How it is chosen

`yastationd/init.d/ssh.sh`, installed as `<cfw>/init.d/ssh.sh`:

- `<cfw>/ssh_server` holds `openssh` or `dropbear`; missing means dropbear.
- `ssh.sh start` starts that one on port 22. OpenSSH starts only when its files are there and
  `sshd -t` accepts the config it writes (`/tmp/sshd_config.22`, log `/tmp/sshd-22.log`);
  anything else starts dropbear, so a broken OpenSSH never leaves the speaker without SSH.
- `ssh.sh test PORT [SUFFIX]` starts OpenSSH (or `SERVER=dropbear` dropbear) on a test port
  beside the live server; `ssh.sh config` prints the sshd_config.

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

Every deploy makes `SSH` (default `openssh`; `SSH=dropbear` for dropbear; with
`DROPBEAR=only` nothing else is built or updated) the server on port 22. A speaker already running
the same OpenSSH binaries and `ssh.sh` is left alone; otherwise, in steps that can't lock you out:

1. The files go up beside the live ones as `.new` (OpenSSH's binaries, `ssh.sh`), and
   `dropbearconvert`.
2. The new server starts on port **2222** from the `.new` files. The login to it is checked
   against the speaker's port-22 `known_hosts` entry (`HostKeyAlias`), so it must have the same
   host key.
3. A login through 2222 copies the files into place (the old ones as `.prev`), writes
   `<cfw>/ssh_server` (the previous value as `ssh_server.prev`) and restarts port 22 through
   `ssh.sh` (sessions in progress stay up).
4. A login on port 22 must find the chosen server running there. Then the test server stops and
   the `.new` files go. If not, the test server, still running from the `.new` files, puts the
   previous server back and the run stops.

Tried on bergamot and orion: dropbear → OpenSSH → dropbear → OpenSSH, each step confirmed on
port 22, nothing left on 2222.

**dropbear as the fallback** stays in `<cfw>/bin` and is still updated by `DROPBEAR=1` / `only`:
when it is not running, the new binary must serve a login on port 2222 before it replaces the
old one (kept as `.prev`).

## Boot

The install kit's `/etc/init.d/S42zdropbear` starts dropbear and knows nothing of
`<cfw>/ssh_server`. Once OpenSSH answers on port 22, `tools/deploy.sh` replaces that hook (once:
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
