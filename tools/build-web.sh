#!/bin/sh
# build-web.sh — build the web app (web/) into yastationd/internal/webui/dist/app in the pinned builder
# container (the root Dockerfile: Node 18.20.8, pnpm 8.14.1), so the host needs only docker.
# `make build` runs it whenever web/ changed; the output is not committed (docs/web-ui.md).
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
command -v docker >/dev/null 2>&1 || { echo "build-web: docker not found (docs/building.md)" >&2; exit 2; }
docker build -q -t yastation-build "$here" >/dev/null
# The package store goes under node_modules (ignored), not pnpm's default next to the repo root.
docker run --rm -u "$(id -u):$(id -g)" -v "$here:/src" -w /src/web \
	-e npm_config_store_dir=/src/web/node_modules/.pnpm-store yastation-build \
	sh -c 'pnpm install --frozen-lockfile --reporter=silent && pnpm build'
