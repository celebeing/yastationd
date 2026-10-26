#!/bin/sh
# build.sh — build the CFW's artifacts, each in its own pinned builder container (docker/*.Dockerfile;
# docs/building.md). The host needs only docker (and sh, git).
#
#   tools/build.sh [target...]        (default: all)
#
# Targets, and where they land (where tools/deploy.sh and the Makefiles look):
#   web               the web app             yastationd/internal/webui/dist/app/   (node)
#   yastationd        tests + the daemon      yastationd/build/yastationd           (go; builds web first when stale)
#   hostapd           the Wi-Fi setup AP      hostapd/build/hostapd                 (zig)
#   sendspin:<arch>   the music player        sendspin-host/build/<arch>/sendspin-host (zig; armhf | aarch64)
#   spotter:<arch>    the voice front-end     spotter-host/build/<arch>/spotter-host   (cross gcc + a vendor sysroot)
#   sendspin, spotter both arches; all = web yastationd hostapd sendspin spotter
#
# spotter-host links the vendor's libraries: it needs SPOTTER_SYSROOT_ARMHF / SPOTTER_SYSROOT_AARCH64
# (a directory with lib/ and system/vendor/quasar/libs/ of the firmware the units run). Set them in
# the environment or in tools/build.env (gitignored, KEY=VALUE lines). Without one, spotter:<arch>
# is skipped with a note ("all") or fails (asked for by name).
#
# The repo is mounted at its own path, so CMake caches stay valid inside and out. Caches (Go,
# zig) live in ${XDG_CACHE_HOME:-~/.cache}/yastation-build; containers run as the calling user.
set -eu
# Note: no target may run in an && list or a pipeline whose status is lost — set -e ignores those.

here=$(cd "$(dirname "$0")/.." && pwd)
[ -f "$here/tools/build.env" ] && . "$here/tools/build.env"
command -v docker >/dev/null 2>&1 || { echo "build.sh: docker not found (docs/building.md)" >&2; exit 2; }
cache=${XDG_CACHE_HOME:-$HOME/.cache}/yastation-build
mkdir -p "$cache/go" "$cache/gomod" "$cache/zig"

# image NAME: build (or reuse) yastation-<name> from docker/<name>.Dockerfile.
image() {
	docker build -q -t "yastation-$1" -f "$here/docker/$1.Dockerfile" "$here/docker" >/dev/null
}

# run NAME DIR CMD [extra docker args via $RUN_ARGS]: CMD in DIR (relative to the repo).
run() {
	img=$1 dir=$2
	shift 2
	image "$img"
	# shellcheck disable=SC2086 # RUN_ARGS is a list of options
	docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp \
		-v "$here:$here" -w "$here/$dir" \
		-v "$cache/go:/cache/go" -e GOCACHE=/cache/go \
		-v "$cache/gomod:/cache/gomod" -e GOMODCACHE=/cache/gomod \
		-v "$cache/zig:/cache/zig" -e ZIG_GLOBAL_CACHE_DIR=/cache/zig \
		${RUN_ARGS:-} "yastation-$img" sh -c "$*"
}

# web_stale: the built app is missing or older than its sources.
web_stale() {
	out=$here/yastationd/internal/webui/dist/app/index.html
	[ -f "$out" ] || return 0
	[ -n "$(find "$here/web/src" "$here/web/index.html" "$here/web/package.json" "$here/web/pnpm-lock.yaml" \
		"$here/web/vite.config.ts" "$here/web/tsconfig.json" -newer "$out" 2>/dev/null | head -1)" ]
}

build_web() {
	echo "== web"
	run node web 'npm_config_store_dir=$PWD/node_modules/.pnpm-store pnpm install --frozen-lockfile --reporter=silent && pnpm build --logLevel warn'
}

build_yastationd() {
	web_stale && build_web
	echo "== yastationd"
	# WEB_SRC= : the app was just built if stale; the Makefile must not call docker from in here.
	run go yastationd 'make -s test build WEB_SRC='
}

build_hostapd() {
	echo "== hostapd"
	run zig hostapd 'make -s'
}

build_sendspin() {
	case $1 in armhf | aarch64) ;; *) echo "sendspin:$1: arch must be armhf or aarch64" >&2; exit 2 ;; esac
	echo "== sendspin-host ($1)"
	run zig sendspin-host "make -s ARCH=$1 binary"
}

# spotter ARCH REQUIRED: REQUIRED=1 fails without a sysroot, 0 skips.
build_spotter() {
	case $1 in
	armhf) sr=${SPOTTER_SYSROOT_ARMHF:-} ;;
	aarch64) sr=${SPOTTER_SYSROOT_AARCH64:-} ;;
	*) echo "spotter: arch must be armhf or aarch64" >&2; exit 2 ;;
	esac
	if [ -z "$sr" ]; then
		[ "$2" = 1 ] && {
			echo "spotter:$1: spotter-host links the vendor's libraries and needs a sysroot (the firmware's" >&2
			echo "  lib/ and system/vendor/quasar/libs/): set SPOTTER_SYSROOT_$(echo "$1" | tr a-z A-Z) in tools/build.env" >&2
			echo "  (copy tools/build.env.example; docs/building.md, \"The sysroot\")" >&2
			exit 2
		}
		echo "== spotter-host ($1): skipped, no SPOTTER_SYSROOT_$(echo "$1" | tr a-z A-Z)"
		return 0
	fi
	sr=$(cd "$sr" && pwd)
	echo "== spotter-host ($1, $sr)"
	mkdir -p "$here/spotter-host/build/$1"
	RUN_ARGS="-v $sr:$sr:ro" run cross spotter-host \
		"ARCH=$1 SYSROOT='$sr' OUT=build/$1/spotter-host sh ./build.sh"
}

[ $# -gt 0 ] || set -- all
for t in "$@"; do
	case $t in
	web) build_web ;;
	yastationd) build_yastationd ;;
	hostapd) build_hostapd ;;
	sendspin)
		build_sendspin armhf
		build_sendspin aarch64
		;;
	sendspin:*) build_sendspin "${t#sendspin:}" ;;
	spotter)
		build_spotter armhf 0
		build_spotter aarch64 0
		;;
	spotter:*) build_spotter "${t#spotter:}" 1 ;;
	spotter-if:*) build_spotter "${t#spotter-if:}" 0 ;; # skipped without a sysroot (deploy.sh)
	all)
		build_web
		build_yastationd
		build_hostapd
		build_sendspin armhf
		build_sendspin aarch64
		build_spotter armhf 0
		build_spotter aarch64 0
		;;
	*) echo "build.sh: unknown target '$t' (see the header)" >&2; exit 2 ;;
	esac
done
