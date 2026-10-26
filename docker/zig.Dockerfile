# sendspin-host and hostapd: zig 0.14.1 exactly (0.13 emits armhf C++ binaries that crash before
# main), plus what their builds fetch and run with (tools/build.sh sendspin:<arch>, hostapd).
FROM debian:bookworm-slim
ARG ZIG_VERSION=0.14.1
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl xz-utils bzip2 binutils git make cmake ninja-build file \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://ziglang.org/download/${ZIG_VERSION}/zig-x86_64-linux-${ZIG_VERSION}.tar.xz" | tar -xJ -C /opt \
 && ln -s "/opt/zig-x86_64-linux-${ZIG_VERSION}/zig" /usr/local/bin/zig \
 && zig version
