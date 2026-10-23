#!/bin/sh
# One-time fetch of sendspin-cpp and its dependencies into third_party/src/ (gitignored).
# Every dependency is pinned to an exact commit or tag: sendspin-cpp to a main commit, the rest to
# the tags its own host build pins (cmake/host.cmake of that commit). Re-running is a no-op for
# checkouts that already exist; delete third_party/src/<name> to refetch after a pin change.
# Adapted from pulsar's satellite/third_party/fetch.sh (MIT, (c) 2026 pulsar contributors).
set -eu
cd "$(dirname "$0")"
mkdir -p src

# name  url  ref  [submodules...]
fetch() {
	name=$1 url=$2 ref=$3
	shift 3
	dir=src/$name
	if [ -e "$dir/.fetched" ] && [ "$(cat "$dir/.ref")" = "$ref" ]; then
		return
	fi
	rm -rf "$dir"
	git init -q "$dir"
	git -C "$dir" remote add origin "$url"
	git -C "$dir" fetch -q --depth 1 origin "$ref"
	git -C "$dir" -c advice.detachedHead=false checkout -q FETCH_HEAD
	for sm in "$@"; do
		git -C "$dir" submodule update -q --init --depth 1 "$sm"
	done
	echo "$ref" >"$dir/.ref"
	git -C "$dir" rev-parse HEAD >"$dir/.fetched"
	echo "fetched $name @ $(cat "$dir/.fetched")"
}

fetch sendspin-cpp https://github.com/Sendspin/sendspin-cpp.git 985fba57afb0462b520ab9ce974d1860858ed685
fetch ArduinoJson https://github.com/bblanchon/ArduinoJson.git refs/tags/v7.4.1
fetch noise-c https://github.com/esphome-libs/noise-c.git refs/tags/v0.1.13
fetch IXWebSocket https://github.com/machinezone/IXWebSocket.git refs/tags/v12.0.1
fetch micro-flac https://github.com/esphome-libs/micro-flac.git refs/tags/v0.3.0
