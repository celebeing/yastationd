# spotter-host: the cross gcc it was proven with (Ubuntu 22.04: gcc 11, glibc 2.35 — not newer
# than the devices' 2.30 armhf, linked against the device's own; 2.36 aarch64), linked against a
# vendor sysroot mounted at run time (tools/build.sh spotter:<arch>).
FROM ubuntu:22.04
RUN apt-get update \
 && apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu libc6-dev-arm64-cross \
      gcc-arm-linux-gnueabihf libc6-dev-armhf-cross file \
 && rm -rf /var/lib/apt/lists/*
