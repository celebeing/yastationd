# sendspin-host, hostapd and dropbear: zig 0.14.1 exactly (0.13 emits armhf C++ binaries that crash
# before main), plus what their builds fetch and run with (tools/build.sh sendspin:<arch>, hostapd).
# The zig tarball matches the builder's own arch (x86_64 or aarch64 hosts) and is checked against
# ziglang.org's published sha256.
FROM debian:bookworm-slim
ARG ZIG_VERSION=0.14.1
ARG ZIG_SHA256_X86_64=24aeeec8af16c381934a6cd7d95c807a8cb2cf7df9fa40d359aa884195c4716c
ARG ZIG_SHA256_AARCH64=f7a654acc967864f7a050ddacfaa778c7504a0eca8d2b678839c21eea47c992b
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils bzip2 binutils git make cmake ninja-build file \
 && rm -rf /var/lib/apt/lists/*
RUN set -eu; \
    arch=$(uname -m); \
    case $arch in \
      x86_64) sum=$ZIG_SHA256_X86_64 ;; \
      aarch64) sum=$ZIG_SHA256_AARCH64 ;; \
      *) echo "no zig build pinned for $arch" >&2; exit 1 ;; \
    esac; \
    f=/tmp/zig.tar.xz; \
    curl -fsSL --retry 3 --retry-delay 5 -o $f "https://ziglang.org/download/${ZIG_VERSION}/zig-${arch}-linux-${ZIG_VERSION}.tar.xz"; \
    echo "$sum  $f" | sha256sum -c -; \
    tar -xJf $f -C /opt; \
    rm $f; \
    ln -s "/opt/zig-${arch}-linux-${ZIG_VERSION}/zig" /usr/local/bin/zig; \
    zig version
