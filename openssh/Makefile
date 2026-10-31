# OpenSSH, the speakers' optional SSH server (README.md): the alternative to dropbear when the
# owner wants OpenSSH certificates (a trusted user CA), SFTP and OpenSSH's own algorithms. Static
# aarch64 binaries (musl, zig cc) against a static OpenSSL, for every board: the minis' armhf
# userland runs them on their 64-bit kernel. Output in build/: sshd, sshd-session, sshd-auth,
# sftp-server, scp (stripped), plus dropbearconvert from ../dropbear (to take over the
# speaker's dropbear host key). Host tools: zig 0.14.1 (ZIG=/path/to/zig), curl, make, perl.
ZIG ?= zig
TARGET  := aarch64-linux-musl
OPENSSL := src/openssl-3.5.9
OPENSSH := src/openssh-10.6p1
SSL     := $(CURDIR)/src/ssl
CC_     := $(ZIG) cc -target $(TARGET)
BINS    := sshd sshd-session sshd-auth sftp-server scp
# Every binary links libcrypto statically: sections and the algorithms OpenSSH never asks for are
# dropped, which halves each.
GC      := -ffunction-sections -fdata-sections
SSLOFF  := no-cms no-ocsp no-ts no-srp no-ct no-rfc3779 no-seed no-idea no-camellia no-aria \
	no-sm2 no-sm3 no-sm4 no-whirlpool no-mdc2 no-md4 no-rc2 no-rc4 no-rc5 no-bf no-cast \
	no-scrypt no-siphash no-cmp no-quic no-http no-sock no-ui-console no-ssl-trace \
	no-ml-dsa no-ml-kem no-slh-dsa no-dsa no-ec2m no-argon2 no-gost no-ocb no-sm2-precomp

all: $(addprefix build/,$(BINS)) build/dropbearconvert

src/.fetched: fetch.sh
	./fetch.sh
	touch $@

# OpenSSL: libcrypto only, static, no engines/modules (nothing loaded at runtime).
$(SSL)/lib/libcrypto.a: src/.fetched
	cd $(OPENSSL) && CC="$(CC_)" AR="$(ZIG) ar" RANLIB="$(ZIG) ranlib" \
		./Configure linux-aarch64 --prefix=$(SSL) --libdir=lib no-shared no-module no-dso \
		no-engine no-tests no-docs no-apps no-ssl no-tls no-dtls no-comp $(SSLOFF) -Os $(GC) >/dev/null
	$(MAKE) -s -C $(OPENSSL) -j8 build_libs >/dev/null 2>&1
	$(MAKE) -s -C $(OPENSSL) install_dev >/dev/null 2>&1

# The paths a speaker needs are not compiled in: sshd is started with -f <cfw>/openssh/sshd_config,
# which names the host key, sshd-session/sshd-auth (SshdSessionPath/SshdAuthPath), sftp-server and
# the CA (yastationd/init.d/ssh.sh writes it). Compiled in: the session PATH (<cfw>/openssh first,
# for scp's server side) and the privilege-separation user (nobody: the read-only rootfs has no
# sshd user) and directory (created at start on tmpfs). The pre-auth sandbox is seccomp_filter
# (rlimit can't be configured when cross-compiling); deploy.sh proves a login on port 2222
# before the swap, so a kernel that refused it would never get this sshd on port 22.
$(OPENSSH)/Makefile: $(SSL)/lib/libcrypto.a
	cd $(OPENSSH) && CC="$(CC_)" AR="$(ZIG) ar" RANLIB="$(ZIG) ranlib" CFLAGS="-Os -w $(GC)" \
		LDFLAGS="-static -Wl,--gc-sections" ./configure -q --host=$(TARGET) --with-ssl-dir=$(SSL) --without-zlib \
		--prefix=/opt/openssh --sysconfdir=/opt/openssh --libexecdir=/opt/openssh \
		--with-privsep-user=nobody --with-privsep-path=/tmp/sshd-empty --with-pid-dir=/var/run \
		--with-sandbox=seccomp_filter --with-default-path=/usr/bin:/bin:/usr/sbin:/sbin:/bkupgrade/cfw/openssh:/data/cfw/openssh:/bkupgrade/cfw/bin:/data/cfw/bin \
		--disable-lastlog --disable-utmp --disable-utmpx --disable-wtmp --disable-wtmpx \
		--disable-pututline --disable-pututxline --without-pam 2>&1 | grep -v 'manpage formatter' >&2 || true

$(addprefix build/,$(BINS)) &: $(OPENSSH)/Makefile
	$(MAKE) -s -C $(OPENSSH) -j8 $(BINS) >/dev/null
	@mkdir -p build
	for b in $(BINS); do $(ZIG) objcopy --strip-all $(OPENSSH)/$$b build/$$b; done
	@ls -l $(addprefix build/,$(BINS))

# dropbearconvert, built by itself from the dropbear tree (static, the same toolchain).
build/dropbearconvert:
	$(MAKE) -s -C ../dropbear src/dropbear-2026.94/Makefile
	$(MAKE) -s -C ../dropbear/src/dropbear-2026.94 -j8 PROGRAMS=dropbearconvert STATIC=1 dropbearconvert
	@mkdir -p build
	$(ZIG) objcopy --strip-all ../dropbear/src/dropbear-2026.94/dropbearconvert $@

clean:
	rm -rf build
	-$(MAKE) -C $(OPENSSH) clean

distclean:
	rm -rf build src

.PHONY: all clean distclean
