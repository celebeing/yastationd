# openssh — the speakers' optional SSH server

Our build of [OpenSSH](https://www.openssh.com/portable.html), for a speaker whose owner wants
user certificates (a trusted CA), SFTP or OpenSSH itself instead of dropbear. dropbear stays the
default. How it is chosen, configured and switched to: [docs/ssh.md](../docs/ssh.md).

- **Versions:** OpenSSH 10.6p1 on OpenSSL 3.5.9 (the LTS line), the release tarballs pinned by
  sha256 in `fetch.sh`.
- **Static aarch64 (musl, `zig cc`) for every board**, like dropbear: the minis' armhf userland
  runs it on their 64-bit kernel. libcrypto only (no TLS, engines or loadable modules), with
  the algorithm families OpenSSH never asks for left out; each binary still carries it, ~3 MB.
- **What's in it:** `sshd`, `sshd-session`, `sshd-auth`, `sftp-server`, `scp` (the server side of
  the legacy protocol), and `dropbearconvert` from `../dropbear` (to take over the speaker's
  dropbear host key). No `ssh-keygen`, no client.
- **Compiled in:** the session `PATH` (`<cfw>/openssh` first), the privilege-separation user
  `nobody` (the read-only rootfs has no `sshd` user) and directory `/tmp/sshd-empty` (made at
  start), the seccomp sandbox. Everything else comes from the config `ssh.sh` writes.
- **Build:** `tools/build.sh openssh` → `build/` ([docs/building.md](../docs/building.md)).
- **On a speaker:** `SSH=openssh tools/deploy.sh` ([docs/ssh.md](../docs/ssh.md)).

## A new version

1. Download the tarballs and their `.asc` from <https://cdn.openbsd.org/pub/OpenBSD/OpenSSH/portable/>
   (key: `RELEASE_KEY.asc` one directory up) and
   <https://github.com/openssl/openssl/releases> (key: <https://openssl-library.org/source/pubkeys.asc>).
2. Check both: `gpg --verify` must say "Good signature" from Damien Miller,
   `7168 B983 815A 5EEF 59A4  ADFD 2A3F 414E 7360 60BA`, and from OpenSSL,
   `B146 647E 45A7 B339 47AB  226B 2A2C 87D1 6169 2D40`.
3. Put the versions and sha256s into `fetch.sh` and the directory names into the `Makefile`,
   delete `src/`, build, and try it on one speaker through port 2222 (docs/ssh.md, "Trying it by
   hand"), then with `SSH=openssh`.
