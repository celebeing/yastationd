# dropbear, the speakers' SSH server, built from the upstream release (README.md). One static
# aarch64 binary (musl) serves every board: the minis' armhf userland runs it on their 64-bit
# kernel. dropbearmulti with the server, the client, dropbearkey and scp. Output:
# build/dropbearmulti (stripped). Host tools: zig 0.14.1 (ZIG=/path/to/zig), curl, bzip2, make.
ZIG ?= zig
TARGET   := aarch64-linux-musl
DROPBEAR := src/dropbear-2026.94

all: build/dropbearmulti

src/.fetched: fetch.sh
	./fetch.sh
	touch $@

$(DROPBEAR)/Makefile: src/.fetched
	cd $(DROPBEAR) && CC="$(ZIG) cc -target $(TARGET)" AR="$(ZIG) ar" RANLIB="$(ZIG) ranlib" CFLAGS="-Os -w" \
		./configure -q --host=$(TARGET) --enable-static --disable-zlib \
		--disable-lastlog --disable-utmp --disable-utmpx --disable-wtmp --disable-wtmpx \
		--disable-pututline --disable-pututxline >/dev/null

build/dropbearmulti: $(DROPBEAR)/Makefile localoptions.h
	cp localoptions.h $(DROPBEAR)/localoptions.h
	$(MAKE) -s -C $(DROPBEAR) -j8 PROGRAMS="dropbear dbclient dropbearkey scp" MULTI=1 STATIC=1
	@mkdir -p build
	$(ZIG) objcopy --strip-all $(DROPBEAR)/dropbearmulti $@
	@ls -l $@

clean:
	rm -rf build
	-$(MAKE) -C $(DROPBEAR) clean

distclean:
	rm -rf build src
