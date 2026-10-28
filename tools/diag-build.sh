#!/bin/sh
# diag-build.sh — collect what is needed to debug a failing build or deploy on the developer's
# machine (Linux, WSL, macOS) into one archive, to send to whoever helps.
#
#   tools/diag-build.sh [-r] [-s] [-o DIR]
#
#   -o DIR   where to write diag-build-<host>-<YYYYmmdd-HHMMSS>.tar.gz (default: the current
#            directory); its path is the last line on stdout
#   -r       raw: no redaction (keep it private)
#   -s       also check that every speaker in tools/deploy.hosts answers ssh: BatchMode, a 5 s
#            connect timeout, the command `true`. A setup whose key needs a hardware tap or a PIN
#            fails here; that is reported, never retried.
#
# Read-only: it never builds, pulls, prunes or deletes an image, never deploys, never writes into
# the checkout. What it looks at is what tools/build.sh and tools/deploy.sh depend on: git state,
# docker, the builder images and their Dockerfile-hash labels against docker/*.Dockerfile (the
# check build.sh's image() makes: a mismatch means the next build rebuilds that image), the build
# cache, tools/build.env and the spotter-host sysroots, the built artifacts, host toolchains.
# docs/diagnostics.md has the details.
#
# Redaction (default): remote URLs reduced to their host (a host other than a public forge
# becomes <private-host>), $HOME shown as ~, the user name and the
# host name replaced, tools/deploy.hosts reduced to a count, build.env values other than the
# SPOTTER_SYSROOT_* paths dropped. -r keeps it all.

LC_ALL=C
export LC_ALL
OUTDIR=.
RAW=0
SSHCHK=0
while [ $# -gt 0 ]; do
	case $1 in
	-o) [ $# -ge 2 ] || { echo "diag-build.sh: -o needs a directory" >&2; exit 2; }; OUTDIR=$2; shift ;;
	-r) RAW=1 ;;
	-s) SSHCHK=1 ;;
	-h | --help) sed -n '2,24p' "$0"; exit 0 ;;
	*) echo "diag-build.sh: unknown argument '$1' (usage: diag-build.sh [-r] [-s] [-o DIR])" >&2; exit 2 ;;
	esac
	shift
done
[ -d "$OUTDIR" ] || { echo "diag-build.sh: no directory $OUTDIR" >&2; exit 2; }
OUTDIR=$(cd "$OUTDIR" && pwd)

here=$(cd "$(dirname "$0")/.." && pwd)
cache=${XDG_CACHE_HOME:-$HOME/.cache}/yastation-build

os=$(uname -s)
case $os in
Linux) if grep -qi microsoft /proc/version 2>/dev/null; then plat=wsl; else plat=linux; fi ;;
Darwin) plat=macos ;;
*) plat=$(echo "$os" | tr 'A-Z' 'a-z') ;;
esac
hn=$(hostname 2>/dev/null || uname -n)
if [ "$RAW" = 1 ]; then tag=$(echo "$hn" | tr -c 'A-Za-z0-9_.\n-' '_'); else tag=$plat; fi
NAME=diag-build-$tag-$(date +%Y%m%d-%H%M%S)
WORK=$(mktemp -d "${TMPDIR:-/tmp}/diag-build.XXXXXX") || exit 1
D=$WORK/$NAME
mkdir -p "$D"
trap 'rm -rf "$WORK"' EXIT
trap 'exit 130' INT TERM HUP

# sha256 of a file (GNU coreutils, or macOS's shasum).
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }
# tmo SECS CMD...: with a time limit where `timeout` exists (not on stock macOS).
tmo() { if command -v timeout >/dev/null 2>&1; then timeout "$@"; else shift; "$@"; fi; }
sec() { F=$D/$1; : >>"$F"; }
note() { printf '%s\n' "$*" >>"$F"; }
run() {
	printf '\n$ %s\n' "$1" >>"$F"
	tmo "${2:-20}" sh -c "$1" 2>&1 </dev/null | head -n "${3:-200}" >>"$F"
}
mtime() { date -r "$1" '+%F %T' 2>/dev/null || ls -l "$1" | awk '{print $6, $7, $8}'; }
finfo() {
	if [ -f "$1" ]; then
		note "$1: $(wc -c <"$1" | tr -d ' ') bytes, $(mtime "$1"), sha256 $(sha "$1" | cut -c1-16)"
	else
		note "$1: absent"
	fi
}

