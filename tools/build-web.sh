#!/bin/sh
# build-web.sh — build the web app (web/) into yastationd/internal/webui/dist in a pinned
# container (web/Dockerfile: Node 18.20.8, pnpm 8.14.1), with no Node on the host.
# `make web` uses this when pnpm is not installed.
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
command -v docker >/dev/null 2>&1 || { echo "docker not found (or install Node 18+ and pnpm)" >&2; exit 2; }
docker build -q -t yastation-web -f "$here/web/Dockerfile" "$here/web" >/dev/null
docker run --rm -u "$(id -u):$(id -g)" -v "$here:/src" yastation-web
echo "built into $here/yastationd/internal/webui/dist"
