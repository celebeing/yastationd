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
#   dropbear          the SSH server          dropbear/build/dropbearmulti          (zig)
#   openssh           the optional OpenSSH    openssh/build/{sshd,sshd-session,…}   (zig; SSH=openssh in deploy.sh)
#   sendspin:<arch>   the music player        sendspin-host/build/<arch>/sendspin-host (zig; armhf | aarch64)
#   spotter:<arch>    the voice front-end     spotter-host/build/<arch>/spotter-host   (cross gcc + a vendor sysroot)
#   packtool          the asset-pack checker  yastationd/build/packtool  (go; for this host, not the speakers;
#                     builds and checks packs: docs/asset-packs.md)
#   hosttool:<cmd>    a host tool of cmd/     yastationd/build/<cmd>     (go; for this host: ledsim, demoreel,
#                     clipconv, …; packtool is hosttool:packtool)
#   golden:<dir>:<p>  a golden restore image   <dir>/golden.img  (from what tools/golden.sh pulled into <dir>)
#   docs-check        every relative link in the Markdown resolves (tools/check-docs.sh; no container)
#   make:<target>     a yastationd make target that needs ffmpeg/zstd/Go (the one-off install
#                     conversions: sounds-convert, clips-convert, video-convert-in, build-ringdemo,
#                     build-fbleddemo), run in the tools image; MAKE_VARS="K=V ..." are passed to it
#
# The host needs only docker, sh, git (and ssh for the speakers): every toolchain is in an image.
#   sendspin, spotter both arches; all = web yastationd hostapd dropbear sendspin spotter
#
# spotter-host links the vendor's libraries: it needs SPOTTER_SYSROOT_ARMHF / SPOTTER_SYSROOT_AARCH64
# (a directory with lib/ and system/vendor/quasar/libs/ of the firmware the units run). Set them in
# the environment or in tools/build.env (gitignored, KEY=VALUE lines; the environment wins), or
# fetch one off a speaker with tools/fetch-vendor.sh: a lone vendor-local/<board>/<version_full>
# tree of the arch is used by itself. Without one, spotter:<arch> is skipped with a note ("all")
# or fails (asked for by name).
#
# The repo is mounted at its own path, so CMake caches stay valid inside and out. Caches (Go,
# zig) live in ${XDG_CACHE_HOME:-~/.cache}/yastation-build; containers run as the calling user.
set -eu
# Note: no target may run in an && list or a pipeline whose status is lost — set -e ignores those.

here=$(cd "$(dirname "$0")/.." && pwd)
# tools/build.env holds the local settings; a variable set in the environment wins over it.
for v in SPOTTER_SYSROOT_ARMHF SPOTTER_SYSROOT_AARCH64; do eval "env_$v=\${$v:-}"; done
if [ -f "$here/tools/build.env" ]; then . "$here/tools/build.env"; fi
for v in SPOTTER_SYSROOT_ARMHF SPOTTER_SYSROOT_AARCH64; do eval "[ -z \"\$env_$v\" ] || $v=\$env_$v"; done
command -v docker >/dev/null 2>&1 || { echo "build.sh: docker not found (docs/building.md)" >&2; exit 2; }
cache=${XDG_CACHE_HOME:-$HOME/.cache}/yastation-build
mkdir -p "$cache/go" "$cache/gomod" "$cache/zig"