echo "diag-build: collecting into $D" >&2
cd "$here" || exit 1

# ---------------------------------------------------------------- host
sec host.txt
note "platform: $plat"
run "uname -a"
run "cat /etc/os-release" 5 20
run "sw_vers" 5
run "cat /proc/version"
run "nproc 2>/dev/null || sysctl -n hw.ncpu"
run "free -m 2>/dev/null || sysctl -n hw.memsize"
run "df -h . \"$cache\" \"${TMPDIR:-/tmp}\"" 10
run "echo \"shell: \$SHELL; sh is \$(command -v sh) -> \$(readlink -f \"\$(command -v sh)\" 2>/dev/null)\""
for t in git docker go zig node pnpm npm make cmake gcc arm-linux-gnueabihf-gcc aarch64-linux-gnu-gcc ssh sha256sum shasum realpath; do
	p=$(command -v "$t" 2>/dev/null) || p=
	note "$(printf '%-24s %s' "$t" "${p:-not found}")"
done
run "git --version; go version; zig version; node --version; pnpm --version; make --version | head -1; cmake --version | head -1" 20
[ "$plat" = wsl ] && run "cat /etc/wsl.conf; echo; echo \"repo on: \$(df -T . | awk 'NR==2 {print \$2}')\"" 5 30

# ---------------------------------------------------------------- git
sec git.txt
run "git describe --tags --always --dirty"
run "git rev-parse HEAD; git branch --show-current"
run "git status --short --branch" 10 120
run "git log --oneline -n 15"
if [ "$RAW" = 1 ]; then
	run "git remote -v"
else
	# URLs reduced to their host (user, path and credentials dropped).
	run "git remote -v | awk '{u = \$2; sub(/^[a-z+]+:\\/\\//, \"\", u); sub(/^[^@]*@/, \"\", u); sub(/[:\\/].*/, \"\", u); if (u !~ /^(github\\.com|gitlab\\.com|codeberg\\.org|bitbucket\\.org)$/) u = \"<private-host>\"; print \$1, u \"/…\", \$3}'"
fi
run "git submodule status" 10
run "git stash list | wc -l"
run "git config --get core.autocrlf; git config --get core.filemode"
run "git ls-files --eol tools/build.sh tools/deploy.sh yastationd/init.d/supervise.sh docker/zig.Dockerfile" 5
note ""
note "== files the build expects, and their line endings (CRLF breaks the shell scripts)"
for f in tools/build.sh tools/deploy.sh tools/fetch-vendor.sh yastationd/init.d/supervise.sh yastationd/scripts/diag.sh; do
	if [ -f "$f" ]; then
		c=$(tr -cd '\r' <"$f" | wc -c | tr -d ' ')
		note "$f: $([ -x "$f" ] && echo executable || echo NOT-executable), CR bytes $c"
	else
		note "$f: absent"
	fi
done

# ---------------------------------------------------------------- docker and the builder images
sec docker.txt
if command -v docker >/dev/null 2>&1; then
	run "docker version" 20 40
	run "docker info" 20 80
	run "docker buildx version" 10
	run "docker system df" 60
	note ""
	note "== builder images: label yastation.dockerfile vs the Dockerfile's hash (build.sh image())"
	for df in docker/*.Dockerfile; do
		[ -f "$df" ] || continue
		n=$(basename "$df" .Dockerfile)
		want=$(sha "$df" | cut -c1-16)
		have=$(tmo 20 docker image inspect -f '{{ index .Config.Labels "yastation.dockerfile" }}' "yastation-$n" 2>/dev/null)
		if [ -z "$have" ]; then
			st="MISSING or unlabelled: the next build builds it"
		elif [ "$have" = "$want" ]; then
			st=ok
		else
			st="STALE: label $have, the next build rebuilds it"
		fi
		note "$(printf 'yastation-%-6s want %s  %s' "$n" "$want" "$st")"
	done
	run "docker images --format '{{.Repository}}:{{.Tag}} {{.ID}} {{.CreatedSince}} {{.Size}}' | grep -E '^(yastation|<none>)'" 20 60
	run "docker image inspect -f '{{.RepoTags}} created={{.Created}} arch={{.Architecture}} labels={{json .Config.Labels}}' \$(docker images -q 'yastation-*' | sort -u)" 20 40
	run "docker ps -a --filter ancestor=yastation-zig --filter ancestor=yastation-go --filter ancestor=yastation-cross --filter ancestor=yastation-node --format '{{.ID}} {{.Image}} {{.Status}}'" 20 20
	run "docker builder du 2>/dev/null | tail -n 5" 60
	run "df -h \"\$(docker info -f '{{.DockerRootDir}}' 2>/dev/null)\" 2>&1" 20
else
	note "docker: not found (tools/build.sh needs it; docs/building.md)"
fi

# ---------------------------------------------------------------- build inputs and outputs
sec build.txt
note "build cache: $cache"
run "ls -la \"$cache\"; du -sh \"$cache\"/* 2>/dev/null" 60
note ""
if [ -f tools/build.env ]; then
	note "== tools/build.env: present; keys set:"
	grep -E '^[[:space:]]*[A-Za-z_][A-Za-z0-9_]*=' tools/build.env | while IFS= read -r l; do
		k=$(echo "$l" | sed -E 's/^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)=.*/\1/')
		case $k in
		SPOTTER_SYSROOT_*) note "  $l" ;;
		*) if [ "$RAW" = 1 ]; then note "  $l"; else note "  $k=<set>"; fi ;;
		esac
	done
