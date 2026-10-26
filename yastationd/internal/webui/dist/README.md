The web app is built into `app/` here by `make build` (or `make web`), from `../../../../web` in
the pinned builder container (`tools/build.sh web`). `app/` is not committed; this file keeps the
directory, so `go build` works without it.
