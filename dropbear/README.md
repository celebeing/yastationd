# dropbear — the speakers' fallback SSH server

Our build of [dropbear](https://matt.ucc.asn.au/dropbear/dropbear.html), the SSH server the
speakers run from `<cfw>/bin/dropbearmulti`. How a speaker got SSH in the first place is out of
this project's scope; this only keeps the server we already use current.

- **Version:** 2026.94, the release tarball pinned by sha256 in `fetch.sh`.
- **One binary for every board:** static aarch64 (musl, `zig cc`), which the minis' armhf
  userland runs on their 64-bit kernel. Reproducible: the same source and zig give the same bytes.
- **What's in it:** `dropbearmulti` with the server, the client (`dbclient`), `dropbearkey` and
  `scp`. Sessions get `<cfw>/bin` on `PATH` (`localoptions.h`), so with the `scp` link there,
  `scp` to a speaker works (plain and `scp -O`). No SFTP server.
- **Build:** `tools/build.sh dropbear` → `build/dropbearmulti` (docs/building.md).
- **Since 2026-10-31 the fallback:** OpenSSH serves port 22 by default ([openssh/](../openssh/README.md),
  [docs/ssh.md](../docs/ssh.md)); dropbear starts when OpenSSH can't, or with `SSH=dropbear`.
- **On a speaker:** `tools/deploy.sh` replaces the server on a first deploy or with `DROPBEAR=1`,
  through a test server on port 2222 (docs/guides/deploying.md, "Updating").

## A new version

1. Download the tarball and its `.asc` from <https://matt.ucc.asn.au/dropbear/releases/>, and the
   release key `dropbear-key-2015.asc` from the same place.
2. Check the signature: `gpg --verify dropbear-<v>.tar.bz2.asc dropbear-<v>.tar.bz2` must say
   "Good signature" from `F734 7EF2 EE2E 07A2 6762  8CA9 4493 1494 F29C 6773` (Dropbear SSH
   Release Signing).
3. Put the version and the tarball's sha256 into `fetch.sh` and the directory name into the
   `Makefile`, delete `src/`, build, and try it on one speaker (`DROPBEAR=1`).

Why it was replaced: the units shipped 2022.83 (static aarch64, from the install kit), which
predates the fix for Terrapin (CVE-2023-48795, fixed in 2024.84) and has no scp.
