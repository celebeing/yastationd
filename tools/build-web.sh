#!/bin/sh
# build-web.sh — build the web app (web/) into yastationd/internal/webui/dist/app in its pinned
# builder container (docker/node.Dockerfile). `make build` in yastationd/ runs it whenever web/
# changed; tools/build.sh does the same. The output is not committed (docs/web-ui.md).
exec "$(dirname "$0")/build.sh" web
