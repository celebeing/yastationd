#!/bin/sh
# Cross-build spotter-host against the device's own libraries.
#   SYSROOT  dir with lib/ (device /lib) and system/vendor/quasar/libs/ (full libyandex_evil.so)
#   OUT      output binary path
#   ARCH     armhf (default; bergamot, glibc 2.30) or aarch64 (pickle, glibc 2.36)
# armhf links against the device's glibc 2.30 (not the toolchain's), so the binary only needs
# symbol versions the device has. On aarch64 the toolchain's glibc (2.35) is older than the
# device's, so a plain dynamic link is enough.
set -e
: "${SYSROOT:?}" "${OUT:=spotter-host}" "${ARCH:=armhf}"
V=$SYSROOT/system/vendor/quasar/libs
L=$SYSROOT/lib
HERE=$(dirname "$0")
case $ARCH in
aarch64)
  CC=${CC:-aarch64-linux-gnu-gcc}
  $CC -O2 -Wall -Wextra -o "$OUT" "$HERE/spotter_host.c" \
    -L"$V" -lyandex_evil -lm -lpthread -Wl,-rpath-link,"$V:$L" \
    -Wl,--dynamic-linker=/lib/ld-linux-aarch64.so.1 -Wl,--disable-new-dtags \
    -Wl,-rpath,/system/vendor/quasar/libs
  ;;
armhf)
  CC=${CC:-arm-linux-gnueabihf-gcc}
  U=$($CC -print-sysroot)/usr/lib
  [ -f "$U/crt1.o" ] || U=/usr/arm-linux-gnueabihf/lib
  $CC -O2 -Wall -Wextra -c "$HERE/spotter_host.c" -o "$OUT.o"
  $CC -nostdlib -no-pie -o "$OUT" \
    "$U/crt1.o" "$U/crti.o" "$($CC -print-file-name=crtbegin.o)" "$OUT.o" \
    -L"$V" -lyandex_evil "$L/libm.so.6" "$L/libpthread.so.0" "$L/libc.so.6" "$L/libgcc_s.so.1" "$L/ld-linux-armhf.so.3" \
    "$($CC -print-libgcc-file-name)" "$($CC -print-file-name=crtend.o)" "$U/crtn.o" \
    -Wl,--dynamic-linker=/lib/ld-linux-armhf.so.3 -Wl,-rpath-link,"$V:$L" \
    -Wl,-rpath,/system/vendor/quasar/libs
  rm -f "$OUT.o"
  ;;
*) echo "ARCH must be armhf or aarch64" >&2; exit 1 ;;
esac