else
	note "== tools/build.env: absent (tools/build.env.example)"
fi
note "env: SPOTTER_SYSROOT_ARMHF=${SPOTTER_SYSROOT_ARMHF:-} SPOTTER_SYSROOT_AARCH64=${SPOTTER_SYSROOT_AARCH64:-} XDG_CACHE_HOME=${XDG_CACHE_HOME:-} DOCKER_HOST=${DOCKER_HOST:+<set>}"
note ""
note "== spotter-host sysroots"
for v in SPOTTER_SYSROOT_ARMHF SPOTTER_SYSROOT_AARCH64; do
	sr=$(eval "echo \"\${$v:-}\"")
	[ -n "$sr" ] || sr=$(sed -n "s/^[[:space:]]*$v=//p" tools/build.env 2>/dev/null | tail -1 | sed -e 's/^"//' -e 's/"$//')
	if [ -z "$sr" ]; then note "$v: not set"; continue; fi
	note "$v = $sr"
	if [ -d "$sr" ]; then
		for p in lib system/vendor/quasar/libs system/vendor/quasar/libs/libyandex_evil.so system/vendor/quasar/version_full etc/os-release; do
			[ -e "$sr/$p" ] && note "  $p: present" || note "  $p: MISSING"
		done
		note "  firmware: $(cat "$sr/system/vendor/quasar/version_full" 2>/dev/null)"
		note "  libc: $(ls "$sr"/lib/libc-* "$sr"/lib/libc.so.6 2>/dev/null | sed 's#.*/##' | tr '\n' ' ')"
		note "  absolute links (break a sysroot): $(find "$sr/lib" -type l 2>/dev/null | while read -r l; do readlink "$l"; done | grep -c '^/')"
	else
		note "  directory MISSING"
	fi
done
run "ls -d vendor-local/*/* 2>/dev/null" 5
note ""
note "== built artifacts"
for f in yastationd/build/yastationd hostapd/build/hostapd dropbear/build/dropbearmulti \
	sendspin-host/build/armhf/sendspin-host sendspin-host/build/aarch64/sendspin-host \
	spotter-host/build/armhf/spotter-host spotter-host/build/aarch64/spotter-host \
	yastationd/internal/webui/dist/app/index.html; do
	finfo "$f"
done
run "file yastationd/build/yastationd hostapd/build/hostapd dropbear/build/dropbearmulti sendspin-host/build/*/sendspin-host spotter-host/build/*/spotter-host" 10
n=$(find web/src web/index.html web/package.json web/pnpm-lock.yaml -newer yastationd/internal/webui/dist/app/index.html 2>/dev/null | wc -l | tr -d ' ')
note "web sources newer than the built app: $n (non-zero: the next yastationd build rebuilds the app)"
run "for c in sendspin-host/build/*/CMakeCache.txt; do echo \"\$c: \$(grep -E '^CMAKE_HOME_DIRECTORY' \"\$c\")\"; done" 5
note "(a CMakeCache made in another directory is wiped by the Makefile; this checkout is at $here)"
run "ls -la web/node_modules/.pnpm-store 2>/dev/null | head -3; ls web/node_modules 2>/dev/null | wc -l" 10
note "build.sh writes no log of its own: send the failing command's output along with this archive."

