# Cross-build image for every artifact the CFW puts on a speaker (docs/building.md).
# Build: docker build -t yastation-build .        (or let tools/build-dist.sh do it)
# Run:   docker run --rm -v "$PWD:/src" -v /path/to/<vendor-repo>:/vendor:ro yastation-build
#
# Why a container: the pins are not optional. sendspin-host and hostapd need zig 0.14.1 exactly
# (0.13 emits armhf C++ binaries that crash before main), yastationd is built with Go 1.22.5, and
# spotter-host needs a cross gcc plus a vendor sysroot. A rolling distro gives you none of this
# reproducibly.
#
# The vendor sysroot is NOT baked in: it is the owner's material (their private vendor-tree checkout) and is mounted
# read-only at /vendor. Everything else is pinned here.
#
# Status (2026-10-26): written against the versions the Makefiles enforce; not yet run end to end
# on a docker host. The commands mirror docs/deploying.md §1 — if the first run needs a tweak,
# fix it here rather than in a local recipe.
FROM debian:bookworm-slim

ARG ZIG_VERSION=0.14.1
ARG GO_VERSION=1.22.5
ARG NODE_VERSION=18.20.8
ARG PNPM_VERSION=8.14.1

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential ca-certificates curl xz-utils git make cmake ninja-build pkg-config file \
      gcc-aarch64-linux-gnu libc6-dev-arm64-cross \
      gcc-arm-linux-gnueabihf libc6-dev-armhf-cross \
 && rm -rf /var/lib/apt/lists/*

# zig: sendspin-host (both arches) and hostapd (one static armv7 binary)
RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-x86_64-linux-${ZIG_VERSION}.tar.xz" \
      | tar -xJ -C /opt \
 && ln -s "/opt/zig-x86_64-linux-${ZIG_VERSION}/zig" /usr/local/bin/zig \
 && zig version

# Node + pnpm: the web app (web/, embedded in yastationd; tools/build-web.sh runs this image too)
RUN curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
      | tar -xJ -C /opt \
 && ln -s "/opt/node-v${NODE_VERSION}-linux-x64/bin/node" /usr/local/bin/node \
 && ln -s "/opt/node-v${NODE_VERSION}-linux-x64/bin/npm" /usr/local/bin/npm \
 && npm install -g "pnpm@${PNPM_VERSION}" --prefix /usr/local \
 && pnpm --version

# Go: yastationd (CGO off, GOARCH=arm GOARM=7 — one static binary for every board)
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" | tar -xz -C /usr/local
ENV PATH=/usr/local/go/bin:$PATH \
    GOCACHE=/tmp/go-cache \
    GOMODCACHE=/tmp/go-mod \
    HOME=/tmp

WORKDIR /src
CMD ["sh", "-c", "go version && zig version && cmake --version | head -1"]
