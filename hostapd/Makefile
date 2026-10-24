# hostapd for the Wi-Fi setup mode (docs/wifi-setup.md): the firmware's wpa_supplicant is built
# without AP support and the firmware has no hostapd. One static binary (armv7, musl) runs on every
# board, like yastationd: it links nothing from the device. Output: build/hostapd (stripped).
# Host tools: zig 0.14.1 (ZIG=/path/to/zig for one outside PATH), curl, git, make.
ZIG ?= zig
TARGET := arm-linux-musleabihf
CC     := $(ZIG) cc -target $(TARGET) -mcpu=cortex_a53
HOSTAPD := src/hostapd-2.11
NL      := src/libnl-tiny
NL_SRCS := attr cache cache_mngt error genl genl_ctrl genl_family genl_mngt handlers msg nl object socket
NL_OBJS := $(patsubst %,build/nl/%.o,$(NL_SRCS))

all: build/hostapd

src/.fetched: fetch.sh
	./fetch.sh
	touch $@

build/nl/%.o: src/.fetched
	@mkdir -p build/nl
	$(CC) -Os -D_GNU_SOURCE -I$(NL)/include -c $(NL)/$*.c -o $@

build/libnl-tiny.a: $(NL_OBJS)
	$(ZIG) ar rcs $@ $^

build/hostapd: build/libnl-tiny.a hostapd.config src/.fetched
	cp hostapd.config $(HOSTAPD)/hostapd/.config
	$(MAKE) -C $(HOSTAPD)/hostapd -j8 CC="$(CC)" \
		EXTRA_CFLAGS="-Os -D_GNU_SOURCE -I$(CURDIR)/$(NL)/include -DCONFIG_LIBNL20" \
		LDFLAGS="-static -L$(CURDIR)/build" hostapd
	$(ZIG) objcopy --strip-all $(HOSTAPD)/hostapd/hostapd $@
	@ls -l $@

clean:
	rm -rf build
	-$(MAKE) -C $(HOSTAPD)/hostapd clean

distclean:
	rm -rf build src

.PHONY: all clean distclean
