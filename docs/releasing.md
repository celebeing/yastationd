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

## Cutting a release

1. Update `CHANGELOG.md`: a new `## vX.Y.Z — date` section on top, what changed for a user.
2. Commit it.
3. Tag, signed with the same SSH key as the commits: `git tag -s vX.Y.Z -m "vX.Y.Z"`.
4. Push the commit and the tag to both remotes: `git push gitea main vX.Y.Z`, then
   `git push origin main vX.Y.Z` (GitHub, a key tap).
5. Build and deploy (`tools/deploy.sh`, and `make -C sendspin-host` when it changed): the speakers
   then report the tag.

Grep the diff for private values before pushing, as for every push (`CLAUDE.md`).
