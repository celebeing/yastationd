# web — the speakers' web app

The app every speaker serves at `http://<speaker>/` ([docs/web-ui.md](../docs/web-ui.md)): the
speakers on the LAN, and per speaker its sensors, runtime controls, config editor and login.
TypeScript + Preact, built with Vite (versions pinned exactly in `package.json`, Node 18+).

```sh
../tools/build-web.sh            # build into ../yastationd/internal/webui/dist/app (pinned container)
pnpm install --frozen-lockfile && pnpm build   # the same with a local Node 18+ and pnpm 8
YASTATION=http://<speaker> pnpm dev   # live development against a real speaker's API
```

The build output is not committed: `make build` in `yastationd/` runs `tools/build-web.sh` whenever
the source here changed, in the pinned builder image (the root `Dockerfile`).
