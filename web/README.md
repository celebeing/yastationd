# web — the speakers' web app

The app every speaker serves at `http://<speaker>/` ([docs/web-ui.md](../docs/web-ui.md)): the
speakers on the LAN, and per speaker its sensors, runtime controls, config editor and login.
TypeScript + Preact, built with Vite (versions pinned exactly in `package.json`, Node 18+).

```sh
pnpm install --frozen-lockfile
pnpm build                       # type-check, then build into ../yastationd/internal/webui/dist
YASTATION=http://<speaker> pnpm dev   # live development against a real speaker's API
```

The build output in `yastationd/internal/webui/dist/` is committed, so building `yastationd`
never needs Node; rebuild and commit it with the source (`make web` in `yastationd/`).
