# Releasing

## Versions

`vMAJOR.MINOR.PATCH` tags on `main` ([SemVer](https://semver.org/)). Before 1.0, MINOR adds
features, PATCH fixes things. The build burns `git describe --tags --match 'v*' --dirty` in:

| Build from | Version |
|---|---|
| a release tag | `v0.3.0` |
| 5 commits after it | `v0.3.0-5-gabc1234` |
| an uncommitted tree | `…-dirty` |
| a plain `go build` (no make) | `dev` |

Where it shows: `yastationd -version`, the daemon's first log line, the HA device card ("Firmware")
and the diagnostic sensor "Version", whose attributes carry the `sendspin-host` version (it reports
it in its `READY` line; it is also the software version the player announces to Music Assistant)
and the board. `spotter-host` has no version of its own yet.

## Before tagging

Go through this for everything that landed since the last tag (`git log <last-tag>..`):

1. **Docs match the code.** New areas have a doc and an entry in [docs/index.md](index.md); changed
   behaviour is reflected where it is described; resolved wishlist entries leave it (what is worth keeping goes into their area's doc).
2. **User-facing:** [README.md](../README.md) and [README.en.md](../README.en.md) (and [yastationd/README.md](../yastationd/README.md)) mention what a user meets — features,
   addresses, logins and their defaults, keys to turn things off.
3. **Deploying:** [docs/guides/deploying.md](guides/deploying.md) lists every file the CFW puts on a speaker and every install
   or update step; `tools/deploy.sh` uploads every binary that must be on the speaker.
4. **Config:** every new key is in `yastationd/configs/yastationd.conf.example` and
   `orion.conf.example`; every new profile key in [docs/porting-a-speaker.md](porting-a-speaker.md).
5. **Builds:** new components have a Makefile target, a row in [docs/building.md](building.md), and a step in
   a target in `tools/build.sh` (and an image in `docker/` when it needs a new toolchain).
6. **Vendor repo:** binaries we keep but don't rebuild are recorded there with provenance; units
   whose non-stock files changed are re-snapshotted.
7. **A fresh clone builds:** `git clone` the repo into a temporary directory, copy
   `tools/build.env` in, and run `tools/build.sh` there — local build outputs hide files that never got
   committed (v0.13.0 shipped without its web app: a `.gitignore` rule had swallowed it). Check
   that the binary carries the app: `curl http://<speaker>/` after the deploy is the app, not the
   "built without its web app" page.
8. **Tests and a real deploy:** `go test ./...` passes; the build ran on at least one speaker of
   each board it touches, and every unit's deploy printed `ok`.
9. **No private values** in the diff since the last tag (LAN addresses, hostnames, serials).
10. **The docs' links resolve:** `tools/build.sh docs-check` (a moved doc breaks links silently).

## Cutting a release

1. Update [CHANGELOG.md](../CHANGELOG.md): a new `## vX.Y.Z — date` section on top, what changed for a user.
2. Commit it.
3. Tag, signed with the same SSH key as the commits: `git tag -s vX.Y.Z -m "vX.Y.Z"`.
4. Push the commit and the tag to both remotes: `git push gitea main vX.Y.Z`, then
   `git push origin main vX.Y.Z` (GitHub, a key tap).
5. The tag push to GitHub runs `.github/workflows/release.yml`: it creates the GitHub Release with
   that version's [CHANGELOG.md](../CHANGELOG.md) section as the notes (no artifacts yet). Running the workflow by
   hand creates releases for every tag that has none.
6. Build and deploy (`tools/deploy.sh` builds everything it ships): the speakers
   then report the tag.

Grep the diff for private values before pushing, as for every push ([CLAUDE.md](../CLAUDE.md)).