# image NAME: build (or reuse) yastation-<name> from docker/<name>.Dockerfile.
# The image carries its Dockerfile's hash as a label and is rebuilt only when that changed: a
# build never depends on docker's layer cache telling it nothing changed (a broken cache made a
# builder rebuild the image on every target, downloading zig each time). A rebuild shows its
# progress; when it fails, `docker builder prune` clears a broken cache.
image() {
	df=$here/docker/$1.Dockerfile
	sum=$(sha256sum "$df" | cut -c1-16)
	have=$(docker image inspect -f '{{ index .Config.Labels "yastation.dockerfile" }}' "yastation-$1" 2>/dev/null || true)
	[ "$have" = "$sum" ] && return 0
	echo "== image yastation-$1 (docker/$1.Dockerfile changed or missing: building it once)"
	docker build -t "yastation-$1" --label "yastation.dockerfile=$sum" -f "$df" "$here/docker" ||
		{ echo "build.sh: building the yastation-$1 image failed; if it keeps failing, try: docker builder prune" >&2; exit 1; }
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

# hosttool CMD: a tool of cmd/ that runs on the host (packtool for the asset repo's
# tools/build-packs.sh, ledsim, demoreel…), built for the builder's own platform, not the speakers'.
build_hosttool() {
	[ -d "$here/yastationd/cmd/$1" ] || { echo "hosttool:$1: no yastationd/cmd/$1" >&2; exit 2; }
	echo "== $1 (host tool)"
	run go yastationd "CGO_ENABLED=0 GOOS=linux GOARCH=\$(go env GOHOSTARCH) GOARM= go build -o build/$1 ./cmd/$1"
}

# make TARGET: a yastationd make target in the tools image (IN_BUILDER=1: the Makefile does the
# work itself instead of calling back here).
# build_golden <work-dir>:<platform>: a golden restore image out of what tools/golden.sh pulled.
build_golden() {
	echo "== golden image"
	run golden . "fakeroot python3 -I tools/golden/mkgolden.py '${1%:*}' '${1##*:}'"
}

build_make() {
	echo "== make $1"
	run tools yastationd "make -s IN_BUILDER=1 $1 ${MAKE_VARS:-}"
}

build_openssh() {
	echo "== openssh"
	run zig openssh 'make -s'
}

build_dropbear() {
	echo "== dropbear"
	run zig dropbear 'make -s'
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
# local_sysroot ARCH: the one vendor-local/<board>/<version_full>/ tree (tools/fetch-vendor.sh) of a
# board with that userland arch; nothing when there is none, or several to choose from (then name
# one in tools/build.env).
local_sysroot() {
	found=
	n=0
	for d in "$here"/vendor-local/*/*/; do
		[ -d "${d}system/vendor/quasar/libs" ] || continue
		b=$(basename "$(dirname "$d")")
		a=$(awk -F= '{k=$1; gsub(/[ \t]/,"",k)} k=="platform.userland_arch" {v=$2; gsub(/[ \t]/,"",v); print v; exit}' \
			"$here/yastationd/internal/board/profiles/$b.conf" 2>/dev/null)
		[ "$a" = "$1" ] || continue
		found=${d%/}
		n=$((n + 1))
	done
	if [ "$n" -gt 1 ]; then
		echo "build.sh: several $1 trees in vendor-local/: name one as SPOTTER_SYSROOT_$(echo "$1" | tr a-z A-Z) in tools/build.env" >&2
		return 0
	fi
	echo "$found"
}

build_spotter() {
	case $1 in
	armhf) sr=${SPOTTER_SYSROOT_ARMHF:-} ;;
	aarch64) sr=${SPOTTER_SYSROOT_AARCH64:-} ;;
	*) echo "spotter: arch must be armhf or aarch64" >&2; exit 2 ;;
	esac
	[ -n "$sr" ] || sr=$(local_sysroot "$1")
	if [ -z "$sr" ]; then
		[ "$2" = 1 ] && {
			echo "spotter:$1: spotter-host links the vendor's libraries and needs a sysroot (the firmware's" >&2
			echo "  lib/ and system/vendor/quasar/libs/): set SPOTTER_SYSROOT_$(echo "$1" | tr a-z A-Z) in tools/build.env" >&2
			echo "  (copy tools/build.env.example; docs/building.md, \"The sysroot\")" >&2
			exit 2
		}
		echo "== spotter-host ($1): skipped, no SPOTTER_SYSROOT_$(echo "$1" | tr a-z A-Z) — set it in tools/build.env"
		echo "   (or fetch one off a speaker: tools/fetch-vendor.sh <ssh-target> <board>; docs/building.md, \"The sysroot\")"
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
	dropbear) build_dropbear ;;
	openssh) build_openssh ;;
	packtool) build_hosttool packtool ;;
	golden:*) build_golden "${t#golden:}" ;; # tools/golden.sh: golden:<work-dir>:<platform>
	docs-check) "$here/tools/check-docs.sh" || exit 1 ;;
	hosttool:*) build_hosttool "${t#hosttool:}" ;;
	make:*) build_make "${t#make:}" ;;
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
	has-sysroot:*) # deploy.sh: is there a sysroot for this arch? (exit status only)
		a=${t#has-sysroot:}
		eval "sr=\${SPOTTER_SYSROOT_$(echo "$a" | tr a-z A-Z):-}"
		[ -n "$sr" ] || sr=$(local_sysroot "$a" 2>/dev/null)
		[ -n "$sr" ]
		;;
	all)
		build_web
		build_yastationd
		build_hostapd
		build_dropbear
		build_sendspin armhf
		build_sendspin aarch64
		build_spotter armhf 0
		build_spotter aarch64 0
		;;
	*) echo "build.sh: unknown target '$t' (see the header)" >&2; exit 2 ;;
	esac
done