# ---------------------------------------------------------------- deploy hosts
sec deploy.txt
if [ -f tools/deploy.hosts ]; then
	hosts=$(grep -v '^[[:space:]]*\(#\|$\)' tools/deploy.hosts)
	note "tools/deploy.hosts: present, $(printf '%s\n' "$hosts" | grep -c .) entries"
	note "platforms: $(printf '%s\n' "$hosts" | awk '{print $2}' | sort | uniq -c | tr -s ' \n' ' ')"
	[ "$RAW" = 1 ] && { note ""; printf '%s\n' "$hosts" >>"$F"; }
	if [ "$SSHCHK" = 1 ]; then
		note ""
		note "== ssh reachability (BatchMode, 5 s; a key that needs a tap or a PIN fails here)"
		i=0
		printf '%s\n' "$hosts" | while read -r t p _; do
			[ -n "$t" ] || continue
			i=$((i + 1))
			if [ "$RAW" = 1 ]; then lbl=$t; else lbl="host#$i"; fi
			out=$(ssh -n -o BatchMode=yes -o ConnectTimeout=5 "$t" true 2>&1)
			rc=$?
			note "$lbl ($p): rc=$rc $(echo "$out" | tail -n 1)"
		done
	fi
else
	note "tools/deploy.hosts: absent (tools/deploy.hosts.example)"
fi
note "SSHKEY: ${SSHKEY:+<set>} VENDOR: ${VENDOR:+<set>} DROPBEAR: ${DROPBEAR:-} NATIVE: ${NATIVE:-}"
run "ssh -V" 5

# ---------------------------------------------------------------- redaction
if [ "$RAW" = 0 ]; then
	u=$(id -un 2>/dev/null)
	esc() { printf '%s\n' "$1" | sed -e 's/[]\/$*.^[]/\\&/g'; }
	{
		[ -n "$HOME" ] && [ "$HOME" != / ] && echo "s/$(esc "$HOME")/~/g"
		[ "${#hn}" -ge 3 ] && echo "s/$(esc "$hn")/<host>/g"
		[ "${#u}" -ge 3 ] && echo "s/$(esc "$u")/<user>/g"
	} >"$WORK/map.sed"
	for f in "$D"/*; do
		sed -f "$WORK/map.sed" "$f" | sed -E 's/(([0-9A-Fa-f]{2}:){3})[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}:[0-9A-Fa-f]{2}/\1xx:xx:xx/g' >"$f.r" && mv "$f.r" "$f"
	done
fi

# ---------------------------------------------------------------- summary
F=$D/summary.txt
: >"$F"
note "yastation build diagnostics — $NAME"
note "redaction: $([ "$RAW" = 1 ] && echo 'OFF (-r): keep it private' || echo 'ON (remote URLs, home, user and host names, deploy hosts)')"
note "platform: $plat $(uname -m); checkout: $(git describe --tags --always --dirty 2>/dev/null)"
note "docker: $(docker version -f '{{.Server.Version}}' 2>/dev/null || echo 'not reachable')"
grep -E '^yastation-' "$D/docker.txt" 2>/dev/null >>"$F"
grep -E '^(SPOTTER_SYSROOT_|  directory)' "$D/build.txt" 2>/dev/null >>"$F"
grep -E ': (absent|.*bytes)' "$D/build.txt" 2>/dev/null | grep -E '^(yastationd|hostapd|dropbear|sendspin|spotter)' >>"$F"
if [ "$RAW" = 0 ]; then sed -f "$WORK/map.sed" "$F" >"$F.r" && mv "$F.r" "$F"; fi

cd "$WORK" || exit 1
out=$OUTDIR/$NAME.tar.gz
# shellcheck disable=SC2046 # names without spaces
tar cf - "$NAME/summary.txt" $(ls "$NAME" | grep -v '^summary\.txt$' | sed "s|^|$NAME/|") | gzip -c >"$out" ||
	{ echo "diag-build.sh: writing $out failed" >&2; exit 1; }
echo "diag-build: $(wc -c <"$out" | tr -d ' ') bytes" >&2
echo "$out"
